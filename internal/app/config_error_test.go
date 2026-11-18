package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/daemon"
)

func testConfigErrorModel(t *testing.T, err error) *Model {
	t.Helper()
	return newError(err, "/tmp/wool-config.toml", daemon.NewManagerWithDirs(t.TempDir(), t.TempDir()))
}

func TestConfigErrorOverlay(t *testing.T) {
	err := &config.Error{
		File: "/tmp/wool-config.toml", Line: 3, Column: 5,
		Key: "server.prt", Message: "unknown field",
	}
	m := testConfigErrorModel(t, err)
	if !m.configError || m.overlayTitle != "config error" {
		t.Fatalf("model = configError %v, title %q", m.configError, m.overlayTitle)
	}
	m.width, m.height = 100, 30
	out := m.View()
	want := `/tmp/wool-config.toml:3:5: unknown field "server.prt"`
	if !strings.Contains(out, want) {
		t.Fatalf("overlay missing %q:\n%s", want, out)
	}
	checkView(t, m, 100, 30)
	checkView(t, m, 60, 12)
	checkView(t, m, 20, 6)
}

func TestConfigErrorOverlayListsAllErrors(t *testing.T) {
	err := config.Errors{
		{File: "/tmp/wool-config.toml", Line: 2, Column: 1, Key: "prot", Message: "unknown field"},
		{File: "/tmp/wool-config.toml", Line: 3, Column: 1, Key: "porrt", Message: "unknown field"},
	}
	m := testConfigErrorModel(t, err)
	m.width, m.height = 100, 30
	out := m.View()
	for _, want := range []string{"2:1: unknown field \"prot\"", "3:1: unknown field \"porrt\""} {
		if !strings.Contains(out, want) {
			t.Fatalf("overlay missing %q:\n%s", want, out)
		}
	}
}

func TestConfigErrorOverlayDismissQuits(t *testing.T) {
	m := testConfigErrorModel(t, &config.Error{File: "x", Message: "bad"})
	_, cmd := m.handleKey(keyMsg("esc"))
	if m.overlay != "" {
		t.Fatalf("overlay stayed open: %q", m.overlay)
	}
	if cmd == nil {
		t.Fatal("dismissing a fatal config error did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("dismiss command = %T, want tea.Quit", cmd())
	}
}
