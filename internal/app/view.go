package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"wool/internal/alerts"
	"wool/internal/config"
	"wool/internal/gguf"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/reqlog"
	"wool/internal/ui"
)

// View renders the whole screen.
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}
	if m.editor != nil {
		return m.editorView()
	}
	if m.overlay != "" {
		return m.overlayView()
	}
	header := m.headerView()

	bodyH := m.height - 3 // header, input, footer
	if bodyH < 6 {
		bodyH = 6
	}
	main, side := m.bodyView(bodyH)

	lines := []string{header}
	mainLines := strings.Split(main, "\n")
	sideLines := strings.Split(side, "\n")
	for i := 0; i < bodyH; i++ {
		left := ""
		if i < len(mainLines) {
			left = mainLines[i]
		}
		right := ""
		if i < len(sideLines) {
			right = sideLines[i]
		}
		lines = append(lines, left+right)
	}
	lines = append(lines, m.inputView())
	lines = append(lines, m.footerView())
	return strings.Join(lines, "\n")
}

func (m *Model) headerView() string {
	st := m.render.Styles
	build := func(compact bool) string {
		var tabs []string
		sep := " "
		if compact {
			sep = ""
		}
		for i, name := range tabTitles {
			label := fmt.Sprintf("[%d] %s", i+1, name)
			if compact {
				label = fmt.Sprintf("[%d]", i+1)
			}
			if tab(i) == m.tab {
				tabs = append(tabs, st.Selected.Padding(0, 1).Render(label))
			} else {
				tabs = append(tabs, st.Muted.Padding(0, 1).Render(label))
			}
		}
		return "  " + strings.Join(tabs, sep)
	}
	title := st.App.Bold(true).Render("wool") + "  "
	left := build(false)
	if lipgloss.Width(left)+lipgloss.Width(title)+1 > m.width {
		left = build(true)
	}
	return sides(left, title, m.width)
}

func (m *Model) bodyView(h int) (string, string) {
	sideW := clamp(m.width/3, 28, 44)
	if m.width < 84 {
		sideW = 0
	}
	mainW := m.width - sideW
	if sideW > 0 {
		mainW--
	}
	main := m.mainPanel(mainW, h)
	if sideW == 0 {
		return main, ""
	}
	topH := clamp(h/2, 8, h-6)
	side := m.sidebarView(sideW, h, topH)
	// Pad the main panel to the sidebar height so both columns join cleanly.
	mainLines := strings.Split(main, "\n")
	for len(mainLines) < h {
		mainLines = append(mainLines, strings.Repeat(" ", mainW))
	}
	if len(mainLines) > h {
		mainLines = mainLines[:h]
	}
	sideLines := strings.Split(side, "\n")
	for i := range mainLines {
		right := ""
		if i < len(sideLines) {
			right = sideLines[i]
		}
		if sideW > 0 {
			mainLines[i] = mainLines[i] + " " + right
		} else {
			mainLines[i] = mainLines[i] + right
		}
	}
	return strings.Join(mainLines, "\n"), ""
}

func (m *Model) mainPanel(w, h int) string {
	if w < 10 || h < 4 {
		return ""
	}
	focused := !m.command
	switch m.tab {
	case tabDashboard:
		return m.render.Panel(m.render.Icons.Activity+" Dashboard", m.dashRight(), m.dashboardBody(w, h-2), w, h, focused)
	case tabModels:
		return m.render.Panel(m.render.Icons.Model+" Models", fmt.Sprintf("%d configured", len(m.models)), m.modelsBody(w, h-2), w, h, focused)
	case tabPresets:
		return m.render.Panel(m.render.Icons.Preset+" Presets", fmt.Sprintf("%d recipes", len(m.presets)), m.presetsBody(w, h-2), w, h, focused)
	case tabRouter:
		return m.render.Panel(m.render.Icons.Router+" Router", m.routerRight(), m.routerBody(w, h-2), w, h, focused)
	case tabInference:
		return m.render.Panel(m.render.Icons.Activity+" Inference", m.inferenceRight(), m.inferenceBody(w, h-2), w, h, focused)
	case tabHardware:
		return m.render.Panel(m.render.Icons.Hardware+" Hardware", m.hardwareRight(), m.hardwareBody(h-2), w, h, focused)
	case tabLogs:
		return m.render.Panel(m.render.Icons.Logs+" Logs", m.logRight(), m.logsBody(w, h-2), w, h, focused)
	case tabBench:
		return m.render.Panel(m.render.Icons.Activity+" Bench", m.benchRight(), m.benchBody(w, h-2), w, h, focused)
	}
	return ""
}

func (m *Model) dashRight() string {
	st := m.render.Styles
	if m.crash != nil {
		return st.Bad.Render("server crashed")
	}
	if m.hasServer {
		return st.Good.Render("server up")
	}
	return st.Muted.Render("server down")
}

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

func (m *Model) hardwareRight() string {
	if gpu, ok := m.snap.Primary(); ok && gpu.HasVRAM {
		return m.render.Styles.Muted.Render(fmt.Sprintf("%s / %s", ui.HumanBytes(gpu.VRAMUsed), ui.HumanBytes(gpu.VRAMTotal)))
	}
	return m.render.Styles.Muted.Render(fmt.Sprintf("%s RAM", ui.HumanBytes(m.snap.MemTotal)))
}

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
func (m *Model) dashboardBody(w, h int) []string {
	st := m.render.Styles
	var lines []string
	id, kind, ok := m.target()
	if !ok {
		lines = append(lines, st.Muted.Render("No presets configured. Add [presets.*] to "+m.cfgPath), "")
	} else {
		r, err := m.resolveTarget(id, kind)
		if err != nil {
			lines = append(lines, st.Bad.Render(err.Error()))
		} else {
			modelID := r.ModelID
			title := id
			if kind == "model" {
				title = modelID
			}
			lines = append(lines, st.Accent.Render(title)+"  "+st.Muted.Render(kind))
			lines = append(lines, st.Muted.Render(shortenPath(r.ModelPath, w-4)))
			lines = append(lines, "")
			lines = append(lines, kv("model", modelID)+"   "+kv("ctx", fmt.Sprint(r.Ctx))+"   "+kv("gpu layers", fmt.Sprint(r.GPULayers)))
			lines = append(lines, kv("threads", fmt.Sprintf("%d/%d", r.Threads, r.ThreadsBatch))+
				"   "+kv("batch", fmt.Sprintf("%d/%d", r.BatchSize, r.UBatchSize))+
				"   "+kv("parallel", fmt.Sprint(r.Parallel)))
			lines = append(lines, kv("flash attn", r.FlashAttn)+"   "+kv("cache", r.CacheTypeK+"/"+r.CacheTypeV)+"   "+kv("metrics", boolWord(r.Metrics)))
			lines = append(lines, kv("sampling", fmt.Sprintf("temp %.2f · top_p %.2f · top_k %d · min_p %.2f · rep %.2f",
				r.Sampling.Temp, r.Sampling.TopP, r.Sampling.TopK, r.Sampling.MinP, r.Sampling.RepeatPenalty)))
			if len(r.Extra) > 0 {
				var extras []string
				for _, k := range sortedKeys(r.Extra) {
					extras = append(extras, k+"="+r.Extra[k])
				}
				lines = append(lines, st.Muted.Render("extra: "+strings.Join(extras, " ")))
			}
			lines = append(lines, "")
			lines = append(lines, m.fitLine(w)...)
		}
	}
	lines = append(lines, "")
	if card := m.crashCard(w); len(card) > 0 {
		lines = append(lines, card...)
		lines = append(lines, "")
	}
	lines = append(lines, st.Header.Render("log tail"))
	logLines := m.logLines
	if len(logLines) == 0 {
		logLines = []string{st.Muted.Render("no log output yet")}
	}
	maxLog := h - len(lines) - 1
	if maxLog < 0 {
		maxLog = 0
	}
	if len(logLines) > maxLog {
		logLines = logLines[len(logLines)-maxLog:]
	}
	for _, l := range logLines {
		lines = append(lines, m.logLine(l, w-2))
	}
	return lines
}

func (m *Model) fitLine(w int) []string {
	st := m.render.Styles
	fi := m.fit
	if fi.Err != "" {
		return []string{st.Warn.Render("fit: " + fi.Err)}
	}
	if !fi.Ready {
		return []string{st.Muted.Render("fit: model file not readable")}
	}
	est := fi.Est
	budget := m.budget()
	lines := []string{st.Muted.Render("fit " + fitBreakdown(est))}
	if fi.HasCalib {
		lines = append(lines, st.Muted.Render(calibrationText(est.Total, fi.Calib)))
	}
	if fi.HasVerify {
		if fi.Verify.OK() {
			lines = append(lines, st.Good.Render(fmt.Sprintf("✓ verified %s · %d ms",
				fi.Verify.At.Local().Format("01-02 15:04"), int64(fi.Verify.LatencyMS))))
		} else {
			lines = append(lines, st.Warn.Render("smoke test failed: "+fi.Verify.Err))
		}
	}
	if !budget.Known {
		return append(lines, st.Muted.Render("budget unknown (no GPU VRAM detected)"))
	}
	head := budget.Headroom(est)
	style := st.Good
	verdict := "headroom " + ui.HumanBytes(uint64(head))
	if head < 0 {
		style = st.Bad
		verdict = "over by " + ui.HumanBytes(uint64(-head))
	} else if budget.Available > 0 && ui.Percent(est.Total, budget.Available) > 90 {
		style = st.Warn
	}
	pct := ui.Percent(est.Total, budget.Available)
	barW := clamp(w-28, 10, 26)
	lines = append(lines, m.fitBar(est, budget, barW)+" "+
		style.Render(fmt.Sprintf("%3.0f%% %s", pct, verdict)))
	detail := fmt.Sprintf("budget %s · used %s of %s · reserve %s",
		ui.HumanBytes(budget.Available), ui.HumanBytes(budget.Used),
		ui.HumanBytes(budget.Total), ui.HumanBytes(budget.Reserve))
	if budget.OwnKnown && budget.OwnUsed > 0 {
		detail += " · server " + ui.HumanBytes(budget.OwnUsed)
	}
	lines = append(lines, st.Muted.Render(detail+" · limit "+est.LimitingTerm()))
	return lines
}

// fitBreakdown renders the VRAM estimate as gpu weights + KV + overhead = total,
// with the CPU-resident weight term and the approximate flag alongside.
func fitBreakdown(e gguf.Estimate) string {
	s := fmt.Sprintf("gpu %s + kv %s + overhead %s = %s",
		ui.HumanBytes(e.GPUWeights), ui.HumanBytes(e.KVBytes),
		ui.HumanBytes(e.Overhead), ui.HumanBytes(e.Total))
	if e.CPUWeights > 0 {
		s += " · cpu " + ui.HumanBytes(e.CPUWeights)
	}
	if e.Approximate {
		s += " · approx"
	}
	return s
}

// calibrationText compares the live estimate with the observations for the
// current model and environment. It always shows the raw estimate and the
// sample count; the corrected value appears only once there are enough samples.
func calibrationText(estimate uint64, c measure.Calibration) string {
	switch {
	case c.Samples == 0 && c.Unknown > 0:
		return fmt.Sprintf("observed unknown (n=%d)", c.Unknown)
	case c.Samples == 0 && c.Expired > 0:
		return fmt.Sprintf("estimate %s · no current observations (%d expired)",
			ui.HumanBytes(estimate), c.Expired)
	case c.Samples == 0:
		return fmt.Sprintf("estimate %s · no observations", ui.HumanBytes(estimate))
	case !c.OK():
		return fmt.Sprintf("estimate %s · observed %s (n=%d)",
			ui.HumanBytes(estimate), ui.HumanBytes(c.ObservedBytes), c.Samples)
	}
	text := fmt.Sprintf("estimate %s · corrected %s · observed %s (n=%d, ±%.0f%%)",
		ui.HumanBytes(estimate), ui.HumanBytes(c.Corrected(estimate)),
		ui.HumanBytes(c.ObservedBytes), c.Samples, c.Spread*100)
	if c.Clamped {
		text += " · clamped"
	}
	return text
}

// fitBar draws the estimate as segments against the GPU's total VRAM.
func (m *Model) fitBar(e gguf.Estimate, b gguf.Budget, width int) string {
	st := m.render.Styles
	if width <= 0 {
		return ""
	}
	scale := e.Total
	if b.Known && b.Total > scale {
		scale = b.Total
	}
	if scale == 0 {
		return st.Muted.Render(strings.Repeat("░", width))
	}
	cells := func(v uint64) int {
		return int(float64(v) / float64(scale) * float64(width))
	}
	wc, kc, oc := cells(e.GPUWeights), cells(e.KVBytes), cells(e.Overhead)
	for wc+kc+oc > width {
		switch {
		case oc > 0:
			oc--
		case kc > 0:
			kc--
		default:
			wc--
		}
	}
	free := width - wc - kc - oc
	return st.Accent.Render(strings.Repeat("█", wc)) +
		st.Warn.Render(strings.Repeat("▓", kc)) +
		st.Muted.Render(strings.Repeat("▒", oc)) +
		st.Muted.Render(strings.Repeat("░", free))
}

func signedBytes(n int64) string {
	switch {
	case n > 0:
		return "+" + ui.HumanBytes(uint64(n))
	case n < 0:
		return "-" + ui.HumanBytes(uint64(-n))
	default:
		return "0 B"
	}
}

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
	var lines []string
	if m.routerErr != "" {
		lines = append(lines, st.Bad.Render("router: "+oneLine(m.routerErr, w-4)))
	}
	lines = append(lines, m.routerSummary(w)...)
	headers := fmt.Sprintf("%-24s %-10s %-8s %s", "MODEL", "STATUS", "CONTEXT", "TPS")
	lines = append(lines, st.Header.Render(ui.Fit(headers, w-2)))
	visible := h - len(lines) - 2
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
		line := fmt.Sprintf("%-24s %-10s %-8s %s", truncate(rm.ID, 24), status, ctx, tps)
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
	lines = append(lines, "")
	sel, ok := m.selectedRouterModel()
	if ok {
		lines = append(lines, st.Muted.Render(fmt.Sprintf("selected %s · enter load · u unload · m metrics", sel.ID)))
		if m.statsID == sel.ID && (sel.Status.Value == "loaded" || sel.Status.Value == "sleeping") {
			lines = append(lines, m.statsLines(w)...)
		}
	}
	return lines
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
		if fi := m.fitFor(rm.ID, "model"); fi.Ready {
			r.Bytes, r.Known = fi.Est.Total, true
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
		if fi := m.fitFor(candidate, "model"); fi.Ready {
			candidateBytes, candidateKnown = fi.Est.Total, true
		}
	}
	plan := predictRouterEviction(resident, candidate, candidateBytes, candidateKnown, limit)

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
		case len(plan.Victims) == 0:
			load = "predicted eviction: none if " + load
		case !plan.EstimatesKnown:
			load = "predicted eviction: unknown (estimate missing) if " + load
		default:
			load = "predicted eviction: " + strings.Join(plan.Victims, ", ") + " (LRU approx.) if " + load
		}
		lines = append(lines, st.Muted.Render(ui.Fit(load, w-2)))
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

func (m *Model) hardwareBody(h int) []string {
	st := m.render.Styles
	s := m.snap
	graphW := clamp(m.width/4, 12, 34)
	if m.width < 84 {
		graphW = clamp(m.width/2, 12, 40)
	}
	var lines []string
	line := func(label string, value string, series []float64, max float64) {
		frac := 0.0
		if max > 0 {
			frac = latestFraction(series, max)
		}
		lines = append(lines, fmt.Sprintf("%-10s %s %s",
			label,
			st.Accent.Render(ui.Sparkline(series, graphW)),
			st.Muted.Render(fmt.Sprintf("%5.0f%%", frac*100)),
		))
	}
	line("cpu", "", m.hist.CPU, 100)
	lines = append(lines, "")
	line("memory", "", m.hist.Mem, 100)
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("%-10s %s %s", "ram",
		st.Accent.Render(ui.Bar(s.MemPercent(), graphW, '█', '░')),
		st.Muted.Render(fmt.Sprintf("%s/%s", ui.HumanBytes(s.MemUsed()), ui.HumanBytes(s.MemTotal)))))
	lines = append(lines, "")
	if gpu, ok := s.Primary(); ok {
		name := gpu.Name
		if name == "" {
			name = gpu.Card
		}
		lines = append(lines, st.Header.Render(truncate(name, graphW+16)))
		lines = append(lines, "")
		if gpu.HasTemp {
			line("temp", "", m.hist.Temp, 100)
			if len(m.hist.Temp) > 0 {
				last := m.hist.Temp[len(m.hist.Temp)-1]
				lines[len(lines)-1] = fmt.Sprintf("%-10s %s %s", "temp",
					st.Accent.Render(ui.Sparkline(m.hist.Temp, graphW)),
					st.Muted.Render(fmt.Sprintf("%.0f°C", last)))
			}
			lines = append(lines, "")
		}
		if gpu.HasVRAM {
			lines = append(lines, fmt.Sprintf("%-10s %s %s", "vram",
				st.Accent.Render(ui.Bar(gpu.VRAMPercent(), graphW, '█', '░')),
				st.Muted.Render(fmt.Sprintf("%s/%s", ui.HumanBytes(gpu.VRAMUsed), ui.HumanBytes(gpu.VRAMTotal)))))
			line("vram hist", "", m.hist.VRAM, 100)
			lines = append(lines, "")
		}
		if gpu.HasBusy {
			line("gpu busy", "", m.hist.Busy, 100)
		}
		if gpu.HasPower {
			lines = append(lines, fmt.Sprintf("%-10s %s %s", "power",
				st.Accent.Render(ui.Sparkline(m.hist.Power, graphW)),
				st.Muted.Render(fmt.Sprintf("%.0f W", gpu.PowerW))))
		}
		lines = append(lines, "")
		if gpu.HasFan {
			lines = append(lines, fmt.Sprintf("%-10s %s", "fan", st.Muted.Render(fmt.Sprintf("%d rpm", gpu.FanRPM))))
		}
	} else {
		lines = append(lines, st.Muted.Render("no DRM GPU detected; CPU-only inference"))
	}
	lines = append(lines, "")
	lines = append(lines, st.Muted.Render(fmt.Sprintf("load %.2f %.2f %.2f · cpus %d · up %s",
		s.Load[0], s.Load[1], s.Load[2], s.CPUs, shortDuration(s.Uptime))))
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
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

func (m *Model) sidebarView(w, h, topH int) string {
	top := m.render.Panel(m.render.Icons.Server+" Status", "", m.statusBody(w-2), w, topH, false)
	bottomH := h - topH
	if bottomH < 4 {
		bottomH = 4
	}
	bottom := m.render.Panel(m.render.Icons.Hardware+" Machine", "", m.machineBody(w-2), w, bottomH, false)
	return top + "\n" + bottom
}

func (m *Model) statusBody(w int) []string {
	st := m.render.Styles
	var lines []string
	if len(m.alertList) > 0 {
		lines = append(lines, st.Header.Render("ALERTS"))
		start := len(m.alertList) - 2
		if start < 0 {
			start = 0
		}
		for _, a := range m.alertList[start:] {
			style := st.Warn
			if a.Severity == alerts.SeverityCritical {
				style = st.Bad
			}
			lines = append(lines, style.Render(ui.Fit(a.Rule+" · "+a.Message, w)))
		}
		if more := len(m.alertList) - 2; more > 0 {
			lines = append(lines, st.Muted.Render(fmt.Sprintf("%d older alert(s)", more)))
		}
		lines = append(lines, "")
	}
	lines = append(lines, st.Header.Render("SERVER"))
	if m.hasServer {
		lines = append(lines, st.Good.Render("running")+"  "+st.Muted.Render(m.server.URL()))
		lines = append(lines, fmt.Sprintf("pid %d · up %s", m.server.PID, uptimeSince(m.server.StartedAt)))
		what := m.server.Model
		if m.server.Preset != "" {
			what = m.server.Preset
		}
		lines = append(lines, "target "+fallback(what, "unknown"))
		lines = append(lines, st.Muted.Render("health "+fallback(m.serverHealth, "unknown")))
	} else if m.crash != nil {
		lines = append(lines, m.crashSidebar()...)
	} else {
		lines = append(lines, st.Muted.Render("not running"))
		lines = append(lines, st.Muted.Render("s start · S restart"))
	}
	lines = append(lines, "")
	lines = append(lines, st.Header.Render("ROUTER"))
	if m.hasRouter {
		lines = append(lines, st.Good.Render("running")+"  "+st.Muted.Render(m.router.URL()))
		lines = append(lines, fmt.Sprintf("pid %d · up %s", m.router.PID, uptimeSince(m.router.StartedAt)))
		loaded := 0
		for _, rm := range m.routerModels {
			if rm.Status.Value == "loaded" || rm.Status.Value == "sleeping" {
				loaded++
			}
		}
		lines = append(lines, fmt.Sprintf("models %d/%d · health %s", loaded, len(m.routerModels), fallback(m.routerHealth, "unknown")))
	} else {
		lines = append(lines, st.Muted.Render("not running"))
		lines = append(lines, st.Muted.Render("r start · l load · u unload"))
	}
	lines = append(lines, "")
	lines = append(lines, st.Header.Render("SELECTED"))
	if id, kind, ok := m.target(); ok {
		lines = append(lines, id+" "+st.Muted.Render("("+kind+")"))
		if m.fit.Ready {
			lines = append(lines, fmt.Sprintf("est %s", ui.HumanBytes(m.fit.Est.Total)))
		}
	}
	return lines
}

func (m *Model) machineBody(w int) []string {
	st := m.render.Styles
	s := m.snap
	barW := clamp(w-12, 8, 30)
	cpu := s.CPUPercent
	if m.cpuSamples > 0 {
		cpu = m.cpuAvg
	}
	var lines []string
	lines = append(lines, "cpu  "+st.Accent.Render(ui.Bar(cpu, barW, '█', '░'))+fmt.Sprintf(" %3.0f%%", cpu))
	lines = append(lines, "")
	lines = append(lines, "mem  "+st.Accent.Render(ui.Bar(s.MemPercent(), barW, '█', '░'))+fmt.Sprintf(" %3.0f%%", s.MemPercent()))
	lines = append(lines, st.Muted.Render(fmt.Sprintf("%s used of %s", ui.HumanBytes(s.MemUsed()), ui.HumanBytes(s.MemTotal))))
	lines = append(lines, "")
	if gpu, ok := s.Primary(); ok {
		name := truncate(gpu.Name, w-2)
		lines = append(lines, st.Header.Render(name))
		if gpu.HasVRAM {
			lines = append(lines, "vram "+st.Accent.Render(ui.Bar(gpu.VRAMPercent(), barW, '█', '░'))+fmt.Sprintf(" %3.0f%%", gpu.VRAMPercent()))
			lines = append(lines, st.Muted.Render(fmt.Sprintf("%s of %s", ui.HumanBytes(gpu.VRAMUsed), ui.HumanBytes(gpu.VRAMTotal))))
		}
		if gpu.HasTemp {
			tempStyle := st.Good
			if gpu.TempC >= 85 {
				tempStyle = st.Bad
			} else if gpu.TempC >= 70 {
				tempStyle = st.Warn
			}
			lines = append(lines, fmt.Sprintf("temp %s", tempStyle.Render(fmt.Sprintf("%d°C", gpu.TempC))))
		}
		if gpu.HasPower {
			lines = append(lines, st.Muted.Render(fmt.Sprintf("power %.0f W", gpu.PowerW)))
		}
	}
	return lines
}

func (m *Model) inputView() string {
	st := m.render.Styles
	if m.command {
		return st.Accent.Render("❯ ") + m.input.View()
	}
	if m.toastActive() {
		return st.Muted.Render(ui.Fit(" "+m.toast, m.width))
	}
	if m.busy != "" {
		return st.Accent.Render(" " + m.busy)
	}
	prompt := st.Muted.Render(" press / to type a command")
	theme := st.Muted.Render(m.themeName + " ")
	return sides(prompt, theme, m.width)
}

func (m *Model) footerView() string {
	st := m.render.Styles
	var hints string
	if m.command {
		hints = "enter run · esc cancel · tab complete"
	} else if m.tab == tabBench {
		hints = "j/k move · space mark · enter compare · f filter · D config diff · B bench · ? help · q quit"
	} else if m.tab == tabInference {
		hints = "m switch source · j/k select router model · L logs · B bench · ? help · q quit"
	} else if m.tab == tabLogs {
		hints = "j/k scroll · f follow · v raw/structured · m filter · enter target · ? help · q quit"
	} else {
		if m.crash != nil {
			hints = "S restart · x dismiss · tab switch · j/k move · / command · ? help · q quit"
		} else {
			hints = "tab switch · j/k move · enter start · x stop · r router · d doctor · D diff · / command · ? help · q quit"
		}
	}
	left := st.Muted.Render(" " + hints)
	size := st.Muted.Render(fmt.Sprintf("%dx%d ", m.width, m.height))
	return sides(left, size, m.width)
}

func (m *Model) overlayView() string {
	st := m.render.Styles
	lines := strings.Split(m.overlay, "\n")
	bodyH := m.height - 4
	if bodyH < 4 {
		bodyH = 4
	}
	start := clamp(m.overlayScroll, 0, maxInt(0, len(lines)-bodyH))
	end := start + bodyH
	if end > len(lines) {
		end = len(lines)
	}
	body := make([]string, 0, bodyH)
	for _, l := range lines[start:end] {
		body = append(body, " "+l)
	}
	panel := m.render.Panel(m.overlayTitle, "esc close · j/k scroll", body, m.width, m.height-1, true)
	return panel + "\n" + st.Muted.Render(ui.Fit(" overlay "+m.overlayTitle+" ("+fmt.Sprint(len(lines))+" lines)", m.width))
}

// helpers ----------------------------------------------------------------

// sides lays out left and right on one row, keeping the right side visible and
// truncating the left side when space is tight.
func sides(left, right string, width int) string {
	rw := lipgloss.Width(right)
	if rw+1 >= width {
		return ui.Fit(right, width)
	}
	return ui.Fit(left, width-rw-1) + " " + right
}

func kv(k, v string) string {
	return k + " " + v
}

func fallback(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func truncateRight(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

func shortenPath(p string, w int) string {
	if len(p) <= w {
		return p
	}
	if w <= 8 {
		return p[len(p)-w:]
	}
	return "…" + p[len(p)-(w-1):]
}

func windowStart(sel, visible, total int) int {
	if visible <= 0 || total <= visible {
		return 0
	}
	start := sel - visible/2
	if start < 0 {
		start = 0
	}
	if start+visible > total {
		start = total - visible
	}
	return start
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// uptimeSince renders a process uptime, or "-" when the start time is unknown
// (for example a state file written by an older version).
func uptimeSince(start time.Time) string {
	if start.IsZero() {
		return "-"
	}
	return shortDuration(time.Since(start))
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

// humanTokens renders a token count with k/M suffixes.
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// value2 returns the newest value of a series, scaled by max.
func latestFraction(series []float64, max float64) float64 {
	if len(series) == 0 || max <= 0 {
		return 0
	}
	return series[len(series)-1] / max
}

// abs expands ~ and makes a model path absolute.
func abs(p string) string {
	return config.AbsPath(p)
}
