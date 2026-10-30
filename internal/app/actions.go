package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/doctor"
	"wool/internal/llamaserver"
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
	return func() tea.Msg {
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
		return actionMsg{label: "started " + label + " on port " + strconv.Itoa(state.Port), err: err, state: state, hasSt: err == nil}
	}
}

func (m *Model) stopServer() tea.Cmd {
	mgr := m.mgr
	m.busy = "stopping server..."
	return func() tea.Msg {
		err := mgr.Stop("server", 20*time.Second)
		if errors.Is(err, daemon.ErrNotRunning) {
			return actionMsg{label: "server was not running"}
		}
		return actionMsg{label: "server stopped", err: err}
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
	m.busy = "restarting " + id + "..."
	return func() tea.Msg {
		_ = mgr.Stop("server", 20*time.Second)
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
		return actionMsg{label: "restarted " + label, err: err, state: state, hasSt: err == nil}
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

func (m *Model) showHelp() {
	m.overlayTitle = "keys and commands"
	m.overlayScroll = 0
	m.overlay = strings.Join([]string{
		"keys",
		"  tab / shift+tab        next / previous tab",
		"  1..6                   jump to a tab",
		"  j/k, up/down           move selection",
		"  g/G                    first / last entry",
		"  enter                  start selected server or load router model",
		"  s / x                  start / stop the standalone server",
		"  S                      restart the selected preset or model",
		"  r / R                  start / stop the router",
		"  l / u                  load / unload the selected router model",
		"  L                      focus the logs tab (enter toggles target)",
		"  e                      edit the selected preset/model options",
		"  d                      run doctor",
		"  t                      next theme",
		"  ctrl+r                 reload config.toml",
		"  /                      command line",
		"  ?                      this help",
		"  q or ctrl+c            quit (servers keep running)",
		"",
		"commands",
		"  /start [name]          start a preset or model",
		"  /restart [name]        restart with the selected target",
		"  /stop [server|router|all]",
		"  /router [start|stop|reload|models]",
		"  /load ID               ask the router to load a model",
		"  /unload ID             ask the router to unload a model",
		"  /reload                reload config.toml",
		"  /edit                  open the option editor",
		"  /doctor                run readiness checks",
		"  /theme [name]          switch theme",
		"  /tab NAME              jump to dashboard, models, presets, router, hardware, logs",
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
		return m.restartServer(), true
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
		default:
			m.setToast("router: want start, stop, reload or models")
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
	case "reload", "refresh":
		m.reloadConfig()
	case "doctor":
		return m.runDoctor(), true
	case "edit":
		m.openEditor()
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
