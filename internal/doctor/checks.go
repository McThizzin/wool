package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wool/internal/config"
	"wool/internal/daemon"
	flagcat "wool/internal/flags"
	"wool/internal/gguf"
	"wool/internal/measure"
	"wool/internal/sysinfo"
	"wool/internal/ui"
)

func Run(cfg *config.Config, cfgPath string) Report {
	rep := Report{ConfigPath: cfgPath}
	store := measure.NewStore()
	add := func(level Level, name, format string, args ...any) {
		rep.Checks = append(rep.Checks, Check{Level: level, Name: name, Detail: fmt.Sprintf(format, args...)})
	}

	add(LevelOK, "config", "%d models, %d presets, theme %s", len(cfg.Models), len(cfg.Presets), cfg.App.Theme)

	bin := config.ExpandPath(cfg.App.LlamaServer)
	serverVersion := ""
	path, lookPathErr := exec.LookPath(bin)
	if lookPathErr != nil {
		add(LevelFail, "llama-server", "%s not found in PATH", bin)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, verr := exec.CommandContext(ctx, path, "--version").CombinedOutput()
		cancel()
		version := firstLine(string(out))
		serverVersion = version
		switch {
		case verr != nil && version == "":
			add(LevelWarn, "llama-server", "%s found but --version failed: %v", path, verr)
		case version == "":
			add(LevelOK, "llama-server", "%s", path)
		default:
			add(LevelOK, "llama-server", "%s (%s)", path, version)
		}
	}

	rep.InstalledLlamaServer = serverVersion
	if catalog, catalogErr := flagcat.Embedded(); catalogErr != nil {
		add(LevelWarn, "flag catalog", "catalog unavailable: %v", catalogErr)
	} else {
		rep.FlagCatalog = catalog.Source
		add(LevelOK, "flag catalog", "%s (%s)", catalog.Source.Version, catalog.Source.Commit)
		warnings := flagcat.ConfigWarnings(cfg, catalog)
		if len(warnings) > 0 {
			messages := flagcat.WarningMessages(warnings)
			add(LevelWarn, "extra flags", "%d warning(s): %s", len(messages), strings.Join(messages, "; "))
		}
		if lookPathErr == nil {
			help, helpErr := flagcat.ReadHelp(path)
			if helpErr == nil {
				version := flagcat.DetectVersion(help)
				if version == "" {
					version = flagcat.DetectVersion(serverVersion)
				}
				if rep.InstalledLlamaServer == "" {
					rep.InstalledLlamaServer = version
				}
				installed, perr := flagcat.ParseHelp(help, flagcat.CatalogSource{Version: version})
				if perr == nil {
					result := flagcat.Compare(installed, catalog)
					if result.OK {
						add(LevelOK, "flag catalog check", "installed help matches the embedded catalog")
					} else {
						add(LevelWarn, "flag catalog check", "%d new, %d missing, %d changed",
							len(result.New), len(result.Missing), len(result.Changed))
					}
				}
			}
		}
	}

	collector := sysinfo.NewCollector()
	snap, err := collector.Sample()
	if err != nil {
		add(LevelWarn, "hardware", "telemetry unavailable: %v", err)
	} else {
		rep.GPUs = snap.GPUs
		rep.MemTotal = snap.MemTotal
		rep.MemAvail = snap.MemAvailable
		if len(snap.GPUs) == 0 {
			add(LevelWarn, "hardware", "no DRM GPU found; llama-server will use CPU")
		} else {
			var parts []string
			for _, g := range snap.GPUs {
				if g.HasVRAM {
					parts = append(parts, fmt.Sprintf("%s: %s VRAM", g.Name, humanBytes(g.VRAMTotal)))
				} else {
					parts = append(parts, g.Name)
				}
			}
			add(LevelOK, "hardware", "%s; %s RAM", strings.Join(parts, ", "), humanBytes(snap.MemTotal))
		}
	}

	scanModelDirs(cfg, add)

	// Duplicate files mean the router exposes the same weights under more
	// than one ID, which is usually a config mistake.
	byPath := map[string][]string{}
	for _, id := range cfg.ModelIDs() {
		byPath[config.AbsPath(cfg.Models[id].Path)] = append(byPath[config.AbsPath(cfg.Models[id].Path)], id)
	}
	for path, ids := range byPath {
		if len(ids) > 1 {
			sort.Strings(ids)
			add(LevelWarn, "duplicate model", "%s is used by %s", path, strings.Join(ids, ", "))
		}
	}

	var primary sysinfo.GPU
	if len(rep.GPUs) > 0 {
		primary, _ = snap.Primary()
	}
	rep.Budget = budgetFor(cfg, primary)

	for _, id := range cfg.ModelIDs() {
		m := cfg.Models[id]
		r := ModelReport{ID: id, Path: config.AbsPath(m.Path)}
		if err := ValidateIDAlias(id); err != nil {
			add(LevelFail, "model id", "%v", err)
		}
		var modTime time.Time
		info, statErr := os.Stat(r.Path)
		if statErr != nil {
			r.Exists = false
			add(LevelWarn, "model "+id, "%s is missing", r.Path)
		} else if info.IsDir() {
			r.Exists = false
			add(LevelWarn, "model "+id, "%s is a directory", r.Path)
		} else {
			r.Exists = true
			r.Size = uint64(info.Size())
			modTime = info.ModTime()
			meta, metaErr := gguf.ReadMeta(r.Path)
			if metaErr != nil {
				r.MetaErr = metaErr.Error()
				add(LevelWarn, "model "+id, "cannot read GGUF metadata: %v", metaErr)
			} else {
				r.Meta = meta
				r.HasMeta = true
			}
		}
		identity := measure.Identity{
			ModelSize:          r.Size,
			ModelModTime:       modTime,
			GPUPCIID:           primary.PCIID,
			GPUDriver:          primary.Driver,
			LlamaServerVersion: serverVersion,
		}

		resolved, err := cfg.Resolve(id, nil)
		if err != nil {
			add(LevelFail, "model "+id, "%v", err)
		} else {
			r.Ctx = resolved.Ctx
			if r.HasMeta {
				r.Est = gguf.EstimateVRAM(r.Meta, r.Size, resolved.Ctx, resolved.GPULayers, resolved.NCPUMoE, resolved.CacheTypeK, resolved.CacheTypeV)
				r.HasEst = true
				r.Limiting = r.Est.LimitingTerm()
				r.Headroom = rep.Budget.Headroom(r.Est)
				r.FitNote = fitNote(r.Est, resolved, rep.Budget)
				if r.FitNote != "" {
					add(LevelWarn, "fit "+id, "%s", r.FitNote)
				}
				shape := measure.Shape{
					ModelID:    id,
					Ctx:        resolved.Ctx,
					CacheTypeK: resolved.CacheTypeK,
					CacheTypeV: resolved.CacheTypeV,
					GPULayers:  resolved.GPULayers,
					NCPUMoE:    resolved.NCPUMoE,
				}
				if cal, ok := store.Calibration(shape, identity); ok {
					r.Calibration = &cal
				}
			}
		}

		for _, pid := range cfg.PresetIDs() {
			p := cfg.Presets[pid]
			if p.Model != id {
				continue
			}
			pr, err := cfg.Resolve("", &p)
			if err != nil {
				continue
			}
			pf := PresetFit{ID: pid, Ctx: pr.Ctx}
			shape := measure.Shape{
				ModelID:    id,
				Ctx:        pr.Ctx,
				CacheTypeK: pr.CacheTypeK,
				CacheTypeV: pr.CacheTypeV,
				GPULayers:  pr.GPULayers,
				NCPUMoE:    pr.NCPUMoE,
			}
			if cal, ok := store.Calibration(shape, identity); ok {
				pf.Calibration = &cal
			}
			if r.HasMeta {
				pf.Known = true
				pf.Estimate = gguf.EstimateVRAM(r.Meta, r.Size, pr.Ctx, pr.GPULayers, pr.NCPUMoE, pr.CacheTypeK, pr.CacheTypeV)
				pf.Limiting = pf.Estimate.LimitingTerm()
				pf.Headroom = rep.Budget.Headroom(pf.Estimate)
				pf.Fits = true
				if rep.Budget.Known {
					pf.Fits = pf.Headroom >= 0
				}
			}
			r.Presets = append(r.Presets, pf)
		}
		rep.Models = append(rep.Models, r)
	}

	for _, w := range cfg.Warnings() {
		// Missing model files are already reported above with a fit context.
		if strings.Contains(w, "model ") && strings.Contains(w, "no such file") {
			continue
		}
		add(LevelWarn, "config", "%s", w)
	}
	return rep
}

// ValidateIDAlias catches IDs that cannot be router sections.
func ValidateIDAlias(id string) error {
	if id == "" || len(id) > 64 {
		return fmt.Errorf("model id %q is empty or too long", id)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("model id %q contains an unsafe character", id)
		}
	}
	return nil
}

// scanModelDirs reports directories and counts top-level GGUFs.
func scanModelDirs(cfg *config.Config, add func(Level, string, string, ...any)) map[string]int {
	counts := map[string]int{}
	for _, dir := range cfg.App.ModelDirs {
		path := config.AbsPath(dir)
		entries, err := os.ReadDir(path)
		if err != nil {
			add(LevelWarn, "model dir", "%s: %v", path, err)
			continue
		}
		n := 0
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".gguf") {
				n++
			}
		}
		counts[path] = n
		add(LevelOK, "model dir", "%s (%d GGUF files)", path, n)
	}
	return counts
}

// budgetFor derives the VRAM available to a new server. The running
// standalone server's own allocation is excluded so a restart is not charged
// twice; router child allocations are not identified.
func budgetFor(cfg *config.Config, gpu sysinfo.GPU) gguf.Budget {
	if !gpu.HasVRAM || gpu.VRAMTotal == 0 {
		return gguf.Budget{}
	}
	reserve := uint64(0)
	if cfg.Router.MemoryMarginMiB > 0 {
		reserve = uint64(cfg.Router.MemoryMarginMiB) << 20
	}
	var own uint64
	var ownKnown bool
	if inst, err := daemon.NewManager().Status("server"); err == nil && inst.Alive {
		own, ownKnown = sysinfo.ProcessVRAM(inst.PID)
	}
	return gguf.ComputeBudget(gpu.VRAMTotal, gpu.VRAMUsed, reserve, own, ownKnown)
}

func fitNote(est gguf.Estimate, r config.Resolved, budget gguf.Budget) string {
	if !budget.Known {
		return ""
	}
	head := budget.Headroom(est)
	if head >= 0 {
		return ""
	}
	note := fmt.Sprintf("estimated %s at ctx %d exceeds the %s budget by %s",
		humanBytes(est.Total), r.Ctx, humanBytes(budget.Available), humanBytes(uint64(-head)))
	if est.Approximate {
		note += " (estimate is approximate)"
	}
	return note
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func humanBytes(n uint64) string {
	return ui.HumanBytes(n)
}

// estimateLine formats the estimate decomposition, limiting term and headroom.
func estimateLine(e gguf.Estimate, headroom int64, limiting string, budgetKnown bool) string {
	line := fmt.Sprintf("gpu weights %s + kv %s + overhead %s = %s",
		humanBytes(e.GPUWeights), humanBytes(e.KVBytes), humanBytes(e.Overhead), humanBytes(e.Total))
	if e.CPUWeights > 0 {
		line += " · cpu weights " + humanBytes(e.CPUWeights)
	}
	if e.Approximate {
		line += " · approximate"
		if len(e.Reasons) > 0 {
			line += ": " + strings.Join(e.Reasons, "; ")
		}
	}
	if limiting != "" {
		line += " · limit " + limiting
	}
	if budgetKnown {
		if headroom >= 0 {
			line += " · headroom " + humanBytes(uint64(headroom))
		} else {
			line += " · over by " + humanBytes(uint64(-headroom))
		}
	}
	return line
}

// observationLine compares the live estimate with the observations for the
// current model and environment.
func observationLine(estimate uint64, c *measure.Calibration) string {
	return fmt.Sprintf("estimate %s · %s", humanBytes(estimate), observedShort(estimate, c))
}

// observedShort is the observation without repeating the estimate, for lines
// that already show one.
func observedShort(estimate uint64, c *measure.Calibration) string {
	if c == nil {
		return "no observations"
	}
	switch {
	case c.Samples == 0 && c.Unknown > 0:
		return fmt.Sprintf("observed unknown (n=%d)", c.Unknown)
	case c.Samples == 0 && c.Expired > 0:
		return fmt.Sprintf("no current observations (%d expired)", c.Expired)
	case c.Samples == 0:
		return "no observations"
	case !c.OK():
		return fmt.Sprintf("observed %s (n=%d)", humanBytes(c.ObservedBytes), c.Samples)
	}
	text := fmt.Sprintf("corrected %s · observed %s (n=%d, ±%.0f%%)",
		humanBytes(c.Corrected(estimate)), humanBytes(c.ObservedBytes),
		c.Samples, c.Spread*100)
	if c.Clamped {
		text += " · clamped"
	}
	return text
}

// Print writes a plain-text report. Kept here so the CLI and TUI show the
// same wording.
