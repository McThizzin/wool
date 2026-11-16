package app

import (
	"fmt"
	"wool/internal/measure"
	"wool/internal/ui"
)

func (m *Model) benchRight() string {
	st := m.render.Styles
	if m.benchErr != "" {
		return st.Bad.Render("error")
	}
	filter := "all"
	if m.benchFilter != "" {
		filter = truncate(m.benchFilter, 18)
	}
	return st.Muted.Render(fmt.Sprintf("%s · %d run(s)", filter, len(m.benchRuns)))
}

// benchBody lists recorded benchmark runs oldest first, with throughput
// sparklines above the table and full details for the selected run below it.
func (m *Model) benchBody(w, h int) []string {
	st := m.render.Styles
	if m.benchErr != "" {
		return []string{st.Bad.Render(ui.Fit("bench history: "+m.benchErr, w-2))}
	}
	if len(m.benchRuns) == 0 {
		msg := "no benchmark runs recorded"
		if m.benchFilter != "" {
			msg = "no runs for " + m.benchFilter
		}
		return []string{
			st.Muted.Render(msg),
			"",
			st.Plain.Render("  wool bench TARGET [--runs N]"),
			st.Muted.Render("  f cycles the target filter · B returns here from any tab"),
		}
	}

	gen := make([]float64, len(m.benchRuns))
	prompt := make([]float64, len(m.benchRuns))
	for i, r := range m.benchRuns {
		if r.Bench != nil {
			gen[i] = r.Bench.Median.GenerationTPS
			prompt[i] = r.Bench.Median.PromptTPS
		}
	}
	sparkW := clamp(w-24, 8, 60)
	latest := func(v []float64) string {
		if len(v) == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f t/s", v[len(v)-1])
	}
	lines := []string{
		fmt.Sprintf("%-10s %s %s", "gen", st.Accent.Render(ui.Sparkline(gen, sparkW)), st.Muted.Render(latest(gen))),
		fmt.Sprintf("%-10s %s %s", "prompt", st.Accent.Render(ui.Sparkline(prompt, sparkW)), st.Muted.Render(latest(prompt))),
		"",
	}
	headers := fmt.Sprintf("  %-11s %-12s %6s %6s %8s %5s %-8s",
		"WHEN", "TARGET", "PROMPT", "GEN", "VRAM", "LOAD", "CONFIG")
	lines = append(lines, st.Header.Render(ui.Fit(headers, w-2)))

	visible := h - len(lines) - 3
	if visible < 1 {
		visible = 1
	}
	start := windowStart(m.benchSel, visible, len(m.benchRuns))
	for i := start; i < len(m.benchRuns) && i-start < visible; i++ {
		r := m.benchRuns[i]
		mark := " "
		if m.benchMarkSet && r.At.Equal(m.benchMarkAt) {
			mark = "*"
		}
		var promptTPS, genTPS float64
		if r.Bench != nil {
			promptTPS = r.Bench.Median.PromptTPS
			genTPS = r.Bench.Median.GenerationTPS
		}
		row := fmt.Sprintf("%s %-11s %-12s %6.1f %6.1f %8s %5s %-8s",
			mark,
			r.At.Local().Format("01-02 15:04"),
			truncate(measure.BenchTargetName(r), 12),
			promptTPS,
			genTPS,
			benchVRAM(r.Bench),
			benchLoad(r.Bench),
			truncate(fallbackDash(r.ConfigHash), 8),
		)
		switch {
		case i == m.benchSel:
			lines = append(lines, st.Selected.Render(ui.Fit(" "+row, w-2)))
		case mark == "*":
			lines = append(lines, st.Warn.Render(ui.Fit(" "+row, w-2)))
		default:
			lines = append(lines, ui.Fit(" "+row, w-2))
		}
	}

	lines = append(lines, "")
	if sel, ok := m.selectedBench(); ok && sel.Bench != nil {
		lines = append(lines,
			st.Muted.Render(ui.Fit(fmt.Sprintf(" %s · %s · %s · config %s",
				sel.At.Local().Format("2006-01-02 15:04"),
				measure.BenchTargetName(sel), benchEngine(sel), fallbackDash(sel.ConfigHash)), w-2)),
			st.Muted.Render(ui.Fit(fmt.Sprintf(" prompt %.1f t/s · gen %.1f t/s · total %.0f ms · vram %s · load %s",
				sel.Bench.Median.PromptTPS, sel.Bench.Median.GenerationTPS,
				sel.Bench.Median.TotalMS, benchVRAM(sel.Bench), benchLoad(sel.Bench)), w-2)),
		)
	}
	return lines
}

// dashboardBody shows the selected preset, its fit and a short log tail.
