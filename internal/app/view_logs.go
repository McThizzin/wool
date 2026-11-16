package app

import (
	"fmt"
	"strings"
	"wool/internal/reqlog"
	"wool/internal/ui"
)

func (m *Model) logRight() string {
	name := m.logTarget()
	if name == "" {
		return m.render.Styles.Muted.Render("none")
	}
	mode := "paused"
	if m.follow {
		mode = "follow"
	}
	view := "best effort"
	if m.logRaw {
		view = "raw"
	}
	right := name + " · " + view + " · " + mode
	if m.logFilter != "" {
		right += " · " + m.logFilter
	}
	return m.render.Styles.Muted.Render(right)
}

func (m *Model) logsBody(w, h int) []string {
	st := m.render.Styles
	if m.logRaw || m.reqlog == nil {
		return m.rawLogsBody(w, h)
	}
	if len(m.reqlog.Rows()) == 0 {
		note := "no completed requests yet (best effort); showing raw log"
		if !m.reqlog.Recognized() {
			note = "unrecognised llama-server request log (best effort); showing raw log"
		}
		lines := []string{st.Muted.Render(ui.Fit(note, w-2))}
		if h <= 1 {
			return lines
		}
		return append(lines, m.rawLogsBody(w, h-1)...)
	}

	rows := m.filteredLogRows()
	filter := "all"
	if m.logFilter != "" {
		filter = m.logFilter
	}
	meta := fmt.Sprintf("best effort · %d row(s) · filter %s · v raw log", len(rows), filter)
	lines := []string{
		st.Muted.Render(ui.Fit(meta, w-2)),
		m.logTableHeader(w),
	}
	if len(rows) == 0 {
		lines = append(lines, st.Muted.Render(ui.Fit("no rows for "+filter+"; m cycles the filter", w-2)))
		return lines
	}
	visible := h - len(lines)
	if visible < 1 {
		visible = 1
	}
	end := len(rows)
	start := clamp(end-visible, 0, end)
	if !m.follow {
		start = clamp(end-visible-m.logScroll, 0, maxInt(0, end-visible))
	}
	for _, r := range rows[start:end] {
		lines = append(lines, m.logTableRow(r, w))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// rawLogsBody renders the log tail as plain lines.
func (m *Model) rawLogsBody(w, h int) []string {
	st := m.render.Styles
	if len(m.logLines) == 0 {
		return []string{st.Muted.Render("no log output")}
	}
	visible := h
	if visible < 1 {
		visible = 1
	}
	end := len(m.logLines)
	start := clamp(end-visible, 0, end)
	if !m.follow {
		start = clamp(end-visible-m.logScroll, 0, maxInt(0, end-visible))
	}
	var lines []string
	for _, l := range m.logLines[start:end] {
		lines = append(lines, m.logLine(l, w-2))
	}
	return lines
}

// logTableHeader labels the structured request table.
func (m *Model) logTableHeader(w int) string {
	st := m.render.Styles
	modelW := clamp(w-54, 6, 28)
	line := fmt.Sprintf("%-8s %-*s %7s %5s %8s %7s %6s %4s",
		"WHEN", modelW, "MODEL", "PROMPT", "GEN", "P-EVAL", "G-EVAL", "CACHE", "ST")
	return st.Header.Render(ui.Fit(line, w-2))
}

// logTableRow renders one completed request.
func (m *Model) logTableRow(r reqlog.Row, w int) string {
	st := m.render.Styles
	modelW := clamp(w-54, 6, 28)
	prompt := fmt.Sprint(r.PromptTokens)
	gen := fmt.Sprint(r.GenTokens)
	cache := fmt.Sprint(r.CacheHits)
	ptime, gtime := shortMS(r.PromptMS), shortMS(r.GenMS)
	status := "ok"
	style := st.Plain
	switch {
	case r.Status >= 400:
		status = fmt.Sprint(r.Status)
		style = st.Bad
		if r.PromptTokens == 0 {
			prompt, gen, cache, ptime, gtime = "-", "-", "-", "-", "-"
		}
	case r.Status == 0:
		status = "-"
	}
	line := fmt.Sprintf("%-8s %-*s %7s %5s %8s %7s %6s %4s",
		r.At.Format("15:04:05"), modelW, truncate(m.logRowModel(r), modelW),
		prompt, gen, ptime, gtime, cache, status)
	return style.Render(ui.Fit(line, w-2))
}

// shortMS renders a millisecond duration compactly.
func shortMS(ms float64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms >= 10000:
		return fmt.Sprintf("%.0fs", ms/1000)
	case ms >= 1000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		return fmt.Sprintf("%.0fms", ms)
	}
}

// logLine trims a log line and colors common llama-server levels.
func (m *Model) logLine(s string, w int) string {
	s = strings.TrimRight(s, "\r")
	st := m.render.Styles
	style := st.Muted
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, " error") || strings.Contains(lower, "failed") || strings.Contains(lower, "panic"):
		style = st.Bad
	case strings.Contains(lower, " warning") || strings.Contains(lower, "warn:"):
		style = st.Warn
	case strings.Contains(s, " starting ") || strings.Contains(s, "server is listening"):
		style = st.Good
	}
	return style.Render(ui.Fit(truncateRight(s, w), w))
}
