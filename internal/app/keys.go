package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/theme"
)

var commandNames = []string{
	"start", "restart", "stop", "router", "load", "unload", "reload",
	"edit", "doctor", "theme", "tab", "logs", "help", "quit",
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editor != nil {
		return m.updateEditor(msg)
	}
	if m.overlay != "" {
		switch msg.String() {
		case "esc", "enter", "q":
			m.overlay = ""
		case "j", "down":
			m.overlayScroll++
		case "k", "up":
			m.overlayScroll--
		case "g":
			m.overlayScroll = 0
		case "G":
			m.overlayScroll = 1 << 20
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}
	if m.command {
		return m.updateCommand(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.showHelp()
	case "/", ":":
		m.openCommand("")
	case "esc":
		m.toast = ""
	case "tab":
		m.setTab((m.tab + 1) % tabCount)
	case "shift+tab":
		m.setTab((m.tab - 1 + tabCount) % tabCount)
	case "1", "2", "3", "4", "5", "6":
		m.setTab(tab(msg.String()[0] - '1'))
	case "j", "down":
		switch m.tab {
		case tabLogs:
			m.scrollLogs(1)
		case tabHardware:
		default:
			m.moveSelection(1)
		}
	case "k", "up":
		switch m.tab {
		case tabLogs:
			m.scrollLogs(-1)
		case tabHardware:
		default:
			m.moveSelection(-1)
		}
	case "g":
		if m.tab == tabLogs {
			m.follow = false
			m.logScroll = 1 << 20
		} else {
			m.moveSelection(-1 << 20)
		}
	case "G":
		if m.tab == tabLogs {
			m.follow = true
			m.logScroll = 0
		} else {
			m.moveSelection(1 << 20)
		}
	case "enter":
		return m, m.enterAction()
	case "s":
		if id, kind, ok := m.target(); ok {
			return m, m.startTarget(id, kind)
		}
		m.setToast("nothing selected")
	case "x":
		return m, m.stopServer()
	case "S":
		return m, m.restartServer()
	case "r":
		return m, m.startRouter()
	case "R":
		return m, m.stopRouter()
	case "l":
		return m, m.routerAction(true)
	case "u":
		return m, m.routerAction(false)
	case "m":
		if id := m.selectedRouterID(); id != "" {
			m.statsID = id
			m.setToast("showing stats for " + id)
		}
	case "L":
		m.setTab(tabLogs)
	case "e":
		m.openEditor()
	case "d":
		return m, m.runDoctor()
	case "t":
		m.cycleTheme(1)
	case "f":
		if m.tab == tabLogs {
			m.follow = !m.follow
			if m.follow {
				m.logScroll = 0
			}
		}
	case "ctrl+r":
		m.reloadConfig()
	}
	return m, nil
}

// enterAction is the primary action for the focused tab.
func (m *Model) enterAction() tea.Cmd {
	switch m.tab {
	case tabDashboard, tabPresets, tabModels:
		id, kind, ok := m.target()
		if !ok {
			m.setToast("nothing selected")
			return nil
		}
		return m.startTarget(id, kind)
	case tabRouter:
		if !m.hasRouter {
			return m.startRouter()
		}
		return m.routerAction(true)
	case tabLogs:
		if m.logSel == 0 {
			m.logSel, m.logName = 1, "router"
		} else {
			m.logSel, m.logName = 0, "server"
		}
		m.follow = true
		m.logScroll = 0
		m.loadLogs()
	}
	return nil
}

func (m *Model) openCommand(prefix string) {
	m.command = true
	m.input.SetValue(prefix)
	m.input.CursorEnd()
	m.input.Focus()
	m.histIdx = len(m.history)
}

func (m *Model) updateCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.command = false
		m.input.Blur()
		m.input.SetValue("")
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.input.Value())
		m.command = false
		m.input.Blur()
		m.input.SetValue("")
		if line == "" {
			return m, nil
		}
		if len(m.history) == 0 || m.history[len(m.history)-1] != line {
			m.history = append(m.history, line)
			if len(m.history) > 50 {
				m.history = m.history[len(m.history)-50:]
			}
		}
		m.histIdx = len(m.history)
		cmd, _ := m.runCommand(line)
		return m, cmd
	case "up":
		if len(m.history) == 0 {
			return m, nil
		}
		if m.histIdx > 0 {
			m.histIdx--
		}
		m.input.SetValue(m.history[m.histIdx])
		m.input.CursorEnd()
		return m, nil
	case "down":
		if len(m.history) == 0 {
			return m, nil
		}
		if m.histIdx < len(m.history)-1 {
			m.histIdx++
			m.input.SetValue(m.history[m.histIdx])
		} else {
			m.histIdx = len(m.history)
			m.input.SetValue("")
		}
		m.input.CursorEnd()
		return m, nil
	case "tab":
		m.completeCommand()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// completeCommand completes a command word, or a model ID where one is
// expected. It never guesses a multi-word completion.
func (m *Model) completeCommand() {
	value := m.input.Value()
	trimmed := strings.TrimPrefix(value, "/")
	if trimmed == "" {
		m.input.SetValue("/" + commandNames[0])
		m.input.CursorEnd()
		return
	}
	if strings.ContainsAny(trimmed, " ") {
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			return
		}
		prefix := fields[len(fields)-1]
		candidates := m.modelCandidates(fields[0])
		head := strings.Join(fields[:len(fields)-1], " ")
		for _, c := range candidates {
			if strings.HasPrefix(c, prefix) {
				m.input.SetValue("/" + head + " " + c)
				m.input.CursorEnd()
				return
			}
		}
		return
	}
	for _, c := range commandNames {
		if strings.HasPrefix(c, trimmed) {
			m.input.SetValue("/" + c + " ")
			m.input.CursorEnd()
			return
		}
	}
}

func (m *Model) modelCandidates(cmd string) []string {
	switch cmd {
	case "start", "restart":
		out := append([]string{}, m.presets...)
		return append(out, m.models...)
	case "load", "unload":
		var out []string
		for _, rm := range m.routerModels {
			out = append(out, rm.ID)
		}
		return out
	case "logs":
		return []string{"server", "router"}
	case "stop":
		return []string{"server", "router", "all"}
	case "router":
		return []string{"start", "stop", "reload", "models"}
	case "theme":
		return theme.Order()
	case "tab":
		return tabNames
	}
	return nil
}
