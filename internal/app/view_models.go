package app

import (
	"fmt"
	"wool/internal/ui"
)

func (m *Model) modelsBody(w, h int) []string {
	st := m.render.Styles
	if len(m.models) == 0 {
		return []string{st.Muted.Render("no models configured")}
	}
	visible := h - 1
	start := windowStart(m.modelSel, visible, len(m.models))
	var lines []string
	for i := start; i < len(m.models) && len(lines) < visible; i++ {
		id := m.models[i]
		mc := m.cfg.Models[id]
		size := ""
		if entry, ok := m.meta[abs(mc.Path)]; ok {
			if entry.Ready {
				size = ui.HumanBytes(entry.Size)
			} else if _, err := statFile(abs(mc.Path)); err != nil {
				size = "missing"
			} else {
				size = "unreadable"
			}
		} else if sz, err := statFile(abs(mc.Path)); err == nil {
			size = ui.HumanBytes(sz)
		} else {
			size = "missing"
		}
		fit := m.fitFor(id, "model")
		fitText := "-"
		if fit.Ready {
			fitText = ui.HumanBytes(fit.Est.Total)
			if b := m.budget(); b.Known && b.Headroom(fit.Est) < 0 {
				fitText += " !"
			}
		}
		line := fmt.Sprintf("%-22s %-9s %-9s %s", truncate(id, 22), size, fitText, mc.Alias)
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
