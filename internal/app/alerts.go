package app

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/alerts"
)

// evaluateAlerts turns one tick into alert-engine input. It runs in Update
// with data already gathered by the background refresh, so it never blocks on
// HTTP, disk or notification processes.
func (m *Model) evaluateAlerts(msg tickMsg) {
	if m.alertEngine == nil || !m.alertEngine.Enabled() {
		return
	}
	in := alerts.Input{
		At:   msg.at,
		Logs: msg.alertLogs,
	}
	if gpu, ok := msg.snap.Primary(); ok {
		in.HasTemp, in.TempC = gpu.HasTemp, gpu.TempC
		in.HasVRAM = gpu.HasVRAM
		in.VRAMPercent = gpu.VRAMPercent()
	}
	if msg.hasServer {
		in.Instances = append(in.Instances, alerts.Instance{
			Name:      "server",
			Alive:     true,
			Health:    msg.serverHealth,
			StartedAt: msg.server.StartedAt,
		})
	}
	if msg.hasRouter {
		in.Instances = append(in.Instances, alerts.Instance{
			Name:      "router",
			Alive:     true,
			Health:    msg.routerHealth,
			StartedAt: msg.router.StartedAt,
		})
	}
	for _, rm := range msg.models {
		in.Models = append(in.Models, alerts.Model{
			ID:     rm.ID,
			Status: rm.Status.Value,
			Failed: rm.Status.Failed,
		})
	}

	newAlerts := m.alertEngine.Evaluate(in)
	if len(newAlerts) == 0 {
		return
	}
	m.alertList = append(m.alertList, newAlerts...)
	if len(m.alertList) > 20 {
		m.alertList = m.alertList[len(m.alertList)-20:]
	}
	last := newAlerts[len(newAlerts)-1]
	m.setToast(last.Rule + ": " + last.Message)
	m.alertCmd = m.notifyAlerts(newAlerts)
}

// notifyAlerts dispatches to the optional notifiers off the UI goroutine.
// Missing binaries and webhook failures are ignored: the alert is already in
// the status bar and must not turn into a second, noisier failure.
func (m *Model) notifyAlerts(list []alerts.Alert) tea.Cmd {
	ns := alerts.Notifiers(alerts.FromConfig(m.cfg.Alerts))
	if len(ns) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		alerts.Dispatch(ctx, ns, list)
		return nil
	}
}
