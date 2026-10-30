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
	"wool/internal/gguf"
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
	Level  Level
	Name   string
	Detail string
}

// PresetFit is a memory estimate for one preset.
type PresetFit struct {
	ID       string
	Ctx      int
	Estimate gguf.Estimate
	Known    bool
	Fits     bool
	Note     string
}

// ModelReport describes one configured model.
type ModelReport struct {
	ID      string
	Path    string
	Alias   string
	Ctx     int
	Exists  bool
	Size    uint64
	Meta    gguf.Meta
	HasMeta bool
	MetaErr string
	Est     gguf.Estimate
	HasEst  bool
	FitNote string
	Presets []PresetFit
}

// Report is the full doctor result.
type Report struct {
	ConfigPath string
	Checks     []Check
	Models     []ModelReport
	GPUs       []sysinfo.GPU
	MemTotal   uint64
	MemAvail   uint64
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
	add := func(level Level, name, format string, args ...any) {
		rep.Checks = append(rep.Checks, Check{Level: level, Name: name, Detail: fmt.Sprintf(format, args...)})
	}

	add(LevelOK, "config", "%d models, %d presets, theme %s", len(cfg.Models), len(cfg.Presets), cfg.App.Theme)

	bin := config.ExpandPath(cfg.App.LlamaServer)
	path, err := exec.LookPath(bin)
	if err != nil {
		add(LevelFail, "llama-server", "%s not found in PATH", bin)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, verr := exec.CommandContext(ctx, path, "--version").CombinedOutput()
		cancel()
		version := firstLine(string(out))
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

	for _, id := range cfg.ModelIDs() {
		m := cfg.Models[id]
		r := ModelReport{ID: id, Path: config.AbsPath(m.Path)}
		if err := ValidateIDAlias(id); err != nil {
			add(LevelFail, "model id", "%v", err)
		}
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
			meta, metaErr := gguf.ReadMeta(r.Path)
			if metaErr != nil {
				r.MetaErr = metaErr.Error()
				add(LevelWarn, "model "+id, "cannot read GGUF metadata: %v", metaErr)
			} else {
				r.Meta = meta
				r.HasMeta = true
			}
		}

		resolved, err := cfg.Resolve(id, nil)
		if err != nil {
			add(LevelFail, "model "+id, "%v", err)
		} else {
			r.Ctx = resolved.Ctx
			if r.HasMeta {
				r.Est = gguf.EstimateVRAM(r.Meta, r.Size, resolved.Ctx, resolved.GPULayers, resolved.CacheTypeK, resolved.CacheTypeV)
				r.HasEst = true
				r.FitNote = fitNote(r.Est, resolved, primary)
				if r.FitNote != "" {
					add(LevelWarn, "fit "+id, "%s", r.FitNote)
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
			if r.HasMeta {
				pf.Known = true
				pf.Estimate = gguf.EstimateVRAM(r.Meta, r.Size, pr.Ctx, pr.GPULayers, pr.CacheTypeK, pr.CacheTypeV)
				pf.Fits = true
				if primary.HasVRAM && primary.VRAMTotal > 0 {
					pf.Fits = pf.Estimate.Total <= primary.VRAMTotal
				}
				if pr.NCPUMoE > 0 {
					pf.Note = "MoE CPU offload not included"
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

func fitNote(est gguf.Estimate, r config.Resolved, gpu sysinfo.GPU) string {
	if !gpu.HasVRAM || gpu.VRAMTotal == 0 {
		return ""
	}
	over := int64(est.Total) - int64(gpu.VRAMTotal)
	if over <= 0 {
		return ""
	}
	note := fmt.Sprintf("estimated %s at ctx %d exceeds %s VRAM by %s",
		humanBytes(est.Total), r.Ctx, humanBytes(gpu.VRAMTotal), humanBytes(uint64(over)))
	if r.NCPUMoE > 0 {
		note += " (estimate ignores n_cpu_moe)"
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

// Print writes a plain-text report. Kept here so the CLI and TUI show the
// same wording.
func (r Report) Print() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s\n", r.ConfigPath)
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  [%s] %-14s %s\n", c.Level, c.Name, c.Detail)
	}
	if len(r.Models) > 0 {
		b.WriteString("\nmodels:\n")
		for _, m := range r.Models {
			state := "missing"
			if m.Exists {
				state = humanBytes(m.Size)
			}
			fmt.Fprintf(&b, "  %-22s %-10s ctx %-6d", m.ID, state, m.Ctx)
			if m.HasEst {
				fmt.Fprintf(&b, " est %s", humanBytes(m.Est.Total))
			}
			if m.MetaErr != "" {
				b.WriteString(" (metadata unreadable)")
			}
			b.WriteString("\n")
			for _, p := range m.Presets {
				mark := "ok"
				est := humanBytes(p.Estimate.Total)
				if !p.Known {
					mark, est = "?", "-"
				} else if !p.Fits {
					mark = "over"
				}
				fmt.Fprintf(&b, "    %-24s ctx %-6d est %-9s %s", p.ID, p.Ctx, est, mark)
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
