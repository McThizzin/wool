package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"wool/internal/app"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/doctor"
	"wool/internal/llamaserver"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usageText = `wool - llama.cpp server launcher and router TUI

Usage:
  wool [--config PATH]                 open the TUI
  wool doctor                          check binary, models and VRAM fit
  wool list [--json]                   list models and presets
  wool run TARGET [options]            start a model or preset detached
  wool stop [server|router|all]        stop detached servers
  wool status [name] [--json]          show detached server state
  wool logs [name] [-n N] [-f]         show a server log
  wool router [start|stop|status|models|load ID|unload ID|reload|ini]
  wool config path                     print the config path
  wool config init [--force] [PATH]    write a sample config
  wool version

TARGET is a preset name or a model name. run options:
  --dry-run        print the llama-server command without starting it
  --restart        stop an existing "server" first
  --port N         use this port instead of the configured one
  --no-wait        return as soon as the process is spawned

Router options are accepted after "router start": --dry-run, --restart,
--port N, --no-wait.

The config defaults to ~/.config/wool/config.toml.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfgPath, rest := extractConfig(args)
	if len(rest) == 0 {
		return openTUI(cfgPath)
	}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usageText)
		return 0
	case "-v", "--version", "version":
		fmt.Printf("wool %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "doctor":
		return cmdDoctor(cfgPath)
	case "config":
		return cmdConfig(cfgPath, rest)
	case "list":
		return cmdList(cfgPath, rest)
	case "run", "start":
		return cmdRun(cfgPath, rest)
	case "stop":
		return cmdStop(cfgPath, rest)
	case "status":
		return cmdStatus(cfgPath, rest)
	case "logs", "log":
		return cmdLogs(cfgPath, rest)
	case "router":
		return cmdRouter(cfgPath, rest)
	default:
		fmt.Fprintf(os.Stderr, "wool: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
}

// extractConfig accepts --config/-c before or after the subcommand so both
// "wool --config x doctor" and "wool doctor --config x" work.
func extractConfig(args []string) (string, []string) {
	path := config.DefaultPath()
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-c":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--config="):
			path = strings.TrimPrefix(a, "--config=")
		default:
			rest = append(rest, a)
		}
	}
	return config.ExpandPath(path), rest
}

func openTUI(cfgPath string) int {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	if err := app.Run(cfg, cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	return 0
}

func loadConfig(path string) (*config.Config, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return config.Load(path)
}

func printConfigError(path string, err error) {
	fmt.Fprintf(os.Stderr, "wool: config %s: %v\n", path, err)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "Create one with: wool config init\n")
	}
}

func cmdDoctor(cfgPath string) int {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	rep := doctor.Run(cfg, cfgPath)
	fmt.Print(rep.Print())
	if len(rep.Failures()) > 0 {
		return 1
	}
	return 0
}

func cmdConfig(cfgPath string, args []string) int {
	if len(args) == 0 {
		fmt.Print("usage: wool config path|init [--force] [PATH]\n")
		return 2
	}
	switch args[0] {
	case "path":
		fmt.Println(cfgPath)
		return 0
	case "init":
		fs := flag.NewFlagSet("config init", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		force := fs.Bool("force", false, "overwrite an existing config")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		path := cfgPath
		if fs.NArg() > 0 {
			path = config.ExpandPath(fs.Arg(0))
		}
		if err := config.WriteExample(path, *force); err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		fmt.Printf("wrote %s\n", path)
		if _, err := config.Load(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: generated config does not load: %v\n", err)
			return 1
		}
		fmt.Println("Edit model paths, then run: wool doctor")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "wool: unknown config command %q\n", args[0])
		return 2
	}
}

type listEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Alias  string `json:"alias,omitempty"`
	Model  string `json:"model,omitempty"`
	Path   string `json:"path,omitempty"`
	Ctx    int    `json:"ctx"`
	Exists bool   `json:"exists"`
}

func cmdList(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	var entries []listEntry
	for _, id := range cfg.ModelIDs() {
		r, err := cfg.Resolve(id, nil)
		if err != nil {
			continue
		}
		exists := false
		if _, err := os.Stat(r.ModelPath); err == nil {
			exists = true
		}
		entries = append(entries, listEntry{ID: id, Kind: "model", Alias: r.Alias, Path: r.ModelPath, Ctx: r.Ctx, Exists: exists})
	}
	for _, id := range cfg.PresetIDs() {
		p := cfg.Presets[id]
		r, err := cfg.Resolve("", &p)
		if err != nil {
			continue
		}
		exists := false
		if _, err := os.Stat(r.ModelPath); err == nil {
			exists = true
		}
		entries = append(entries, listEntry{ID: id, Kind: "preset", Model: p.Model, Path: r.ModelPath, Ctx: r.Ctx, Exists: exists})
	}
	if *asJSON {
		return writeJSON(entries)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tCTX\tALIAS/MODEL\tSTATUS")
	for _, e := range entries {
		extra := e.Alias
		if e.Kind == "preset" {
			extra = e.Model
		}
		status := "ok"
		if !e.Exists {
			status = "missing"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", e.ID, e.Kind, e.Ctx, extra, status)
	}
	w.Flush()
	return 0
}

func cmdRun(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dry := fs.Bool("dry-run", false, "print the command and exit")
	restart := fs.Bool("restart", false, "stop an existing server first")
	port := fs.Int("port", 0, "override the configured port")
	noWait := fs.Bool("no-wait", false, "do not wait for /health")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool run TARGET [options]")
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	if *port > 0 {
		cfg.Server.Port = *port
		off := false
		cfg.Server.AutoPort = &off
	}
	target := fs.Arg(0)
	resolved, preset, err := resolveTarget(cfg, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if _, err := os.Stat(resolved.ModelPath); err != nil {
		fmt.Fprintf(os.Stderr, "wool: model file for %s is missing: %s\nRun `wool doctor` for details.\n", target, resolved.ModelPath)
		return 1
	}

	mgr := daemon.NewManager()
	bin := config.ExpandPath(cfg.App.LlamaServer)
	host := cfg.Server.Host
	if *dry {
		args := llamaserver.SingleArgs(resolved, host, cfg.Server.Port)
		fmt.Println(llamaserver.CommandLine(bin, args))
		return 0
	}
	if *restart {
		if err := mgr.Stop("server", 20*time.Second); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
			fmt.Fprintf(os.Stderr, "wool: restart: %v\n", err)
			return 1
		}
	}

	state, err := mgr.Start(daemon.Spec{
		Name:     "server",
		Kind:     daemon.KindServer,
		Preset:   preset,
		Model:    resolved.ModelID,
		Bin:      bin,
		Host:     host,
		Port:     cfg.Server.Port,
		AutoPort: cfg.Server.AutoPortEnabled(),
		Args: func(port int) []string {
			return llamaserver.SingleArgs(resolved, host, port)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if !*noWait {
		if err := waitReady(state, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			printLogTail(mgr, state.Name, 40)
			_ = mgr.Stop(state.Name, 10*time.Second)
			return 1
		}
	}
	printServer(state, preset)
	return 0
}

func resolveTarget(cfg *config.Config, name string) (config.Resolved, string, error) {
	if p, ok := cfg.Presets[name]; ok {
		r, err := cfg.Resolve("", &p)
		if err != nil {
			return config.Resolved{}, "", err
		}
		return r, name, nil
	}
	if _, ok := cfg.Models[name]; ok {
		r, err := cfg.Resolve(name, nil)
		if err != nil {
			return config.Resolved{}, "", err
		}
		return r, "", nil
	}
	return config.Resolved{}, "", fmt.Errorf("no preset or model named %q", name)
}

func waitReady(state daemon.State, cfg *config.Config) error {
	timeout := durationOr(cfg.Server.StartupTimeout, 120*time.Second)
	interval := durationOr(cfg.Server.PollInterval, time.Second)
	client := llamaserver.NewClient(state.Host, state.Port)
	if err := client.WaitReady(context.Background(), timeout, interval); err != nil {
		return fmt.Errorf("server %s:%d did not become ready: %w", state.Host, state.Port, err)
	}
	return nil
}

func durationOr(s string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return fallback
}

func printServer(state daemon.State, preset string) {
	label := state.Model
	if preset != "" {
		label = preset
	}
	fmt.Printf("%s started\n", label)
	fmt.Printf("  url   %s\n", state.URL())
	fmt.Printf("  pid   %d\n", state.PID)
	fmt.Printf("  port  %d\n", state.Port)
	fmt.Printf("  log   %s\n", state.LogPath)
	fmt.Println("The server keeps running after this command exits. Use `wool stop`.")
}

func printLogTail(mgr *daemon.Manager, name string, n int) {
	lines, err := mgr.Tail(name, n)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\nlast %d log lines (%s):\n%s\n", len(lines), mgr.LogDir, strings.Join(lines, "\n"))
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

func cmdLogs(cfgPath string, args []string) int {
	_ = cfgPath
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	n := fs.Int("n", 100, "number of lines")
	follow := fs.Bool("f", false, "follow the log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mgr := daemon.NewManager()
	name := fs.Arg(0)
	if name == "" {
		for _, candidate := range []string{"server", "router"} {
			if _, err := mgr.Status(candidate); err == nil {
				name = candidate
				break
			}
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "wool: no server log found")
		return 1
	}
	lines, err := mgr.Tail(name, *n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	fmt.Println(strings.Join(lines, "\n"))
	if !*follow {
		return 0
	}
	path, err := mgr.LogPath(name)
	if err != nil {
		return 1
	}
	if err := followFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	return 0
}

func followFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	buf := make([]byte, 32<<10)
	for {
		select {
		case <-sig:
			return nil
		case <-ticker.C:
			for {
				n, err := f.Read(buf)
				if n > 0 {
					os.Stdout.Write(buf[:n])
				}
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}
				if n == 0 {
					break
				}
			}
		}
	}
}

func cmdRouter(cfgPath string, args []string) int {
	sub := "start"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "start":
		return routerStart(cfgPath, args)
	case "stop":
		mgr := daemon.NewManager()
		if err := mgr.Stop("router", 30*time.Second); err != nil {
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Println("router: not running")
				return 0
			}
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		fmt.Println("router: stopped")
		return 0
	case "status":
		return cmdStatus(cfgPath, []string{"router"})
	case "models", "ps":
		client, err := routerClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		return printModels(client)
	case "load":
		return routerAction("load", args)
	case "unload":
		return routerAction("unload", args)
	case "reload":
		client, err := routerClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		if err := client.ReloadModels(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "wool: reload: %v\n", err)
			return 1
		}
		fmt.Println("router: presets reloaded")
		return 0
	case "ini":
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			printConfigError(cfgPath, err)
			return 1
		}
		data, err := llamaserver.RenderRouterINI(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		os.Stdout.Write(data)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "wool: unknown router command %q\n", sub)
		return 2
	}
}

func routerStart(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("router start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dry := fs.Bool("dry-run", false, "print the command and exit")
	restart := fs.Bool("restart", false, "stop an existing router first")
	port := fs.Int("port", 0, "override the configured port")
	noWait := fs.Bool("no-wait", false, "do not wait for /health")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	if *port > 0 {
		cfg.Server.Port = *port
		off := false
		cfg.Server.AutoPort = &off
	}
	bin := config.ExpandPath(cfg.App.LlamaServer)
	host := cfg.Server.Host
	ini := config.RouterINIPath()
	mgr := daemon.NewManager()
	if *dry {
		fmt.Println(llamaserver.CommandLine(bin, llamaserver.RouterArgs(cfg, host, cfg.Server.Port, ini)))
		fmt.Println("# models.ini would be written to " + ini)
		return 0
	}
	if *restart {
		if err := mgr.Stop("router", 30*time.Second); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
			fmt.Fprintf(os.Stderr, "wool: restart: %v\n", err)
			return 1
		}
	}
	if err := llamaserver.WriteRouterINI(cfg, ini); err != nil {
		fmt.Fprintf(os.Stderr, "wool: write %s: %v\n", ini, err)
		return 1
	}
	state, err := mgr.Start(daemon.Spec{
		Name:     "router",
		Kind:     daemon.KindRouter,
		Model:    "router",
		Bin:      bin,
		Host:     host,
		Port:     cfg.Server.Port,
		AutoPort: cfg.Server.AutoPortEnabled(),
		Args: func(port int) []string {
			return llamaserver.RouterArgs(cfg, host, port, ini)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if !*noWait {
		if err := waitReady(state, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			printLogTail(mgr, state.Name, 40)
			_ = mgr.Stop(state.Name, 10*time.Second)
			return 1
		}
	}
	fmt.Println("router started")
	fmt.Printf("  url   %s\n", state.URL())
	fmt.Printf("  pid   %d\n", state.PID)
	fmt.Printf("  ini   %s\n", ini)
	fmt.Printf("  log   %s\n", state.LogPath)
	fmt.Println("Load a model with: wool router load ID")
	return 0
}

func routerClient() (*llamaserver.Client, error) {
	mgr := daemon.NewManager()
	inst, err := mgr.Status("router")
	if err != nil {
		if errors.Is(err, daemon.ErrNotRunning) {
			return nil, errors.New("router is not running (start it with `wool router start`)")
		}
		return nil, err
	}
	if !inst.Alive {
		return nil, fmt.Errorf("router is not running (stale state at %s)", inst.LogPath)
	}
	return llamaserver.NewClient(inst.Host, inst.Port), nil
}

func printModels(client *llamaserver.Client) int {
	models, err := client.Models(context.Background(), false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tFAILED\tALIASES")
	for _, m := range models {
		status := m.Status.Value
		failed := ""
		if m.Status.Failed {
			failed = "yes"
			if m.Status.ExitCode != nil {
				failed = fmt.Sprintf("exit %d", *m.Status.ExitCode)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.ID, status, failed, strings.Join(m.Aliases, ","))
	}
	w.Flush()
	return 0
}

func routerAction(action string, args []string) int {
	fs := flag.NewFlagSet("router "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("timeout", 120*time.Second, "how long to wait for the final state")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: wool router %s MODEL\n", action)
		return 2
	}
	id := fs.Arg(0)
	client, err := routerClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	ctx := context.Background()
	if action == "load" {
		err = client.Load(ctx, id)
	} else {
		err = client.Unload(ctx, id)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %s: %v\n", action, err)
		return 1
	}
	want := "loaded"
	if action == "unload" {
		want = "unloaded"
	}
	model, err := waitModelState(client, id, want, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %s %s: %v\n", action, id, err)
		mgr := daemon.NewManager()
		printLogTail(mgr, "router", 30)
		return 1
	}
	fmt.Printf("%s: %s (%s)\n", model.ID, model.Status.Value, action)
	return 0
}

// waitModelState polls the router until the model reaches want. A load that
// fails is reported even though the status value falls back to unloaded.
func waitModelState(client *llamaserver.Client, id, want string, timeout time.Duration) (llamaserver.Model, error) {
	deadline := time.Now().Add(timeout)
	var last llamaserver.Model
	for {
		models, err := client.Models(context.Background(), false)
		if err != nil {
			return last, err
		}
		found := false
		for _, m := range models {
			if m.ID != id {
				continue
			}
			found = true
			last = m
			if m.Status.Failed {
				msg := m.Status.Value + " (load failed"
				if m.Status.ExitCode != nil {
					msg += fmt.Sprintf(", exit %d", *m.Status.ExitCode)
				}
				return m, errors.New(msg + ")")
			}
			if want == "loaded" && (m.Status.Value == "loaded" || m.Status.Value == "sleeping") {
				return m, nil
			}
			if want == "unloaded" && m.Status.Value == "unloaded" {
				return m, nil
			}
		}
		if !found {
			return last, fmt.Errorf("model %q is not in the router list", id)
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("timed out in state %q, want %q", last.Status.Value, want)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func writeJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	return 0
}
