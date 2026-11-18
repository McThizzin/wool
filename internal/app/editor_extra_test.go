package app

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/llamaserver"
)

func TestEditorExtraFlagLifecycle(t *testing.T) {
	m := testModel(t)
	m.setTab(tabPresets)
	m.presetSel = indexOf(m.presets, "gemma-heretic-32k")
	if m.presetSel < 0 {
		t.Fatal("example preset missing")
	}
	m.openEditor()
	if m.editor == nil {
		t.Fatal("editor did not open")
	}
	m.width, m.height = 110, 34
	if view := m.View(); !strings.Contains(view, "extras are not in the VRAM estimate") {
		t.Fatalf("editor missing extras caveat:\n%s", view)
	}

	m.updateEditor(keyMsg("a"))
	if !m.editor.adding {
		t.Fatal("a did not open the add-flag prompt")
	}
	m.editor.input.SetValue("spec-mode=fast")
	m.updateEditor(keyMsg("enter"))
	if m.editor == nil {
		t.Fatal("editor closed after add")
	}
	text, err := os.ReadFile(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "[presets.gemma-heretic-32k.extra]") ||
		!strings.Contains(string(text), `spec-mode = "fast"`) {
		t.Fatalf("extra was not written to the preset table:\n%s", text)
	}
	if !strings.Contains(string(text), "# Gemma-family sampling") {
		t.Fatalf("add lost an existing comment:\n%s", text)
	}
	idx := findEditorField(m.editor, "extra.spec-mode")
	if idx < 0 {
		t.Fatalf("new extra missing from editor fields: %+v", m.editor.fields)
	}
	if got := m.editor.fields[idx].source; got.Source != config.SourcePreset || got.Layer != "presets.gemma-heretic-32k" {
		t.Fatalf("new extra source = %+v", got)
	}

	// An empty value turns the flag into a bare flag in argv.
	m.editor.sel = idx
	m.updateEditor(keyMsg("enter"))
	m.editor.input.SetValue("")
	m.updateEditor(keyMsg("enter"))
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Presets["gemma-heretic-32k"]
	if p.Extra["spec-mode"] != "" {
		t.Fatalf("bare extra = %q", p.Extra["spec-mode"])
	}
	r, err := cfg.Resolve("", &p)
	if err != nil {
		t.Fatal(err)
	}
	args := llamaserver.SingleArgs(r, "127.0.0.1", 8080)
	found := false
	for _, arg := range args {
		if arg == "--spec-mode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bare extra missing from argv: %v", args)
	}
	m.width, m.height = 110, 38
	if view := m.View(); !strings.Contains(view, "bare") {
		t.Fatalf("editor did not mark the bare flag:\n%s", view)
	}

	// x removes only the preset key. There is no lower-layer value here.
	idx = findEditorField(m.editor, "extra.spec-mode")
	if idx < 0 {
		t.Fatal("bare extra disappeared from editor")
	}
	m.editor.sel = idx
	m.updateEditor(keyMsg("x"))
	cfg, err = config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Presets["gemma-heretic-32k"].Extra["spec-mode"]; ok {
		t.Fatal("preset extra was not removed")
	}
	if findEditorField(m.editor, "extra.spec-mode") >= 0 {
		t.Fatal("removed extra still listed")
	}
}

func TestEditorExtraResetFallsBackAndWarnsOnConflict(t *testing.T) {
	m := testModel(t)
	m.setTab(tabPresets)
	m.presetSel = indexOf(m.presets, "qwen35-mtp-32k")
	if m.presetSel < 0 {
		t.Fatal("example preset missing")
	}
	m.openEditor()
	if m.editor == nil {
		t.Fatal("editor did not open")
	}
	idx := findEditorField(m.editor, "extra.spec-type")
	if idx < 0 {
		t.Fatal("inherited model extra missing from editor")
	}
	if got := m.editor.fields[idx].source; got.Source != config.SourceModel || got.Layer != "models.qwen35-4b-mtp" {
		t.Fatalf("inherited extra source = %+v", got)
	}

	m.updateEditor(keyMsg("a"))
	m.editor.input.SetValue("spec-type=custom")
	m.updateEditor(keyMsg("enter"))
	idx = findEditorField(m.editor, "extra.spec-type")
	if idx < 0 || m.editor.fields[idx].source.Source != config.SourcePreset {
		t.Fatalf("preset override source = %+v", m.editor.fields[idx])
	}

	m.editor.sel = idx
	m.updateEditor(keyMsg("backspace"))
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Presets["qwen35-mtp-32k"].Extra["spec-type"]; ok {
		t.Fatal("reset left the preset override in place")
	}
	if cfg.Models["qwen35-4b-mtp"].Extra["spec-type"] != "draft-mtp" {
		t.Fatal("reset removed the lower-layer value too")
	}
	idx = findEditorField(m.editor, "extra.spec-type")
	if idx < 0 || m.editor.fields[idx].source.Source != config.SourceModel {
		t.Fatalf("source after reset = %+v", m.editor.fields[idx])
	}

	m.updateEditor(keyMsg("a"))
	m.editor.input.SetValue("ctx-size=12345")
	m.updateEditor(keyMsg("enter"))
	if !strings.Contains(m.toast, "warning: overrides ctx") {
		t.Fatalf("conflict toast = %q", m.toast)
	}
	cfg, err = config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Presets["qwen35-mtp-32k"].Extra["ctx-size"] != "12345" {
		t.Fatal("conflicting extra was not saved")
	}
	m.width, m.height = 110, 34
	if view := m.View(); !strings.Contains(view, "duplicates first-class option") {
		t.Fatalf("editor missing conflict warning:\n%s", view)
	}
}

func TestEditorAddsBareExtraByNameOnly(t *testing.T) {
	m := testModel(t)
	m.setTab(tabModels)
	m.modelSel = indexOf(m.models, "qwen35-4b-mtp")
	if m.modelSel < 0 {
		t.Fatal("example model missing")
	}
	m.openEditor()
	m.updateEditor(keyMsg("a"))
	m.editor.input.SetValue("  --no-warmup  ")
	m.updateEditor(keyMsg("enter"))
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := cfg.Models["qwen35-4b-mtp"].Extra["no-warmup"]
	if !ok || value != "" {
		t.Fatalf("bare model extra = %q, present = %v", value, ok)
	}
	idx := findEditorField(m.editor, "extra.no-warmup")
	if idx < 0 {
		t.Fatal("new bare extra missing from editor")
	}
	m.editor.sel = idx
	m.updateEditor(tea.KeyMsg{Type: tea.KeyEnter})
	m.editor.input.SetValue("warm")
	m.updateEditor(tea.KeyMsg{Type: tea.KeyEnter})
	cfg, err = config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models["qwen35-4b-mtp"].Extra["no-warmup"] != "warm" {
		t.Fatalf("edited extra = %q", cfg.Models["qwen35-4b-mtp"].Extra["no-warmup"])
	}
}
