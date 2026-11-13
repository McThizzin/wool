package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/daemon"
	"wool/internal/ui"
)

const maxRestartBackoff = time.Minute

// detectCrash compares the previous tick with the new one. A state file that
// is present but whose process is gone means the server died without going
// through wool. A clean stop removes the state file, so it never reaches this
// path.
func (m *Model) detectCrash(msg tickMsg) {
	if msg.hasServer {
		if m.crash != nil {
			m.clearCrash()
			// Keep the attempt window across an automatic restart; only a
			// manual revival resets it.
			if !m.expectStop {
				m.resetRestartState()
			}
		}
		m.expectServer = true
		return
	}
	if m.expectStop || msg.server.Name == "" {
		if msg.server.Name == "" && !m.expectStop {
			// The state file was removed, usually by `wool stop`.
			m.expectServer = false
		}
		return
	}
	if !m.hasServer && !m.expectServer {
		return
	}
	// Do not re-report the same dead process on every tick.
	if m.crash != nil && m.crashState.PID == msg.server.PID &&
		m.crashState.StartedAt.Equal(msg.server.StartedAt) {
		return
	}
	c := daemon.ClassifyCrash(msg.serverLog)
	c.At = msg.at
	m.crash = &c
	m.crashState = msg.server.State
	m.expectServer = false
	m.scheduleRestart(msg.at)
}

// clearCrash removes the crash card and its remembered launch.
func (m *Model) clearCrash() {
	m.crash = nil
	m.crashState = daemon.State{}
	m.restartAt = time.Time{}
}

// resetRestartState forgets the attempt history. Manual starts and stops call
// it so a fresh launch is not punished for an earlier crash loop.
func (m *Model) resetRestartState() {
	m.restartAt = time.Time{}
	m.restartHist = nil
}

// pruneRestartHist drops attempts outside the configured sliding window.
func (m *Model) pruneRestartHist(now time.Time) {
	cutoff := now.Add(-m.cfg.Server.RestartWindowDuration())
	kept := m.restartHist[:0]
	for _, at := range m.restartHist {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	m.restartHist = kept
}

// restartAttempts counts attempts inside the window without mutating history,
// so rendering a status line is side-effect free.
func (m *Model) restartAttempts(now time.Time) int {
	cutoff := now.Add(-m.cfg.Server.RestartWindowDuration())
	n := 0
	for _, at := range m.restartHist {
		if at.After(cutoff) {
			n++
		}
	}
	return n
}

// nextRestartDelay doubles the configured backoff per attempt, capped at one
// minute so a persistent failure is retried often enough to be useful.
func (m *Model) nextRestartDelay(attempt int) time.Duration {
	d := m.cfg.Server.RestartBackoffDuration()
	for i := 0; i < attempt; i++ {
		if d >= maxRestartBackoff {
			return maxRestartBackoff
		}
		d *= 2
	}
	if d > maxRestartBackoff {
		return maxRestartBackoff
	}
	return d
}

// scheduleRestart arms the next automatic attempt after a crash. It does
// nothing when the policy is disabled, the window is exhausted, or the crash
// was already handled.
func (m *Model) scheduleRestart(at time.Time) {
	if m.crash == nil || m.crashState.Name == "" {
		return
	}
	if !m.cfg.Server.RestartEnabled() || m.expectStop {
		return
	}
	max := m.cfg.Server.RestartMaxAttempts()
	if max <= 0 {
		return
	}
	attempts := m.restartAttempts(at)
	if attempts >= max {
		return
	}
	m.restartAt = at.Add(m.nextRestartDelay(attempts))
}

// autoRestartCmd fires a scheduled restart once its deadline has passed. The
// attempt is recorded before launching so a failure cannot loop without
// counting against the window.
func (m *Model) autoRestartCmd() tea.Cmd {
	if m.crash == nil || m.restartAt.IsZero() || m.expectStop {
		return nil
	}
	now := time.Now()
	if now.Before(m.restartAt) {
		return nil
	}
	state := m.crashState
	mgr := m.mgr
	m.restartAt = time.Time{}
	m.restartHist = append(m.restartHist, now)
	m.pruneRestartHist(now)
	m.expectServer = true
	m.expectStop = true
	m.busy = "restarting server..."
	return func() tea.Msg {
		_ = mgr.Stop(state.Name, 20*time.Second)
		st, err := mgr.Start(daemon.SpecFromState(state))
		return actionMsg{label: "restarted server", err: err, state: st, hasSt: err == nil, auto: true}
	}
}

// restartCrashed relaunches the recorded instance. It is the manual equivalent
// of an automatic attempt and resets the policy history.
func (m *Model) restartCrashed() tea.Cmd {
	if m.crash == nil || m.crashState.Name == "" {
		return nil
	}
	state := m.crashState
	mgr := m.mgr
	m.resetRestartState()
	m.expectServer = true
	m.expectStop = true
	m.busy = "restarting server..."
	return func() tea.Msg {
		_ = mgr.Stop(state.Name, 20*time.Second)
		st, err := mgr.Start(daemon.SpecFromState(state))
		return actionMsg{label: "restarted server", err: err, state: st, hasSt: err == nil}
	}
}

// dismissCrash hides the card but keeps the state file so an explicit
// `wool restart` can still reuse the recorded launch.
func (m *Model) dismissCrash() {
	if m.crash == nil {
		return
	}
	m.clearCrash()
	m.resetRestartState()
	m.expectServer = false
	m.setToast("crash dismissed; state kept for wool restart")
}

// restartStatus is the one-line policy summary shown on the crash card.
func (m *Model) restartStatus(now time.Time) string {
	if !m.cfg.Server.RestartEnabled() {
		return "auto-restart disabled"
	}
	max := m.cfg.Server.RestartMaxAttempts()
	if max <= 0 {
		return "auto-restart disabled (restart_max = 0)"
	}
	if m.restartAttempts(now) >= max {
		return fmt.Sprintf("auto-restart stopped after %d attempt(s)", max)
	}
	if !m.restartAt.IsZero() {
		d := m.restartAt.Sub(now)
		if d < 0 {
			d = 0
		}
		return fmt.Sprintf("restart in %s (attempt %d/%d)",
			shortDuration(d), m.restartAttempts(now)+1, max)
	}
	return fmt.Sprintf("press S to restart (attempt %d/%d)", m.restartAttempts(now)+1, max)
}

// crashCard renders the dashboard crash block. It returns no lines when there
// is nothing to show.
func (m *Model) crashCard(w int) []string {
	if m.crash == nil {
		return nil
	}
	st := m.render.Styles
	now := time.Now()
	lines := []string{
		st.Bad.Render("⚠ server crashed · " + m.crash.Reason.Label()),
		st.Muted.Render(fmt.Sprintf("%s · %s",
			m.crash.At.Local().Format("15:04:05"), m.restartStatus(now))),
	}
	shown := 0
	for _, l := range m.crash.Lines {
		if shown >= 4 {
			break
		}
		lines = append(lines, st.Muted.Render(ui.Fit("  "+l, w-2)))
		shown++
	}
	lines = append(lines, st.Muted.Render("  S restart · x dismiss"))
	return lines
}

// crashSidebar is the compact crash status used in the sidebar.
func (m *Model) crashSidebar() []string {
	if m.crash == nil {
		return nil
	}
	st := m.render.Styles
	return []string{
		st.Bad.Render("crashed: " + m.crash.Reason.Label()),
		st.Muted.Render(m.crash.At.Local().Format("15:04:05") + " · " + m.restartStatus(time.Now())),
		st.Muted.Render("S restart · x dismiss"),
	}
}
