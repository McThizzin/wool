package app

import (
	"fmt"
	"time"
	"wool/internal/llamaserver"
	"wool/internal/ui"
)

func (m *Model) inferenceRight() string {
	st := m.render.Styles
	switch m.infResolve().state {
	case infOK:
		return st.Good.Render("live")
	case infNoMetrics:
		return st.Warn.Render("no metrics")
	case infNotLoaded:
		return st.Muted.Render("not loaded")
	default:
		return st.Muted.Render("down")
	}
}

// inferenceBody renders the live inference panel: throughput rates and
// sparklines from /metrics, slot occupancy and KV fill from /slots.
func (m *Model) inferenceBody(w, h int) []string {
	st := m.render.Styles
	src := m.infResolve()
	var lines []string

	switch src.state {
	case infDown:
		return []string{
			st.Muted.Render("Nothing is serving inference."),
			"",
			st.Plain.Render("  s  start the standalone server      r  start the router"),
			st.Muted.Render("  wool stats prints the same numbers for scripts."),
		}
	case infNotLoaded:
		what := "no router model is selected"
		if src.model != "" {
			what = src.model + " is not loaded on the router"
		}
		return []string{
			st.Muted.Render(what),
			"",
			st.Plain.Render("  l  load the selected model      j/k  pick another"),
		}
	}

	lines = append(lines, st.Accent.Render(ui.Fit(src.kind+" · "+fallback(src.model, "-")+"  "+src.url, w-2)))
	if src.state == infNoMetrics {
		lines = append(lines, st.Warn.Render(ui.Fit("metrics unavailable: "+oneLine(src.err, w-4), w-2)))
		if src.kind == "server" {
			lines = append(lines, st.Muted.Render("restart it with metrics = true to expose counters"))
		} else {
			lines = append(lines, st.Muted.Render("set [router] metrics = true and restart the router"))
		}
	}

	if src.state == infOK {
		switch {
		case m.inf.rates.Window > 0:
			rates := m.inf.rates
			sparkW := clamp(w-34, 8, 40)
			row := func(label string, series []float64, value string) {
				lines = append(lines, fmt.Sprintf("%-10s %s %s",
					label,
					st.Accent.Render(ui.Sparkline(series, sparkW)),
					st.Muted.Render(value)))
			}
			row("prompt", m.inf.hist.Prompt, fmt.Sprintf("%.1f t/s", rates.PromptTPS))
			row("gen", m.inf.hist.Gen, fmt.Sprintf("%.1f t/s", rates.GenTPS))
			row("requests", m.inf.hist.Requests,
				fmt.Sprintf("%.0f proc · %.0f defer", src.metrics.RequestsProcessing, src.metrics.RequestsDeferred))
			row("kv fill", m.inf.hist.KV,
				fmt.Sprintf("%.0f%%", llamaserver.SummarizeSlots(src.slots).KVPercent()))
			if rates.SpecSeen {
				row("spec", m.inf.hist.Spec, fmt.Sprintf("%.0f%% accepted", rates.SpecRate))
			}
			lines = append(lines, st.Muted.Render(ui.Fit(fmt.Sprintf(
				"lifetime prompt %.1f t/s · gen %.1f t/s · tokens %s in / %s out",
				src.metrics.PromptTPS(), src.metrics.PredictedTPS(),
				trimFloat(src.metrics.PromptTokens), trimFloat(src.metrics.PredictedTokens)), w-2)))
			if time.Since(m.inf.resetAt) < 8*time.Second {
				lines = append(lines, st.Warn.Render("counter reset detected (server restarted); rates re-seeded"))
			}
		case m.inf.rates.Reset:
			// The counters went backwards; the next window is measured
			// against the new baseline. Zeroes would lie in the meantime.
			lines = append(lines, st.Warn.Render("counter reset detected (server restarted); rates re-seeded"))
		default:
			lines = append(lines, st.Muted.Render("collecting the first scrape window..."))
		}
	}

	if len(src.slots) > 0 {
		sum := llamaserver.SummarizeSlots(src.slots)
		lines = append(lines, "")
		lines = append(lines, st.Header.Render(ui.Fit(fmt.Sprintf("SLOTS  %d/%d busy · KV %s/%s tok (%.0f%%)",
			sum.Busy, sum.Total, humanTokens(sum.KVUsed), humanTokens(sum.KVCap), sum.KVPercent()), w-2)))
		barW := clamp(w-30, 6, 20)
		for _, sl := range src.slots {
			state := "idle"
			style := st.Plain
			if sl.IsProcessing {
				state = "busy"
				style = st.Good
			}
			pct := 0.0
			if sl.NCtx > 0 {
				pct = float64(sl.NPast) / float64(sl.NCtx) * 100
			}
			line := fmt.Sprintf("%2d %-5s %s %5.1f%%  %s/%s tok",
				sl.ID, state, ui.Bar(pct, barW, '█', '░'), pct,
				humanTokens(sl.NPast), humanTokens(sl.NCtx))
			lines = append(lines, style.Render(ui.Fit(" "+line, w-2)))
		}
	}

	if m.hasServer && m.hasRouter {
		lines = append(lines, "", st.Muted.Render("m switches source · j/k selects the router model"))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m *Model) statsLines(w int) []string {
	st := m.render.Styles
	if m.statsErr != "" {
		return []string{st.Warn.Render(ui.Fit("metrics unavailable: "+oneLine(m.statsErr, w-4), w-2))}
	}
	s := m.stats
	var lines []string
	prompt := fmt.Sprintf("prompt %.1f t/s", s.PromptTPS())
	pred := fmt.Sprintf("gen %.1f t/s", s.PredictedTPS())
	tokens := fmt.Sprintf("tokens %s in / %s out", trimFloat(s.PromptTokens), trimFloat(s.PredictedTokens))
	lines = append(lines, st.Plain.Render(prompt+"  "+pred+"  "+tokens))
	if s.SpecDraftTokens > 0 {
		lines = append(lines, st.Muted.Render(fmt.Sprintf("speculative accept %.0f%% (%s/%s)", s.SpecAcceptRate(), trimFloat(s.SpecAcceptedTokens), trimFloat(s.SpecDraftTokens))))
	}
	if len(m.slots) > 0 {
		sum := llamaserver.SummarizeSlots(m.slots)
		lines = append(lines, st.Muted.Render(fmt.Sprintf("slots %d/%d busy · processing requests %.0f", sum.Busy, sum.Total, s.RequestsProcessing)))
	}
	return lines
}
