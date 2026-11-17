package app

import (
	"fmt"
	"wool/internal/ui"
)

func (m *Model) hardwareRight() string {
	if gpu, ok := m.snap.Primary(); ok && gpu.HasVRAM {
		return m.render.Styles.Muted.Render(fmt.Sprintf("%s / %s", ui.HumanBytes(gpu.VRAMUsed), ui.HumanBytes(gpu.VRAMTotal)))
	}
	return m.render.Styles.Muted.Render(fmt.Sprintf("%s RAM", ui.HumanBytes(m.snap.MemTotal)))
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
