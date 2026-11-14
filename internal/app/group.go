package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/llamaserver"
)

// openGroupPicker shows the configured warm sets. Selecting one loads it.
func (m *Model) openGroupPicker() {
	if len(m.groups) == 0 {
		m.setToast("no groups configured")
		return
	}
	m.groupPick = true
	m.groupSel = clamp(m.groupSel, 0, len(m.groups)-1)
	m.renderGroupPicker()
}

func (m *Model) renderGroupPicker() {
	lines := make([]string, 0, len(m.groups)+2)
	for i, name := range m.groups {
		marker := "  "
		if i == m.groupSel {
			marker = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%-20s %s", marker, name,
			strings.Join(m.cfg.Groups[name].Models, ", ")))
	}
	lines = append(lines, "", "enter load · esc cancel")
	m.overlayTitle = "load group"
	m.overlay = strings.Join(lines, "\n")
	m.overlayScroll = 0
}

func (m *Model) updateGroupPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.groupPick = false
		m.overlay = ""
	case "j", "down":
		m.groupSel = clamp(m.groupSel+1, 0, len(m.groups)-1)
		m.renderGroupPicker()
	case "k", "up":
		m.groupSel = clamp(m.groupSel-1, 0, len(m.groups)-1)
		m.renderGroupPicker()
	case "g":
		m.groupSel = 0
		m.renderGroupPicker()
	case "G":
		m.groupSel = maxInt(0, len(m.groups)-1)
		m.renderGroupPicker()
	case "enter":
		name := m.groups[m.groupSel]
		m.groupPick = false
		m.overlay = ""
		return m, m.requestGroupLoad(name)
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// updateGroupConfirm handles the over-limit warning. Loading starts only after
// an explicit enter.
func (m *Model) updateGroupConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := m.groupWait
		m.groupWait = ""
		m.overlay = ""
		return m, m.startGroupLoad(name)
	case "esc", "q":
		m.groupWait = ""
		m.overlay = ""
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// requestGroupLoad starts a group load, or asks for confirmation when the
// group is larger than models_max.
func (m *Model) requestGroupLoad(name string) tea.Cmd {
	group, ok := m.cfg.Groups[name]
	if !ok {
		m.setToast("unknown group " + name)
		return nil
	}
	if len(group.Models) == 0 {
		m.setToast("group " + name + " has no models")
		return nil
	}
	if !m.hasRouter {
		m.setToast("router is not running")
		return nil
	}
	if warning := groupWarning(name, len(group.Models), m.cfg.Router.ModelsMax); warning != "" {
		m.groupWait = name
		m.overlayTitle = "load group " + name
		m.overlay = warning + "\n\nenter  load anyway\nesc    cancel"
		m.overlayScroll = 0
		return nil
	}
	return m.startGroupLoad(name)
}

// startGroupLoad loads the group in the background. LoadGroup waits for each
// model, so the summary reports real load results rather than request
// acceptance.
func (m *Model) startGroupLoad(name string) tea.Cmd {
	group, ok := m.cfg.Groups[name]
	if !ok || len(group.Models) == 0 {
		m.setToast("group " + name + " is no longer configured")
		return nil
	}
	if !m.hasRouter {
		m.setToast("router is not running")
		return nil
	}
	host, port := m.router.Host, m.router.Port
	models := append([]string(nil), group.Models...)
	limit := m.cfg.Router.ModelsMax
	m.busy = "loading group " + name + "..."
	return func() tea.Msg {
		client := llamaserver.NewClient(host, port)
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(len(models))*120*time.Second+10*time.Second)
		defer cancel()
		results := llamaserver.LoadGroup(ctx, client, models, 120*time.Second, 400*time.Millisecond)
		return groupMsg{name: name, results: results, limit: limit}
	}
}

// groupWarning explains that a group cannot stay fully resident under
// models_max. An unlimited or large enough limit has no warning.
func groupWarning(name string, models, limit int) string {
	if limit <= 0 || models <= limit {
		return ""
	}
	return fmt.Sprintf("Group %s has %d models but router.models_max is %d.\nThe router will evict earlier models as later ones load.",
		name, models, limit)
}

// groupSummary is the one-line result shown in the status bar.
func groupSummary(name string, results []llamaserver.GroupResult, limit int) (string, []string) {
	var failed []string
	loaded := 0
	for _, r := range results {
		if r.Err != nil {
			failed = append(failed, r.Model)
		} else {
			loaded++
		}
	}
	summary := fmt.Sprintf("group %s: %d/%d loaded", name, loaded, len(results))
	if len(failed) > 0 {
		summary += "; failed: " + strings.Join(failed, ", ")
	} else if limit > 0 && len(results) > limit {
		summary += fmt.Sprintf(" (models_max %d; earlier models evicted)", limit)
	}
	return summary, failed
}

// groupDetails lists every model's outcome for the failure overlay.
func groupDetails(results []llamaserver.GroupResult, limit int) string {
	lines := make([]string, 0, len(results)+2)
	for _, r := range results {
		if r.Err != nil {
			lines = append(lines, fmt.Sprintf("%-24s FAILED  %v", r.Model, r.Err))
			continue
		}
		status := r.Status
		if status == "" {
			status = "loaded"
		}
		lines = append(lines, fmt.Sprintf("%-24s %s", r.Model, status))
	}
	if limit > 0 && len(results) > limit {
		lines = append(lines, "",
			fmt.Sprintf("router.models_max is %d; only the last %d model(s) remain resident.", limit, limit))
	}
	return strings.Join(lines, "\n")
}
