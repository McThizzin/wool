package app

import (
	"fmt"
	"strings"
	"wool/internal/alerts"
	"wool/internal/ui"
)

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
