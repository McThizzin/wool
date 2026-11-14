package app

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/config"
	"wool/internal/llamaserver"
)

func TestGroupWarning(t *testing.T) {
	if got := groupWarning("coding", 2, 2); got != "" {
		t.Fatalf("warning at limit = %q", got)
	}
	if got := groupWarning("coding", 2, 0); got != "" {
		t.Fatalf("warning for unlimited = %q", got)
	}
	got := groupWarning("coding", 2, 1)
	for _, want := range []string{"coding", "2 models", "models_max is 1", "evict"} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning missing %q: %s", want, got)
		}
	}
}

func TestGroupSummaryAndDetails(t *testing.T) {
	results := []llamaserver.GroupResult{
		{Model: "a", Status: "loaded"},
		{Model: "b", Err: errors.New("boom")},
	}
	summary, failed := groupSummary("coding", results, 1)
	for _, want := range []string{"group coding: 1/2 loaded", "failed: b"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	if len(failed) != 1 || failed[0] != "b" {
		t.Fatalf("failed = %v", failed)
	}
	details := groupDetails(results, 1)
	for _, want := range []string{"a", "loaded", "b", "FAILED", "boom", "models_max is 1"} {
		if !strings.Contains(details, want) {
			t.Fatalf("details missing %q:\n%s", want, details)
		}
	}

	summary, failed = groupSummary("coding", []llamaserver.GroupResult{
		{Model: "a", Status: "loaded"},
		{Model: "b", Status: "loaded"},
	}, 1)
	if len(failed) != 0 || !strings.Contains(summary, "earlier models evicted") {
		t.Fatalf("summary = %q, failed = %v", summary, failed)
	}
}

func TestGroupPickerWarnsBeforeOverLimitLoad(t *testing.T) {
	m := testModel(t)
	m.hasRouter = true
	m.setTab(tabRouter)
	m.openGroupPicker()
	if !m.groupPick || m.overlay == "" {
		t.Fatalf("picker did not open: pick=%v overlay=%q", m.groupPick, m.overlay)
	}
	if !strings.Contains(m.overlay, "coding") {
		t.Fatalf("picker missing group:\n%s", m.overlay)
	}
	for _, size := range [][2]int{{100, 30}, {60, 20}, {40, 12}} {
		checkView(t, m, size[0], size[1])
	}

	_, cmd := m.updateGroupPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("over-limit group should ask for confirmation before starting")
	}
	if m.groupWait != "coding" {
		t.Fatalf("groupWait = %q", m.groupWait)
	}
	for _, want := range []string{"models_max is 1", "load anyway", "cancel"} {
		if !strings.Contains(m.overlay, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, m.overlay)
		}
	}
	for _, size := range [][2]int{{100, 30}, {60, 20}, {40, 12}} {
		checkView(t, m, size[0], size[1])
	}

	// Escape cancels without starting a load.
	m.updateGroupConfirm(tea.KeyMsg{Type: tea.KeyEsc})
	if m.groupWait != "" || m.overlay != "" {
		t.Fatalf("cancel left wait=%q overlay=%q", m.groupWait, m.overlay)
	}
}

func TestGroupPickerStartsWithinLimit(t *testing.T) {
	m := testModel(t)
	m.hasRouter = true
	m.cfg.Router.ModelsMax = 2
	m.cfg.Groups["single"] = config.GroupConfig{Models: []string{"qwen3-4b-cyber"}}
	m.groups = []string{"single"}
	m.groupSel = 0
	m.setTab(tabRouter)
	m.openGroupPicker()

	_, cmd := m.updateGroupPicker(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("within-limit group should start immediately")
	}
	if m.groupWait != "" {
		t.Fatalf("unexpected confirmation for %q", m.groupWait)
	}
	if m.busy == "" {
		t.Fatal("group load should mark the UI busy")
	}
}

func TestLoadGroupCommandRequiresGroup(t *testing.T) {
	m := testModel(t)
	cmd, handled := m.runCommand("/load-group")
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	if !strings.Contains(m.toast, "group name required") {
		t.Fatalf("toast = %q", m.toast)
	}
}
