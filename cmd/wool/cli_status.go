package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"wool/internal/config"
	"wool/internal/daemon"
)

func cmdRestart(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noWait := fs.Bool("no-wait", false, "do not wait for /health")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: wool restart [server|router] [--no-wait]")
		return 2
	}
	name := "server"
	if fs.NArg() == 1 {
		name = fs.Arg(0)
	}
	if name != "server" && name != "router" {
		fmt.Fprintf(os.Stderr, "wool: unknown target %q (want server or router)\n", name)
		return 2
	}

	mgr := daemon.NewManager()
	inst, err := mgr.Status(name)
	if err != nil {
		if errors.Is(err, daemon.ErrNotRunning) {
			fmt.Fprintf(os.Stderr, "wool: %s has no recorded launch; use `wool run TARGET`\n", name)
			return 1
		}
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if inst.Alive {
		timeout := 20 * time.Second
		if name == "router" {
			timeout = 30 * time.Second
		}
		if err := mgr.Stop(name, timeout); err != nil {
			fmt.Fprintf(os.Stderr, "wool: restart: %v\n", err)
			return 1
		}
	}

	state, err := mgr.Start(daemon.SpecFromState(inst.State))
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if !*noWait {
		cfg, _ := loadConfig(cfgPath)
		if cfg == nil {
			cfg = &config.Config{}
		}
		if err := waitReady(state, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			printLogTail(mgr, state.Name, 40)
			_ = mgr.Stop(state.Name, 10*time.Second)
			return 1
		}
	}
	printServerVerb(state, inst.Preset, "restarted")
	return 0
}

func cmdStop(cfgPath string, args []string) int {
	_ = cfgPath
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: wool stop [server|router|all]")
		return 2
	}
	target := "all"
	if len(args) == 1 {
		target = args[0]
	}
	names, err := stopTargets(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 2
	}
	mgr := daemon.NewManager()
	code := 0
	for _, name := range names {
		timeout := 20 * time.Second
		if name == "router" {
			timeout = 30 * time.Second
		}
		if err := mgr.Stop(name, timeout); err != nil {
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Printf("%s: not running\n", name)
				continue
			}
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			code = 1
			continue
		}
		fmt.Printf("%s: stopped\n", name)
	}
	return code
}

func stopTargets(target string) ([]string, error) {
	switch target {
	case "all":
		return []string{"server", "router"}, nil
	case "server", "router":
		return []string{target}, nil
	default:
		return nil, fmt.Errorf("unknown target %q (want server, router or all)", target)
	}
}

func cmdStatus(cfgPath string, args []string) int {
	_ = cfgPath
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mgr := daemon.NewManager()
	var instances []daemon.Instance
	if fs.NArg() > 0 {
		for _, name := range fs.Args() {
			inst, err := mgr.Status(name)
			if err != nil {
				if errors.Is(err, daemon.ErrNotRunning) {
					fmt.Printf("%s: not running\n", name)
					continue
				}
				fmt.Fprintf(os.Stderr, "wool: %v\n", err)
				return 1
			}
			instances = append(instances, inst)
		}
	} else {
		list, err := mgr.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		instances = list
	}
	if *asJSON {
		return writeJSON(instances)
	}
	if len(instances) == 0 {
		fmt.Println("no servers recorded")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tURL\tPID\tWHAT\tUPTIME\tLOG")
	for _, inst := range instances {
		state := "dead"
		if inst.Alive {
			state = "running"
			if inst.Supervised {
				if inst.SupervisorAlive {
					state = "supervised"
				} else {
					state = "orphaned"
				}
			}
		}
		what := inst.Model
		if inst.Preset != "" {
			what = inst.Preset
		}
		if what == "" {
			what = "-"
		}
		uptime := time.Since(inst.StartedAt).Round(time.Second)
		if !inst.Alive {
			uptime = 0
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", inst.Name, state, inst.URL(), inst.PID, what, uptime, inst.LogPath)
	}
	w.Flush()
	return 0
}
