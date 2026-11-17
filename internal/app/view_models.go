package app

import (
	"fmt"

	"wool/internal/discovery"
	"wool/internal/ui"
)

func (m *Model) modelsRight() string {
	if len(m.models) == 0 {
		return "0 configured"
	}
	return fmt.Sprintf("%s · %d configured", ui.HumanBytes(m.disk.TotalBytes), len(m.models))
}

func (m *Model) modelsBody(w, h int) []string {
	st := m.render.Styles
	if len(m.models) == 0 {
		return []string{st.Muted.Render("no models configured")}
	}
	summary := fmt.Sprintf("%s on disk · %d unused · %d missing",
		ui.HumanBytes(m.disk.TotalBytes), len(m.disk.Unused), m.disk.Missing)
	if len(m.disk.DuplicatePaths) > 0 {
		summary += fmt.Sprintf(" · %d duplicate path(s)", len(m.disk.DuplicatePaths))
	}
	lines := []string{st.Muted.Render(ui.Fit(summary, w-2))}
	header := fmt.Sprintf("%-20s %-9s %-9s %-5s %-10s %s",
		"ID", "SIZE", "FIT", "USE", "STATUS", "ALIAS")
	lines = append(lines, st.Muted.Render(ui.Fit(header, w-2)))

	visible := h - len(lines)
	start := windowStart(m.modelSel, visible, len(m.models))
	for i := start; i < len(m.models) && len(lines) < h; i++ {
		id := m.models[i]
		dm, _ := m.disk.Find(id)
		size := "-"
		if dm.Status == discovery.DiskStatusOK {
			size = ui.HumanBytes(dm.Size)
		}
		use := "-"
		if len(dm.Presets) > 0 {
			use = fmt.Sprintf("%d", len(dm.Presets))
		}
		status := dm.Status
		if dm.DuplicatePath {
			if status == discovery.DiskStatusOK {
				status = "dup path"
			} else {
				status += " dup"
			}
		}
		fit := m.fitFor(id, "model")
		fitText := "-"
		if fit.Ready {
			fitText = ui.HumanBytes(fit.Est.Total)
			if b := m.budget(); b.Known && b.Headroom(fit.Est) < 0 {
				fitText += " !"
			}
		}
		line := fmt.Sprintf("%-20s %-9s %-9s %-5s %-10s %s",
			truncate(id, 20), size, fitText, use, status, dm.Alias)
		if i == m.modelSel {
			lines = append(lines, st.Selected.Render(ui.Fit(" "+line, w-2)))
		} else {
			lines = append(lines, ui.Fit(" "+line, w-2))
		}
	}
	return lines
}

func (m *Model) presetsBody(w, h int) []string {
	st := m.render.Styles
	if len(m.presets) == 0 {
		return []string{st.Muted.Render("no presets configured")}
	}
	visible := h - 1
	start := windowStart(m.presetSel, visible, len(m.presets))
	var lines []string
	for i := start; i < len(m.presets) && len(lines) < visible; i++ {
		id := m.presets[i]
		p := m.cfg.Presets[id]
		r, err := m.cfg.Resolve("", &p)
		if err != nil {
			lines = append(lines, st.Bad.Render(fmt.Sprintf("%-22s %v", id, err)))
			continue
		}
		fit := ""
		if fi := m.fitFor(id, "preset"); fi.Ready {
			fit = ui.HumanBytes(fi.Est.Total)
			if b := m.budget(); b.Known && b.Headroom(fi.Est) < 0 {
				fit += " !"
			}
		}
		line := fmt.Sprintf("%-22s %-18s ctx %-6d %s", truncate(id, 22), truncate(r.ModelID, 18), r.Ctx, fit)
		if i == m.presetSel {
			lines = append(lines, st.Selected.Render(ui.Fit(" "+line, w-2)))
		} else {
			lines = append(lines, ui.Fit(" "+line, w-2))
		}
	}
	return lines
}
