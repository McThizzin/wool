package app

import (
	"fmt"
	"strings"
	"time"
	"wool/internal/ui"
)

func (m *Model) routerRight() string {
	st := m.render.Styles
	switch {
	case m.hasRouter && m.routerHealth == "ok":
		return st.Good.Render("router up")
	case m.hasRouter:
		return st.Warn.Render("router " + fallback(m.routerHealth, "starting"))
	default:
		return st.Muted.Render("router down")
	}
}

func (m *Model) routerBody(w, h int) []string {
	st := m.render.Styles
	if !m.hasRouter {
		return []string{
			st.Muted.Render("Router is not running."),
			"",
			st.Plain.Render("  r  start router      R  stop router"),
			st.Muted.Render("The router loads one model at a time from the generated models.ini."),
		}
	}
	now := time.Now()
	var lines []string
	if m.routerErr != "" {
		lines = append(lines, st.Bad.Render("router: "+oneLine(m.routerErr, w-4)))
	}
	lines = append(lines, m.routerSummary(w)...)
	headers := fmt.Sprintf("%-24s %-10s %-8s %-6s %-5s %-8s %s",
		"MODEL", "STATUS", "CONTEXT", "TPS", "LOADS", "TOKENS", "LAST")
	lines = append(lines, st.Header.Render(ui.Fit(headers, w-2)))

	footer := []string{""}
	sel, ok := m.selectedRouterModel()
	if ok {
		footer = append(footer, st.Muted.Render(fmt.Sprintf("selected %s · enter load · u unload · m metrics", sel.ID)))
		life := m.usage.life.Lookup(sel.ID)
		session := m.usage.session.Lookup(sel.ID)
		footer = append(footer, st.Muted.Render(ui.Fit(fmt.Sprintf(
			"usage lifetime: %d loads · %s in / %s out · last used %s",
			life.Loads, humanTokenCount(life.PromptTokens), humanTokenCount(life.GenTokens),
			relativeAge(life.LastUsed, now)), w-2)))
		footer = append(footer, st.Muted.Render(ui.Fit(fmt.Sprintf(
			"usage session: %d loads · %s in / %s out",
			session.Loads, humanTokenCount(session.PromptTokens), humanTokenCount(session.GenTokens)), w-2)))
		if n := m.usage.life.Router.Restarts; n > 0 {
			footer = append(footer, st.Muted.Render(fmt.Sprintf("router restarts recorded: %d", n)))
		}
		if m.statsID == sel.ID && routerResident(sel) {
			footer = append(footer, m.statsLines(w)...)
		}
	}

	visible := h - len(lines) - len(footer)
	start := windowStart(m.routerSel, visible, len(m.routerModels))
	for i := start; i < len(m.routerModels) && i-start < visible; i++ {
		rm := m.routerModels[i]
		status := rm.Status.Value
		tps := ""
		ctx := ""
		if i == m.routerSel {
			if m.statsID == rm.ID {
				if v := m.stats.PredictedTPS(); v > 0 {
					tps = fmt.Sprintf("%.1f", v)
				}
			}
			if len(m.slots) > 0 {
				ctx = fmt.Sprintf("%d/%d", m.slots[0].NPast, m.slots[0].NCtx)
			}
		}
		usage := m.usage.life.Lookup(rm.ID)
		line := fmt.Sprintf("%-24s %-10s %-8s %-6s %-5d %-8s %s",
			truncate(rm.ID, 24), truncate(status, 10), truncate(ctx, 8), truncate(tps, 6),
			usage.Loads, humanTokenCount(usage.PromptTokens+usage.GenTokens),
			truncate(relativeAge(usage.LastUsed, now), 8))
		style := st.Plain
		switch status {
		case "loaded":
			style = st.Good
		case "loading":
			style = st.Warn
		case "failed":
			style = st.Bad
		case "unloaded":
			style = st.Muted
		}
		if i == m.routerSel {
			lines = append(lines, st.Selected.Render(ui.Fit(" "+line, w-2)))
		} else {
			lines = append(lines, style.Render(ui.Fit(" "+line, w-2)))
		}
	}
	return append(lines, footer...)
}

// routerSummary shows the resident count, the models_max limit, the configured
// memory margin and a best-effort eviction prediction for loading the selected
// model. The victim order approximates the router's LRU clock from load time,
// because the API does not expose last_used.
func (m *Model) routerSummary(w int) []string {
	st := m.render.Styles
	limit := m.cfg.Router.ModelsMax
	resident := make([]routerResidentModel, 0, len(m.routerModels))
	var residentBytes uint64
	residentKnown := true
	for _, rm := range m.routerModels {
		if !routerResident(rm) {
			continue
		}
		r := routerResidentModel{ID: rm.ID, LoadedAt: m.routerLoadedAt[rm.ID]}
		fi := m.fitFor(rm.ID, "model")
		fallback, fallbackKnown := uint64(0), false
		if fi.Ready {
			fallback, fallbackKnown = fi.Est.Total, true
		}
		if rm.Status.Value == "sleeping" {
			// A sleeping model keeps its models_max slot but frees its memory.
			r.Bytes, r.Known = 0, true
		} else {
			r.Bytes, r.Known = routerModelBytes(rm, fallback, fallbackKnown)
		}
		if r.Known {
			residentBytes += r.Bytes
		} else {
			residentKnown = false
		}
		resident = append(resident, r)
	}

	sel, hasSel := m.selectedRouterModel()
	candidate := ""
	var candidateBytes uint64
	candidateKnown := false
	if hasSel {
		candidate = sel.ID
		fi := m.fitFor(candidate, "model")
		if fi.Ready {
			candidateBytes, candidateKnown = fi.Est.Total, true
		}
		candidateBytes, candidateKnown = routerModelBytes(sel, candidateBytes, candidateKnown)
	}
	memory := routerMemoryBudget{
		Enabled: routerMemoryEnabled(m.routerModels),
		Margin:  uint64(maxInt(0, m.cfg.Router.MemoryMarginMiB)) << 20,
	}
	if gpu, ok := m.snap.Primary(); ok && gpu.HasVRAM && gpu.VRAMTotal > 0 {
		memory.Known = true
		if gpu.VRAMUsed < gpu.VRAMTotal {
			memory.Free = gpu.VRAMTotal - gpu.VRAMUsed
		}
	}
	plan := predictRouterEviction(resident, candidate, candidateBytes, candidateKnown, limit, memory)

	count := fmt.Sprintf("resident %d", len(resident))
	if limit > 0 {
		count += fmt.Sprintf("/%d", limit)
		if len(resident) > limit {
			count += " (over limit)"
		}
	} else {
		count += "/unlimited"
	}
	margin := fmt.Sprintf("%d MiB", maxInt(0, m.cfg.Router.MemoryMarginMiB))
	if memory.Enabled {
		margin += " (enforced)"
	}
	summary := count + " · margin " + margin
	if len(resident) > 0 {
		if residentKnown {
			summary += " · est " + ui.HumanBytes(residentBytes)
		} else {
			summary += " · est unknown"
		}
	}
	style := st.Muted
	if limit > 0 && len(resident) > limit {
		style = st.Warn
	}
	lines := []string{style.Render(ui.Fit(summary, w-2))}

	switch {
	case len(m.routerModels) == 0:
		lines = append(lines, st.Muted.Render("no models reported by the router"))
	case !hasSel:
		lines = append(lines, st.Muted.Render("select a model to predict eviction"))
	case plan.Resident:
		lines = append(lines, st.Muted.Render(ui.Fit(
			"selected "+plan.Candidate+" is resident; no eviction predicted", w-2)))
	default:
		load := "loading " + plan.Candidate
		if plan.CandidateKnown {
			load += " (" + ui.HumanBytes(plan.CandidateBytes) + ")"
		}
		switch {
		case !plan.EstimatesKnown:
			load = "predicted eviction: unknown (estimate missing) if " + load
		case len(plan.Victims) == 0:
			load = "predicted eviction: none if " + load
		default:
			load = "predicted eviction: " + strings.Join(plan.Victims, ", ") + " (LRU approx.) if " + load
		}
		lines = append(lines, st.Muted.Render(ui.Fit(load, w-2)))
	}
	return lines
}
