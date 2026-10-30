package app

import (
	"testing"
	"time"

	"wool/internal/theme"
)

func TestPollInterval(t *testing.T) {
	m := testModel(t)
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", time.Second},
		{"bogus", time.Second},
		{"-1s", time.Second},
		{"10ms", 250 * time.Millisecond},
		{"5s", 5 * time.Second},
		{"2h", time.Minute},
	}
	for _, tc := range cases {
		m.cfg.Server.PollInterval = tc.raw
		if got := m.pollInterval(); got != tc.want {
			t.Fatalf("pollInterval(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestNewModelRegistersCustomThemes(t *testing.T) {
	t.Cleanup(func() { _ = theme.RegisterAll(nil) })
	m := testModel(t)
	m.cfg.Themes = map[string]theme.Definition{
		"ocean": {Base: "nord", Colors: map[string]string{"accent": "#00aaff"}},
	}
	m.cfg.App.Theme = "ocean"
	m = newModel(m.cfg, m.cfgPath, m.mgr)
	if m.themeName != "ocean" {
		t.Fatalf("theme = %q", m.themeName)
	}
	if _, ok := theme.Get("ocean"); !ok {
		t.Fatal("custom theme not registered")
	}
	m.cycleTheme(1)
	found := false
	for _, name := range theme.Order() {
		if name == "ocean" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom theme missing from cycle order")
	}
}
