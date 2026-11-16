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
