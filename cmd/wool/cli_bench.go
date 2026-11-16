package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"wool/internal/bench"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/sysinfo"
)

const benchUsage = `usage: wool bench TARGET [options]
       wool bench --url URL --model ID [options]
       wool bench history [TARGET] [options]
       wool bench compare A B [options]

options:
  --runs N         repetitions (default 3)
  --pp N           approximate prompt tokens (default 512)
  --tg N           generated tokens (default 128)
  --engine E       server (default) or llama-bench
  --url URL        benchmark an existing server instead of starting one
  --model ID       model ID for --url
  --no-warmup      skip the warm-up request
  --json           print the recorded measurement and raw runs

history options:
  --limit N        newest runs to show (default 20)
  --json           print the matching records as JSON`

type benchOptions struct {
	target   string
	runs     int
	pp       int
	tg       int
	engine   string
	url      string
	model    string
	noWarmup bool
	asJSON   bool
}

func (o *benchOptions) set(name, value string) error {
	switch name {
	case "--runs":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("--runs must be a positive integer")
		}
		o.runs = n
	case "--pp":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("--pp must be a non-negative integer")
		}
		o.pp = n
	case "--tg":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("--tg must be a positive integer")
		}
		o.tg = n
	case "--engine":
		switch value {
		case bench.EngineServer, bench.EngineLlamaBench:
			o.engine = value
		default:
			return fmt.Errorf("--engine must be %s or %s", bench.EngineServer, bench.EngineLlamaBench)
		}
	case "--url":
		o.url = strings.TrimSpace(value)
	case "--model":
		o.model = strings.TrimSpace(value)
	default:
		return fmt.Errorf("unknown option %q", name)
	}
	return nil
}

// scanBenchArgs parses bench flags shared by `bench` and `bench compare` and
// returns the positional targets without enforcing how many are allowed.
func scanBenchArgs(args []string) (benchOptions, []string, error) {
	o := benchOptions{
		runs:   bench.DefaultRuns,
		pp:     bench.DefaultPromptTokens,
		tg:     bench.DefaultGenerationTokens,
		engine: bench.EngineServer,
	}
	var targets []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "--no-warmup":
			o.noWarmup = true
		case a == "-h" || a == "--help":
			return o, targets, flag.ErrHelp
		case a == "--runs" || a == "--pp" || a == "--tg" || a == "--engine" ||
			a == "--url" || a == "--model":
			if i+1 >= len(args) {
				return o, targets, fmt.Errorf("%s needs a value", a)
			}
			i++
			if err := o.set(a, args[i]); err != nil {
				return o, targets, err
			}
		case strings.HasPrefix(a, "--runs="), strings.HasPrefix(a, "--pp="),
			strings.HasPrefix(a, "--tg="), strings.HasPrefix(a, "--engine="),
			strings.HasPrefix(a, "--url="), strings.HasPrefix(a, "--model="):
			name, value, _ := strings.Cut(a, "=")
			if err := o.set(name, value); err != nil {
				return o, targets, err
			}
		case strings.HasPrefix(a, "-"):
			return o, targets, fmt.Errorf("unknown option %q", a)
		default:
			targets = append(targets, a)
		}
	}
	return o, targets, nil
}
func parseBenchArgs(args []string) (benchOptions, error) {
	o, targets, err := scanBenchArgs(args)
	if err != nil {
		return o, err
	}
	if len(targets) > 1 {
		return o, fmt.Errorf("only one TARGET is allowed")
	}
	if len(targets) == 1 {
		o.target = targets[0]
	}
	if o.url != "" {
		if o.target != "" {
			return o, fmt.Errorf("--url cannot be combined with TARGET")
		}
		if o.model == "" {
			return o, fmt.Errorf("--url requires --model")
		}
		if o.engine != bench.EngineServer {
			return o, fmt.Errorf("--engine %s cannot be used with --url", bench.EngineLlamaBench)
		}
		if !strings.HasPrefix(o.url, "http://") && !strings.HasPrefix(o.url, "https://") {
			return o, fmt.Errorf("--url must start with http:// or https://")
		}
		return o, nil
	}
	if o.target == "" {
		return o, fmt.Errorf("TARGET is required (or use --url with --model)")
	}
	if o.model != "" {
		return o, fmt.Errorf("--model is only used with --url")
	}
	return o, nil
}
func benchRunOptions(o benchOptions) bench.Options {
	return bench.Options{
		Runs:             o.runs,
		PromptTokens:     o.pp,
		GenerationTokens: o.tg,
		Seed:             bench.DefaultSeed,
		Warmup:           !o.noWarmup,
	}
}

type benchRecordInput struct {
	cfg      *config.Config
	resolved config.Resolved
	modelID  string
	gpu      sysinfo.GPU
	bin      string

	target     string
	targetKind string

	engine     string
	attached   bool
	url        string
	version    string
	configHash string

	opts benchOptions

	runs   []measure.BenchRun
	median measure.BenchStats
	min    measure.BenchStats
	max    measure.BenchStats

	loadMS    float64
	loadKnown bool

	baseline      uint64
	baselineKnown bool
	peak          uint64
	peakKnown     bool
	own           uint64
	ownKnown      bool
}

func (in benchRecordInput) record() measure.Record {
	var rec measure.Record
	if in.resolved.ModelID != "" {
		rec = launchRecord(in.cfg, in.resolved, in.gpu, in.bin)
		rec.Instance = "bench"
	} else {
		rec = measure.Record{
			Version:   measure.Version,
			ModelID:   in.modelID,
			GPUName:   in.gpu.Name,
			GPUPCIID:  in.gpu.PCIID,
			GPUDriver: in.gpu.Driver,
		}
	}
	rec.Kind = measure.KindBench
	rec.Target = in.target
	rec.TargetKind = in.targetKind
	rec.ConfigHash = in.configHash
	if in.version != "" {
		rec.LlamaServerVersion = in.version
	}

	delta, deltaKnown := uint64(0), false
	if in.baselineKnown && in.peakKnown && in.peak >= in.baseline {
		delta, deltaKnown = in.peak-in.baseline, true
	}
	rec.BaselineBytes = in.baseline
	rec.BaselineKnown = deltaKnown
	rec.ObservedBytes = delta
	rec.Bench = &measure.Bench{
		Engine:           in.engine,
		Attached:         in.attached,
		URL:              in.url,
		Runs:             len(in.runs),
		Warmup:           !in.opts.noWarmup,
		PromptTokens:     in.opts.pp,
		GenerationTokens: in.opts.tg,
		Seed:             bench.DefaultSeed,
		LoadMS:           in.loadMS,
		LoadKnown:        in.loadKnown,
		BaselineBytes:    in.baseline,
		PeakBytes:        in.peak,
		VRAMDeltaBytes:   delta,
		VRAMKnown:        deltaKnown,
		OwnBytes:         in.own,
		OwnKnown:         in.ownKnown,
		Samples:          in.runs,
		Median:           in.median,
		Min:              in.min,
		Max:              in.max,
	}
	return rec
}
func cmdBench(cfgPath string, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "history":
			return cmdBenchHistory(args[1:])
		case "compare":
			return cmdBenchCompare(cfgPath, args[1:])
		}
	}
	opts, err := parseBenchArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(benchUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, benchUsage)
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		// Benchmarking an explicit URL does not need a wool config. An
		// invalid config still fails loudly so a typo is not ignored.
		if opts.url == "" || !errors.Is(err, os.ErrNotExist) {
			printConfigError(cfgPath, err)
			return 1
		}
		cfg = &config.Config{}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var resolved config.Resolved
	preset := ""
	target := opts.target
	targetKind := ""
	if opts.url == "" {
		resolved, preset, err = resolveTarget(cfg, opts.target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		targetKind = "model"
		if preset != "" {
			targetKind = "preset"
		}
		if _, err := os.Stat(resolved.ModelPath); err != nil {
			fmt.Fprintf(os.Stderr, "wool: model file for %s is missing: %s\nRun `wool doctor` for details.\n", opts.target, resolved.ModelPath)
			return 1
		}
	} else {
		target = opts.model
		targetKind = "url"
		// The model may not be in the config, but if it is, the record can
		// still carry the estimate and hardware shape.
		if _, ok := cfg.Models[opts.model]; ok {
			if r, p, err := resolveTarget(cfg, opts.model); err == nil {
				resolved, preset = r, p
			}
		}
	}

	serverBin := config.ExpandPath(cfg.App.LlamaServer)
	var (
		gpu                                sysinfo.GPU
		runs                               []measure.BenchRun
		median, min, max                   measure.BenchStats
		engine                             = opts.engine
		attached                           bool
		url                                string
		loadMS                             float64
		loadKnown                          bool
		baseline, peak, own                uint64
		baselineKnown, peakKnown, ownKnown bool
		version, configHash                string
	)

	switch {
	case opts.url != "":
		engine = bench.EngineServer
		attached = true
		client := llamaserver.NewClientURL(opts.url)
		url = client.BaseURL
		gpu, _, _ = samplePrimaryGPU()
		monitor := bench.StartMonitor(measure.SampleVRAM, 100*time.Millisecond)
		defer monitor.Stop()
		res, runErr := (&bench.Runner{Client: client, Options: benchRunOptions(opts)}).Run(ctx)
		peak, peakKnown = monitor.Stop()
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "wool: bench: %v\n", runErr)
			return 1
		}
		runs, median, min, max = res.Runs, res.Median, res.Min, res.Max
		version = benchServerVersion(ctx, client, "")

	case opts.engine == bench.EngineLlamaBench:
		lbBin, err := findLlamaBench(serverBin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		var used uint64
		var usedKnown bool
		gpu, used, usedKnown = samplePrimaryGPU()
		baseline, baselineKnown = used, usedKnown
		monitor := bench.StartMonitor(measure.SampleVRAM, 100*time.Millisecond)
		defer monitor.Stop()
		lres, runErr := bench.RunLlamaBench(ctx, bench.LlamaBenchOptions{
			Bin:              lbBin,
			ModelPath:        resolved.ModelPath,
			Runs:             opts.runs,
			PromptTokens:     opts.pp,
			GenerationTokens: opts.tg,
			Threads:          resolved.Threads,
			GPULayers:        resolved.GPULayers,
			NCPUMoE:          resolved.NCPUMoE,
			BatchSize:        resolved.BatchSize,
			UBatchSize:       resolved.UBatchSize,
			CacheTypeK:       resolved.CacheTypeK,
			CacheTypeV:       resolved.CacheTypeV,
			FlashAttn:        resolved.FlashAttn,
			NoWarmup:         opts.noWarmup,
		})
		peak, peakKnown = monitor.Stop()
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "wool: bench: %v\n", runErr)
			return 1
		}
		runs, median, min, max = lres.Runs, lres.Median, lres.Min, lres.Max
		version = lres.Version
		if version == "" {
			version = measure.ServerVersion(serverBin)
		}
		configHash = llamaserver.ConfigHash(lres.Args)

	default:
		mgr := daemon.NewManager()
		inst, statusErr := mgr.Status("server")
		if statusErr == nil && inst.Alive && benchServerMatches(cfg, inst, resolved) {
			attached = true
			url = inst.URL()
			client := llamaserver.NewClient(inst.Host, inst.Port)
			if err := waitReadyContext(ctx, inst.State, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "wool: bench: %v\n", err)
				return 1
			}
			var used uint64
			var usedKnown bool
			gpu, used, usedKnown = samplePrimaryGPU()
			baseline, baselineKnown, own, ownKnown = attachedBaseline(used, usedKnown, inst.PID)
			monitor := bench.StartMonitor(measure.SampleVRAM, 100*time.Millisecond)
			defer monitor.Stop()
			res, runErr := (&bench.Runner{Client: client, Options: benchRunOptions(opts)}).Run(ctx)
			peak, peakKnown = monitor.Stop()
			if runErr != nil {
				fmt.Fprintf(os.Stderr, "wool: bench: %v\n", runErr)
				return 1
			}
			runs, median, min, max = res.Runs, res.Median, res.Min, res.Max
			version = measure.ServerVersion(inst.Bin)
			if version == "" {
				version = benchServerVersion(ctx, client, "")
			}
			configHash = llamaserver.ConfigHash(inst.Args)
			fmt.Fprintf(os.Stderr, "wool: attached to the managed server at %s\n", url)
		} else {
			tmp, err := os.MkdirTemp("", "wool-bench-")
			if err != nil {
				fmt.Fprintf(os.Stderr, "wool: bench: %v\n", err)
				return 1
			}
			defer os.RemoveAll(tmp)
			bmgr := daemon.NewManagerWithDirs(filepath.Join(tmp, "servers"), filepath.Join(tmp, "logs"))
			var used uint64
			var usedKnown bool
			gpu, used, usedKnown = samplePrimaryGPU()
			baseline, baselineKnown = used, usedKnown
			monitor := bench.StartMonitor(measure.SampleVRAM, 100*time.Millisecond)
			defer monitor.Stop()

			start := time.Now()
			state, err := bmgr.Start(daemon.Spec{
				Name:     "bench",
				Kind:     daemon.KindServer,
				Preset:   preset,
				Model:    resolved.ModelID,
				Bin:      serverBin,
				Host:     cfg.Server.Host,
				Port:     0,
				AutoPort: true,
				Args: func(port int) []string {
					return llamaserver.SingleArgs(resolved, cfg.Server.Host, port)
				},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "wool: bench: %v\n", err)
				return 1
			}
			stopped := false
			stopServer := func() {
				if stopped {
					return
				}
				stopped = true
				_ = bmgr.Stop("bench", 10*time.Second)
			}
			defer stopServer()
			if err := waitReadyContext(ctx, state, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "wool: bench: %v\n", err)
				printLogTail(bmgr, state.Name, 40)
				return 1
			}
			loadMS = float64(time.Since(start)) / float64(time.Millisecond)
			loadKnown = true
			url = state.URL()

			client := llamaserver.NewClient(state.Host, state.Port)
			res, runErr := (&bench.Runner{Client: client, Options: benchRunOptions(opts)}).Run(ctx)
			peak, peakKnown = monitor.Stop()
			stopServer()
			if runErr != nil {
				fmt.Fprintf(os.Stderr, "wool: bench: %v\n", runErr)
				return 1
			}
			runs, median, min, max = res.Runs, res.Median, res.Min, res.Max
			version = measure.ServerVersion(serverBin)
			configHash = llamaserver.ConfigHash(llamaserver.SingleArgs(resolved, cfg.Server.Host, state.Port))
		}
	}

	rec := benchRecordInput{
		cfg:           cfg,
		resolved:      resolved,
		modelID:       target,
		gpu:           gpu,
		bin:           serverBin,
		target:        target,
		targetKind:    targetKind,
		engine:        engine,
		attached:      attached,
		url:           url,
		version:       version,
		configHash:    configHash,
		opts:          opts,
		runs:          runs,
		median:        median,
		min:           min,
		max:           max,
		loadMS:        loadMS,
		loadKnown:     loadKnown,
		baseline:      baseline,
		baselineKnown: baselineKnown,
		peak:          peak,
		peakKnown:     peakKnown,
		own:           own,
		ownKnown:      ownKnown,
	}.record()

	store := measure.NewStore()
	if err := store.Append(rec); err != nil {
		fmt.Fprintf(os.Stderr, "wool: measurement store: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(rec)
	}
	printBench(rec, store.Path)
	return 0
}
func printBench(rec measure.Record, storePath string) {
	b := rec.Bench
	if b == nil {
		return
	}
	label := rec.Target
	if label == "" {
		label = rec.ModelID
	}
	kind := rec.TargetKind
	if kind == "" {
		kind = "model"
	}
	fmt.Printf("%s (%s) · model %s · %s\n", label, kind, rec.ModelID, b.Engine)
	mode := "ephemeral server"
	if b.Attached {
		mode = "attached server"
	}
	if b.URL != "" {
		mode += " " + b.URL
	}
	fmt.Printf("%s · runs %d · warmup %t · prompt %d tok · generation %d tok · seed %d\n",
		mode, b.Runs, b.Warmup, b.PromptTokens, b.GenerationTokens, b.Seed)
	if b.LoadKnown {
		fmt.Printf("load: %.2fs\n", b.LoadMS/1000)
	}
	switch {
	case b.VRAMKnown:
		fmt.Printf("peak VRAM delta: %s", humanSize(b.VRAMDeltaBytes))
		if b.OwnKnown {
			fmt.Printf(" (server holds %s)", humanSize(b.OwnBytes))
		}
		fmt.Println()
	case b.PeakBytes > 0:
		fmt.Printf("peak VRAM: %s (baseline unknown)\n", humanSize(b.PeakBytes))
	}
	fmt.Printf("prompt tok/s:     median %.1f · min %.1f · max %.1f\n",
		b.Median.PromptTPS, b.Min.PromptTPS, b.Max.PromptTPS)
	fmt.Printf("generation tok/s: median %.1f · min %.1f · max %.1f\n",
		b.Median.GenerationTPS, b.Min.GenerationTPS, b.Max.GenerationTPS)
	fmt.Printf("total time:       median %.2fs · min %.2fs · max %.2fs\n",
		b.Median.TotalMS/1000, b.Min.TotalMS/1000, b.Max.TotalMS/1000)
	if rec.ConfigHash != "" {
		fmt.Printf("config hash: %s\n", rec.ConfigHash)
	}
	fmt.Printf("stored: %s\n", storePath)
}
func samplePrimaryGPU() (sysinfo.GPU, uint64, bool) {
	snap, err := sysinfo.NewCollector().Sample()
	if err != nil {
		return sysinfo.GPU{}, 0, false
	}
	gpu, ok := snap.Primary()
	if !ok {
		return sysinfo.GPU{}, 0, false
	}
	return gpu, gpu.VRAMUsed, gpu.HasVRAM
}

// attachedBaseline removes the running server's own VRAM so the delta measures
// the model rather than the whole card. It is deliberately conservative: an
// unknown per-process footprint means no delta at all.
func attachedBaseline(used uint64, usedKnown bool, pid int) (baseline uint64, known bool, own uint64, ownKnown bool) {
	if !usedKnown {
		return 0, false, 0, false
	}
	own, ownKnown = sysinfo.ProcessVRAM(pid)
	if ownKnown && used >= own {
		return used - own, true, own, true
	}
	return 0, false, own, ownKnown
}
func benchServerMatches(cfg *config.Config, inst daemon.Instance, r config.Resolved) bool {
	if inst.Model != r.ModelID || len(inst.Args) == 0 {
		return false
	}
	expected := llamaserver.SingleArgs(r, cfg.Server.Host, inst.Port)
	return llamaserver.ConfigHash(inst.Args) == llamaserver.ConfigHash(expected)
}
func benchServerVersion(ctx context.Context, client *llamaserver.Client, bin string) string {
	if bin != "" {
		if v := measure.ServerVersion(bin); v != "" {
			return v
		}
	}
	props, err := client.Props(ctx)
	if err != nil {
		return ""
	}
	for _, key := range []string{"build_info", "build", "version"} {
		if s, ok := props[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func findLlamaBench(serverBin string) (string, error) {
	if serverBin != "" {
		resolved := serverBin
		if p, err := exec.LookPath(serverBin); err == nil {
			resolved = p
		}
		sibling := filepath.Join(filepath.Dir(resolved), "llama-bench")
		if p, err := exec.LookPath(sibling); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("llama-bench"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("llama-bench not found next to %s or on PATH; install the llama.cpp tools or use --engine %s",
		serverBin, bench.EngineServer)
}
