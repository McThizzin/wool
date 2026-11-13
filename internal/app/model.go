package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/alerts"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/gguf"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/reqlog"
	"wool/internal/sysinfo"
	"wool/internal/theme"
	"wool/internal/ui"
)

type tab int

const (
	tabDashboard tab = iota
	tabModels
	tabPresets
	tabRouter
	tabInference
	tabHardware
	tabLogs
	tabBench
	tabCount
)

var tabNames = []string{"dashboard", "models", "presets", "router", "inference", "hardware", "logs", "bench"}
var tabTitles = []string{"Dashboard", "Models", "Presets", "Router", "Inference", "Hardware", "Logs", "Bench"}

func (t tab) String() string {
	if t < 0 || int(t) >= len(tabNames) {
		return "?"
	}
	return tabNames[t]
}

// Model (the Bubble Tea model, not a llama model) holds all TUI state.
type Model struct {
	cfg     *config.Config
	cfgPath string
	mgr     *daemon.Manager
	sys     *sysinfo.Collector
	hist    *sysinfo.History
	meas    *measure.Store
	render  ui.Renderer

	width, height int
	tab           tab

	snap   sysinfo.Snapshot
	server daemon.Instance
	router daemon.Instance

	cpuAvg     float64
	cpuSamples int

	hasServer bool
	hasRouter bool

	serverHealth string
	routerHealth string
	routerModels []llamaserver.Model
	routerErr    string
	stats        llamaserver.Metrics
	slots        []llamaserver.Slot
	statsID      string
	statsErr     string // router-model scrape failure reason, "" when healthy
	srvStats     llamaserver.Metrics
	srvSlots     []llamaserver.Slot
	srvStatsErr  string // standalone-server scrape failure reason
	inf          inference

	presets     []string
	models      []string
	presetSel   int
	modelSel    int
	routerSel   int
	routerSelID string

	fitKey   string
	fit      fitInfo
	fitCache map[string]fitInfo
	meta     map[string]metaEntry

	versionBin string
	versionVal string
	versionSet bool

	budgetKey string
	budgetVal gguf.Budget

	logLines  []string
	logSel    int // 0 server, 1 router
	logScroll int
	follow    bool
	logName   string

	reqlog    *reqlog.Parser // structured request rows for logKey
	logKey    string         // target the parser belongs to
	logSeen   []string       // previous tail, used to feed only new lines
	logRaw    bool           // show the raw log instead of the table
	logFilter string         // model filter; "" shows every model

	benchRuns    []measure.Record
	benchAll     []measure.Record
	benchTargets []string
	benchSel     int
	benchMarkAt  time.Time
	benchMarkSet bool
	benchFilter  string
	benchErr     string
	benchStamp   string
	benchLoaded  bool

	alertEngine *alerts.Engine
	alertList   []alerts.Alert
	alertCmd    tea.Cmd

	command  bool
	input    textinput.Model
	history  []string
	histIdx  int
	busy     string
	toast    string
	toastAt  time.Time
	actionAt time.Time

	crash        *daemon.Crash
	crashState   daemon.State
	restartAt    time.Time
	restartHist  []time.Time
	expectServer bool
	expectStop   bool

	themeName string
	helpOpen  bool

	overlay       string
	overlayTitle  string
	overlayScroll int

	editor *editor
}

type fitInfo struct {
	Ready     bool
	Err       string
	Est       gguf.Estimate
	VRAMTotal uint64
	GPUName   string
	Ctx       int
	Layers    int
	Calib     measure.Calibration
	HasCalib  bool
	Verify    measure.Verification
	HasVerify bool
}

type metaEntry struct {
	Meta    gguf.Meta
	Size    uint64
	ModTime time.Time
	Err     string
	Ready   bool
}

type tickMsg struct {
	at           time.Time
	snap         sysinfo.Snapshot
	server       daemon.Instance
	router       daemon.Instance
	hasServer    bool
	hasRouter    bool
	serverHealth string
	routerHealth string
	models       []llamaserver.Model
	routerErr    string
	stats        llamaserver.Metrics
	slots        []llamaserver.Slot
	statsID      string
	statsErr     string
	srvStats     llamaserver.Metrics
	srvSlots     []llamaserver.Slot
	srvStatsErr  string
	serverLog    []string
	alertLogs    map[string][]string
}

type actionMsg struct {
	label   string
	err     error
	state   daemon.State
	hasSt   bool
	router  bool
	stopped bool
	auto    bool
	measure *measureJob
}

// measureJob carries the pre-launch state needed to record one observation
// after the server becomes healthy.
type measureJob struct {
	store         *measure.Store
	rec           measure.Record
	baseline      uint64
	baselineKnown bool
	host          string
	port          int
	bin           string
	timeout       time.Duration
	interval      time.Duration
	smoke         bool
	smokeTimeout  time.Duration
}

type measuredMsg struct {
	err   error
	smoke *measure.SmokeResult
}

type doctorMsg struct {
	text     string
	failures int
	err      error
}

// Run starts the TUI. Servers keep running when it exits.
func Run(cfg *config.Config, cfgPath string) error {
	m := New(cfg, cfgPath)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// New builds a TUI model. It samples hardware once so the first frame is
// already populated.
func New(cfg *config.Config, cfgPath string) *Model {
	return newModel(cfg, cfgPath, daemon.NewManager())
}

func newModel(cfg *config.Config, cfgPath string, mgr *daemon.Manager) *Model {
	if err := theme.RegisterAll(cfg.Themes); err != nil {
		// Config.Validate rejects bad themes; fall back to built-ins if a
		// caller constructed the config by hand.
		_ = theme.RegisterAll(nil)
	}
	th, ok := theme.Get(cfg.App.Theme)
	if !ok {
		th, _ = theme.Get("mocha")
	}
	m := &Model{
		cfg:         cfg,
		cfgPath:     cfgPath,
		mgr:         mgr,
		sys:         sysinfo.NewCollector(),
		hist:        sysinfo.NewHistory(120),
		meas:        measure.NewStore(),
		render:      ui.New(th, cfg.App.NerdFonts),
		themeName:   th.Name,
		fitCache:    map[string]fitInfo{},
		meta:        map[string]metaEntry{},
		follow:      true,
		input:       textinput.New(),
		alertEngine: alerts.New(alerts.FromConfig(cfg.Alerts)),
	}
	m.presets = cfg.PresetIDs()
	m.models = cfg.ModelIDs()
	snap, _ := m.sys.Sample()
	m.snap = snap
	m.hist.Push(snap)
	_ = m.mgr.Cleanup()
	if inst, err := m.mgr.Status("server"); err == nil {
		m.server, m.hasServer = inst, inst.Alive
		m.logName = "server"
	}
	if inst, err := m.mgr.Status("router"); err == nil {
		m.router, m.hasRouter = inst, inst.Alive
		if !m.hasServer {
			m.logSel, m.logName = 1, "router"
		}
	}
	m.input.Prompt = "> "
	m.input.Placeholder = "type / for commands"
	m.input.CharLimit = 300
	m.input.Width = 60
	m.ensureFit()
	return m
}

func newRenderer(th theme.Theme, nerdFonts bool) ui.Renderer {
	return ui.New(th, nerdFonts)
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refreshLater(0), m.spinnerTick(), textinput.Blink)
}

func (m *Model) spinnerTick() tea.Cmd {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = m.render.Styles.Accent
	return sp.Tick
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = maxInt(20, msg.Width-4)
		if m.editor != nil {
			m.editor.input.Width = m.input.Width
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
	case doctorMsg:
		m.busy = ""
		if msg.err != nil {
			m.setToast("doctor: " + msg.err.Error())
			return m, nil
		}
		m.overlay = msg.text
		m.overlayTitle = "doctor"
		m.overlayScroll = 0
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
					msg.models = mm
					msg.statsID = selected
					if selected != "" {
						for _, rm := range mm {
							if rm.ID != selected {
								continue
							}
							if rm.Status.Value == "loaded" || rm.Status.Value == "sleeping" {
								msg.stats, msg.slots, msg.statsErr = c.ScrapeInference(ctx, selected)
							}
							break
						}
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

func (m *Model) loadLogs() {
	name := m.logTarget()
	if name == "" {
		m.logLines = []string{"no server has been started yet"}
		m.logSeen = nil
		m.reqlog = nil
		m.logKey = ""
		return
	}
	lines, err := m.mgr.Tail(name, 500)
	if err != nil {
		if errors.Is(err, daemon.ErrNotRunning) {
			m.logLines = []string{"no " + name + " state; start it to create a log"}
		} else {
			m.logLines = []string{"log: " + err.Error()}
		}
		m.logSeen = nil
		m.reqlog = nil
		m.logKey = ""
		return
	}
	if m.logKey != name {
		m.resetReqlog(name)
	}
	if m.reqlog == nil {
		m.reqlog = reqlog.New(reqlogRows)
	}
	fresh, reset := newLogLines(m.logSeen, lines)
	if reset {
		m.reqlog.Reset()
	}
	at := time.Now()
	for _, line := range fresh {
		m.reqlog.Feed(line, at)
	}
	m.logSeen = lines
	m.logLines = lines
	if m.follow {
		m.logScroll = 0
	}
}

func (m *Model) logTarget() string {
	if m.logSel == 1 {
		return "router"
	}
	if m.logSel == 0 {
		return "server"
	}
	if m.hasServer {
		return "server"
	}
	if m.hasRouter {
		return "router"
	}
	return ""
}

func (m *Model) setToast(s string) {
	m.toast = oneLine(s, 160)
	m.toastAt = time.Now()
}

func (m *Model) toastActive() bool {
	return m.toast != "" && time.Since(m.toastAt) < 6*time.Second
}

func (m *Model) clampSelections() {
	m.presetSel = clamp(m.presetSel, 0, maxInt(0, len(m.presets)-1))
	m.modelSel = clamp(m.modelSel, 0, maxInt(0, len(m.models)-1))
	m.routerSel = clamp(m.routerSel, 0, maxInt(0, len(m.routerModels)-1))
	if len(m.routerModels) > 0 && m.routerSel < len(m.routerModels) {
		m.routerSelID = m.routerModels[m.routerSel].ID
	}
}

func (m *Model) selectedRouterID() string {
	if m.routerSel >= 0 && m.routerSel < len(m.routerModels) {
		return m.routerModels[m.routerSel].ID
	}
	return ""
}

func (m *Model) selectedRouterModel() (llamaserver.Model, bool) {
	if m.routerSel >= 0 && m.routerSel < len(m.routerModels) {
		return m.routerModels[m.routerSel], true
	}
	return llamaserver.Model{}, false
}

// target returns the preset for the dashboard/presets tab and the model for
// the models/router tabs.
func (m *Model) target() (id, kind string, ok bool) {
	switch m.tab {
	case tabModels:
		if m.modelSel < len(m.models) {
			return m.models[m.modelSel], "model", true
		}
	case tabRouter:
		if id := m.selectedRouterID(); id != "" {
			return id, "model", true
		}
	case tabDashboard:
		if len(m.presets) > 0 && m.presetSel < len(m.presets) {
			return m.presets[m.presetSel], "preset", true
		}
		fallthrough
	case tabPresets:
		if m.presetSel < len(m.presets) {
			return m.presets[m.presetSel], "preset", true
		}
	}
	return "", "", false
}

func (m *Model) resolveTarget(id, kind string) (config.Resolved, error) {
	if kind == "preset" {
		p, ok := m.cfg.Presets[id]
		if !ok {
			return config.Resolved{}, fmt.Errorf("preset %q no longer exists", id)
		}
		return m.cfg.Resolve("", &p)
	}
	if kind == "model" {
		return m.cfg.Resolve(id, nil)
	}
	return config.Resolved{}, fmt.Errorf("nothing selected")
}

func (m *Model) ensureFit() {
	id, kind, ok := m.target()
	if !ok {
		m.fitKey = ""
		return
	}
	key := kind + ":" + id
	if key == m.fitKey {
		return
	}
	m.fitKey = key
	m.fit = m.fitFor(id, kind)
}

// fitFor estimates memory for a target, caching by ID. Unreadable models are
// cached too so a missing file is not stat'ed every frame.
func (m *Model) fitFor(id, kind string) fitInfo {
	key := kind + ":" + id
	if fi, ok := m.fitCache[key]; ok {
		return fi
	}
	r, err := m.resolveTarget(id, kind)
	if err != nil {
		fi := fitInfo{Err: err.Error()}
		m.fitCache[key] = fi
		return fi
	}
	fi := m.computeFit(r)
	m.fitCache[key] = fi
	return fi
}

func (m *Model) computeFit(r config.Resolved) fitInfo {
	fi := fitInfo{Ctx: r.Ctx, Layers: r.GPULayers}
	if gpu, ok := m.snap.Primary(); ok {
		fi.VRAMTotal = gpu.VRAMTotal
		fi.GPUName = gpu.Name
	}
	meta, err := m.metaFor(r.ModelPath)
	if err != nil {
		fi.Err = err.Error()
		return fi
	}
	fi.Est = gguf.EstimateVRAM(meta.Meta, meta.Size, r.Ctx, r.GPULayers, r.NCPUMoE, r.CacheTypeK, r.CacheTypeV)
	fi.Ready = true
	if m.meas != nil {
		shape := measure.Shape{
			ModelID:    r.ModelID,
			Ctx:        r.Ctx,
			CacheTypeK: r.CacheTypeK,
			CacheTypeV: r.CacheTypeV,
			GPULayers:  r.GPULayers,
			NCPUMoE:    r.NCPUMoE,
		}
		id := measure.Identity{
			ModelSize:          meta.Size,
			ModelModTime:       meta.ModTime,
			LlamaServerVersion: m.llamaVersion(),
		}
		if gpu, ok := m.snap.Primary(); ok {
			id.GPUPCIID = gpu.PCIID
			id.GPUDriver = gpu.Driver
		}
		if cal, ok := m.meas.Calibration(shape, id); ok {
			fi.Calib, fi.HasCalib = cal, true
		}
		if verify, ok := m.meas.LatestVerification(shape); ok {
			fi.Verify, fi.HasVerify = verify, true
		}
	}
	return fi
}

// llamaVersion caches the configured llama-server version for calibration
// identity. The version is display metadata, so a stale cache is acceptable.
func (m *Model) llamaVersion() string {
	bin := config.ExpandPath(m.cfg.App.LlamaServer)
	if m.versionSet && m.versionBin == bin {
		return m.versionVal
	}
	m.versionBin = bin
	m.versionVal = measure.ServerVersion(bin)
	m.versionSet = true
	return m.versionVal
}

// budget is the VRAM available to a new server. The running standalone
// server's own allocation is excluded so a restart is not counted twice.
func (m *Model) budget() gguf.Budget {
	gpu, ok := m.snap.Primary()
	if !ok || !gpu.HasVRAM || gpu.VRAMTotal == 0 {
		return gguf.Budget{}
	}
	reserve := uint64(maxInt(0, m.cfg.Router.MemoryMarginMiB)) << 20
	pid := 0
	if m.hasServer {
		pid = m.server.PID
	}
	key := fmt.Sprintf("%d/%d/%d/%d", gpu.VRAMTotal, gpu.VRAMUsed, reserve, pid)
	if key == m.budgetKey {
		return m.budgetVal
	}
	var own uint64
	var ownKnown bool
	if pid > 0 {
		own, ownKnown = sysinfo.ProcessVRAM(pid)
	}
	m.budgetKey = key
	m.budgetVal = gguf.ComputeBudget(gpu.VRAMTotal, gpu.VRAMUsed, reserve, own, ownKnown)
	return m.budgetVal
}

func (m *Model) metaFor(path string) (metaEntry, error) {
	if e, ok := m.meta[path]; ok {
		if e.Ready {
			return e, nil
		}
		return e, errors.New(e.Err)
	}
	e := metaEntry{}
	info, err := os.Stat(path)
	if err != nil {
		e.Err = err.Error()
		m.meta[path] = e
		return e, err
	}
	if info.IsDir() {
		e.Err = fmt.Sprintf("%s is a directory", path)
		m.meta[path] = e
		return e, errors.New(e.Err)
	}
	meta, err := gguf.ReadMeta(path)
	if err != nil {
		e.Err = err.Error()
		m.meta[path] = e
		return e, err
	}
	e = metaEntry{Meta: meta, Size: uint64(info.Size()), ModTime: info.ModTime(), Ready: true}
	m.meta[path] = e
	return e, nil
}

func (m *Model) moveSelection(delta int) {
	switch m.tab {
	case tabDashboard, tabPresets:
		m.presetSel = clamp(m.presetSel+delta, 0, maxInt(0, len(m.presets)-1))
	case tabModels:
		m.modelSel = clamp(m.modelSel+delta, 0, maxInt(0, len(m.models)-1))
	case tabRouter, tabInference:
		m.routerSel = clamp(m.routerSel+delta, 0, maxInt(0, len(m.routerModels)-1))
		m.routerSelID = m.selectedRouterID()
	case tabBench:
		m.benchSel = clamp(m.benchSel+delta, 0, maxInt(0, len(m.benchRuns)-1))
	default:
		return
	}
	m.ensureFit()
}

func (m *Model) setTab(t tab) {
	if t < 0 || t >= tabCount {
		return
	}
	m.tab = t
	if t == tabLogs && m.logName == "" {
		if m.hasServer {
			m.logSel, m.logName = 0, "server"
		} else if m.hasRouter {
			m.logSel, m.logName = 1, "router"
		}
	}
	if t == tabLogs || t == tabDashboard {
		m.loadLogs()
	}
	if t == tabBench {
		m.loadBenchHistory()
	}
	m.ensureFit()
}

func (m *Model) cycleTheme(delta int) {
	order := theme.Order()
	idx := 0
	for i, n := range order {
		if n == m.themeName {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(order)) % len(order)
	if th, ok := theme.Get(order[idx]); ok {
		m.themeName = th.Name
		m.render = ui.New(th, m.cfg.App.NerdFonts)
		m.setToast("theme: " + th.Name)
	}
}

func (m *Model) reloadConfig() {
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		m.setToast("config reload failed: " + err.Error())
		return
	}
	presetID, _, _ := m.target()
	oldPreset := ""
	if m.presetSel < len(m.presets) {
		oldPreset = m.presets[m.presetSel]
	}
	oldModel := ""
	if m.modelSel < len(m.models) {
		oldModel = m.models[m.modelSel]
	}
	if err := theme.RegisterAll(cfg.Themes); err != nil {
		m.setToast("config reload failed: " + err.Error())
		return
	}
	m.cfg = cfg
	m.presets = cfg.PresetIDs()
	m.models = cfg.ModelIDs()
	m.presetSel = indexOf(m.presets, oldPreset)
	m.modelSel = indexOf(m.models, oldModel)
	if m.presetSel < 0 {
		m.presetSel = 0
	}
	if m.modelSel < 0 {
		m.modelSel = 0
	}
	if th, ok := theme.Get(cfg.App.Theme); ok {
		m.themeName = th.Name
		m.render = ui.New(th, cfg.App.NerdFonts)
	}
	m.fitCache = map[string]fitInfo{}
	m.meta = map[string]metaEntry{}
	m.fitKey = ""
	m.alertEngine = alerts.New(alerts.FromConfig(cfg.Alerts))
	m.alertList = nil
	m.alertCmd = nil
	m.ensureFit()
	_ = presetID
	m.setToast("config reloaded")
}

func (m *Model) scrollLogs(delta int) {
	m.follow = false
	m.logScroll += delta
	maxScroll := maxInt(0, m.logRowCount()-1)
	m.logScroll = clamp(m.logScroll, 0, maxScroll)
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > max && max > 3 {
		s = s[:max-3] + "..."
	}
	return s
}

// statFile returns a file size, or an error if it is not a regular file.
func statFile(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		return 0, fmt.Errorf("%s is a directory", path)
	}
	return uint64(info.Size()), nil
}
