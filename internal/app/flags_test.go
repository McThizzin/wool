package app

import (
	"strings"
	"testing"
)

func TestFlagPickerFromCommand(t *testing.T) {
	m := testModel(t)
	_, handled := m.runCommand("/flags ctx")
	if !handled || m.flagPicker == nil {
		t.Fatalf("handled = %v, picker = %+v", handled, m.flagPicker)
	}
	if got := m.flagPicker.query.Value(); got != "ctx" {
		t.Fatalf("query = %q", got)
	}
	if len(m.flagPicker.results) == 0 || m.flagPicker.results[0].Flag.Name != "ctx-size" {
		t.Fatalf("results = %+v", m.flagPicker.results)
	}

	m.width, m.height = 100, 30
	if view := m.View(); !strings.Contains(view, "search:") || !strings.Contains(view, "ctx-size") {
		t.Fatalf("picker view = %q", view)
	}
	checkView(t, m, 80, 24)
	checkView(t, m, 40, 14)

	m.updateFlagPicker(keyMsg("enter"))
	if m.flagPicker != nil {
		t.Fatal("enter did not close the picker")
	}
	if !strings.Contains(m.toast, "--ctx-size") {
		t.Fatalf("toast = %q", m.toast)
	}
}

func TestFlagPickerFromEditorPrefillsExtra(t *testing.T) {
	m := testModel(t)
	m.setTab(tabPresets)
	m.presetSel = indexOf(m.presets, "gemma-heretic-32k")
	if m.presetSel < 0 {
		t.Fatal("example preset missing")
	}
	m.openEditor()
	m.updateEditor(keyMsg("a"))
	if m.editor == nil || !m.editor.adding {
		t.Fatal("add-flag prompt did not open")
	}

	m.editor.input.SetValue("gpu")
	m.updateEditor(keyMsg("tab"))
	if m.flagPicker == nil {
		t.Fatal("tab did not open the flag picker")
	}
	if len(m.flagPicker.results) == 0 || m.flagPicker.results[0].Flag.Name != "gpu-layers" {
		t.Fatalf("results = %+v", m.flagPicker.results)
	}
	m.width, m.height = 100, 30
	if view := m.View(); !strings.Contains(view, "add extra flag") {
		t.Fatalf("picker view = %q", view)
	}
	m.updateFlagPicker(keyMsg("enter"))
	if m.flagPicker != nil || m.editor == nil {
		t.Fatal("picker did not return to the editor")
	}
	if !m.editor.adding || m.editor.input.Value() != "gpu-layers=" {
		t.Fatalf("value-bearing prefill = %q, adding = %v", m.editor.input.Value(), m.editor.adding)
	}
	if !strings.Contains(m.toast, "warning") {
		t.Fatalf("conflict toast = %q", m.toast)
	}

	m.editor.input.SetValue("verbose")
	m.updateEditor(keyMsg("tab"))
	if m.flagPicker == nil || len(m.flagPicker.results) == 0 || m.flagPicker.results[0].Flag.Name != "verbose" {
		t.Fatalf("verbose picker = %+v", m.flagPicker)
	}
	m.updateFlagPicker(keyMsg("enter"))
	if m.editor == nil || m.editor.input.Value() != "verbose" {
		t.Fatalf("bare prefill = %q", m.editor.input.Value())
	}
}

func TestFlagCommandCompletion(t *testing.T) {
	m := testModel(t)
	m.openCommand("/fl")
	m.updateCommand(keyMsg("tab"))
	if !strings.Contains(m.input.Value(), "flags") {
		t.Fatalf("completion = %q", m.input.Value())
	}
}
