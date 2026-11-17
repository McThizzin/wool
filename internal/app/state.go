package app

import (
	"errors"
	"fmt"
	"os"
	"time"

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
	m.groups = cfg.GroupIDs()
	m.presetSel = indexOf(m.presets, oldPreset)
	m.modelSel = indexOf(m.models, oldModel)
	m.groupSel = clamp(m.groupSel, 0, maxInt(0, len(m.groups)-1))
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
