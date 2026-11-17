package app

import (
	"fmt"
	"strings"

	"wool/internal/ui"
)

func (m *Model) discoveredRight() string {
	if m.discoveryBusy {
		return "scanning..."
	}
	return fmt.Sprintf("%d new · %d configured", m.newDiscoveredCount(), len(m.discovery.Configured))
}

func (m *Model) discoveredBody(w, h int) []string {
	st := m.render.Styles
	if m.discoveryBusy && !m.discoveryDone {
		return []string{st.Muted.Render("scanning " + ui.Fallback(joinDirs(m.discovery.Dirs), "model_dirs") + "...")}
	}
	if !m.discoveryDone {
		return []string{st.Muted.Render("press r to scan model_dirs")}
	}
	missing, duplicates := 0, 0
	for _, c := range m.discovery.Configured {
		if !c.Exists {
			missing++
		}
		if c.Duplicate {
			duplicates++
		}
	}
	summary := fmt.Sprintf("configured: %d · missing: %d · duplicates: %d · warnings: %d",
		len(m.discovery.Configured), missing, duplicates, len(m.discovery.Warnings))
	lines := []string{st.Muted.Render(ui.Fit(summary, w-2))}
	if len(m.discovery.Entries) == 0 {
		lines = append(lines, st.Muted.Render("no GGUF files found in "+ui.Fallback(joinDirs(m.discovery.Dirs), "model_dirs")))
		return lines
	}

	header := fmt.Sprintf("%-20s %-9s %-7s %-6s %-9s %-10s %s",
		"ID", "ARCH", "PARAMS", "CTX", "SIZE", "STATUS", "PATH")
	lines = append(lines, st.Muted.Render(ui.Fit(header, w-2)))

	visible := h - len(lines)
	start := windowStart(m.discoveredSel, visible, len(m.discovery.Entries))
	for i := start; i < len(m.discovery.Entries) && len(lines) < h; i++ {
		e := m.discovery.Entries[i]
		id := e.SuggestedID
		status := "new"
		switch {
		case len(e.Configured) > 1:
			status = "duplicate"
			id = e.Configured[0] + " +…"
		case len(e.Configured) == 1:
			status = "configured"
			if e.Duplicate {
				status = "duplicate"
			}
			id = e.Configured[0]
		}
		params := "-"
		if e.Parameters > 0 {
			params = ui.HumanCount(e.Parameters)
		}
		ctx := "-"
		if e.CtxTrain > 0 {
			ctx = ui.HumanTokens(e.CtxTrain)
		}
		line := fmt.Sprintf("%-20s %-9s %-7s %-6s %-9s %-10s %s",
			truncate(id, 20),
			truncate(ui.Fallback(e.Architecture, "-"), 9),
			params, ctx, ui.HumanBytes(e.Size), status, e.Path)
		if i == m.discoveredSel {
			lines = append(lines, st.Selected.Render(ui.Fit(" "+line, w-2)))
		} else {
			lines = append(lines, ui.Fit(" "+line, w-2))
		}
	}
	return lines
}

func joinDirs(dirs []string) string {
	return strings.Join(dirs, ", ")
}
