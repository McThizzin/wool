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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"wool/internal/app"
	"wool/internal/bench"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/doctor"
	"wool/internal/fit"
	"wool/internal/gguf"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/sysinfo"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

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
  wool stop [server|router|all]        stop detached servers
  wool status [name] [--json]          show detached server state
  wool logs [name] [-n N] [-f]         show a server log
  wool router [start|stop|status|models|load ID|unload ID|reload|ini]
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

Router options are accepted after "router start": --dry-run, --restart,
--port N, --no-wait.

fit options:
  --vram SIZE      total card size (7G, 7GiB); default is the live GPU budget
  --cache-type T   override cache_type_k and cache_type_v
  --gpu-layers N   override gpu_layers
  --n-cpu-moe N    override n_cpu_moe
  --save-preset NAME  write the recommendation as presets.NAME
  --force          replace an existing preset
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

const fitUsage = `usage: wool fit TARGET [options]

options:
  --vram SIZE      total card size (7G, 7GiB); default is the live GPU budget
  --cache-type T   override cache_type_k and cache_type_v
  --gpu-layers N   override gpu_layers
  --n-cpu-moe N    override n_cpu_moe
  --save-preset NAME  write the recommendation as presets.NAME
  --force          replace an existing preset
  --calibrated     apply observed/estimate correction and label it
  --json           print the stable machine-readable result`

type fitOptions struct {
	target     string
	vram       string
	cacheType  string
	gpuLayers  *int
	nCPUMoE    *int
	savePreset string
	force      bool
	calibrated bool
	asJSON     bool
}

func (o *fitOptions) set(name, value string) error {
	switch name {
	case "--vram":
		o.vram = value
	case "--cache-type":
		o.cacheType = value
	case "--gpu-layers":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("--gpu-layers must be a non-negative integer")
		}
		o.gpuLayers = &n
	case "--n-cpu-moe":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("--n-cpu-moe must be a non-negative integer")
		}
		o.nCPUMoE = &n
	case "--save-preset":
		o.savePreset = value
	default:
		return fmt.Errorf("unknown option %q", name)
	}
	return nil
}

func parseFitArgs(args []string) (fitOptions, error) {
	var o fitOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "--force":
			o.force = true
		case a == "--calibrated":
			o.calibrated = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case a == "--vram" || a == "--cache-type" || a == "--gpu-layers" ||
			a == "--n-cpu-moe" || a == "--save-preset":
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a value", a)
			}
			i++
			if err := o.set(a, args[i]); err != nil {
				return o, err
			}
		case strings.HasPrefix(a, "--vram="), strings.HasPrefix(a, "--cache-type="),
			strings.HasPrefix(a, "--gpu-layers="), strings.HasPrefix(a, "--n-cpu-moe="),
			strings.HasPrefix(a, "--save-preset="):
			name, value, _ := strings.Cut(a, "=")
			if err := o.set(name, value); err != nil {
				return o, err
			}
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.target != "" {
				return o, fmt.Errorf("only one TARGET is allowed")
			}
			o.target = a
		}
	}
	if o.target == "" {
		return o, fmt.Errorf("TARGET is required")
	}
	if o.force && o.savePreset == "" {
		return o, fmt.Errorf("--force requires --save-preset")
	}
	return o, nil
}

func cmdFit(cfgPath string, args []string) int {
	opts, err := parseFitArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(fitUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, fitUsage)
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	r, _, kind, err := resolveNamedTarget(cfg, opts.target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	info, err := os.Stat(r.ModelPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: model file for %s is missing: %s\nRun `wool doctor` for details.\n", opts.target, r.ModelPath)
		return 1
	}
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "wool: model path for %s is a directory: %s\n", opts.target, r.ModelPath)
		return 1
	}
	meta, err := gguf.ReadMeta(r.ModelPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: cannot read metadata for %s: %v\nRun `wool doctor` for details.\n", r.ModelPath, err)
		return 1
	}
	b, err := fitBudget(cfg, opts.vram)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}

	settings := fit.Settings{
		Ctx:        r.Ctx,
		GPULayers:  r.GPULayers,
		NCPUMoE:    r.NCPUMoE,
		CacheTypeK: r.CacheTypeK,
		CacheTypeV: r.CacheTypeV,
	}
	if opts.cacheType != "" {
		settings.CacheTypeK, settings.CacheTypeV = opts.cacheType, opts.cacheType
	}
	if opts.gpuLayers != nil {
		settings.GPULayers = *opts.gpuLayers
	}
	if opts.nCPUMoE != nil {
		settings.NCPUMoE = *opts.nCPUMoE
	}

	res, err := fit.Solve(fit.Request{
		ModelID:   r.ModelID,
		ModelPath: r.ModelPath,
		Meta:      meta,
		FileBytes: uint64(info.Size()),
		Budget:    b,
		Settings:  settings,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: fit %s: %v\n", opts.target, err)
		return 1
	}
	res.Target, res.Kind = opts.target, kind
	if opts.calibrated {
		res.Calibrated = true
		applyFitCalibration(&res, cfg, uint64(info.Size()), info.ModTime())
	}
	if opts.savePreset != "" {
		rec := res.Recommendation
		err := cfg.SavePreset(cfgPath, opts.savePreset, r.ModelID, config.PresetFit{
			Ctx:        rec.Ctx,
			GPULayers:  rec.GPULayers,
			NCPUMoE:    rec.NCPUMoE,
			CacheTypeK: rec.CacheTypeK,
			CacheTypeV: rec.CacheTypeV,
		}, opts.force)
		if errors.Is(err, config.ErrPresetExists) {
			fmt.Fprintf(os.Stderr, "wool: %v; pass --force to replace it\n", err)
			return 1
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "wool: save preset: %v\n", err)
			return 1
		}
		res.SavedPreset = opts.savePreset
	}
	if opts.asJSON {
		return writeJSON(res)
	}
	printFit(res)
	return 0
}

// applyFitCalibration attaches the measured correction to each recommendation.
// The raw estimate and solver verdict stay untouched; the calibrated view is
// additional information that the caller explicitly requested.
func applyFitCalibration(res *fit.Result, cfg *config.Config, size uint64, modTime time.Time) {
	store := measure.NewStore()
	id := measure.Identity{
		ModelSize:          size,
		ModelModTime:       modTime,
		LlamaServerVersion: measure.ServerVersion(config.ExpandPath(cfg.App.LlamaServer)),
	}
	if snap, err := sysinfo.NewCollector().Sample(); err == nil {
		if gpu, ok := snap.Primary(); ok {
			id.GPUPCIID = gpu.PCIID
			id.GPUDriver = gpu.Driver
		}
	}
	apply := func(rec *fit.Recommendation) {
		shape := measure.Shape{
			ModelID:    res.ModelID,
			Ctx:        rec.Ctx,
			CacheTypeK: rec.CacheTypeK,
			CacheTypeV: rec.CacheTypeV,
			GPULayers:  rec.GPULayers,
			NCPUMoE:    rec.NCPUMoE,
		}
		cal, ok := store.Calibration(shape, id)
		if !ok {
			return
		}
		rec.Calibration = &cal
		if !cal.OK() {
			return
		}
		rec.Calibrated = true
		rec.Corrected = cal.Corrected(rec.Estimate.Total)
		rec.CorrectedHeadroom = int64(res.Budget.Available) - int64(rec.Corrected)
	}
	apply(&res.Recommendation)
	for i := range res.Alternatives {
		apply(&res.Alternatives[i])
	}
}

// fitBudget derives the budget for a fit. An explicit --vram is the total card
// size and deliberately ignores live other usage so repeated runs are stable.
func fitBudget(cfg *config.Config, vram string) (gguf.Budget, error) {
	reserve := uint64(0)
	if cfg.Router.MemoryMarginMiB > 0 {
		reserve = uint64(cfg.Router.MemoryMarginMiB) << 20
	}
	if vram != "" {
		total, err := fit.ParseSize(vram)
		if err != nil {
			return gguf.Budget{}, fmt.Errorf("--vram: %w", err)
		}
		b := gguf.ComputeBudget(total, 0, reserve, 0, false)
		if !b.Known || b.Available == 0 {
			return b, fmt.Errorf("--vram %s leaves no room after the %s reserve", vram, humanSize(reserve))
		}
		return b, nil
	}

	snap, err := sysinfo.NewCollector().Sample()
	if err != nil {
		return gguf.Budget{}, fmt.Errorf("cannot read GPU telemetry: %v; pass --vram SIZE", err)
	}
	gpu, ok := snap.Primary()
	if !ok || !gpu.HasVRAM || gpu.VRAMTotal == 0 {
		return gguf.Budget{}, fmt.Errorf("no GPU VRAM detected; pass --vram SIZE (for example --vram 7G)")
	}
	var own uint64
	var ownKnown bool
	if inst, err := daemon.NewManager().Status("server"); err == nil && inst.Alive {
		own, ownKnown = sysinfo.ProcessVRAM(inst.PID)
	}
	b := gguf.ComputeBudget(gpu.VRAMTotal, gpu.VRAMUsed, reserve, own, ownKnown)
	if !b.Known || b.Available == 0 {
		return b, fmt.Errorf("no VRAM is available after the %s reserve and current GPU usage; free VRAM or pass --vram SIZE", humanSize(reserve))
	}
	return b, nil
}

func printFit(res fit.Result) {
	head := fmt.Sprintf("%s (%s) · model %s", res.Target, res.Kind, res.ModelID)
	if res.Calibrated {
		head += " · calibrated"
	}
	fmt.Println(head)
	fmt.Printf("path: %s\n", res.ModelPath)
	if res.Budget.Known {
		fmt.Printf("budget: %s available of %s · reserve %s\n",
			humanSize(res.Budget.Available), humanSize(res.Budget.Total), humanSize(res.Budget.Reserve))
		if res.Budget.OwnKnown && res.Budget.OwnUsed > 0 {
			fmt.Printf("server own usage excluded: %s\n", humanSize(res.Budget.OwnUsed))
		}
	}
	fmt.Printf("native context: %d · minimum useful: %d\n", res.NativeContext, res.MinimumContext)
	if res.SavedPreset != "" {
		fmt.Printf("saved preset: %s\n", res.SavedPreset)
	}
	fmt.Printf("requested: ctx %d · gpu_layers %d · n_cpu_moe %d · cache %s/%s\n",
		res.Requested.Ctx, res.Requested.GPULayers, res.Requested.NCPUMoE,
		res.Requested.CacheTypeK, res.Requested.CacheTypeV)
	fmt.Println()
	if res.Calibrated && !fitHasCalibration(res) {
		fmt.Println("calibration: no observations for this model and environment")
		fmt.Println()
	}
	fmt.Println("recommended:")
	printFitRecommendation(res.Recommendation)
	if len(res.Alternatives) > 0 {
		fmt.Println("\nalternatives:")
		for _, rec := range res.Alternatives {
			printFitRecommendation(rec)
		}
	}
	for _, note := range res.Notes {
		fmt.Printf("\nnote: %s\n", note)
	}
}

func printFitRecommendation(rec fit.Recommendation) {
	line := fmt.Sprintf("  %d. ctx %d · gpu_layers %d · n_cpu_moe %d · cache %s/%s",
		rec.Rank, rec.Ctx, rec.GPULayers, rec.NCPUMoE, rec.CacheTypeK, rec.CacheTypeV)
	if rec.Fallback != "" {
		line += " · fallback " + rec.Fallback
	}
	fmt.Println(line)
	fmt.Printf("     gpu weights %s + kv %s + overhead %s = %s\n",
		humanSize(rec.Estimate.GPUWeights), humanSize(rec.Estimate.KVBytes),
		humanSize(rec.Estimate.Overhead), humanSize(rec.Estimate.Total))
	if rec.Estimate.CPUWeights > 0 {
		fmt.Printf("     cpu weights %s\n", humanSize(rec.Estimate.CPUWeights))
	}
	approx := ""
	if rec.Estimate.Approximate {
		approx = " · approximate"
	}
	fmt.Printf("     headroom %s · limit %s · %s%s\n", signedSize(rec.Headroom), rec.Limiting, fitVerdict(rec.Fits), approx)
	if rec.Calibration != nil {
		fmt.Printf("     %s\n", fitCalibrationText(rec.Estimate.Total, rec.Calibration))
		if rec.Calibrated {
			fmt.Printf("     calibrated headroom %s\n", signedSize(rec.CorrectedHeadroom))
		}
	}
	for _, change := range rec.Changes {
		fmt.Printf("     %s\n", change)
	}
}

// fitHasCalibration reports whether any recommendation carries observations.
func fitHasCalibration(res fit.Result) bool {
	if res.Recommendation.Calibration != nil {
		return true
	}
	for _, rec := range res.Alternatives {
		if rec.Calibration != nil {
			return true
		}
	}
	return false
}

// fitCalibrationText mirrors the TUI wording and always keeps the raw estimate.
func fitCalibrationText(estimate uint64, c *measure.Calibration) string {
	switch {
	case c == nil:
		return "calibration: no observations"
	case c.Samples == 0 && c.Unknown > 0:
		return fmt.Sprintf("calibration: observed unknown (n=%d)", c.Unknown)
	case c.Samples == 0 && c.Expired > 0:
		return fmt.Sprintf("calibration: no current observations (%d expired)", c.Expired)
	case c.Samples == 0:
		return "calibration: no observations"
	case !c.OK():
		return fmt.Sprintf("calibration: estimate %s · observed %s (n=%d) · not calibrated (need %d samples)",
			humanSize(estimate), humanSize(c.ObservedBytes), c.Samples, measure.MinCalibrationSamples)
	}
	text := fmt.Sprintf("calibrated: estimate %s · corrected %s · observed %s (n=%d, ±%.0f%%)",
		humanSize(estimate), humanSize(c.Corrected(estimate)),
		humanSize(c.ObservedBytes), c.Samples, c.Spread*100)
	if c.Clamped {
		text += " · clamped"
	}
	return text
}

func fitVerdict(fits bool) string {
	if fits {
		return "fits"
	}
	return "over budget"
}

func signedSize(n int64) string {
	if n < 0 {
		return "-" + humanSize(uint64(-n))
	}
	return humanSize(uint64(n))
}

func humanSize(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

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

const benchHistoryUsage = `usage: wool bench history [TARGET] [options]

options:
  --limit N        newest runs to show (default 20)
  --json           print the matching records as JSON`

type benchHistoryOptions struct {
	target string
	limit  int
	asJSON bool
}

func parseBenchHistoryArgs(args []string) (benchHistoryOptions, error) {
	o := benchHistoryOptions{limit: 20}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case a == "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--limit needs a value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return o, fmt.Errorf("--limit must be a positive integer")
			}
			o.limit = n
		case strings.HasPrefix(a, "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			if err != nil || n < 1 {
				return o, fmt.Errorf("--limit must be a positive integer")
			}
			o.limit = n
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.target != "" {
				return o, fmt.Errorf("only one TARGET is allowed")
			}
			o.target = a
		}
	}
	return o, nil
}

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

func cmdBenchHistory(args []string) int {
	opts, err := parseBenchHistoryArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(benchHistoryUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, benchHistoryUsage)
		return 2
	}
	store := measure.NewStore()
	records, err := store.BenchHistory(opts.target, opts.limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: bench history: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(records)
	}
	if len(records) == 0 {
		if opts.target != "" {
			fmt.Printf("no benchmarks recorded for %s\n", opts.target)
		} else {
			fmt.Println("no benchmarks recorded")
		}
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tTARGET\tPROMPT T/S\tGEN T/S\tVRAM\tLOAD\tCONFIG\tENGINE")
	for _, r := range records {
		b := r.Bench
		if b == nil {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%.1f\t%.1f\t%s\t%s\t%s\t%s\n",
			r.At.Local().Format("2006-01-02 15:04"),
			measure.BenchTargetName(r),
			b.Median.PromptTPS,
			b.Median.GenerationTPS,
			benchHistoryVRAM(b),
			benchHistoryLoad(b),
			fallbackDash(r.ConfigHash),
			b.Engine,
		)
	}
	w.Flush()
	fmt.Printf("\n%d run(s) · %s\n", len(records), store.Path)
	return 0
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

func benchHistoryVRAM(b *measure.Bench) string {
	switch {
	case b.VRAMKnown:
		return humanSize(b.VRAMDeltaBytes)
	case b.PeakBytes > 0:
		return "~" + humanSize(b.PeakBytes)
	default:
		return "-"
	}
}

func benchHistoryLoad(b *measure.Bench) string {
	if !b.LoadKnown {
		return "-"
	}
	return fmt.Sprintf("%.1fs", b.LoadMS/1000)
}

func fallbackDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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

func cmdConfig(cfgPath string, args []string) int {
	if len(args) == 0 {
		fmt.Print("usage: wool config path|init|explain|diff\n")
		return 2
	}
	switch args[0] {
	case "path":
		fmt.Println(cfgPath)
		return 0
	case "explain":
		return cmdConfigExplain(cfgPath, args[1:])
	case "diff":
		return cmdConfigDiff(cfgPath, args[1:])
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

type explainField struct {
	Field  string `json:"field"`
	Value  any    `json:"value"`
	Source string `json:"source"`
	Layer  string `json:"layer"`
}

type explainOutput struct {
	Target string         `json:"target"`
	Kind   string         `json:"kind"`
	Model  string         `json:"model"`
	Fields []explainField `json:"fields"`
}

// cmdConfigExplain prints every resolved launch option with the layer that
// supplied it. It is the CLI counterpart of the TUI editor's source column.
func cmdConfigExplain(cfgPath string, args []string) int {
	// Accept --json before or after TARGET; flag.Parse stops at the first
	// positional argument.
	asJSON := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "wool: unknown option %q\n", arg)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool config explain TARGET [--json]")
		return 2
	}
	target := positional[0]
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}

	var r config.Resolved
	var sources map[string]config.Provenance
	kind := ""
	r, sources, kind, err = resolveNamedTarget(cfg, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}

	out := explainOutput{Target: target, Kind: kind, Model: r.ModelID}
	for _, field := range config.OptionFields() {
		p := sources[field]
		out.Fields = append(out.Fields, explainField{
			Field:  field,
			Value:  config.FieldValue(r, field),
			Source: p.Source,
			Layer:  p.Layer,
		})
	}
	if asJSON {
		return writeJSON(out)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Printf("%s (%s), model %s\n", out.Target, out.Kind, out.Model)
	fmt.Fprintln(w, "FIELD\tVALUE\tSOURCE\tLAYER")
	for _, f := range out.Fields {
		fmt.Fprintf(w, "%s\t%v\t%s\t%s\n", f.Field, f.Value, f.Source, f.Layer)
	}
	w.Flush()
	return 0
}

// resolveNamedTarget resolves a preset or model by name.
func resolveNamedTarget(cfg *config.Config, target string) (config.Resolved, map[string]config.Provenance, string, error) {
	if _, ok := cfg.Presets[target]; ok {
		r, sources, err := cfg.ResolveDetailed("", target)
		return r, sources, "preset", err
	}
	if _, ok := cfg.Models[target]; ok {
		r, sources, err := cfg.ResolveDetailed(target, "")
		return r, sources, "model", err
	}
	return config.Resolved{}, nil, "", fmt.Errorf("no preset or model named %q", target)
}

type diffOutput struct {
	Target  string             `json:"target"`
	Kind    string             `json:"kind"`
	Model   string             `json:"model"`
	Against string             `json:"against"`
	Fields  []config.DiffField `json:"fields"`
}

// cmdConfigDiff prints only the resolved launch options that differ from a
// comparison layer. A preset defaults to its model; a model defaults to the
// defaults layer. No fields means no output, like a quiet diff.
func cmdConfigDiff(cfgPath string, args []string) int {
	asJSON := false
	against := ""
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			asJSON = true
		case arg == "--against":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: wool config diff TARGET [--against defaults|model] [--json]")
				return 2
			}
			i++
			against = args[i]
		case strings.HasPrefix(arg, "--against="):
			against = strings.TrimPrefix(arg, "--against=")
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "wool: unknown option %q\n", arg)
			return 2
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool config diff TARGET [--against defaults|model] [--json]")
		return 2
	}
	if against != "" && against != config.SourceDefaults && against != config.SourceModel {
		fmt.Fprintf(os.Stderr, "wool: --against must be defaults or model, not %q\n", against)
		return 2
	}
	target := positional[0]
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	r, _, kind, err := resolveNamedTarget(cfg, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	presetName := ""
	if kind == "preset" {
		presetName = target
	}
	res, err := cfg.Diff(r.ModelID, presetName, against)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	out := diffOutput{Target: target, Kind: kind, Model: r.ModelID, Against: res.Against, Fields: res.Fields}
	if asJSON {
		return writeJSON(out)
	}
	if len(res.Fields) == 0 {
		return 0
	}
	fmt.Printf("%s (%s), model %s, against %s\n", out.Target, out.Kind, out.Model, out.Against)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tTARGET\tAGAINST")
	for _, f := range res.Fields {
		fmt.Fprintf(w, "%s\t%v (%s %s)\t%v (%s %s)\n",
			f.Field,
			f.Target.Value, f.Target.Source, f.Target.Layer,
			f.Against.Value, f.Against.Source, f.Against.Layer)
	}
	w.Flush()
	return 0
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
	printServer(state, preset)
	if !*noWait {
		fmt.Fprintln(os.Stderr, "wool: observing VRAM (5s, 15s)...")
		rec := launchRecord(cfg, resolved, gpu, bin)
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
