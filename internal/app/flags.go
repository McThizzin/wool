package app

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	flagcat "wool/internal/flags"
	"wool/internal/ui"
)

// flagPicker is the fuzzy catalog overlay. It is opened by /flags or from the
// editor's add-extra prompt.
type flagPicker struct {
	query   textinput.Model
	results []flagcat.SearchResult
	catalog *flagcat.Catalog
	sel     int
	scroll  int
	origin  string // "command" or "editor"
	err     string
}

func (m *Model) openFlagPicker(query, origin string) {
	catalog, err := flagcat.Embedded()
	if err != nil {
		m.setToast("flags: " + err.Error())
		return
	}
	input := textinput.New()
	input.CharLimit = 120
	input.Width = 48
	input.SetValue(query)
	input.CursorEnd()
	input.Focus()
	p := &flagPicker{catalog: catalog, query: input, origin: origin}
	p.refresh()
	m.flagPicker = p
}

func (p *flagPicker) refresh() {
	p.results = flagcat.Search(p.catalog, p.query.Value(), 0)
	if p.sel >= len(p.results) {
		p.sel = len(p.results) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
	if p.sel < p.scroll {
		p.scroll = p.sel
	}
	if p.sel >= p.scroll+12 {
		p.scroll = p.sel - 11
	}
}

func (m *Model) updateFlagPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.flagPicker
	switch msg.String() {
	case "esc":
		m.flagPicker = nil
		if p.origin == "editor" && m.editor != nil {
			m.editor.input.Focus()
		}
		return m, nil
	case "enter":
		if len(p.results) == 0 {
			p.err = "no matching flags"
			return m, nil
		}
		return m, m.selectFlag(p.results[p.sel].Flag)
	case "up", "ctrl+p":
		if p.sel > 0 {
			p.sel--
		}
		p.ensureScroll()
		return m, nil
	case "down", "ctrl+n":
		if p.sel < len(p.results)-1 {
			p.sel++
		}
		p.ensureScroll()
		return m, nil
	case "g":
		p.sel, p.scroll = 0, 0
		return m, nil
	case "G":
		p.sel = maxInt(0, len(p.results)-1)
		p.ensureScroll()
		return m, nil
	}
	var cmd tea.Cmd
	p.query, cmd = p.query.Update(msg)
	p.sel = 0
	p.scroll = 0
	p.err = ""
	p.refresh()
	return m, cmd
}

func (p *flagPicker) ensureScroll() {
	if p.sel < p.scroll {
		p.scroll = p.sel
	}
	if p.sel >= p.scroll+12 {
		p.scroll = p.sel - 11
	}
}

func (m *Model) selectFlag(f flagcat.Flag) tea.Cmd {
	origin := m.flagPicker.origin
	m.flagPicker = nil
	if origin == "editor" && m.editor != nil {
		e := m.editor
		e.adding = true
		e.editing = false
		e.err = ""
		value := f.Name
		if f.NeedsValue() {
			value += "="
		}
		e.input.SetValue(value)
		e.input.CursorEnd()
		e.input.Focus()
		if conflict := config.ConflictingField(f.Name); conflict != "" {
			m.setToast("selected --" + f.Name + " · warning: overrides " + conflict)
		} else {
			m.setToast("selected --" + f.Name + " · enter to add")
		}
		return textinput.Blink
	}
	m.setToast(f.Usage() + " — " + f.Description)
	return nil
}

func (m *Model) flagPickerView() string {
	p := m.flagPicker
	st := m.render.Styles
	contentWidth := m.width - 4
	if contentWidth < 8 {
		contentWidth = 8
	}
	queryWidth := m.width - 12
	if queryWidth < 8 {
		queryWidth = 8
	}
	p.query.Width = queryWidth
	bodyH := m.height - 6
	if bodyH < 4 {
		bodyH = 4
	}
	start := clamp(p.scroll, 0, maxInt(0, len(p.results)-bodyH))
	end := start + bodyH
	if end > len(p.results) {
		end = len(p.results)
	}
	body := []string{
		st.Accent.Render("search: ") + p.query.View(),
		"",
	}
	if p.err != "" {
		body = append(body, st.Warn.Render(p.err), "")
	}
	for i := start; i < end; i++ {
		r := p.results[i]
		mark := " "
		if i == p.sel {
			mark = "▸"
		}
		line := fmt.Sprintf("%s %-24s %-8s %s", mark, "--"+r.Flag.Name, r.Flag.Value, r.Flag.Description)
		if i == p.sel {
			body = append(body, st.Selected.Render(ui.Fit(line, contentWidth)))
		} else {
			body = append(body, ui.Fit(line, contentWidth))
		}
	}
	if len(p.results) == 0 {
		body = append(body, st.Muted.Render("no matching flags"))
	}
	if p.sel < len(p.results) {
		f := p.results[p.sel].Flag
		detail := "--" + f.Name + " · " + f.Category
		if f.Default != "" {
			detail += " · default " + f.Default
		}
		if f.Deprecated {
			detail += " · deprecated"
		}
		body = append(body, "", st.Muted.Render(ui.Fit(detail, contentWidth)))
		body = append(body, st.Muted.Render(ui.Fit(f.Description, contentWidth)))
	}
	title := "llama-server flags"
	if p.origin == "editor" {
		title = "add extra flag"
	}
	panel := m.render.Panel(title, "up/down select · enter add · esc close", body, m.width, m.height-1, true)
	return panel + "\n" + st.Muted.Render(ui.Fit(fmt.Sprintf(" catalog %s · %d matches", p.catalog.Source.Version, len(p.results)), m.width))
}
