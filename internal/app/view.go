package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"wool/internal/config"
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
	case tabHardware:
		return m.render.Panel(m.render.Icons.Hardware+" Hardware", m.hardwareRight(), m.hardwareBody(h-2), w, h, focused)
	case tabLogs:
		return m.render.Panel(m.render.Icons.Logs+" Logs", m.logRight(), m.logsBody(w, h-2), w, h, focused)
	}
	return ""
}

func (m *Model) dashRight() string {
	st := m.render.Styles
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
	return m.render.Styles.Muted.Render(name + " · " + mode)
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
	if fi.VRAMTotal == 0 {
		return []string{st.Muted.Render(fmt.Sprintf("estimated %s (no GPU VRAM detected)", ui.HumanBytes(fi.Est.Total)))}
	}
	pct := float64(fi.Est.Total) / float64(fi.VRAMTotal) * 100
	barW := clamp(w-34, 10, 28)
	bar := ui.Bar(pct, barW, '█', '░')
	style := st.Good
	verdict := "fits"
	if fi.Est.Total > fi.VRAMTotal {
		style = st.Bad
		verdict = "over"
	} else if pct > 90 {
		style = st.Warn
		verdict = "tight"
	}
	label := fmt.Sprintf("VRAM %s / %s", ui.HumanBytes(fi.Est.Total), ui.HumanBytes(fi.VRAMTotal))
	return []string{style.Render(label) + " " + style.Render(bar) + fmt.Sprintf(" %3.0f%% %s", pct, verdict)}
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
			if fit.VRAMTotal > 0 && fit.Est.Total > fit.VRAMTotal {
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
			if fi.VRAMTotal > 0 && fi.Est.Total > fi.VRAMTotal {
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
	if len(m.routerModels) == 0 {
		lines = append(lines, st.Muted.Render("no models reported by the router"))
	}
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
		if m.statsID == sel.ID {
			lines = append(lines, m.statsLines(w)...)
		}
	}
	return lines
}

func (m *Model) statsLines(w int) []string {
	st := m.render.Styles
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
		var busy int
		for _, sl := range m.slots {
			if sl.IsProcessing {
				busy++
			}
		}
		lines = append(lines, st.Muted.Render(fmt.Sprintf("slots %d/%d busy · processing requests %.0f", busy, len(m.slots), s.RequestsProcessing)))
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
	} else {
		hints = "tab switch · j/k move · enter start · x stop · r router · d doctor · / command · ? help · q quit"
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
