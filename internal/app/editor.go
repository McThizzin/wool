package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
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
	m.editor = e
}

func quoteTOML(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func fmtFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func (m *Model) editorView() string {
	st := m.render.Styles
	e := m.editor
	w := clamp(m.width-8, 40, 72)
	h := m.height - 2
	if h < 10 {
		h = 10
	}
	bodyH := h - 4
	visible := bodyH
	e.visible = visible
	start := clamp(e.scroll, 0, maxInt(0, len(e.fields)-visible))
	end := start + visible
	if end > len(e.fields) {
		end = len(e.fields)
	}
	var body []string
	body = append(body, st.Muted.Render("values are written to "+e.path))
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
	panel := m.render.Panel(e.title, "enter edit · backspace reset · esc close", body, w, h, true)
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
	if e.editing {
		switch msg.String() {
		case "esc":
			e.editing = false
			e.err = ""
			e.input.Blur()
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
		return m, cmd
	}
	switch msg.String() {
	case "esc", "q":
		m.editor = nil
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
