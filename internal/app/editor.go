package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	flagcat "wool/internal/flags"
	"wool/internal/gguf"
	"wool/internal/ui"
)

// editorField is one TOML key that can be changed from the TUI.
type editorField struct {
	label  string
	table  string // full TOML table, e.g. presets.foo.extra
	key    string // TOML key inside the table
	value  string // current value, TOML-formatted
	kind   string // "int", "float", "string", "bool", "extra"
	extra  bool
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
	adding  bool
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
	extraTable := table + ".extra"
	for _, flag := range config.ExtraFlags(r.Extra) {
		e.fields = append(e.fields, editorField{
			label:  config.ExtraFieldName(flag),
			table:  extraTable,
			key:    flag,
			value:  quoteTOML(r.Extra[flag]),
			kind:   "extra",
			extra:  true,
			source: sources[config.ExtraFieldName(flag)],
		})
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

// quoteTOML formats a string as a TOML basic string.
func quoteTOML(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
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
	notes := e.extraNotes()
	visible := bodyH - 1 - len(notes) - len(preview)
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
	for _, note := range notes {
		style := st.Muted
		if strings.Contains(note, "duplicates") {
			style = st.Warn
		}
		body = append(body, style.Render(ui.Fit(note, w-4)))
	}
	for i := start; i < end; i++ {
		f := e.fields[i]
		mark := " "
		if i == e.sel {
			mark = "▸"
		}
		suffix := "  " + f.source.Source
		if f.extra && f.value == `""` {
			suffix += " · bare"
		}
		line := fmt.Sprintf("%s %-26s %-8s %s", mark, f.label, f.value, st.Muted.Render(suffix))
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
	hint := "enter edit · a add flag (tab flags) · backspace reset · x remove · F fit · esc close"
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
	} else if e.adding {
		out = append(out, "")
		out = append(out, strings.Repeat(" ", left)+st.Accent.Render("new flag [name=value]: ")+e.input.View())
	}
	if e.err != "" {
		out = append(out, strings.Repeat(" ", left)+st.Bad.Render(e.err))
	}
	return strings.Join(out, "\n")
}

// extraNotes keeps the unmodelled-extras caveat and any first-class conflicts
// visible without scrolling. Warnings stay advisory; the write still happens.
func (e *editor) extraNotes() []string {
	notes := []string{"extras are not in the VRAM estimate"}
	warnings := config.ExtraConflictWarnings(e.base.Extra)
	if catalog, err := flagcat.Embedded(); err == nil {
		warnings = flagcat.WarningMessages(flagcat.CheckExtras(e.base.Extra, catalog))
	}
	for _, warning := range warnings {
		notes = append(notes, warning)
	}
	return notes
}

func fieldSourceKey(f editorField) string {
	if f.extra {
		return config.ExtraFieldName(f.key)
	}
	return f.label
}

// updateEditor handles keys while the editor overlay is open.
func (m *Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.editor
	if m.flagPicker != nil {
		return m.updateFlagPicker(msg)
	}
	if e.fit != nil {
		return m.updateFitOverlay(msg)
	}
	if e.adding {
		return m.updateExtraFlagInput(msg)
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
	case "a":
		if e.dirty {
			m.setToast("save the fit draft with ctrl+s or press esc to discard it")
			return m, nil
		}
		m.startExtraFlagInput()
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
	case "backspace", "delete", "x":
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
		key := fieldSourceKey(f)
		if _, ok := m.editor.sources[key]; ok {
			next = m.editor.sources[key].Source
		} else if f.extra {
			next = "removed"
		}
		verb := "reset "
		if f.extra {
			verb = "removed "
		}
		m.setToast(verb + f.label + " to " + next)
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

func (m *Model) startExtraFlagInput() {
	e := m.editor
	e.adding = true
	e.editing = false
	e.err = ""
	e.input = textinput.New()
	e.input.CharLimit = 120
	e.input.Width = 40
	e.input.Focus()
}

func (m *Model) updateExtraFlagInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.editor
	switch msg.String() {
	case "esc":
		e.adding = false
		e.err = ""
		e.input.Blur()
		return m, nil
	case "enter":
		if err := m.addExtraFlag(e.input.Value()); err != nil {
			e.err = err.Error()
			return m, nil
		}
		return m, nil
	case "tab", "ctrl+f":
		m.openFlagPicker(e.input.Value(), "editor")
		return m, nil
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return m, cmd
}

// addExtraFlag writes a new extra to the current layer. "name=value" sets a
// value; a bare name (or an empty value) emits a bare llama-server flag.
func (m *Model) addExtraFlag(raw string) error {
	e := m.editor
	name, value, _ := strings.Cut(strings.TrimSpace(raw), "=")
	flag, err := config.NormalizeExtraFlag(name)
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	formatted := quoteTOML(value)
	table := e.table

	if err := config.SetValue(e.path, table+".extra", flag, formatted); err != nil {
		return err
	}
	m.reloadConfig()
	m.openEditor()
	if m.editor == nil {
		return nil
	}
	m.editor.table = table
	if idx := findEditorField(m.editor, config.ExtraFieldName(flag)); idx >= 0 {
		m.editor.sel = idx
		m.editor.ensureScroll()
	}

	notice := "added extra " + config.ExtraFieldName(flag)
	if value == "" {
		notice += " (bare)"
	}
	if conflict := config.ConflictingField(flag); conflict != "" {
		notice += " · warning: overrides " + conflict
	}
	if catalog, err := flagcat.Embedded(); err == nil {
		for _, warning := range flagcat.CheckExtras(map[string]string{flag: value}, catalog) {
			if warning.Kind != "conflict" {
				notice += " · warning: " + warning.Message
			}
		}
	}
	m.setToast(notice)
	return nil
}

func findEditorField(e *editor, label string) int {
	for i, f := range e.fields {
		if f.label == label {
			return i
		}
	}
	return -1
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
		if value, err := strconv.Unquote(s); err == nil {
			return value
		}
		return s[1 : len(s)-1]
	}
	return s
}

func validateField(f editorField, value string) error {
	if value == "" && f.kind != "extra" {
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
	if f.kind == "string" || f.kind == "extra" {
		return quoteTOML(raw)
	}
	return raw
}
