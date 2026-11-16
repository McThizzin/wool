package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/measure"
)

func cmdSupervise(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("supervise", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	foreground := fs.Bool("foreground", false, "internal: run the supervisor in the foreground")
	restart := fs.Bool("restart", false, "stop an existing server first")
	port := fs.Int("port", 0, "override the configured port")
	noWait := fs.Bool("no-wait", false, "do not wait for /health")
	name := fs.String("name", "server", "internal: instance name")
	kind := fs.String("kind", string(daemon.KindServer), "internal: instance kind")
	host := fs.String("host", "", "internal: bind host")
	childPort := fs.Int("child-port", 0, "internal: child port")
	bin := fs.String("bin", "", "internal: child binary")
	restartMax := fs.Int("restart-max", 3, "internal: maximum restart attempts")
	restartWindow := fs.String("restart-window", "", "internal: restart window")
	restartBackoff := fs.String("restart-backoff", "", "internal: restart backoff")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *foreground {
		return runSupervisorForeground(*name, *kind, *host, *childPort, *bin,
			*restartMax, *restartWindow, *restartBackoff, fs.Args())
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, superviseUsage)
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
	resolved, preset, err := resolveTarget(cfg, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	childBin := config.ExpandPath(cfg.App.LlamaServer)
	if _, err := exec.LookPath(childBin); err != nil {
		fmt.Fprintf(os.Stderr, "wool: find %s: %v\n", childBin, err)
		return 1
	}
	exe, err := woolExecutable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: locate wool executable: %v\n", err)
		return 1
	}
	hostName := cfg.Server.Host
	policy := daemon.RestartPolicy{
		MaxAttempts: cfg.Server.RestartMaxAttempts(),
		Window:      cfg.Server.RestartWindowDuration(),
		Backoff:     cfg.Server.RestartBackoffDuration(),
	}

	mgr := daemon.NewManager()
	if *restart {
		if err := mgr.Stop("server", 20*time.Second); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
			fmt.Fprintf(os.Stderr, "wool: restart: %v\n", err)
			return 1
		}
	}
	state, err := mgr.Start(daemon.Spec{
		Name:       "server",
		Kind:       daemon.KindServer,
		Supervised: true,
		Preset:     preset,
		Model:      resolved.ModelID,
		Bin:        exe,
		Host:       hostName,
		Port:       cfg.Server.Port,
		AutoPort:   cfg.Server.AutoPortEnabled(),
		Args: func(port int) []string {
			childArgs := llamaserver.SingleArgs(resolved, hostName, port)
			return supervisorArgs("server", daemon.KindServer, hostName, port,
				childBin, policy, childArgs)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if !*noWait {
		if err := waitReadySupervised(state, mgr, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			printLogTail(mgr, state.Name, 40)
			_ = mgr.Stop(state.Name, 10*time.Second)
			return 1
		}
	}
	printServerVerb(state, preset, "supervised")
	return 0
}

const superviseUsage = `usage: wool supervise TARGET [options]

Runs a preset or model under a detached supervisor that restarts it after an
unexpected exit. [server] restart is ignored: supervision is explicit, and the
supervisor always retries within restart_max, restart_window and restart_backoff.
Set restart_max = 0 to give up after the first exit.

options:
  --restart        stop an existing "server" first
  --port N         use this port instead of the configured one
  --no-wait        return as soon as the supervisor is spawned`

func supervisorArgs(name string, kind daemon.Kind, host string, port int,
	childBin string, policy daemon.RestartPolicy, childArgs []string) []string {
	args := []string{
		"supervise", "--foreground",
		"--name", name,
		"--kind", string(kind),
		"--host", host,
		"--child-port", strconv.Itoa(port),
		"--bin", childBin,
		"--restart-max", strconv.Itoa(policy.MaxAttempts),
		"--restart-window", policy.Window.String(),
		"--restart-backoff", policy.Backoff.String(),
		"--",
	}
	return append(args, childArgs...)
}

func runSupervisorForeground(name, kind, host string, port int, bin string,
	max int, window, backoff string, childArgs []string) int {
	if bin == "" {
		fmt.Fprintln(os.Stderr, "wool: supervise: missing --bin")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err := daemon.RunSupervisor(ctx, daemon.SupervisorConfig{
		Name: name,
		Kind: daemon.Kind(kind),
		Host: host,
		Port: port,
		Bin:  bin,
		Args: childArgs,
		Policy: daemon.RestartPolicy{
			MaxAttempts: max,
			Window:      durationOr(window, 10*time.Minute),
			Backoff:     durationOr(backoff, 2*time.Second),
		},
	})
	if errors.Is(err, daemon.ErrSupervisorExhausted) {
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: supervise: %v\n", err)
		return 1
	}
	return 0
}

// waitReadySupervised polls /health but fails early when the supervisor has
// given up, instead of waiting for the full startup timeout.
func waitReadySupervised(state daemon.State, mgr *daemon.Manager, cfg *config.Config) error {
	timeout := durationOr(cfg.Server.StartupTimeout, 120*time.Second)
	interval := durationOr(cfg.Server.PollInterval, time.Second)
	deadline := time.Now().Add(timeout)
	client := llamaserver.NewClient(state.Host, state.Port)
	var last error
	for {
		probe := interval
		if probe > 2*time.Second {
			probe = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), probe)
		h, err := client.Health(ctx)
		cancel()
		if err == nil && h.Status == "ok" {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("health status %q", h.Status)
		}
		inst, stErr := mgr.Status(state.Name)
		if stErr != nil || !inst.Alive {
			return fmt.Errorf("supervisor exited before server %s:%d became ready: %w",
				state.Host, state.Port, last)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server %s:%d did not become ready in %s: %w",
				state.Host, state.Port, timeout, last)
		}
		time.Sleep(interval)
	}
}

// applySmokeResult copies an optional smoke test into a record. A successful
// attempt sets verified_at; a failure keeps the reason for later display.
func applySmokeResult(rec *measure.Record, res *measure.SmokeResult) {
	if rec == nil || res == nil {
		return
	}
	rec.SmokeMS = float64(res.Latency.Milliseconds())
	rec.SmokeError = res.Err
	if res.OK() {
		rec.VerifiedAt = res.At
	}
}

// parseSmokeTimeout falls back to the default for an unset or malformed value.
// Config validation rejects malformed values, so the fallback mainly protects
// callers that build a Config in memory.
func parseSmokeTimeout(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return measure.DefaultSmokeTimeout
}

// launchRecord turns a resolved target into a measurement record. The estimate
// is recomputed here so the record matches the launch exactly.
