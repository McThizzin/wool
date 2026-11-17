// Package doctor performs local readiness checks without starting a server.
package doctor

import (
	"fmt"
	"strings"

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
