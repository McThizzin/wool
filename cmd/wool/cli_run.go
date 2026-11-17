package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/gguf"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/sysinfo"
)

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
