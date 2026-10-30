package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/sysinfo"
)

func testModel(t *testing.T) *Model {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr := daemon.NewManagerWithDirs(filepath.Join(dir, "state"), filepath.Join(dir, "logs"))
	m := newModel(cfg, path, mgr)
	m.snap = sysinfo.Snapshot{
		CPUs: 8, CPUPercent: 12.5, MemTotal: 16 << 30, MemAvailable: 8 << 30,
		GPUs: []sysinfo.GPU{{
			Card: "card1", Name: "Test GPU", Driver: "amdgpu",
			TempC: 52, HasTemp: true, VRAMTotal: 8 << 30, VRAMUsed: 2 << 30, HasVRAM: true,
			BusyPercent: 17, HasBusy: true, PowerW: 45, HasPower: true, FanRPM: 900, HasFan: true,
		}},
	}
	m.serverHealth = "ok"
	m.routerHealth = "ok"
	return m
}

func checkView(t *testing.T, m *Model, w, h int) {
	t.Helper()
	m.width, m.height = w, h
	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Fatalf("%dx%d: %d lines, want <= %d", w, h, len(lines), h)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > w {
			t.Fatalf("%dx%d: line %d is %d cells wide: %q", w, h, i+1, got, line)
		}
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("%dx%d: empty view", w, h)
	}
}

func TestViewAllTabsAndSizes(t *testing.T) {
	m := testModel(t)
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}} {
		for tab := tab(0); tab < tabCount; tab++ {
			m.setTab(tab)
			checkView(t, m, size[0], size[1])
		}
	}
}

func TestViewWithLogsAndCommand(t *testing.T) {
	m := testModel(t)
	m.logLines = []string{"0.00 I srv starting", "0.01 W srv warn: slow", "error: boom"}
	m.logName = "server"
	m.setTab(tabLogs)
	checkView(t, m, 100, 30)

	m.openCommand("/sta")
	checkView(t, m, 100, 30)
	if !strings.Contains(m.View(), "/sta") {
		t.Fatal("command line not rendered")
	}
}

func TestEditorOverlayAndSave(t *testing.T) {
	m := testModel(t)
	if len(m.presets) == 0 {
		t.Fatal("no presets")
	}
	m.setTab(tabPresets)
	m.openEditor()
	if m.editor == nil {
		t.Fatal("editor did not open")
	}
	checkView(t, m, 110, 34)

	// Select ctx, type a new value and save.
	idx := -1
	for i, f := range m.editor.fields {
		if f.label == "ctx" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("ctx field missing")
	}
	m.editor.sel = idx
	m.editor.editing = false
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	if _, _ = m.updateEditor(enter); !m.editor.editing {
		t.Fatal("editor did not enter edit mode")
	}
	m.editor.input.SetValue("12345")
	if _, _ = m.updateEditor(enter); m.editor == nil {
		t.Fatal("editor closed after save")
	}
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.Resolve("", ptrPreset(cfg, m.editor.table))
	if err != nil {
		t.Fatal(err)
	}
	if r.Ctx != 12345 {
		t.Fatalf("ctx = %d, want 12345", r.Ctx)
	}
}

func ptrPreset(cfg *config.Config, table string) *config.PresetConfig {
	id := strings.TrimPrefix(table, "presets.")
	p, ok := cfg.Presets[id]
	if !ok {
		return nil
	}
	return &p
}

func TestEditorRejectsBadNumber(t *testing.T) {
	m := testModel(t)
	m.openEditor()
	for i, f := range m.editor.fields {
		if f.label == "gpu_layers" {
			m.editor.sel = i
			break
		}
	}
	m.updateEditor(tea.KeyMsg{Type: tea.KeyEnter})
	m.editor.input.SetValue("not-a-number")
	m.updateEditor(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editor.err == "" {
		t.Fatal("expected validation error")
	}
	if !m.editor.editing {
		t.Fatal("editor should stay in edit mode after an error")
	}
}

func TestHeaderAndFooterLayout(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 120, 40
	lines := strings.Split(m.View(), "\n")
	header := lines[0]
	for _, want := range []string{"[1] Dashboard", "[6] Logs", "wool"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q: %q", want, header)
		}
	}
	tail := lines[len(lines)-2:]
	if !strings.Contains(tail[0], "press /") || !strings.Contains(tail[0], "mocha") {
		t.Fatalf("prompt/theme line = %q", tail[0])
	}
	if !strings.Contains(tail[1], "tab switch") || !strings.Contains(tail[1], "120x40") {
		t.Fatalf("hints/size line = %q", tail[1])
	}

	m.width = 80
	header = strings.Split(m.View(), "\n")[0]
	if strings.Contains(header, "Dashboard") {
		t.Fatalf("expected compact tabs at 80 columns: %q", header)
	}
	for _, want := range []string{"[1]", "[6]", "wool"} {
		if !strings.Contains(header, want) {
			t.Fatalf("compact header missing %q: %q", want, header)
		}
	}
}

func TestCommands(t *testing.T) {
	m := testModel(t)
	if cmd, _ := m.runCommand("/tab router"); cmd != nil {
		_ = cmd
	}
	if m.tab != tabRouter {
		t.Fatalf("tab = %v", m.tab)
	}
	m.runCommand("/theme tokyo")
	if m.themeName != "tokyo" {
		t.Fatalf("theme = %s", m.themeName)
	}
	m.runCommand("/does-not-exist")
	if !strings.Contains(m.toast, "unknown command") {
		t.Fatalf("toast = %q", m.toast)
	}
	m.runCommand("/help")
	if m.overlay == "" {
		t.Fatal("help overlay missing")
	}
	m.overlay = ""
	m.logLines = []string{"x"}
	m.runCommand("/logs router")
	if m.logSel != 1 || m.tab != tabLogs {
		t.Fatalf("logs command: sel=%d tab=%v", m.logSel, m.tab)
	}
}

func TestCommandCompletion(t *testing.T) {
	m := testModel(t)
	m.openCommand("/sta")
	m.completeCommand()
	if got := m.input.Value(); !strings.HasPrefix(got, "/start ") {
		t.Fatalf("completion = %q", got)
	}
	m.input.SetValue("/theme gr")
	m.completeCommand()
	if got := m.input.Value(); got != "/theme gruvbox" {
		t.Fatalf("theme completion = %q", got)
	}
}

func TestMoveSelectionClamped(t *testing.T) {
	m := testModel(t)
	m.setTab(tabModels)
	m.moveSelection(-100)
	if m.modelSel != 0 {
		t.Fatalf("modelSel = %d", m.modelSel)
	}
	m.moveSelection(100)
	if m.modelSel != len(m.models)-1 {
		t.Fatalf("modelSel = %d, want %d", m.modelSel, len(m.models)-1)
	}
	m.setTab(tabPresets)
	m.presetSel = 0
	m.moveSelection(-1)
	if m.presetSel != 0 {
		t.Fatalf("presetSel = %d", m.presetSel)
	}
}

func TestFitCacheReused(t *testing.T) {
	m := testModel(t)
	id := m.presets[0]
	first := m.fitFor(id, "preset")
	second := m.fitFor(id, "preset")
	if first.Err != second.Err || first.Ready != second.Ready {
		t.Fatalf("cache mismatch: %+v vs %+v", first, second)
	}
	if _, ok := m.fitCache["preset:"+id]; !ok {
		t.Fatal("fit was not cached")
	}
}

func TestScrollLogs(t *testing.T) {
	m := testModel(t)
	m.setTab(tabLogs)
	m.logLines = make([]string, 100)
	m.follow = true
	m.scrollLogs(5)
	if m.follow || m.logScroll != 5 {
		t.Fatalf("follow=%v scroll=%d", m.follow, m.logScroll)
	}
	m.scrollLogs(1000)
	if m.logScroll != 99 {
		t.Fatalf("scroll = %d", m.logScroll)
	}
	m.scrollLogs(-1000)
	if m.logScroll != 0 {
		t.Fatalf("scroll = %d", m.logScroll)
	}
}
