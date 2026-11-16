package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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

func (m *Model) sidebarView(w, h, topH int) string {
	top := m.render.Panel(m.render.Icons.Server+" Status", "", m.statusBody(w-2), w, topH, false)
	bottomH := h - topH
	if bottomH < 4 {
		bottomH = 4
	}
	bottom := m.render.Panel(m.render.Icons.Hardware+" Machine", "", m.machineBody(w-2), w, bottomH, false)
	return top + "\n" + bottom
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
	} else if m.tab == tabRouter {
		hints = "l/u load/unload · w group · j/k select · r/R router · E endpoint · enter load · ? help · q quit"
	} else {
		if m.crash != nil {
			hints = "S restart · x dismiss · tab switch · j/k move · / command · ? help · q quit"
		} else {
			hints = "tab switch · j/k move · enter start · x stop · r router · E endpoint · d doctor · D diff · / command · ? help · q quit"
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
	hint := "esc close · j/k scroll"
	footer := " overlay " + m.overlayTitle + " (" + fmt.Sprint(len(lines)) + " lines)"
	if m.endpointOpen {
		hint = "tab/1-4 select · c copy · u URL · y model"
		if m.endpointNotice != "" {
			footer = " " + m.endpointNotice + " · " + m.overlayTitle
		}
	}
	panel := m.render.Panel(m.overlayTitle, hint, body, m.width, m.height-1, true)
	return panel + "\n" + st.Muted.Render(ui.Fit(footer, m.width))
}

// helpers ----------------------------------------------------------------

// sides lays out left and right on one row, keeping the right side visible and
// truncating the left side when space is tight.
