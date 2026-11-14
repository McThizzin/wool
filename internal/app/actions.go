package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/doctor"
	"wool/internal/llamaserver"
	"wool/internal/measure"
	"wool/internal/theme"
)

// startTarget launches a preset or model as the detached "server" instance.
func (m *Model) startTarget(id, kind string) tea.Cmd {
	r, err := m.resolveTarget(id, kind)
	if err != nil {
		m.setToast("start: " + err.Error())
		return nil
	}
	if _, err := statFile(r.ModelPath); err != nil {
		m.setToast("missing model file: " + r.ModelPath)
		return nil
	}
	if inst, err := m.mgr.Status("server"); err == nil && inst.Alive {
		m.setToast("server already running; press S to restart")
		return nil
	}
	m.busy = "starting " + id + "..."
	m.resetRestartState()
	label := id
	preset := ""
	if kind == "preset" {
		preset = id
	}
	cfg := m.cfg
	mgr := m.mgr
	host := cfg.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	job := m.newMeasureJob(r, "server", config.ExpandPath(cfg.App.LlamaServer))
	return func() tea.Msg {
		var baseline uint64
		var baselineKnown bool
		if job != nil {
			baseline, baselineKnown = measure.SampleVRAM()
		}
		state, err := mgr.Start(daemon.Spec{
			Name:     "server",
			Kind:     daemon.KindServer,
			Preset:   preset,
			Model:    r.ModelID,
			Bin:      config.ExpandPath(cfg.App.LlamaServer),
			Host:     host,
			Port:     cfg.Server.Port,
			AutoPort: cfg.Server.AutoPortEnabled(),
			Args: func(port int) []string {
				return llamaserver.SingleArgs(r, host, port)
			},
		})
		if err == nil && job != nil {
			job.baseline, job.baselineKnown = baseline, baselineKnown
			job.host, job.port = state.Host, state.Port
		}
		return actionMsg{label: "started " + label + " on port " + strconv.Itoa(state.Port), err: err, state: state, hasSt: err == nil, measure: job}
	}
}

// newMeasureJob snapshots the config and estimate before a launch. The
// baseline is filled in by the launch goroutine, after any old process stops.
func (m *Model) newMeasureJob(r config.Resolved, instance, bin string) *measureJob {
	if m.meas == nil {
		return nil
	}
	info, err := os.Stat(r.ModelPath)
	if err != nil {
		return nil
	}
	meta, metaErr := m.metaFor(r.ModelPath)
	gpu, _ := m.snap.Primary()
	reserve := uint64(maxInt(0, m.cfg.Router.MemoryMarginMiB)) << 20
	rec := measure.NewRecord(measure.RunInfo{
		Instance:     instance,
		ModelID:      r.ModelID,
		ModelPath:    r.ModelPath,
		FileSize:     uint64(info.Size()),
		FileModTime:  info.ModTime(),
		Meta:         meta.Meta,
		MetaOK:       metaErr == nil,
		Ctx:          r.Ctx,
		CacheTypeK:   r.CacheTypeK,
		CacheTypeV:   r.CacheTypeV,
		GPULayers:    r.GPULayers,
		NCPUMoE:      r.NCPUMoE,
		GPU:          gpu,
		ReserveBytes: reserve,
	})
	timeout := 120 * time.Second
	if d, err := time.ParseDuration(m.cfg.Server.StartupTimeout); err == nil && d > 0 {
		timeout = d
	}
	smokeTimeout := measure.DefaultSmokeTimeout
	if d, err := time.ParseDuration(m.cfg.Server.SmokeTimeout); err == nil && d > 0 {
		smokeTimeout = d
	}
	return &measureJob{
		store:        m.meas,
		rec:          rec,
		bin:          bin,
		timeout:      timeout,
		interval:     m.pollInterval(),
		smoke:        m.cfg.Server.SmokeTest,
		smokeTimeout: smokeTimeout,
	}
}

// captureMeasurement waits for /health, samples the settle window, and writes
// one record. It runs as its own tea.Cmd so the UI never blocks on it.
func (m *Model) captureMeasurement(job *measureJob) tea.Cmd {
	return func() tea.Msg {
		client := llamaserver.NewClient(job.host, job.port)
		ctx, cancel := context.WithTimeout(context.Background(), job.timeout)
		err := client.WaitReady(ctx, job.timeout, job.interval)
		cancel()
		if err != nil {
			return measuredMsg{err: err}
		}
		rec := job.rec
		rec.LlamaServerVersion = measure.ServerVersion(job.bin)
		var smoke *measure.SmokeResult
		if job.smoke {
			client := llamaserver.NewClientURL(llamaserver.BaseURL(job.host, job.port))
			res := measure.RunSmoke(context.Background(), client, job.smokeTimeout)
			smoke = &res
			rec.SmokeMS = float64(res.Latency.Milliseconds())
			rec.SmokeError = res.Err
			if res.OK() {
				rec.VerifiedAt = res.At
			}
		}
		cap := measure.Capture{
			Store:         job.store,
			Record:        rec,
			Baseline:      job.baseline,
			BaselineKnown: job.baselineKnown,
			Sample:        measure.SampleVRAM,
		}
		return measuredMsg{err: cap.Run(context.Background()), smoke: smoke}
	}
}

func (m *Model) stopServer() tea.Cmd {
	mgr := m.mgr
	m.busy = "stopping server..."
	m.expectStop = true
	m.expectServer = false
	m.resetRestartState()
	m.clearCrash()
	return func() tea.Msg {
		err := mgr.Stop("server", 20*time.Second)
		if errors.Is(err, daemon.ErrNotRunning) {
			return actionMsg{label: "server was not running", stopped: true}
		}
		return actionMsg{label: "server stopped", err: err, stopped: err == nil}
	}
}

func (m *Model) restartServer() tea.Cmd {
	id, kind, ok := m.target()
	if !ok {
		m.setToast("nothing selected")
		return nil
	}
	r, err := m.resolveTarget(id, kind)
	if err != nil {
		m.setToast("restart: " + err.Error())
		return nil
	}
	cfg, mgr := m.cfg, m.mgr
	host := cfg.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	preset := ""
	if kind == "preset" {
		preset = id
	}
	label := id
	job := m.newMeasureJob(r, "server", config.ExpandPath(cfg.App.LlamaServer))
	m.busy = "restarting " + id + "..."
	m.expectStop = true
	m.resetRestartState()
	m.clearCrash()
	return func() tea.Msg {
		_ = mgr.Stop("server", 20*time.Second)
		var baseline uint64
		var baselineKnown bool
		if job != nil {
			baseline, baselineKnown = measure.SampleVRAM()
		}
		state, err := mgr.Start(daemon.Spec{
			Name:     "server",
			Kind:     daemon.KindServer,
			Preset:   preset,
			Model:    r.ModelID,
			Bin:      config.ExpandPath(cfg.App.LlamaServer),
			Host:     host,
			Port:     cfg.Server.Port,
			AutoPort: cfg.Server.AutoPortEnabled(),
			Args: func(port int) []string {
				return llamaserver.SingleArgs(r, host, port)
			},
		})
		if err == nil && job != nil {
			job.baseline, job.baselineKnown = baseline, baselineKnown
			job.host, job.port = state.Host, state.Port
		}
		return actionMsg{label: "restarted " + label, err: err, state: state, hasSt: err == nil, measure: job}
	}
}

func (m *Model) startRouter() tea.Cmd {
	if m.hasRouter {
		m.setToast("router already running; press R to stop")
		return nil
	}
	cfg, mgr := m.cfg, m.mgr
	host := cfg.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	ini := config.RouterINIPath()
	m.busy = "starting router..."
	return func() tea.Msg {
		if err := llamaserver.WriteRouterINI(cfg, ini); err != nil {
			return actionMsg{label: "router", err: err}
		}
		state, err := mgr.Start(daemon.Spec{
			Name:     "router",
			Kind:     daemon.KindRouter,
			Model:    "router",
			Bin:      config.ExpandPath(cfg.App.LlamaServer),
			Host:     host,
			Port:     cfg.Server.Port,
			AutoPort: cfg.Server.AutoPortEnabled(),
			Args: func(port int) []string {
				return llamaserver.RouterArgs(cfg, host, port, ini)
			},
		})
		return actionMsg{label: "router started", err: err, state: state, hasSt: err == nil, router: true}
	}
}

func (m *Model) stopRouter() tea.Cmd {
	mgr := m.mgr
	m.busy = "stopping router..."
	return func() tea.Msg {
		err := mgr.Stop("router", 30*time.Second)
		if errors.Is(err, daemon.ErrNotRunning) {
			return actionMsg{label: "router was not running"}
		}
		return actionMsg{label: "router stopped", err: err, router: true}
	}
}

func (m *Model) routerAction(load bool) tea.Cmd {
	id := m.selectedRouterID()
	if id == "" {
		m.setToast("no router model selected")
		return nil
	}
	if !m.hasRouter {
		m.setToast("router is not running")
		return nil
	}
	client := llamaserver.NewClient(m.router.Host, m.router.Port)
	verb := "load"
	if !load {
		verb = "unload"
	}
	m.busy = verb + "ing " + id + "..."
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var err error
		if load {
			err = client.Load(ctx, id)
		} else {
			err = client.Unload(ctx, id)
		}
		return actionMsg{label: verb + " requested for " + id, err: err, router: true}
	}
}

func (m *Model) reloadRouterModels() tea.Cmd {
	if !m.hasRouter {
		m.setToast("router is not running")
		return nil
	}
	cfg := m.cfg
	host, port := m.router.Host, m.router.Port
	m.busy = "reloading router presets..."
	return func() tea.Msg {
		ini := config.RouterINIPath()
		if err := llamaserver.WriteRouterINI(cfg, ini); err != nil {
			return actionMsg{label: "write models.ini", err: err, router: true}
		}
		client := llamaserver.NewClient(host, port)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := client.ReloadModels(ctx)
		return actionMsg{label: "router presets reloaded", err: err, router: true}
	}
}

func (m *Model) runDoctor() tea.Cmd {
	cfg, path := m.cfg, m.cfgPath
	m.busy = "running doctor..."
	return func() tea.Msg {
		rep := doctor.Run(cfg, path)
		return doctorMsg{text: rep.Print(), failures: len(rep.Failures())}
	}
}

// showConfigDiff opens the diff overlay for the selected target. A preset is
// compared with its model; a model with the defaults layer.
func (m *Model) showConfigDiff() {
	id, kind, ok := m.target()
	if !ok {
		m.setToast("nothing selected to diff")
		return
	}
	m.showConfigDiffFor(id, kind)
}

// showConfigDiffFor opens the M1.4 diff for an explicit target. It is shared by
// the config tabs and the bench history view.
func (m *Model) showConfigDiffFor(id, kind string) {
	modelID, preset := "", ""
	if kind == "preset" {
		preset = id
	} else {
		modelID = id
	}
	result, err := m.cfg.Diff(modelID, preset, "")
	if err != nil {
		m.setToast("diff: " + err.Error())
		return
	}
	if len(result.Fields) == 0 {
		m.setToast("config diff: " + id + " matches the " + result.Against + " layer")
		return
	}
	lines := []string{fmt.Sprintf("%s (%s) against %s", id, kind, result.Against), ""}
	for _, f := range result.Fields {
		lines = append(lines,
			f.Field,
			fmt.Sprintf("  target   %v  (%s %s)", f.Target.Value, f.Target.Source, f.Target.Layer),
			fmt.Sprintf("  against  %v  (%s %s)", f.Against.Value, f.Against.Source, f.Against.Layer),
			"",
		)
	}
	m.overlayTitle = "config diff · " + id
	m.overlayScroll = 0
	m.overlay = strings.Join(lines, "\n")
}

func (m *Model) showHelp() {
	m.overlayTitle = "keys and commands"
	m.overlayScroll = 0
	m.overlay = strings.Join([]string{
		"keys",
		"  tab / shift+tab        next / previous tab",
		"  1..8                   jump to a tab",
		"  j/k, up/down           move selection",
		"  g/G                    first / last entry",
		"  enter                  start selected server, load router model or compare bench runs",
		"  s / x                  start / stop the standalone server (x dismisses a crash card)",
		"  S                      restart the selected preset, model or crashed server",
		"  r / R                  start / stop the router",
		"  l / u                  load / unload the selected router model",
		"  w                      load a warm-set group (Router tab)",
		"  i                      focus the inference tab",
		"  m                      switch the inference source, cycle the log model filter, or pin router stats",
		"  v                      show the raw log instead of the request table",
		"  L                      focus the logs tab (enter toggles target)",
		"  B                      focus the bench history tab",
		"  space                  mark a benchmark run for comparison",
		"  f                      follow logs, or cycle the bench target filter",
		"  e                      edit the selected preset/model options",
		"  F                      recommend settings that fit VRAM",
		"  D                      show the selected config diff (or bench run's target)",
		"  d                      run doctor",
		"  t                      next theme",
		"  ctrl+r                 reload config.toml",
		"  /                      command line",
		"  ?                      this help",
		"  q or ctrl+c            quit (servers keep running)",
		"",
		"commands",
		"  /start [name]          start a preset or model",
		"  /restart [name]        restart with the selected target or the crashed server",
		"  /dismiss               hide a crash card and cancel its pending retry",
		"  /stop [server|router|all]",
		"  /router [start|stop|reload|models|groups]",
		"  /load ID               ask the router to load a model",
		"  /unload ID             ask the router to unload a model",
		"  /load-group NAME       load a configured warm-set group",
		"  /reload                reload config.toml",
		"  /edit                  open the option editor",
		"  /fit                   open the option editor and fit overlay",
		"  /doctor                run readiness checks",
		"  /theme [name]          switch theme",
		"  /tab NAME              jump to dashboard, models, presets, router, inference, hardware, logs, bench",
		"  /bench [TARGET|all]    show benchmark history, optionally filtered",
		"  /bench compare A B     compare the newest run of two targets",
		"  /logs [server|router]  show a log",
		"  /quit                  exit",
		"",
		"Detached servers are not children of the TUI. Quitting never stops them.",
	}, "\n")
}

// runCommand parses one slash command. It returns handled=false for unknown
// commands so the caller can show an error.
func (m *Model) runCommand(line string) (tea.Cmd, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "/"))
	if line == "" {
		return nil, true
	}
	fields := strings.Fields(line)
	cmd, args := fields[0], fields[1:]
	switch cmd {
	case "help", "?":
		m.showHelp()
	case "quit", "q", "exit":
		return tea.Quit, true
	case "start":
		if len(args) == 0 {
			id, kind, ok := m.target()
			if !ok {
				m.setToast("nothing selected")
				return nil, true
			}
			return m.startTarget(id, kind), true
		}
		if r, kind, ok := m.findTarget(args[0]); ok {
			return m.startTarget(r, kind), true
		}
		m.setToast("no preset or model named " + args[0])
	case "restart":
		if m.crash != nil {
			return m.restartCrashed(), true
		}
		return m.restartServer(), true
	case "dismiss":
		m.dismissCrash()
		return nil, true
	case "stop":
		target := "server"
		if len(args) > 0 {
			target = args[0]
		}
		switch target {
		case "all":
			return tea.Batch(m.stopServer(), m.stopRouter()), true
		case "router":
			return m.stopRouter(), true
		case "server":
			return m.stopServer(), true
		default:
			m.setToast("stop: want server, router or all")
		}
	case "router":
		if len(args) == 0 || args[0] == "start" {
			return m.startRouter(), true
		}
		switch args[0] {
		case "stop":
			return m.stopRouter(), true
		case "reload":
			return m.reloadRouterModels(), true
		case "models", "ps":
			m.setTab(tabRouter)
		case "groups":
			if len(m.groups) == 0 {
				m.setToast("no groups configured")
			} else {
				m.setToast("groups: " + strings.Join(m.groups, ", "))
			}
		default:
			m.setToast("router: want start, stop, reload, models or groups")
		}
	case "load", "unload":
		if len(args) == 0 {
			m.setToast(cmd + ": model ID required")
			return nil, true
		}
		idx := -1
		for i, rm := range m.routerModels {
			if rm.ID == args[0] {
				idx = i
				break
			}
		}
		if idx < 0 {
			m.setToast("router does not list " + args[0])
			return nil, true
		}
		m.routerSel = idx
		m.routerSelID = args[0]
		return m.routerAction(cmd == "load"), true
	case "load-group":
		if len(args) == 0 {
			m.setToast("load-group: group name required")
			return nil, true
		}
		return m.requestGroupLoad(args[0]), true
	case "reload", "refresh":
		m.reloadConfig()
	case "doctor":
		return m.runDoctor(), true
	case "edit":
		m.openEditor()
	case "fit":
		if m.editor == nil {
			m.openEditor()
		}
		if m.editor != nil {
			m.openFitOverlay()
		}
	case "logs", "log":
		if len(args) > 0 {
			switch args[0] {
			case "router":
				m.logSel, m.logName = 1, "router"
			case "server":
				m.logSel, m.logName = 0, "server"
			default:
				m.logSel, m.logName = 0, args[0]
			}
		}
		m.setTab(tabLogs)
	case "theme":
		if len(args) == 0 {
			m.cycleTheme(1)
			break
		}
		if th, ok := theme.Get(args[0]); ok {
			m.themeName = th.Name
			m.render = newRenderer(th, m.cfg.App.NerdFonts)
			m.setToast("theme: " + th.Name)
		} else {
			m.setToast("unknown theme " + args[0] + " (have " + strings.Join(theme.Order(), ", ") + ")")
		}
	case "tab":
		if len(args) == 0 {
			m.setToast("tab: want " + strings.Join(tabNames, ", "))
			break
		}
		for i, name := range tabNames {
			if name == args[0] {
				m.setTab(tab(i))
				break
			}
		}
	case "bench":
		if len(args) > 0 && args[0] == "compare" {
			m.benchCompareCommand(args[1:])
			break
		}
		target := ""
		if len(args) > 0 && args[0] != "all" {
			target = args[0]
		}
		m.benchFilter = target
		m.benchSel = -1
		m.setTab(tabBench)
		if target == "" {
			m.setToast("bench: all targets")
		} else {
			m.setToast("bench: " + target)
		}
	default:
		m.setToast("unknown command /" + cmd + " (try /help)")
	}
	return nil, true
}

// findTarget resolves a name to a preset or model ID.
func (m *Model) findTarget(name string) (string, string, bool) {
	if _, ok := m.cfg.Presets[name]; ok {
		return name, "preset", true
	}
	if _, ok := m.cfg.Models[name]; ok {
		return name, "model", true
	}
	return "", "", false
}

// currentTargetName is used for status messages.
func (m *Model) currentTargetName() string {
	id, _, ok := m.target()
	if !ok {
		return ""
	}
	return id
}
