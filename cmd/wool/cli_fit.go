package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/fit"
	"wool/internal/gguf"
	"wool/internal/measure"
	"wool/internal/sysinfo"
)

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
