package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/fit"
	"wool/internal/gguf"
	"wool/internal/ui"
)

// editorField is one TOML key that can be changed from the TUI.
type editorField struct {
	label  string
	table  string // full TOML table, e.g. presets.foo.sampling
	key    string // TOML key inside the table
	value  string // current value, TOML-formatted
	kind   string // "int", "float", "string", "bool"
	source config.Provenance
}

// editor is a small overlay for editing preset or model options. Edits are
// written to the TOML file so the config stays the single source of truth.
type editor struct {
	table   string
	title   string
	path    string
	fields  []editorField
	sources map[string]config.Provenance
	sel     int
	scroll  int

	input   textinput.Model
	editing bool
	visible int
	err     string

	base    config.Resolved
	draft   config.Resolved
	dirty   bool
	baseEst gguf.Estimate
	meta    gguf.Meta
	size    uint64
	hasMeta bool
	metaErr string
	preview editorPreview
	fit     *fitOverlay
}

// fitOverlay is the editor's unsaved fit recommendation picker.
type fitOverlay struct {
	result      fit.Result
	recs        []fit.Recommendation
	sel         int
	err         string
	naming      bool
	saveName    textinput.Model
	confirmName string
	notice      string
	noticeErr   bool
}

// editorPreview is the unsaved estimate for the draft value being typed.
type editorPreview struct {
	Ready   bool
	Err     string
	Est     gguf.Estimate
	Budget  gguf.Budget
	Delta   int64
	Changed bool
	Note    string
}

func (m *Model) openEditor() {
	id, kind, ok := m.target()
	if !ok {
		m.setToast("nothing selected to edit")
		return
	}
	table := "presets." + id
	if kind == "model" {
		table = "models." + id
	}
	var r config.Resolved
	var sources map[string]config.Provenance
	var err error
	if kind == "preset" {
		r, sources, err = m.cfg.ResolveDetailed("", id)
	} else {
		r, sources, err = m.cfg.ResolveDetailed(id, "")
	}
	if err != nil {
		m.setToast("edit: " + err.Error())
		return
	}
	e := &editor{table: table, title: "edit " + table, path: m.cfgPath, sources: sources}
	samplingTable := table + ".sampling"
	e.fields = []editorField{
		{label: "ctx", table: table, key: "ctx", value: strconv.Itoa(r.Ctx), kind: "int"},
		{label: "gpu_layers", table: table, key: "gpu_layers", value: strconv.Itoa(r.GPULayers), kind: "int"},
		{label: "n_cpu_moe", table: table, key: "n_cpu_moe", value: strconv.Itoa(r.NCPUMoE), kind: "int"},
		{label: "threads", table: table, key: "threads", value: strconv.Itoa(r.Threads), kind: "int"},
		{label: "threads_batch", table: table, key: "threads_batch", value: strconv.Itoa(r.ThreadsBatch), kind: "int"},
		{label: "batch_size", table: table, key: "batch_size", value: strconv.Itoa(r.BatchSize), kind: "int"},
		{label: "ubatch_size", table: table, key: "ubatch_size", value: strconv.Itoa(r.UBatchSize), kind: "int"},
		{label: "parallel", table: table, key: "parallel", value: strconv.Itoa(r.Parallel), kind: "int"},
		{label: "flash_attn", table: table, key: "flash_attn", value: quoteTOML(r.FlashAttn), kind: "string"},
		{label: "cache_type_k", table: table, key: "cache_type_k", value: quoteTOML(r.CacheTypeK), kind: "string"},
		{label: "cache_type_v", table: table, key: "cache_type_v", value: quoteTOML(r.CacheTypeV), kind: "string"},
		{label: "jinja", table: table, key: "jinja", value: strconv.FormatBool(r.Jinja), kind: "bool"},
		{label: "metrics", table: table, key: "metrics", value: strconv.FormatBool(r.Metrics), kind: "bool"},
		{label: "sampling.temp", table: samplingTable, key: "temp", value: fmtFloat(r.Sampling.Temp), kind: "float"},
		{label: "sampling.top_p", table: samplingTable, key: "top_p", value: fmtFloat(r.Sampling.TopP), kind: "float"},
		{label: "sampling.top_k", table: samplingTable, key: "top_k", value: strconv.Itoa(r.Sampling.TopK), kind: "int"},
		{label: "sampling.min_p", table: samplingTable, key: "min_p", value: fmtFloat(r.Sampling.MinP), kind: "float"},
		{label: "sampling.repeat_penalty", table: samplingTable, key: "repeat_penalty", value: fmtFloat(r.Sampling.RepeatPenalty), kind: "float"},
		{label: "sampling.presence_penalty", table: samplingTable, key: "presence_penalty", value: fmtFloat(r.Sampling.PresencePenalty), kind: "float"},
		{label: "sampling.frequency_penalty", table: samplingTable, key: "frequency_penalty", value: fmtFloat(r.Sampling.FrequencyPenalty), kind: "float"},
		{label: "sampling.typical_p", table: samplingTable, key: "typical_p", value: fmtFloat(r.Sampling.TypicalP), kind: "float"},
		{label: "sampling.mirostat", table: samplingTable, key: "mirostat", value: strconv.Itoa(r.Sampling.Mirostat), kind: "int"},
		{label: "sampling.seed", table: samplingTable, key: "seed", value: strconv.Itoa(r.Sampling.Seed), kind: "int"},
	}
	for i := range e.fields {
		src, ok := sources[e.fields[i].label]
		if !ok {
			src = config.Provenance{Source: config.SourceBuiltin, Layer: "built-in"}
		}
		e.fields[i].source = src
	}
	e.base = r
	e.draft = r
	if meta, err := m.metaFor(r.ModelPath); err != nil {
		e.metaErr = err.Error()
	} else {
		e.meta, e.size, e.hasMeta = meta.Meta, meta.Size, true
		e.baseEst = gguf.EstimateVRAM(meta.Meta, meta.Size, r.Ctx, r.GPULayers, r.NCPUMoE, r.CacheTypeK, r.CacheTypeV)
	}
	e.preview = m.previewFor(e, r)
	m.editor = e
}

// previewFor computes the unsaved estimate for a draft configuration.
func (m *Model) previewFor(e *editor, draft config.Resolved) editorPreview {
	p := editorPreview{Budget: m.budget()}
	if !e.hasMeta {
		p.Err = e.metaErr
		return p
	}
	p.Est = gguf.EstimateVRAM(e.meta, e.size, draft.Ctx, draft.GPULayers, draft.NCPUMoE, draft.CacheTypeK, draft.CacheTypeV)
	p.Ready = true
	p.Delta = int64(p.Est.Total) - int64(e.baseEst.Total)
	p.Changed = p.Delta != 0 ||
		draft.Ctx != e.base.Ctx ||
		draft.GPULayers != e.base.GPULayers ||
		draft.NCPUMoE != e.base.NCPUMoE ||
		draft.CacheTypeK != e.base.CacheTypeK ||
		draft.CacheTypeV != e.base.CacheTypeV
	if len(p.Est.Reasons) > 0 {
		p.Note = "approximate: " + strings.Join(p.Est.Reasons, "; ")
	}
	return p
}

// refreshPreview recomputes the preview from the field currently being edited.
func (m *Model) refreshPreview() {
	e := m.editor
	if e == nil {
		return
	}
	f := e.fields[e.sel]
	raw := strings.TrimSpace(e.input.Value())
	draft := e.base
	switch f.key {
	case "ctx":
		v, err := strconv.Atoi(raw)
		if err != nil {
			e.preview = editorPreview{Err: "ctx must be an integer"}
			return
		}
		draft.Ctx = v
	case "gpu_layers":
		v, err := strconv.Atoi(raw)
		if err != nil {
			e.preview = editorPreview{Err: "gpu_layers must be an integer"}
			return
		}
		draft.GPULayers = v
	case "n_cpu_moe":
		v, err := strconv.Atoi(raw)
		if err != nil {
			e.preview = editorPreview{Err: "n_cpu_moe must be an integer"}
			return
		}
		draft.NCPUMoE = v
	case "cache_type_k":
		draft.CacheTypeK = raw
	case "cache_type_v":
		draft.CacheTypeV = raw
	default:
		return
	}
	e.preview = m.previewFor(e, draft)
}

func fitSettings(r config.Resolved) fit.Settings {
	return fit.Settings{
		Ctx:        r.Ctx,
		GPULayers:  r.GPULayers,
		NCPUMoE:    r.NCPUMoE,
		CacheTypeK: r.CacheTypeK,
		CacheTypeV: r.CacheTypeV,
	}
}

// openFitOverlay solves against the live budget using the editor's current
// draft. The result is only a picker until the user applies it.
func (m *Model) openFitOverlay() {
	e := m.editor
	if e == nil {
		return
	}
	if !e.hasMeta {
		err := e.metaErr
		if err == "" {
			err = "model metadata unreadable"
		}
		e.fit = &fitOverlay{err: err}
		return
	}
	budget := m.budget()
	if !budget.Known {
		e.fit = &fitOverlay{err: "VRAM budget unknown; no GPU VRAM detected"}
		return
	}
	res, err := fit.Solve(fit.Request{
		ModelID:   e.base.ModelID,
		ModelPath: e.base.ModelPath,
		Meta:      e.meta,
		FileBytes: e.size,
		Budget:    budget,
		Settings:  fitSettings(e.draft),
	})
	if err != nil {
		e.fit = &fitOverlay{err: err.Error()}
		return
	}
	recs := append([]fit.Recommendation{res.Recommendation}, res.Alternatives...)
	e.fit = &fitOverlay{result: res, recs: recs}
}

func (m *Model) updateFitOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.editor
	f := e.fit
	if f == nil {
		return m, nil
	}
	if f.naming {
		return m.updateFitSaveName(msg)
	}
	if f.confirmName != "" {
		return m.updateFitConfirmSave(msg)
	}
	switch msg.String() {
	case "esc", "q":
		e.fit = nil
	case "j", "down":
		f.sel = clamp(f.sel+1, 0, maxInt(0, len(f.recs)-1))
	case "k", "up":
		f.sel = clamp(f.sel-1, 0, maxInt(0, len(f.recs)-1))
	case "g", "home":
		f.sel = 0
	case "G", "end":
		f.sel = maxInt(0, len(f.recs)-1)
	case "p":
		if f.err == "" && len(f.recs) > 0 {
			m.startFitPreset()
		}
	case "enter", " ":
		if f.err != "" || len(f.recs) == 0 {
			return m, nil
		}
		m.applyFit(f.recs[f.sel])
		e.fit = nil
		m.setToast("fit applied to draft · ctrl+s saves")
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) startFitPreset() {
	e := m.editor
	f := e.fit
	f.naming = true
	f.confirmName = ""
	f.notice = ""
	f.noticeErr = false
	f.saveName = textinput.New()
	f.saveName.CharLimit = 64
	f.saveName.Width = 40
	f.saveName.SetValue(fitPresetName(e.base.ModelID, f.recs[f.sel].Ctx))
	f.saveName.CursorEnd()
	f.saveName.Focus()
}

func fitPresetName(modelID string, ctx int) string {
	suffix := fmt.Sprintf("-%d", ctx)
	if len(modelID)+len(suffix) > 64 {
		modelID = modelID[:64-len(suffix)]
	}
	return modelID + suffix
}

func (m *Model) updateFitSaveName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.editor.fit
	switch msg.String() {
	case "esc":
		f.naming = false
		f.saveName.Blur()
		f.notice = ""
		f.noticeErr = false
	case "enter":
		name := strings.TrimSpace(f.saveName.Value())
		if err := m.saveFitPreset(name, false); err != nil {
			if errors.Is(err, config.ErrPresetExists) {
				f.naming = false
				f.saveName.Blur()
				f.confirmName = name
				f.notice = name + " already exists · press P to replace"
			} else {
				f.notice = err.Error()
			}
			f.noticeErr = true
			return m, nil
		}
		f.naming = false
		f.saveName.Blur()
		f.notice = "saved preset " + name
		f.noticeErr = false
		m.setToast("saved preset " + name)
	default:
		var cmd tea.Cmd
		f.saveName, cmd = f.saveName.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateFitConfirmSave(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.editor.fit
	switch msg.String() {
	case "esc":
		f.confirmName = ""
		f.notice = ""
		f.noticeErr = false
	case "P", "ctrl+p":
		name := f.confirmName
		if err := m.saveFitPreset(name, true); err != nil {
			f.notice = err.Error()
			f.noticeErr = true
			return m, nil
		}
		f.confirmName = ""
		f.notice = "saved preset " + name
		f.noticeErr = false
		m.setToast("saved preset " + name)
	}
	return m, nil
}

// saveFitPreset writes the selected recommendation as a new preset. It reloads
// the config but leaves the editor and fit overlay open.
func (m *Model) saveFitPreset(name string, force bool) error {
	e := m.editor
	if e == nil || e.fit == nil || len(e.fit.recs) == 0 {
		return fmt.Errorf("no recommendation to save")
	}
	rec := e.fit.recs[e.fit.sel]
	if err := m.cfg.SavePreset(e.path, name, e.base.ModelID, config.PresetFit{
		Ctx:        rec.Ctx,
		GPULayers:  rec.GPULayers,
		NCPUMoE:    rec.NCPUMoE,
		CacheTypeK: rec.CacheTypeK,
		CacheTypeV: rec.CacheTypeV,
	}, force); err != nil {
		return err
	}
	m.reloadConfig()
	return nil
}

// applyFit updates the editor's in-memory draft. Nothing is written until the
// user saves.
func (m *Model) applyFit(rec fit.Recommendation) {
	e := m.editor
	e.draft.Ctx = rec.Ctx
	e.draft.GPULayers = rec.GPULayers
	e.draft.NCPUMoE = rec.NCPUMoE
	e.draft.CacheTypeK = rec.CacheTypeK
	e.draft.CacheTypeV = rec.CacheTypeV
	for i := range e.fields {
		switch e.fields[i].key {
		case "ctx":
			e.fields[i].value = strconv.Itoa(rec.Ctx)
		case "gpu_layers":
			e.fields[i].value = strconv.Itoa(rec.GPULayers)
		case "n_cpu_moe":
			e.fields[i].value = strconv.Itoa(rec.NCPUMoE)
		case "cache_type_k":
			e.fields[i].value = quoteTOML(rec.CacheTypeK)
		case "cache_type_v":
			e.fields[i].value = quoteTOML(rec.CacheTypeV)
		}
	}
	e.dirty = true
	e.preview = m.previewFor(e, e.draft)
}

// saveFitDraft writes only the fit-controlled options that differ from the
// saved configuration. It does not create or edit presets.
func (m *Model) saveFitDraft() {
	e := m.editor
	if e == nil {
		return
	}
	if !e.dirty {
		m.setToast("no draft changes")
		return
	}
	var writes []struct{ key, value string }
	add := func(key, value string) {
		writes = append(writes, struct{ key, value string }{key, value})
	}
	if e.draft.Ctx != e.base.Ctx {
		add("ctx", strconv.Itoa(e.draft.Ctx))
	}
	if e.draft.GPULayers != e.base.GPULayers {
		add("gpu_layers", strconv.Itoa(e.draft.GPULayers))
	}
	if e.draft.NCPUMoE != e.base.NCPUMoE {
		add("n_cpu_moe", strconv.Itoa(e.draft.NCPUMoE))
	}
	if e.draft.CacheTypeK != e.base.CacheTypeK {
		add("cache_type_k", quoteTOML(e.draft.CacheTypeK))
	}
	if e.draft.CacheTypeV != e.base.CacheTypeV {
		add("cache_type_v", quoteTOML(e.draft.CacheTypeV))
	}
	if len(writes) == 0 {
		e.dirty = false
		m.setToast("no draft changes")
		return
	}
	for _, w := range writes {
		if err := config.SetValue(e.path, e.table, w.key, w.value); err != nil {
			e.err = err.Error()
			return
		}
	}
	table, sel := e.table, e.sel
	m.reloadConfig()
	m.openEditor()
	if m.editor != nil {
		m.editor.table = table
		m.editor.sel = clamp(sel, 0, maxInt(0, len(m.editor.fields)-1))
	}
	m.setToast("saved fit draft")
}

func (m *Model) editorFitView() string {
	st := m.render.Styles
	e := m.editor
	w := clamp(m.width-8, 40, 92)
	h := m.height - 2
	if h < 10 {
		h = 10
	}
	var body []string
	f := e.fit
	if f.err != "" {
		body = append(body, st.Warn.Render(ui.Fit("fit: "+f.err, w-4)))
		body = append(body, st.Muted.Render("esc close"))
	} else {
		res := f.result
		body = append(body, st.Muted.Render(ui.Fit(fmt.Sprintf(
			"budget %s available · native %d · min %d",
			ui.HumanBytes(res.Budget.Available), res.NativeContext, res.MinimumContext), w-4)))
		if f.naming {
			body = append(body, st.Accent.Render("save preset: ")+f.saveName.View())
		} else if f.notice != "" {
			style := st.Good
			if f.noticeErr {
				style = st.Warn
			}
			body = append(body, style.Render(ui.Fit(f.notice, w-4)))
		}
		body = append(body, "")
		for i, rec := range f.recs {
			mark := " "
			if i == f.sel {
				mark = "▸"
			}
			fallback := ""
			if rec.Fallback != "" {
				fallback = " · " + rec.Fallback
			}
			headroom := rec.Headroom
			if headroom < 0 {
				headroom = 0
			}
			line := fmt.Sprintf("%s %d. ctx %-7d gpu %-3d moe %-3d %s/%s · headroom %s%s",
				mark, rec.Rank, rec.Ctx, rec.GPULayers, rec.NCPUMoE,
				rec.CacheTypeK, rec.CacheTypeV, ui.HumanBytes(uint64(headroom)), fallback)
			if i == f.sel {
				body = append(body, st.Selected.Render(ui.Fit(line, w-4)))
			} else {
				body = append(body, ui.Fit(line, w-4))
			}
		}
		if len(f.recs) > 0 {
			rec := f.recs[f.sel]
			body = append(body, "")
			body = append(body, st.Muted.Render(ui.Fit(fmt.Sprintf(
				"gpu %s + kv %s + overhead %s = %s · limit %s",
				ui.HumanBytes(rec.Estimate.GPUWeights), ui.HumanBytes(rec.Estimate.KVBytes),
				ui.HumanBytes(rec.Estimate.Overhead), ui.HumanBytes(rec.Estimate.Total), rec.Limiting), w-4)))
			for _, change := range rec.Changes {
				body = append(body, st.Muted.Render(ui.Fit("  "+change, w-4)))
			}
			for _, note := range res.Notes {
				body = append(body, st.Warn.Render(ui.Fit("note: "+note, w-4)))
			}
		} else {
			body = append(body, st.Warn.Render("no recommendation"))
		}
	}
	if len(body) > h-2 {
		body = body[:h-2]
	}
	hint := "enter apply · p save preset · j/k select · esc close"
	if f.naming {
		hint = "enter save preset · esc cancel"
	}
	if f.confirmName != "" {
		hint = "P replace · esc cancel"
	}
	panel := m.render.Panel("fit · "+e.title, hint, body, w, h, true)
	lines := strings.Split(panel, "\n")
	left := clamp((m.width-w)/2, 0, m.width)
	var out []string
	for _, l := range lines {
		out = append(out, strings.Repeat(" ", left)+l)
	}
	return strings.Join(out, "\n")
}

// editorPreviewLines renders the unsaved estimate, its headroom and the
// limiting term. It is capped at three lines so the field list stays usable.
func (m *Model) editorPreviewLines(w int) []string {
	st := m.render.Styles
	p := m.editor.preview
	if p.Err != "" {
		return []string{st.Warn.Render("preview: " + p.Err)}
	}
	if !p.Ready {
		return []string{st.Muted.Render("preview: model metadata unreadable")}
	}
	delta := ""
	if p.Delta != 0 {
		delta = "  Δ " + signedBytes(p.Delta)
	}
	lines := []string{st.Muted.Render("preview " + fitBreakdown(p.Est) + delta)}
	budget := p.Budget
	style := st.Muted
	verdict := "budget unknown"
	if budget.Known {
		head := budget.Headroom(p.Est)
		style = st.Good
		verdict = "headroom " + ui.HumanBytes(uint64(head))
		if head < 0 {
			style = st.Bad
			verdict = "over by " + ui.HumanBytes(uint64(-head))
		} else if budget.Available > 0 && ui.Percent(p.Est.Total, budget.Available) > 90 {
			style = st.Warn
		}
	}
	barW := clamp(w-24, 8, 22)
	lines = append(lines, m.fitBar(p.Est, budget, barW)+" "+
		style.Render(verdict)+" · limit "+p.Est.LimitingTerm())
	switch {
	case p.Note != "":
		lines = append(lines, st.Warn.Render(p.Note))
	case budget.Known:
		detail := fmt.Sprintf("budget %s · used %s of %s · reserve %s",
			ui.HumanBytes(budget.Available), ui.HumanBytes(budget.Used),
			ui.HumanBytes(budget.Total), ui.HumanBytes(budget.Reserve))
		if budget.OwnKnown && budget.OwnUsed > 0 {
			detail += " · server " + ui.HumanBytes(budget.OwnUsed)
		}
		lines = append(lines, st.Muted.Render(detail))
	}
	return lines
}

func quoteTOML(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func fmtFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func (m *Model) editorView() string {
	if m.editor != nil && m.editor.fit != nil {
		return m.editorFitView()
	}
	st := m.render.Styles
	e := m.editor
	w := clamp(m.width-8, 40, 72)
	h := m.height - 2
	if h < 10 {
		h = 10
	}
	bodyH := h - 4
	preview := m.editorPreviewLines(w - 4)
	visible := bodyH - 1 - len(preview)
	if visible < 1 {
		visible = 1
	}
	e.visible = visible
	start := clamp(e.scroll, 0, maxInt(0, len(e.fields)-visible))
	end := start + visible
	if end > len(e.fields) {
		end = len(e.fields)
	}
	var body []string
	header := "values are written to " + e.path
	if e.dirty {
		header = "unsaved fit draft · ctrl+s writes " + e.path
	}
	body = append(body, st.Muted.Render(header))
	for i := start; i < end; i++ {
		f := e.fields[i]
		mark := " "
		if i == e.sel {
			mark = "▸"
		}
		suffix := st.Muted.Render("  " + f.source.Source)
		line := fmt.Sprintf("%s %-26s %-8s %s", mark, f.label, f.value, suffix)
		if i == e.sel {
			body = append(body, st.Selected.Render(ui.Fit(line, w-4)))
		} else {
			body = append(body, ui.Fit(line, w-4))
		}
	}
	body = append(body, "")
	body = append(body, preview...)
	if len(body) > bodyH {
		body = body[:bodyH]
	}
	hint := "enter edit · backspace reset · F fit · esc close"
	if e.dirty {
		hint = "fit draft · ctrl+s save · esc discard"
	}
	panel := m.render.Panel(e.title, hint, body, w, h, true)
	lines := strings.Split(panel, "\n")
	left := clamp((m.width-w)/2, 0, m.width)
	var out []string
	for _, l := range lines {
		out = append(out, strings.Repeat(" ", left)+l)
	}
	if e.editing {
		out = append(out, "")
		out = append(out, strings.Repeat(" ", left)+st.Accent.Render("new value: ")+e.input.View())
	}
	if e.err != "" {
		out = append(out, strings.Repeat(" ", left)+st.Bad.Render(e.err))
	}
	return strings.Join(out, "\n")
}

// updateEditor handles keys while the editor overlay is open.
func (m *Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.editor
	if e.fit != nil {
		return m.updateFitOverlay(msg)
	}
	if e.editing {
		switch msg.String() {
		case "esc":
			e.editing = false
			e.err = ""
			e.input.Blur()
			e.preview = m.previewFor(e, e.base)
			return m, nil
		case "enter":
			value := strings.TrimSpace(e.input.Value())
			f := e.fields[e.sel]
			if err := validateField(f, value); err != nil {
				e.err = err.Error()
				return m, nil
			}
			e.editing = false
			e.input.Blur()
			if err := config.SetValue(e.path, f.table, f.key, editorValue(f, value)); err != nil {
				e.err = err.Error()
				return m, nil
			}
			m.reloadConfig()
			table, sel := e.table, e.sel
			m.openEditor()
			if m.editor != nil {
				m.editor.table = table
				m.editor.sel = clamp(sel, 0, len(m.editor.fields)-1)
			}
			m.setToast("saved " + f.label)
			return m, nil
		}
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(msg)
		m.refreshPreview()
		return m, cmd
	}
	switch msg.String() {
	case "esc", "q":
		if e.dirty {
			m.setToast("fit draft discarded")
		}
		m.editor = nil
	case "F":
		m.openFitOverlay()
	case "ctrl+s":
		m.saveFitDraft()
	case "j", "down":
		e.sel = clamp(e.sel+1, 0, len(e.fields)-1)
		e.ensureScroll()
	case "k", "up":
		e.sel = clamp(e.sel-1, 0, len(e.fields)-1)
		e.ensureScroll()
	case "g", "home":
		e.sel = 0
		e.ensureScroll()
	case "G", "end":
		e.sel = len(e.fields) - 1
		e.ensureScroll()
	case "backspace", "delete":
		if e.dirty {
			m.setToast("save the fit draft with ctrl+s or press esc to discard it")
			return m, nil
		}
		f := e.fields[e.sel]
		if f.source.Layer != e.table {
			m.setToast(f.label + " comes from " + f.source.Source + "; nothing to reset")
			return m, nil
		}
		if err := config.RemoveValue(e.path, f.table, f.key); err != nil {
			e.err = err.Error()
			return m, nil
		}
		m.reloadConfig()
		table, sel := e.table, e.sel
		m.openEditor()
		if m.editor == nil {
			return m, nil
		}
		m.editor.table = table
		m.editor.sel = clamp(sel, 0, len(m.editor.fields)-1)
		next := config.SourceBuiltin
		if src, ok := m.editor.sources[f.label]; ok {
			next = src.Source
		}
		m.setToast("reset " + f.label + " to " + next)
		return m, nil
	case "enter", " ":
		if e.dirty {
			m.setToast("save the fit draft with ctrl+s or press esc to discard it")
			return m, nil
		}
		f := e.fields[e.sel]
		if f.kind == "bool" {
			next := "true"
			if f.value == "true" {
				next = "false"
			}
			if err := config.SetValue(e.path, f.table, f.key, next); err != nil {
				e.err = err.Error()
				return m, nil
			}
			m.reloadConfig()
			table, sel := e.table, e.sel
			m.openEditor()
			if m.editor != nil {
				m.editor.table = table
				m.editor.sel = clamp(sel, 0, len(m.editor.fields)-1)
			}
			m.setToast("saved " + f.label + " = " + next)
			return m, nil
		}
		e.editing = true
		e.err = ""
		e.input = textinput.New()
		e.input.CharLimit = 80
		e.input.Width = 40
		e.input.SetValue(unquoteTOML(f.value))
		e.input.CursorEnd()
		e.input.Focus()
		m.refreshPreview()
		return m, textinput.Blink
	}
	return m, nil
}

func (e *editor) ensureScroll() {
	visible := e.visible
	if visible <= 0 {
		visible = 16
	}
	if e.sel < e.scroll {
		e.scroll = e.sel
	}
	if e.sel >= e.scroll+visible {
		e.scroll = e.sel - visible + 1
	}
	if e.scroll < 0 {
		e.scroll = 0
	}
}

func unquoteTOML(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}

func validateField(f editorField, value string) error {
	if value == "" {
		return fmt.Errorf("%s cannot be empty", f.label)
	}
	switch f.kind {
	case "int":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("%s must be an integer", f.label)
		}
	case "float":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("%s must be a number", f.label)
		}
	case "string":
		return nil // text is quoted by the caller
	}
	return nil
}

// editorValue formats a user string for TOML.
func editorValue(f editorField, raw string) string {
	if f.kind == "string" {
		return quoteTOML(raw)
	}
	return raw
}
