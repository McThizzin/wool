package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"wool/internal/alerts"
	"wool/internal/app"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/doctor"
	"wool/internal/endpoint"
	"wool/internal/gguf"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/reqlog"
	"wool/internal/sysinfo"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// woolExecutable locates the running wool binary for the detached supervisor.
// Tests replace it to inject a fake supervisor.
var woolExecutable = os.Executable

const usageText = `wool - llama.cpp server launcher and router TUI

Usage:
  wool [--config PATH]                 open the TUI
  wool doctor [--json]                 check binary, models and VRAM fit
  wool fit TARGET [options]            recommend settings that fit VRAM
  wool bench TARGET [options]          benchmark prompt and generation throughput
  wool bench --url URL --model ID      benchmark an already-running server
  wool bench history [TARGET]          show recorded benchmark runs
  wool bench compare A B [options]     interleaved A/B benchmark comparison
  wool list [--json]                   list models and presets
  wool run TARGET [options]            start a model or preset detached
  wool supervise [options] TARGET      run a target under a detached supervisor
  wool restart [server|router] [--no-wait]  relaunch the recorded instance
  wool stop [server|router|all]        stop detached servers
  wool status [name] [--json]          show detached server state
  wool stats [MODEL] [options]         live inference rates from the running server
  wool endpoint [server|router|MODEL] [--json]  base URL and client snippets
  wool logs [name] [-n N] [-f] [--json] show a server log, or parsed requests
  wool alerts [--json]                 evaluate alert rules against current state
  wool router [start|stop|status|models|load ID|unload ID|load-group NAME|groups|reload|ini]
  wool config path                     print the config path
  wool config init [--force] [PATH]    write a sample config
  wool config explain TARGET [--json]  show each value and where it came from
  wool config diff TARGET [--against defaults|model] [--json]
  wool version

TARGET is a preset name or a model name. run options:
  --dry-run        print the llama-server command without starting it
  --restart        stop an existing "server" first
  --port N         use this port instead of the configured one
  --no-wait        return as soon as the process is spawned
  --smoke          send a tiny prompt after health
  --strict         fail and stop the server if the smoke test fails

supervise options:
  --restart        stop an existing "server" first
  --port N         use this port instead of the configured one
  --no-wait        return as soon as the supervisor is spawned

Router options are accepted after "router start": --dry-run, --restart,
--port N, --no-wait.

fit options:
  --vram SIZE      total card size (7G, 7GiB); default is the live GPU budget
  --cache-type T   override cache_type_k and cache_type_v
  --gpu-layers N   override gpu_layers
  --n-cpu-moe N    override n_cpu_moe
  --save-preset NAME  write the recommendation as presets.NAME
  --force          replace an existing preset
  --calibrated     apply observed/estimate correction and label it
  --json           print the stable machine-readable result

bench options:
  --runs N         repetitions (default 3)
  --pp N           approximate prompt tokens (default 512)
  --tg N           generated tokens (default 128)
  --engine E       server (default) or llama-bench
  --url URL        benchmark an existing server instead of starting one
  --model ID       model ID for --url
  --no-warmup      skip the warm-up request
  --json           print the recorded measurement and raw runs

bench history options:
  --limit N        newest runs to show (default 20)
  --json           print the matching records as JSON

stats options:
  --window D       time between the two scrapes (default 1s)
  --json           print the stable machine-readable snapshot

endpoint options:
  --json           print the stable machine-readable result

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
		return cmdDoctor(cfgPath, rest)
	case "fit":
		return cmdFit(cfgPath, rest)
	case "bench":
		return cmdBench(cfgPath, rest)
	case "config":
		return cmdConfig(cfgPath, rest)
	case "list":
		return cmdList(cfgPath, rest)
	case "run", "start":
		return cmdRun(cfgPath, rest)
	case "supervise":
		return cmdSupervise(cfgPath, rest)
	case "restart":
		return cmdRestart(cfgPath, rest)
	case "stop":
		return cmdStop(cfgPath, rest)
	case "status":
		return cmdStatus(cfgPath, rest)
	case "stats":
		return cmdStats(cfgPath, rest)
	case "endpoint":
		return cmdEndpoint(cfgPath, rest)
	case "logs", "log":
		return cmdLogs(cfgPath, rest)
	case "alerts":
		return cmdAlerts(cfgPath, rest)
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
		// Everything after -- belongs to a supervised child. In particular,
		// llama-server's -c is the context size, not --config.
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
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

func cmdDoctor(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
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
	rep := doctor.Run(cfg, cfgPath)
	if *asJSON {
		if code := writeJSON(rep); code != 0 {
			return code
		}
	} else {
		fmt.Print(rep.Print())
	}
	if len(rep.Failures()) > 0 {
		return 1
	}
	return 0
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
	smoke := fs.Bool("smoke", false, "send a tiny prompt after health")
	strict := fs.Bool("strict", false, "fail and stop the server if the smoke test fails")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool run TARGET [options]")
		return 2
	}
	if *smoke && *noWait {
		fmt.Fprintln(os.Stderr, "wool: --smoke requires waiting for /health; drop --no-wait")
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	smokeEnabled := cfg.Server.SmokeTest || *smoke
	if *strict && !smokeEnabled {
		fmt.Fprintln(os.Stderr, "wool: --strict requires --smoke or [server] smoke_test = true")
		return 2
	}
	timeout := parseSmokeTimeout(cfg.Server.SmokeTimeout)
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

	// Sample after any restart so the baseline does not include the process
	// that is about to be replaced.
	var gpu sysinfo.GPU
	var baseline uint64
	var baselineKnown bool
	if snap, err := sysinfo.NewCollector().Sample(); err == nil {
		if g, ok := snap.Primary(); ok {
			gpu = g
			baseline, baselineKnown = g.VRAMUsed, g.HasVRAM
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
	var smokeRes *measure.SmokeResult
	if smokeEnabled && *noWait {
		fmt.Fprintln(os.Stderr, "wool: smoke test skipped because --no-wait was used")
	}
	if smokeEnabled && !*noWait {
		fmt.Fprintln(os.Stderr, "wool: smoke test...")
		client := llamaserver.NewClientURL(llamaserver.BaseURL(host, state.Port))
		res := measure.RunSmoke(context.Background(), client, timeout)
		smokeRes = &res
		if !res.OK() {
			fmt.Fprintf(os.Stderr, "wool: smoke test failed: %s\n", res.Err)
			if *strict {
				rec := launchRecord(cfg, resolved, gpu, bin)
				applySmokeResult(&rec, smokeRes)
				rec.At = time.Now()
				if err := measure.NewStore().Append(rec); err != nil {
					fmt.Fprintf(os.Stderr, "wool: record failed smoke test: %v\n", err)
				}
				printLogTail(mgr, state.Name, 40)
				_ = mgr.Stop(state.Name, 10*time.Second)
				return 1
			}
		} else {
			fmt.Fprintf(os.Stderr, "wool: smoke test passed (%d ms)\n", res.Latency.Milliseconds())
		}
	}
	printServer(state, preset)
	if !*noWait {
		fmt.Fprintln(os.Stderr, "wool: observing VRAM (5s, 15s)...")
		rec := launchRecord(cfg, resolved, gpu, bin)
		applySmokeResult(&rec, smokeRes)
		cap := measure.Capture{
			Store:         measure.NewStore(),
			Record:        rec,
			Baseline:      baseline,
			BaselineKnown: baselineKnown,
			Sample:        measure.SampleVRAM,
		}
		if err := cap.Run(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "wool: observation: %v\n", err)
		}
	}
	return 0
}

// cmdSupervise launches a detached supervisor for a preset or model. The
// supervisor re-execs as `wool supervise --foreground`, so the recorded state
// keeps the child argv and a later restart preserves supervision.
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
func launchRecord(cfg *config.Config, r config.Resolved, gpu sysinfo.GPU, bin string) measure.Record {
	var size uint64
	var mod time.Time
	if info, err := os.Stat(r.ModelPath); err == nil {
		size = uint64(info.Size())
		mod = info.ModTime()
	}
	meta, metaErr := gguf.ReadMeta(r.ModelPath)
	reserve := uint64(0)
	if cfg.Router.MemoryMarginMiB > 0 {
		reserve = uint64(cfg.Router.MemoryMarginMiB) << 20
	}
	return measure.NewRecord(measure.RunInfo{
		Instance:           "server",
		ModelID:            r.ModelID,
		ModelPath:          r.ModelPath,
		FileSize:           size,
		FileModTime:        mod,
		Meta:               meta,
		MetaOK:             metaErr == nil,
		Ctx:                r.Ctx,
		CacheTypeK:         r.CacheTypeK,
		CacheTypeV:         r.CacheTypeV,
		GPULayers:          r.GPULayers,
		NCPUMoE:            r.NCPUMoE,
		GPU:                gpu,
		ReserveBytes:       reserve,
		LlamaServerVersion: measure.ServerVersion(bin),
	})
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
	return waitReadyContext(context.Background(), state, cfg)
}

func waitReadyContext(ctx context.Context, state daemon.State, cfg *config.Config) error {
	timeout := durationOr(cfg.Server.StartupTimeout, 120*time.Second)
	interval := durationOr(cfg.Server.PollInterval, time.Second)
	client := llamaserver.NewClient(state.Host, state.Port)
	if err := client.WaitReady(ctx, timeout, interval); err != nil {
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
	printServerVerb(state, preset, "started")
}

func printServerVerb(state daemon.State, preset, verb string) {
	label := state.Model
	if preset != "" {
		label = preset
	}
	fmt.Printf("%s %s\n", label, verb)
	fmt.Printf("  url   %s\n", state.URL())
	if state.Supervised {
		fmt.Printf("  pid   %d (supervisor)\n", state.PID)
	} else {
		fmt.Printf("  pid   %d\n", state.PID)
	}
	fmt.Printf("  port  %d\n", state.Port)
	fmt.Printf("  log   %s\n", state.LogPath)
	if state.Supervised {
		fmt.Println("The supervisor restarts unexpected exits. Use `wool stop` for a clean stop.")
	} else {
		fmt.Println("The server keeps running after this command exits. Use `wool stop`.")
	}
}

func printLogTail(mgr *daemon.Manager, name string, n int) {
	lines, err := mgr.Tail(name, n)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\nlast %d log lines (%s):\n%s\n", len(lines), mgr.LogDir, strings.Join(lines, "\n"))
}

// cmdRestart relaunches the instance recorded in the state directory. The
// recorded argv is reused verbatim so a restart does not silently pick up
// config changes or a different port.
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

const statsUsage = `usage: wool stats [MODEL] [options]

Shows live inference rates by scraping /metrics and /slots of the running
server twice, one window apart. Without MODEL the standalone server is used
when alive, else the router's first loaded model. With MODEL the router must
be running that model.

options:
  --window D       time between the two scrapes (default 1s)
  --json           print the stable machine-readable snapshot`

type statsOptions struct {
	model  string
	window time.Duration
	asJSON bool
}

func parseStatsArgs(args []string) (statsOptions, error) {
	o := statsOptions{window: time.Second}
	setWindow := func(raw string) error {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return fmt.Errorf("--window must be a positive duration like 2s")
		}
		o.window = d
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case a == "--window":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--window needs a value")
			}
			i++
			if err := setWindow(args[i]); err != nil {
				return o, err
			}
		case strings.HasPrefix(a, "--window="):
			if err := setWindow(strings.TrimPrefix(a, "--window=")); err != nil {
				return o, err
			}
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.model != "" {
				return o, fmt.Errorf("only one MODEL is allowed")
			}
			o.model = a
		}
	}
	return o, nil
}

// statsTarget is the server a stats snapshot is scraped from.
type statsTarget struct {
	source string // "server" or "router"
	model  string // router model ID, or the server's preset/model name
	url    string
	client *llamaserver.Client
}

// scrapeModel is the model query parameter /metrics and /slots need. Only a
// router requires one.
func (t statsTarget) scrapeModel() string {
	if t.source == "router" {
		return t.model
	}
	return ""
}

// resolveStatsTarget picks what to scrape. An explicit model requires the
// router to be running it; otherwise the standalone server wins, then the
// router's first loaded model.
func resolveStatsTarget(model string) (statsTarget, error) {
	mgr := daemon.NewManager()
	if model != "" {
		inst, err := mgr.Status("router")
		if err != nil || !inst.Alive {
			return statsTarget{}, errors.New("router is not running (start it with `wool router start`)")
		}
		target, err := routerStatsTarget(inst, model)
		if err != nil {
			return statsTarget{}, err
		}
		return target, nil
	}
	if inst, err := mgr.Status("server"); err == nil && inst.Alive {
		what := inst.Model
		if inst.Preset != "" {
			what = inst.Preset
		}
		return statsTarget{source: "server", model: what, url: inst.URL(), client: llamaserver.NewClient(inst.Host, inst.Port)}, nil
	}
	if inst, err := mgr.Status("router"); err == nil && inst.Alive {
		target, err := routerStatsTarget(inst, "")
		if err != nil {
			return statsTarget{}, err
		}
		return target, nil
	}
	return statsTarget{}, errors.New("no server is running (start one with `wool run TARGET` or `wool router start`)")
}

// routerStatsTarget builds a router target. With want empty it picks the
// first loaded or sleeping model, sorted for determinism.
func routerStatsTarget(inst daemon.Instance, want string) (statsTarget, error) {
	client := llamaserver.NewClient(inst.Host, inst.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := client.Models(ctx, false)
	if err != nil {
		return statsTarget{}, fmt.Errorf("router models: %w", err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	loaded := func(status string) bool {
		return status == "loaded" || status == "sleeping"
	}
	for _, rm := range models {
		if want != "" {
			if rm.ID != want {
				continue
			}
			if !loaded(rm.Status.Value) {
				return statsTarget{}, fmt.Errorf("%s is %s on the router; load it with `wool router load %s`", want, rm.Status.Value, want)
			}
			return statsTarget{source: "router", model: want, url: inst.URL(), client: client}, nil
		}
		if loaded(rm.Status.Value) {
			return statsTarget{source: "router", model: rm.ID, url: inst.URL(), client: client}, nil
		}
	}
	if want != "" {
		return statsTarget{}, fmt.Errorf("router does not list %s", want)
	}
	return statsTarget{}, errors.New("router is running but no model is loaded")
}

// statsSnapshot is the stable `wool stats --json` output. Version 1.
type statsSnapshot struct {
	Version            int        `json:"version"`
	Source             string     `json:"source"`
	Model              string     `json:"model,omitempty"`
	URL                string     `json:"url"`
	WindowSeconds      float64    `json:"window_seconds"`
	PromptTPS          float64    `json:"prompt_tps"`
	GenTPS             float64    `json:"gen_tps"`
	PromptTokens       float64    `json:"prompt_tokens"`
	GenTokens          float64    `json:"gen_tokens"`
	LifetimePromptTPS  float64    `json:"lifetime_prompt_tps"`
	LifetimeGenTPS     float64    `json:"lifetime_gen_tps"`
	RequestsProcessing float64    `json:"requests_processing"`
	RequestsDeferred   float64    `json:"requests_deferred"`
	Slots              statsSlots `json:"slots"`
	Speculative        bool       `json:"speculative"`
	SpecAcceptPercent  float64    `json:"spec_accept_percent"`
	CounterReset       bool       `json:"counter_reset"`
}

// statsSlots is the aggregate /slots occupancy.
type statsSlots struct {
	Total  int     `json:"total"`
	Busy   int     `json:"busy"`
	KVUsed int     `json:"kv_used"`
	KVCap  int     `json:"kv_cap"`
	KVFill float64 `json:"kv_fill_percent"`
}

const statsVersion = 1

func cmdStats(cfgPath string, args []string) int {
	_ = cfgPath
	opts, err := parseStatsArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(statsUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, statsUsage)
		return 2
	}
	target, err := resolveStatsTarget(opts.model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: stats: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	snapshot, err := captureStats(ctx, target, opts.window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: stats: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(snapshot)
	}
	printStats(snapshot)
	return 0
}

// captureStats scrapes the target twice, window apart, and computes rates
// from the counter deltas. Slots come from the second scrape.
func captureStats(ctx context.Context, target statsTarget, window time.Duration) (statsSnapshot, error) {
	client := target.client
	client.HTTP.Timeout = 5 * time.Second
	first, _, reason := client.ScrapeInference(ctx, target.scrapeModel())
	if reason != "" {
		return statsSnapshot{}, errors.New(reason)
	}
	at1 := time.Now()
	select {
	case <-ctx.Done():
		return statsSnapshot{}, ctx.Err()
	case <-time.After(window):
	}
	second, slots, reason := client.ScrapeInference(ctx, target.scrapeModel())
	if reason != "" {
		return statsSnapshot{}, errors.New(reason)
	}
	at2 := time.Now()

	var rates llamaserver.Rates
	rates.Update(first, at1)
	rates.Update(second, at2)
	sum := llamaserver.SummarizeSlots(slots)
	return statsSnapshot{
		Version:            statsVersion,
		Source:             target.source,
		Model:              target.model,
		URL:                target.url,
		WindowSeconds:      at2.Sub(at1).Seconds(),
		PromptTPS:          rates.PromptTPS,
		GenTPS:             rates.GenTPS,
		PromptTokens:       rates.PromptTokens,
		GenTokens:          rates.GenTokens,
		LifetimePromptTPS:  second.PromptTPS(),
		LifetimeGenTPS:     second.PredictedTPS(),
		RequestsProcessing: second.RequestsProcessing,
		RequestsDeferred:   second.RequestsDeferred,
		Slots: statsSlots{
			Total:  sum.Total,
			Busy:   sum.Busy,
			KVUsed: sum.KVUsed,
			KVCap:  sum.KVCap,
			KVFill: sum.KVPercent(),
		},
		Speculative:       rates.SpecSeen,
		SpecAcceptPercent: rates.SpecRate,
		CounterReset:      rates.Reset,
	}, nil
}

func printStats(s statsSnapshot) {
	fmt.Printf("%s %s · %s · window %.1fs\n", s.Source, fallbackDash(s.Model), s.URL, s.WindowSeconds)
	fmt.Printf("prompt:      %.1f t/s (%s tokens)\n", s.PromptTPS, trimTokens(s.PromptTokens))
	fmt.Printf("generation: %.1f t/s (%s tokens)\n", s.GenTPS, trimTokens(s.GenTokens))
	fmt.Printf("lifetime:    %.1f prompt · %.1f gen t/s\n", s.LifetimePromptTPS, s.LifetimeGenTPS)
	fmt.Printf("requests:   %.0f processing · %.0f deferred\n", s.RequestsProcessing, s.RequestsDeferred)
	fmt.Printf("slots:       %d/%d busy · KV %s/%s tok (%.0f%%)\n",
		s.Slots.Busy, s.Slots.Total, humanTokensCLI(s.Slots.KVUsed), humanTokensCLI(s.Slots.KVCap), s.Slots.KVFill)
	if s.Speculative {
		fmt.Printf("speculative: %.0f%% accepted\n", s.SpecAcceptPercent)
	}
	if s.CounterReset {
		fmt.Println("note: a counter reset was detected (the server restarted)")
	}
}

// humanTokensCLI renders a token count with k/M suffixes.
func humanTokensCLI(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// trimTokens drops the fraction of a token count when it is integral.
func trimTokens(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

const endpointUsage = `usage: wool endpoint [server|router|MODEL] [options]

Shows the base URL, model ID and copy-paste snippets for a running instance.
Without a target the standalone server wins, then the router. MODEL must be
listed by the running router.

options:
  --json           print the stable machine-readable result`

type endpointOptions struct {
	target string
	asJSON bool
}

func parseEndpointArgs(args []string) (endpointOptions, error) {
	var o endpointOptions
	for _, a := range args {
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.target != "" {
				return o, fmt.Errorf("only one target is allowed")
			}
			o.target = a
		}
	}
	return o, nil
}

func cmdEndpoint(cfgPath string, args []string) int {
	opts, err := parseEndpointArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(endpointUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, endpointUsage)
		return 2
	}
	ep, err := resolveEndpoint(cfgPath, opts.target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: endpoint: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(ep)
	}
	printEndpoint(ep)
	return 0
}

// resolveEndpoint selects the running instance and model for the helper.
func resolveEndpoint(cfgPath, what string) (endpoint.Endpoint, error) {
	mgr := daemon.NewManager()
	switch what {
	case "server":
		inst, err := mgr.Status("server")
		if err != nil || !inst.Alive {
			return endpoint.Endpoint{}, errors.New("server is not running (start it with `wool run TARGET`)")
		}
		return endpoint.Build("server", inst.URL(), serverEndpointModel(cfgPath, inst)), nil
	case "router":
		inst, err := mgr.Status("router")
		if err != nil || !inst.Alive {
			return endpoint.Endpoint{}, errors.New("router is not running (start it with `wool router start`)")
		}
		model, err := endpointRouterModel(inst, "")
		if err != nil {
			return endpoint.Endpoint{}, err
		}
		return endpoint.Build("router", inst.URL(), model), nil
	case "":
		if inst, err := mgr.Status("server"); err == nil && inst.Alive {
			return endpoint.Build("server", inst.URL(), serverEndpointModel(cfgPath, inst)), nil
		}
		if inst, err := mgr.Status("router"); err == nil && inst.Alive {
			model, err := endpointRouterModel(inst, "")
			if err != nil {
				return endpoint.Endpoint{}, err
			}
			return endpoint.Build("router", inst.URL(), model), nil
		}
		return endpoint.Endpoint{}, errors.New("no server is running (start one with `wool run TARGET` or `wool router start`)")
	default:
		inst, err := mgr.Status("router")
		if err != nil || !inst.Alive {
			return endpoint.Endpoint{}, errors.New("router is not running (start it with `wool router start`)")
		}
		model, err := endpointRouterModel(inst, what)
		if err != nil {
			return endpoint.Endpoint{}, err
		}
		return endpoint.Build("router", inst.URL(), model), nil
	}
}

// serverEndpointModel prefers the model ID the running server actually
// reports. Older or unreachable servers fall back to the recorded config
// alias, then the recorded model ID.
func serverEndpointModel(cfgPath string, inst daemon.Instance) string {
	client := llamaserver.NewClient(inst.Host, inst.Port)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if ids, err := client.OpenAIModels(ctx); err == nil && len(ids) > 0 {
		sort.Strings(ids)
		return ids[0]
	}
	if cfg, err := loadConfig(cfgPath); err == nil && inst.Model != "" {
		if r, err := cfg.Resolve(inst.Model, nil); err == nil && r.Alias != "" {
			return r.Alias
		}
	}
	return inst.Model
}

// endpointRouterModel picks a router model. With want empty it prefers a
// resident model, then the first listed model, both sorted for determinism.
func endpointRouterModel(inst daemon.Instance, want string) (string, error) {
	client := llamaserver.NewClient(inst.Host, inst.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := client.Models(ctx, false)
	if err != nil {
		return "", fmt.Errorf("router models: %w", err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if want != "" {
		for _, rm := range models {
			if rm.ID == want {
				return rm.ID, nil
			}
		}
		return "", fmt.Errorf("router does not list %s", want)
	}
	for _, rm := range models {
		if rm.Status.Value == "loaded" || rm.Status.Value == "sleeping" {
			return rm.ID, nil
		}
	}
	if len(models) > 0 {
		return models[0].ID, nil
	}
	return "", errors.New("router is running but lists no models")
}

func printEndpoint(ep endpoint.Endpoint) {
	fmt.Printf("%s · %s\n", ep.Source, ep.BaseURL)
	fmt.Printf("  base URL  %s\n", ep.BaseURL)
	fmt.Printf("  API base  %s\n", ep.APIBase)
	fmt.Printf("  model     %s\n", ep.Model)
	for _, s := range ep.Snippets {
		fmt.Printf("\n%s\n", s.Name)
		for _, line := range strings.Split(s.Text, "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
}

// logsSnapshot is the stable --json shape for parsed request rows.
type logsSnapshot struct {
	Version    int          `json:"version"`
	Source     string       `json:"source"`
	BestEffort bool         `json:"best_effort"`
	Recognized bool         `json:"recognized"`
	Rows       []reqlog.Row `json:"rows"`
}

const logsVersion = 1

func cmdLogs(cfgPath string, args []string) int {
	_ = cfgPath
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	n := fs.Int("n", 100, "number of lines")
	follow := fs.Bool("f", false, "follow the log")
	asJSON := fs.Bool("json", false, "print parsed request rows as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *asJSON && *follow {
		fmt.Fprintln(os.Stderr, "wool: --json cannot be combined with -f")
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
	if *asJSON {
		p := reqlog.New(0)
		at := time.Now()
		for _, line := range lines {
			p.Feed(line, at)
		}
		rows := p.Rows()
		if rows == nil {
			rows = []reqlog.Row{}
		} else {
			rows = append([]reqlog.Row(nil), rows...)
		}
		model := ""
		if inst, err := mgr.Status(name); err == nil {
			model = inst.Preset
			if model == "" {
				model = inst.Model
			}
			if model == "" && inst.Kind == daemon.KindRouter {
				model = "router"
			}
		}
		for i := range rows {
			if rows[i].Model == "" {
				rows[i].Model = model
			}
		}
		return writeJSON(logsSnapshot{
			Version:    logsVersion,
			Source:     name,
			BestEffort: true,
			Recognized: p.Recognized(),
			Rows:       rows,
		})
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
	case "load-group":
		return routerLoadGroup(cfgPath, args)
	case "groups":
		return routerGroups(cfgPath)
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

func routerGroups(cfgPath string) int {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	ids := cfg.GroupIDs()
	if len(ids) == 0 {
		fmt.Println("no groups configured")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "GROUP\tMODELS")
	for _, id := range ids {
		fmt.Fprintf(w, "%s\t%s\n", id, strings.Join(cfg.Groups[id].Models, ", "))
	}
	w.Flush()
	return 0
}

func routerLoadGroup(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("router load-group", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("timeout", 120*time.Second, "how long to wait for each model")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool router load-group [--timeout D] NAME")
		return 2
	}
	name := fs.Arg(0)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	group, ok := cfg.Groups[name]
	if !ok {
		if ids := cfg.GroupIDs(); len(ids) > 0 {
			fmt.Fprintf(os.Stderr, "wool: unknown group %q (available: %s)\n", name, strings.Join(ids, ", "))
		} else {
			fmt.Fprintf(os.Stderr, "wool: unknown group %q (no groups configured)\n", name)
		}
		return 1
	}
	limit := cfg.Router.ModelsMax
	overLimit := limit > 0 && len(group.Models) > limit
	if overLimit {
		fmt.Fprintf(os.Stderr, "warning: group %s has %d models but router.models_max is %d; earlier models will be evicted\n",
			name, len(group.Models), limit)
	}
	client, err := routerClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	results := llamaserver.LoadGroup(context.Background(), client, group.Models, *timeout, 400*time.Millisecond)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL\tRESULT")
	var failed []string
	loaded := 0
	for _, r := range results {
		value := r.Status
		if r.Err != nil {
			failed = append(failed, r.Model)
			value = "failed: " + r.Err.Error()
		} else {
			loaded++
			if value == "" {
				value = "loaded"
			}
		}
		fmt.Fprintf(w, "%s\t%s\n", r.Model, value)
	}
	w.Flush()
	fmt.Printf("group %s: %d/%d loaded", name, loaded, len(results))
	if len(failed) > 0 {
		fmt.Printf("; failed: %s", strings.Join(failed, ", "))
	}
	fmt.Println()
	if overLimit {
		fmt.Printf("note: router.models_max is %d; only the last %d model(s) remain resident\n", limit, limit)
	}
	if len(failed) > 0 {
		return 1
	}
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
