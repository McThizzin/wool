package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/discovery"
)

// startDiscovery scans model_dirs in the background. The TUI stays responsive
// because discovery.Scan only runs inside the returned command.
func (m *Model) startDiscovery() tea.Cmd {
	if m.discoveryBusy {
		return nil
	}
	m.discoveryBusy = true
	m.busy = "scanning model_dirs..."
	cfg := m.cfg
	return func() tea.Msg {
		return discoveryMsg{report: discovery.Scan(cfg, discovery.Options{})}
	}
}

func (m *Model) selectedDiscovered() (discovery.Entry, bool) {
	if m.discoveredSel >= 0 && m.discoveredSel < len(m.discovery.Entries) {
		return m.discovery.Entries[m.discoveredSel], true
	}
	return discovery.Entry{}, false
}

func (m *Model) newDiscoveredCount() int {
	n := 0
	for _, e := range m.discovery.Entries {
		if len(e.Configured) == 0 {
			n++
		}
	}
	return n
}

// openDiscoverAdd asks for confirmation before writing models.<id>.
func (m *Model) openDiscoverAdd() {
	e, ok := m.selectedDiscovered()
	if !ok {
		m.setToast("nothing selected to add")
		return
	}
	if len(e.Configured) > 0 {
		m.setToast("already configured as " + strings.Join(e.Configured, ", "))
		return
	}
	m.discoveryAdd = true
	m.overlayTitle = "add model"
	m.overlayScroll = 0
	m.overlay = strings.Join([]string{
		"add this GGUF as a wool model?",
		"",
		"id     " + e.SuggestedID,
		"alias  " + fallback(e.SuggestedAlias, e.SuggestedID),
		"path   " + e.Path,
		"",
		"enter add · esc cancel",
	}, "\n")
}

func (m *Model) updateDiscoverConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.discoveryAdd = false
		m.overlay = ""
		return m, m.addDiscovered()
	case "esc", "q":
		m.discoveryAdd = false
		m.overlay = ""
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// addDiscovered writes the selected entry with its suggested ID and alias. It
// reloads from disk first so an external edit is not overwritten.
func (m *Model) addDiscovered() tea.Cmd {
	e, ok := m.selectedDiscovered()
	if !ok {
		m.setToast("nothing selected to add")
		return nil
	}
	if len(e.Configured) > 0 {
		m.setToast("already configured as " + strings.Join(e.Configured, ", "))
		return nil
	}
	cfgPath := m.cfgPath
	id, alias, path := e.SuggestedID, e.SuggestedAlias, e.Path
	m.busy = "adding " + id + "..."
	return func() tea.Msg {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return discoveryAddMsg{id: id, err: err}
		}
		err = cfg.AddModel(cfgPath, id, path, alias)
		return discoveryAddMsg{id: id, err: err}
	}
}

func (m *Model) showDiscoveryWarnings() {
	if len(m.discovery.Warnings) == 0 {
		m.setToast("no scan warnings")
		return
	}
	lines := []string{fmt.Sprintf("%d scan warning(s)", len(m.discovery.Warnings)), ""}
	for _, warning := range m.discovery.Warnings {
		if warning.Path == "" {
			lines = append(lines, warning.Message)
		} else {
			lines = append(lines, warning.Path+": "+warning.Message)
		}
	}
	m.overlayTitle = "scan warnings"
	m.overlayScroll = 0
	m.endpointOpen = false
	m.overlay = strings.Join(lines, "\n")
}
