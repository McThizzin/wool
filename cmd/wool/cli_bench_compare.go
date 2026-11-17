package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"wool/internal/bench"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/sysinfo"
)

const benchCompareUsage = `usage: wool bench compare A B [options]

Runs A and B alternately, starting a fresh server for each measured round.
Both targets use the same prompt, seed and generation length.

options:
  --runs N         rounds per target (default 3)
  --pp N           approximate prompt tokens (default 512)
  --tg N           generated tokens (default 128)
  --engine E       server (default) or llama-bench
  --no-warmup      skip the warm-up request
  --json           print the comparison as JSON`

func parseBenchCompareArgs(args []string) (benchOptions, []string, error) {
	o, targets, err := scanBenchArgs(args)
	if err != nil {
		return o, nil, err
	}
	if o.url != "" || o.model != "" {
		return o, nil, fmt.Errorf("--url and --model are not supported by bench compare")
	}
	if len(targets) != 2 {
		return o, nil, fmt.Errorf("bench compare needs exactly two targets")
	}
	if targets[0] == targets[1] {
		return o, nil, fmt.Errorf("bench compare needs two different targets")
	}
	return o, targets, nil
}

type compareTarget struct {
	name     string
	kind     string
	resolved config.Resolved
	preset   string
}

// benchRound is one measured round for one side of a comparison.
type benchRound struct {
	run           measure.BenchRun
	loadMS        float64
	loadKnown     bool
	gpu           sysinfo.GPU
	version       string
	configHash    string
	baseline      uint64
	baselineKnown bool
	peak          uint64
	peakKnown     bool
	delta         uint64
	deltaKnown    bool
}

// runCompareRound is a variable so tests can exercise the compare flow without
// starting a real server.
var runCompareRound = runBenchCompareRound

func cmdBenchCompare(cfgPath string, args []string) int {
	opts, names, err := parseBenchCompareArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(benchCompareUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, benchCompareUsage)
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}

	targets := make([]compareTarget, len(names))
	for i, name := range names {
		resolved, preset, err := resolveTarget(cfg, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		if _, err := os.Stat(resolved.ModelPath); err != nil {
			fmt.Fprintf(os.Stderr, "wool: model file for %s is missing: %s\nRun `wool doctor` for details.\n", name, resolved.ModelPath)
			return 1
		}
		kind := "model"
		if preset != "" {
			kind = "preset"
		}
		targets[i] = compareTarget{name: name, kind: kind, resolved: resolved, preset: preset}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverBin := config.ExpandPath(cfg.App.LlamaServer)
	var mgr *daemon.Manager
	if opts.engine == bench.EngineServer {
		tmp, err := os.MkdirTemp("", "wool-compare-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: bench compare: %v\n", err)
			return 1
		}
		defer os.RemoveAll(tmp)
		mgr = daemon.NewManagerWithDirs(filepath.Join(tmp, "servers"), filepath.Join(tmp, "logs"))
	}
	lbBin := ""
	if opts.engine == bench.EngineLlamaBench {
		lbBin, err = findLlamaBench(serverBin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
	}

	rounds := make([][]benchRound, len(targets))
	for _, idx := range interleavedOrder(opts.runs) {
		target := targets[idx]
		round, err := runCompareRound(ctx, cfg, target, opts, serverBin, mgr, lbBin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: bench compare: %s: %v\n", target.name, err)
			return 1
		}
		rounds[idx] = append(rounds[idx], round)
	}

	sideA, recA := compareSide(cfg, targets[0], rounds[0], opts, serverBin)
	sideB, recB := compareSide(cfg, targets[1], rounds[1], opts, serverBin)
	cmp := bench.Comparison{
		Version:          bench.ComparisonVersion,
		At:               time.Now(),
		Runs:             opts.runs,
		PromptTokens:     opts.pp,
		GenerationTokens: opts.tg,
		Seed:             bench.DefaultSeed,
		Interleaved:      true,
		A:                sideA,
		B:                sideB,
	}
	cmp.BuildWarnings()

	store := measure.NewStore()
	for _, rec := range []measure.Record{recA, recB} {
		if err := store.Append(rec); err != nil {
			fmt.Fprintf(os.Stderr, "wool: measurement store: %v\n", err)
			return 1
		}
	}
	if opts.asJSON {
		return writeJSON(cmp)
	}
	printBenchCompare(cmp, store.Path)
	return 0
}

// interleavedOrder alternates the target order each round so neither side
// always runs on the hotter machine.
func interleavedOrder(runs int) []int {
	order := make([]int, 0, runs*2)
	for i := 0; i < runs; i++ {
		if i%2 == 0 {
			order = append(order, 0, 1)
		} else {
			order = append(order, 1, 0)
		}
	}
	return order
}

// runBenchCompareRound measures one round for one target. Every round starts a
// fresh server so A and B can be interleaved without holding both models in
// VRAM.
func runBenchCompareRound(ctx context.Context, cfg *config.Config, target compareTarget, opts benchOptions, serverBin string, mgr *daemon.Manager, lbBin string) (benchRound, error) {
	var out benchRound
	var used uint64
	var usedKnown bool
	out.gpu, used, usedKnown = samplePrimaryGPU()
	out.baseline, out.baselineKnown = used, usedKnown

	monitor := bench.StartMonitor(measure.SampleVRAM, 100*time.Millisecond)
	defer monitor.Stop()

	if opts.engine == bench.EngineLlamaBench {
		res, err := bench.RunLlamaBench(ctx, bench.LlamaBenchOptions{
			Bin:              lbBin,
			ModelPath:        target.resolved.ModelPath,
			Runs:             1,
			PromptTokens:     opts.pp,
			GenerationTokens: opts.tg,
			Threads:          target.resolved.Threads,
			GPULayers:        target.resolved.GPULayers,
			NCPUMoE:          target.resolved.NCPUMoE,
			BatchSize:        target.resolved.BatchSize,
			UBatchSize:       target.resolved.UBatchSize,
			CacheTypeK:       target.resolved.CacheTypeK,
			CacheTypeV:       target.resolved.CacheTypeV,
			FlashAttn:        target.resolved.FlashAttn,
			NoWarmup:         opts.noWarmup,
		})
		out.peak, out.peakKnown = monitor.Stop()
		if err != nil {
			return out, err
		}
		if len(res.Runs) == 0 {
			return out, fmt.Errorf("llama-bench returned no runs")
		}
		out.run = res.Runs[0]
		out.version = res.Version
		if out.version == "" {
			out.version = measure.ServerVersion(serverBin)
		}
		out.configHash = llamaserver.ConfigHash(res.Args)
	} else {
		start := time.Now()
		state, err := mgr.Start(daemon.Spec{
			Name:     "bench",
			Kind:     daemon.KindServer,
			Preset:   target.preset,
			Model:    target.resolved.ModelID,
			Bin:      serverBin,
			Host:     cfg.Server.Host,
			Port:     0,
			AutoPort: true,
			Args: func(port int) []string {
				return llamaserver.SingleArgs(target.resolved, cfg.Server.Host, port)
			},
		})
		if err != nil {
			monitor.Stop()
			return out, err
		}
		stopped := false
		stopServer := func() {
			if stopped {
				return
			}
			stopped = true
			_ = mgr.Stop("bench", 10*time.Second)
		}
		defer stopServer()
		if err := waitReadyContext(ctx, state, cfg); err != nil {
			printLogTail(mgr, state.Name, 40)
			monitor.Stop()
			return out, err
		}
		out.loadMS = float64(time.Since(start)) / float64(time.Millisecond)
		out.loadKnown = true

		client := llamaserver.NewClient(state.Host, state.Port)
		res, runErr := (&bench.Runner{Client: client, Options: benchOneRunOptions(opts)}).Run(ctx)
		out.peak, out.peakKnown = monitor.Stop()
		stopServer()
		if runErr != nil {
			return out, runErr
		}
		if len(res.Runs) == 0 {
			return out, fmt.Errorf("no benchmark run completed")
		}
		out.run = res.Runs[0]
		out.version = measure.ServerVersion(serverBin)
		out.configHash = llamaserver.ConfigHash(llamaserver.SingleArgs(target.resolved, cfg.Server.Host, state.Port))
	}

	if out.baselineKnown && out.peakKnown && out.peak >= out.baseline {
		out.delta, out.deltaKnown = out.peak-out.baseline, true
	}
	return out, nil
}

// compareSide aggregates interleaved rounds into a report side and a stored
// benchmark record. Load is the median across rounds; VRAM comes from the round
// with the largest known delta so the stored record stays conservative.
func compareSide(cfg *config.Config, target compareTarget, rounds []benchRound, opts benchOptions, serverBin string) (bench.Side, measure.Record) {
	samples := make([]measure.BenchRun, len(rounds))
	loads := make([]float64, 0, len(rounds))
	loadKnown := len(rounds) > 0
	var rep benchRound
	for i, round := range rounds {
		samples[i] = round.run
		samples[i].Index = i + 1
		if round.loadKnown {
			loads = append(loads, round.loadMS)
		} else {
			loadKnown = false
		}
		if i == 0 || betterVRAMRound(round, rep) {
			rep = round
		}
	}

	side := bench.NewSide(samples)
	side.Target = target.name
	side.TargetKind = target.kind
	side.ModelID = target.resolved.ModelID
	side.Engine = opts.engine
	side.LoadKnown = loadKnown && len(loads) > 0
	side.LoadMS = bench.Median(loads)
	side.BaselineBytes = rep.baseline
	side.PeakBytes = rep.peak
	side.VRAMDeltaBytes = rep.delta
	side.VRAMKnown = rep.deltaKnown
	side.GPUName = rep.gpu.Name
	side.GPUPCIID = rep.gpu.PCIID
	side.GPUDriver = rep.gpu.Driver
	side.LlamaServerVersion = rep.version
	side.ConfigHash = rep.configHash

	rec := benchRecordInput{
		cfg:           cfg,
		resolved:      target.resolved,
		modelID:       target.name,
		gpu:           rep.gpu,
		bin:           serverBin,
		target:        target.name,
		targetKind:    target.kind,
		engine:        opts.engine,
		version:       rep.version,
		configHash:    rep.configHash,
		opts:          opts,
		runs:          samples,
		median:        side.Median,
		min:           side.Min,
		max:           side.Max,
		loadMS:        side.LoadMS,
		loadKnown:     side.LoadKnown,
		baseline:      rep.baseline,
		baselineKnown: rep.baselineKnown,
		peak:          rep.peak,
		peakKnown:     rep.peakKnown,
	}.record()
	return side, rec
}
func betterVRAMRound(a, b benchRound) bool {
	if a.deltaKnown != b.deltaKnown {
		return a.deltaKnown
	}
	if a.deltaKnown {
		return a.delta > b.delta
	}
	return a.peakKnown && (!b.peakKnown || a.peak > b.peak)
}
func benchOneRunOptions(o benchOptions) bench.Options {
	round := benchRunOptions(o)
	round.Runs = 1
	return round
}
func printBenchCompare(cmp bench.Comparison, storePath string) {
	fmt.Printf("A/B comparison · %s · %d rounds per target · interleaved\n",
		fallbackDash(cmp.A.Engine), cmp.Runs)
	fmt.Printf("prompt %d tok · generation %d tok · seed %d\n\n",
		cmp.PromptTokens, cmp.GenerationTokens, cmp.Seed)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tA\tB")
	fmt.Fprintf(w, "target\t%s\t%s\n", fallbackDash(cmp.A.Target), fallbackDash(cmp.B.Target))
	fmt.Fprintf(w, "model\t%s\t%s\n", fallbackDash(cmp.A.ModelID), fallbackDash(cmp.B.ModelID))
	fmt.Fprintf(w, "config hash\t%s\t%s\n", fallbackDash(cmp.A.ConfigHash), fallbackDash(cmp.B.ConfigHash))
	fmt.Fprintf(w, "llama-server\t%s\t%s\n", fallbackDash(cmp.A.LlamaServerVersion), fallbackDash(cmp.B.LlamaServerVersion))
	fmt.Fprintf(w, "gpu\t%s\t%s\n", compareGPULabel(cmp.A), compareGPULabel(cmp.B))
	w.Flush()

	fmt.Println()
	w = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "METRIC\tA MEDIAN\tA SPREAD\tB MEDIAN\tB SPREAD\tDELTA")
	fmt.Fprintf(w, "prompt tok/s\t%.1f\t%s\t%.1f\t%s\t%s\n",
		cmp.A.Median.PromptTPS, compareSpread(cmp.A.Spread.PromptTPS),
		cmp.B.Median.PromptTPS, compareSpread(cmp.B.Spread.PromptTPS),
		compareDelta(cmp.A.Median.PromptTPS, cmp.B.Median.PromptTPS))
	fmt.Fprintf(w, "generation tok/s\t%.1f\t%s\t%.1f\t%s\t%s\n",
		cmp.A.Median.GenerationTPS, compareSpread(cmp.A.Spread.GenerationTPS),
		cmp.B.Median.GenerationTPS, compareSpread(cmp.B.Spread.GenerationTPS),
		compareDelta(cmp.A.Median.GenerationTPS, cmp.B.Median.GenerationTPS))
	fmt.Fprintf(w, "total ms\t%.0f\t-\t%.0f\t-\t%s\n",
		cmp.A.Median.TotalMS, cmp.B.Median.TotalMS,
		compareDelta(cmp.A.Median.TotalMS, cmp.B.Median.TotalMS))
	fmt.Fprintf(w, "load\t%s\t-\t%s\t-\t-\n", compareLoad(cmp.A), compareLoad(cmp.B))
	fmt.Fprintf(w, "vram delta\t%s\t-\t%s\t-\t-\n", compareVRAM(cmp.A), compareVRAM(cmp.B))
	w.Flush()

	fmt.Println("\nraw runs (generation tok/s)")
	fmt.Printf("  A  %s\n", compareSamples(cmp.A.Samples))
	fmt.Printf("  B  %s\n", compareSamples(cmp.B.Samples))

	if len(cmp.Warnings) > 0 {
		fmt.Println("\nwarnings")
		for _, warning := range cmp.Warnings {
			fmt.Println("  - " + warning)
		}
	} else {
		fmt.Println("\nno noise or environment warnings")
	}
	fmt.Printf("\nstored: %s\n", storePath)
}
func compareSpread(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}
func compareDelta(base, next float64) string {
	if base <= 0 {
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", bench.Delta(base, next))
}
func compareLoad(s bench.Side) string {
	if !s.LoadKnown {
		return "-"
	}
	return fmt.Sprintf("%.1fs", s.LoadMS/1000)
}
func compareVRAM(s bench.Side) string {
	switch {
	case s.VRAMKnown:
		return humanSize(s.VRAMDeltaBytes)
	case s.PeakBytes > 0:
		return "~" + humanSize(s.PeakBytes)
	default:
		return "-"
	}
}
func compareGPULabel(s bench.Side) string {
	switch {
	case s.GPUName != "" && s.GPUPCIID != "":
		return s.GPUName + " (" + s.GPUPCIID + ")"
	case s.GPUName != "":
		return s.GPUName
	case s.GPUPCIID != "":
		return s.GPUPCIID
	default:
		return "-"
	}
}
func compareSamples(samples []measure.BenchRun) string {
	if len(samples) == 0 {
		return "-"
	}
	parts := make([]string, len(samples))
	for i, run := range samples {
		parts[i] = fmt.Sprintf("%.1f", run.GenerationTPS)
	}
	return strings.Join(parts, "  ")
}
