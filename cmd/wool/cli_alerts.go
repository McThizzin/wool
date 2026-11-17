package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"wool/internal/alerts"
	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/sysinfo"
)

const alertsUsage = `usage: wool alerts [--json]

Evaluates the [alerts] rules once against the current GPU, running instances,
router model status and log tails. The TUI evaluates the same rules every poll
and shows the results in the Status panel; this command is the script-friendly
one-shot check.

options:
  --json           print the stable machine-readable snapshot`

// alertsSnapshot is the stable `wool alerts --json` output. Version 1.
type alertsSnapshot struct {
	Version int            `json:"version"`
	Enabled bool           `json:"enabled"`
	Alerts  []alerts.Alert `json:"alerts"`
}

const alertsVersion = 1

func cmdAlerts(cfgPath string, args []string) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			fmt.Println(alertsUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "wool: alerts: unknown option %q\n%s\n", a, alertsUsage)
			return 2
		}
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	in := currentAlertInput()
	found := alerts.New(alerts.FromConfig(cfg.Alerts)).Evaluate(in)
	if found == nil {
		found = []alerts.Alert{}
	}
	if asJSON {
		return writeJSON(alertsSnapshot{
			Version: alertsVersion,
			Enabled: cfg.Alerts.Enabled,
			Alerts:  found,
		})
	}
	if !cfg.Alerts.Enabled {
		fmt.Println("alerts disabled (set [alerts] enabled = true)")
		return 0
	}
	if len(found) == 0 {
		fmt.Println("no alerts")
		return 0
	}
	for _, a := range found {
		fmt.Printf("%s  %-8s %-12s %s\n", a.At.Format("15:04:05"), a.Severity, a.Rule, a.Message)
	}
	return 0
}

// currentAlertInput gathers the same observation the TUI evaluates. It is
// best effort: a missing health response becomes "unreachable", and a missing
// log or model list simply contributes no evidence.
func currentAlertInput() alerts.Input {
	in := alerts.Input{At: time.Now(), Logs: map[string][]string{}}
	if snap, err := sysinfo.NewCollector().Sample(); err == nil {
		if gpu, ok := snap.Primary(); ok {
			in.HasTemp, in.TempC = gpu.HasTemp, gpu.TempC
			in.HasVRAM = gpu.HasVRAM
			in.VRAMPercent = gpu.VRAMPercent()
		}
	}
	mgr := daemon.NewManager()
	for _, name := range []string{"server", "router"} {
		inst, err := mgr.Status(name)
		if err != nil {
			continue
		}
		item := alerts.Instance{
			Name:      name,
			Alive:     inst.Alive,
			StartedAt: inst.StartedAt,
		}
		if inst.Alive {
			c := llamaserver.NewClient(inst.Host, inst.Port)
			c.HTTP.Timeout = 2 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if h, err := c.Health(ctx); err == nil {
				item.Health = h.Status
			} else {
				item.Health = "unreachable"
			}
			if name == "router" {
				if mm, err := c.Models(ctx, false); err == nil {
					for _, rm := range mm {
						in.Models = append(in.Models, alerts.Model{
							ID:     rm.ID,
							Status: rm.Status.Value,
							Failed: rm.Status.Failed,
						})
					}
				}
			}
			cancel()
			if lines, err := mgr.Tail(name, 200); err == nil {
				in.Logs[name] = lines
			}
		}
		in.Instances = append(in.Instances, item)
	}
	return in
}
