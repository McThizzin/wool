package app

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/alerts"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/discovery"
	"wool/internal/endpoint"
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
	tabDiscovered
	tabCount
)

var tabNames = []string{"dashboard", "models", "presets", "router", "inference", "hardware", "logs", "bench", "discovered"}
var tabTitles = []string{"Dashboard", "Models", "Presets", "Router", "Inference", "Hardware", "Logs", "Bench", "Discovered"}

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
	usage   usageTracker
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
	// routerLoadedAt records when a model was first seen resident, so the
	// eviction view can approximate the router's LRU order.
	routerLoadedAt map[string]time.Time
	stats          llamaserver.Metrics
	slots          []llamaserver.Slot
	statsID        string
	statsErr       string // router-model scrape failure reason, "" when healthy
	srvStats       llamaserver.Metrics
	srvSlots       []llamaserver.Slot
	srvStatsErr    string // standalone-server scrape failure reason
	inf            inference

	presets     []string
	models      []string
	groups      []string
	presetSel   int
	modelSel    int
	routerSel   int
	routerSelID string
	groupSel    int
	groupPick   bool
	groupWait   string

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

	discovery     discovery.Report
	discoveredSel int
	discoveryBusy bool
	discoveryDone bool
	discoveryAdd  bool
	disk          discovery.DiskReport

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
	configError   bool // the overlay reports a fatal config load failure

	endpointOpen   bool
	endpointData   endpoint.Endpoint
	endpointSel    int
	endpointNotice string

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
	modelsOK     bool
	usage        map[string]llamaserver.Metrics
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

// groupMsg carries the outcome of one warm-set load.
type groupMsg struct {
	name    string
	results []llamaserver.GroupResult
	limit   int
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

type discoveryMsg struct {
	report discovery.Report
}

type discoveryAddMsg struct {
	id  string
	err error
}

// Run starts the TUI. Servers keep running when it exits.
func Run(cfg *config.Config, cfgPath string) error {
	return runModel(New(cfg, cfgPath))
}

func runModel(m *Model) error {
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	m.usage.flushNow(m.meas, time.Now())
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
	m.groups = cfg.GroupIDs()
	m.refreshDisk()
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
