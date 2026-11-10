// Package doctor performs local readiness checks without starting a server.
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
	"wool/internal/gguf"
	"wool/internal/measure"
	"wool/internal/sysinfo"
)

// Level is the severity of a check.
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelFail
)

func (l Level) String() string {
	switch l {
	case LevelWarn:
		return "warn"
	case LevelFail:
		return "fail"
	default:
		return "ok"
	}
}

// Check is one readiness result.
type Check struct {
	Level  Level  `json:"level"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// PresetFit is a memory estimate for one preset.
type PresetFit struct {
	ID          string               `json:"id"`
	Ctx         int                  `json:"ctx"`
	Estimate    gguf.Estimate        `json:"estimate"`
	Known       bool                 `json:"known"`
	Fits        bool                 `json:"fits"`
	Headroom    int64                `json:"headroom_bytes"`
	Limiting    string               `json:"limiting_term,omitempty"`
	Note        string               `json:"note,omitempty"`
	Calibration *measure.Calibration `json:"calibration,omitempty"`
}

// ModelReport describes one configured model.
type ModelReport struct {
	ID          string               `json:"id"`
	Path        string               `json:"path"`
	Alias       string               `json:"alias,omitempty"`
	Ctx         int                  `json:"ctx"`
	Exists      bool                 `json:"exists"`
	Size        uint64               `json:"size_bytes"`
	Meta        gguf.Meta            `json:"meta"`
	HasMeta     bool                 `json:"has_meta"`
	MetaErr     string               `json:"meta_error,omitempty"`
	Est         gguf.Estimate        `json:"estimate"`
	HasEst      bool                 `json:"has_estimate"`
	Headroom    int64                `json:"headroom_bytes"`
	Limiting    string               `json:"limiting_term,omitempty"`
	FitNote     string               `json:"fit_note,omitempty"`
	Calibration *measure.Calibration `json:"calibration,omitempty"`
	Presets     []PresetFit          `json:"presets,omitempty"`
}

// Report is the full doctor result.
type Report struct {
	ConfigPath string        `json:"config_path"`
	Checks     []Check       `json:"checks"`
	Models     []ModelReport `json:"models"`
	GPUs       []sysinfo.GPU `json:"gpus"`
	Budget     gguf.Budget   `json:"budget"`
	MemTotal   uint64        `json:"mem_total_bytes"`
	MemAvail   uint64        `json:"mem_available_bytes"`
}

// Failures returns checks that should make the command exit non-zero.
func (r Report) Failures() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Level == LevelFail {
			out = append(out, c)
		}
	}
	return out
}

// Warnings returns non-fatal checks.
func (r Report) Warnings() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Level == LevelWarn {
			out = append(out, c)
		}
	}
	return out
}

// Run checks the local installation. cfg must already be loaded.
func Run(cfg *config.Config, cfgPath string) Report {
	rep := Report{ConfigPath: cfgPath}
	store := measure.NewStore()
	add := func(level Level, name, format string, args ...any) {
		rep.Checks = append(rep.Checks, Check{Level: level, Name: name, Detail: fmt.Sprintf(format, args...)})
	}

	add(LevelOK, "config", "%d models, %d presets, theme %s", len(cfg.Models), len(cfg.Presets), cfg.App.Theme)

	bin := config.ExpandPath(cfg.App.LlamaServer)
	serverVersion := ""
	path, err := exec.LookPath(bin)
	if err != nil {
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
func (r Report) Print() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s\n", r.ConfigPath)
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  [%s] %-14s %s\n", c.Level, c.Name, c.Detail)
	}
	if r.Budget.Known {
		fmt.Fprintf(&b, "vram: %s total, %s used, %s reserve, %s budget for a new server",
			humanBytes(r.Budget.Total), humanBytes(r.Budget.Used),
			humanBytes(r.Budget.Reserve), humanBytes(r.Budget.Available))
		if r.Budget.OwnKnown && r.Budget.OwnUsed > 0 {
			fmt.Fprintf(&b, " (server own %s)", humanBytes(r.Budget.OwnUsed))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("vram: budget unknown (no GPU VRAM detected)\n")
	}
	if len(r.Models) > 0 {
		b.WriteString("\nmodels:\n")
		for _, m := range r.Models {
			state := "missing"
			if m.Exists {
				state = humanBytes(m.Size)
			}
			fmt.Fprintf(&b, "  %-22s %-10s ctx %-6d", m.ID, state, m.Ctx)
			if m.HasEst && m.Calibration == nil {
				fmt.Fprintf(&b, " est %s", humanBytes(m.Est.Total))
			}
			if m.HasEst && m.Calibration != nil {
				fmt.Fprintf(&b, "  %s", observationLine(m.Est.Total, m.Calibration))
			}
			if m.MetaErr != "" {
				b.WriteString(" (metadata unreadable)")
			}
			b.WriteString("\n")
			if m.HasEst {
				fmt.Fprintf(&b, "    %s\n", estimateLine(m.Est, m.Headroom, m.Limiting, r.Budget.Known))
			}
			for _, p := range m.Presets {
				mark := "ok"
				est := humanBytes(p.Estimate.Total)
				if !p.Known {
					mark, est = "?", "-"
				} else if !p.Fits {
					mark = "over"
				}
				fmt.Fprintf(&b, "    %-24s ctx %-6d est %-9s %s", p.ID, p.Ctx, est, mark)
				if p.Known {
					b.WriteString(" · " + estimateLine(p.Estimate, p.Headroom, p.Limiting, r.Budget.Known))
				}
				if p.Known && p.Calibration != nil {
					b.WriteString(" · " + observedShort(p.Estimate.Total, p.Calibration))
				}
				if p.Note != "" {
					b.WriteString(" (" + p.Note + ")")
				}
				b.WriteString("\n")
			}
		}
	}
	fails := len(r.Failures())
	warns := len(r.Warnings())
	fmt.Fprintf(&b, "\n%d failure(s), %d warning(s)\n", fails, warns)
	return b.String()
}
