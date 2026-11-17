package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = maxInt(20, msg.Width-4)
		if m.editor != nil {
			m.editor.input.Width = m.input.Width
		}
		if m.endpointOpen {
			m.refreshEndpointOverlay()
		}
		m.ensureFit()
		return m, nil
	case spinner.TickMsg:
		// The spinner is decorative; keep only one tick loop alive via busy.
		if m.busy == "" {
			return m, nil
		}
		return m, m.spinnerTick()
	case tickMsg:
		m.applyTick(msg)
		cmd := m.refreshLater(m.pollInterval())
		if auto := m.autoRestartCmd(); auto != nil {
			cmd = tea.Batch(cmd, auto)
		}
		if m.alertCmd != nil {
			cmd = tea.Batch(cmd, m.alertCmd)
			m.alertCmd = nil
		}
		return m, cmd
	case actionMsg:
		m.busy = ""
		m.actionAt = time.Now()
		if msg.err != nil {
			m.expectStop = false
			m.expectServer = false
			m.setToast("error: " + oneLine(msg.err.Error(), 120))
			if msg.auto {
				m.scheduleRestart(time.Now())
			}
		} else {
			m.setToast(msg.label)
			m.expectStop = false
			switch {
			case msg.stopped:
				m.server, m.hasServer = daemon.Instance{}, false
				m.expectServer = false
				m.clearCrash()
				m.resetRestartState()
			case msg.hasSt && msg.router:
				m.router = daemon.Instance{State: msg.state, Alive: true}
				m.hasRouter = true
				m.logSel, m.logName = 1, "router"
			case msg.hasSt:
				m.server = daemon.Instance{State: msg.state, Alive: true}
				m.hasServer = true
				m.expectServer = true
				m.clearCrash()
				if !msg.auto {
					m.resetRestartState()
				}
				m.logSel, m.logName = 0, "server"
			}
		}
		cmd := m.refreshLater(150 * time.Millisecond)
		if msg.measure != nil && msg.err == nil {
			cmd = tea.Batch(cmd, m.captureMeasurement(msg.measure))
		}
		return m, cmd
	case measuredMsg:
		if msg.err != nil {
			m.setToast("measurement: " + oneLine(msg.err.Error(), 120))
		} else {
			m.fitCache = map[string]fitInfo{}
			m.fitKey = ""
			m.ensureFit()
		}
		if msg.smoke != nil && !msg.smoke.OK() {
			m.setToast("smoke test: " + oneLine(msg.smoke.Err, 120))
		}
		return m, nil
	case groupMsg:
		m.busy = ""
		summary, failed := groupSummary(msg.name, msg.results, msg.limit)
		m.setToast(summary)
		if len(failed) > 0 {
			m.overlayTitle = "group " + msg.name
			m.overlay = groupDetails(msg.results, msg.limit)
			m.overlayScroll = 0
			m.endpointOpen = false
		}
		return m, m.refreshLater(150 * time.Millisecond)
	case doctorMsg:
		m.busy = ""
		if msg.err != nil {
			m.setToast("doctor: " + msg.err.Error())
			return m, nil
		}
		m.overlay = msg.text
		m.overlayTitle = "doctor"
		m.overlayScroll = 0
		m.endpointOpen = false
		if msg.failures > 0 {
			m.setToast(fmt.Sprintf("doctor found %d failure(s)", msg.failures))
		} else {
			m.setToast("doctor: no failures")
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) applyTick(msg tickMsg) {
	m.alertCmd = nil
	m.snap = msg.snap
	m.hist.Push(msg.snap)
	if m.cpuSamples == 0 {
		m.cpuAvg = msg.snap.CPUPercent
	} else {
		// Smooth the header/CPU readout. Raw samples are still kept in
		// history, so the graph continues to show the full swing.
		m.cpuAvg = m.cpuAvg*0.6 + msg.snap.CPUPercent*0.4
	}
	m.cpuSamples++
	m.detectCrash(msg)
	m.server, m.hasServer = msg.server, msg.hasServer
	m.router, m.hasRouter = msg.router, msg.hasRouter
	m.serverHealth, m.routerHealth = msg.serverHealth, msg.routerHealth
	m.routerModels = msg.models
	m.trackRouterResidency(msg.at)
	if msg.modelsOK {
		m.usage.track(m.meas, msg.at, msg.router, msg.models, msg.usage)
	}
	m.usage.flush(m.meas, msg.at)
	m.routerErr = msg.routerErr
	m.stats, m.slots, m.statsID = msg.stats, msg.slots, msg.statsID
	m.statsErr = msg.statsErr
	m.srvStats, m.srvSlots, m.srvStatsErr = msg.srvStats, msg.srvSlots, msg.srvStatsErr

	// A pinned inference source is dropped once its instance goes away so
	// the panel follows reality instead of a stale preference.
	if m.inf.prefer == "server" && !m.hasServer {
		m.inf.prefer = ""
	}
	if m.inf.prefer == "router" && !m.hasRouter {
		m.inf.prefer = ""
	}

	if m.routerSelID != "" {
		for i, rm := range m.routerModels {
			if rm.ID == m.routerSelID {
				m.routerSel = i
				break
			}
		}
	}
	m.clampSelections()
	m.updateInference(msg.at)
	if m.tab == tabDashboard || m.tab == tabLogs {
		m.loadLogs()
	}
	m.evaluateAlerts(msg)
	if m.tab == tabBench {
		m.loadBenchHistory()
	}
	m.ensureFit()
}

// pollInterval is how often the TUI refreshes hardware and server state. It
// follows server.poll_interval but is clamped so a typo cannot stall or spin
// the interface.
func (m *Model) pollInterval() time.Duration {
	d, err := time.ParseDuration(m.cfg.Server.PollInterval)
	if err != nil || d <= 0 {
		return time.Second
	}
	if d < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	if d > time.Minute {
		return time.Minute
	}
	return d
}

// refreshLater captures only immutable inputs, then samples hardware and the
// HTTP APIs in the background.
func (m *Model) refreshLater(delay time.Duration) tea.Cmd {
	mgr := m.mgr
	sys := m.sys
	selected := m.selectedRouterID()
	tabAt := m.tab
	alertsOn := m.alertEngine != nil && m.alertEngine.Enabled()
	watchServer := m.hasServer || m.expectServer
	return func() tea.Msg {
		if delay > 0 {
			time.Sleep(delay)
		}
		msg := tickMsg{at: time.Now()}
		msg.snap, _ = sys.Sample()
		if inst, err := mgr.Status("server"); err == nil {
			msg.server = inst
			msg.hasServer = inst.Alive
			if inst.Alive {
				c := llamaserver.NewClient(inst.Host, inst.Port)
				c.HTTP.Timeout = 2 * time.Second
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if h, err := c.Health(ctx); err == nil {
					msg.serverHealth = h.Status
				} else {
					msg.serverHealth = "unreachable"
				}
				msg.srvStats, msg.srvSlots, msg.srvStatsErr = c.ScrapeInference(ctx, "")
				cancel()
			} else if watchServer {
				// Tail only around the alive -> dead transition so the
				// crash card sees the lines that explain this launch.
				if lines, err := mgr.Tail("server", 200); err == nil {
					msg.serverLog = lines
				}
			}
		}
		if inst, err := mgr.Status("router"); err == nil {
			msg.router = inst
			msg.hasRouter = inst.Alive
			if inst.Alive {
				c := llamaserver.NewClient(inst.Host, inst.Port)
				c.HTTP.Timeout = 2 * time.Second
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if h, err := c.Health(ctx); err == nil {
					msg.routerHealth = h.Status
				} else {
					msg.routerHealth = "unreachable"
				}
				if mm, err := c.Models(ctx, false); err != nil {
					msg.routerErr = err.Error()
				} else {
					sort.Slice(mm, func(i, j int) bool { return mm[i].ID < mm[j].ID })
					msg.models, msg.modelsOK = mm, true
					msg.statsID = selected
					if selected != "" {
						for _, rm := range mm {
							if rm.ID != selected {
								continue
							}
							if routerResident(rm) {
								if rm.Status.Value == "loaded" {
									msg.stats, msg.slots, msg.statsErr = c.ScrapeInference(ctx, selected)
								} else {
									// A sleeping model serves cached metrics but
									// /slots would wake it.
									msg.stats, msg.statsErr = c.ScrapeMetrics(ctx, selected)
								}
							}
							break
						}
					}
					// Usage only needs counters. Use /metrics directly for
					// sleeping models so a poll never wakes them.
					msg.usage = map[string]llamaserver.Metrics{}
					metricsOff := msg.statsErr != ""
					for _, rm := range mm {
						if !routerResident(rm) {
							continue
						}
						if rm.ID == selected && !metricsOff {
							msg.usage[rm.ID] = msg.stats
							continue
						}
						if metricsOff {
							continue
						}
						m, err := c.Metrics(ctx, rm.ID)
						if err != nil {
							if errors.Is(err, llamaserver.ErrMetricsDisabled) {
								metricsOff = true
							}
							continue
						}
						msg.usage[rm.ID] = m
					}
				}
				cancel()
			}
		}
		if alertsOn {
			msg.alertLogs = map[string][]string{}
			if msg.hasServer {
				if lines, err := mgr.Tail("server", 200); err == nil {
					msg.alertLogs["server"] = lines
				}
			}
			if msg.hasRouter {
				if lines, err := mgr.Tail("router", 200); err == nil {
					msg.alertLogs["router"] = lines
				}
			}
		}
		_ = tabAt
		return msg
	}
}
