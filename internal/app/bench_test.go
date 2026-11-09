package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/measure"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func seedBench(t *testing.T, m *Model) {
	t.Helper()
	add := func(at int64, target, model string, ctx int, cache string, prompt, gen float64, vram uint64, loadMS float64, hash string) {
		t.Helper()
		r := measure.Record{
			Version: measure.Version, Kind: measure.KindBench,
			At: time.Unix(at, 0), Target: target, TargetKind: "preset", ModelID: model,
			Ctx: ctx, CacheTypeK: cache, CacheTypeV: cache, GPULayers: 999,
			ConfigHash: hash,
			Bench: &measure.Bench{
				Engine: "server", LoadKnown: true, LoadMS: loadMS,
				VRAMKnown: true, VRAMDeltaBytes: vram,
				Median: measure.BenchStats{PromptTPS: prompt, GenerationTPS: gen, TotalMS: 1000},
			},
		}
		if err := m.meas.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	add(100, "gemma-heretic-32k", "gemma-4-heretic", 32768, "q8_0", 200, 40, 4<<30, 2100, "abcdef123456")
	add(200, "gemma-heretic-65k", "gemma-4-heretic", 65536, "f16", 220, 44, 5<<30, 2300, "abcdef123456")
}

func TestBenchViewRendersHistory(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabBench)
	if len(m.benchRuns) != 2 {
		t.Fatalf("runs = %d, want 2", len(m.benchRuns))
	}
	if m.benchSel != 1 {
		t.Fatalf("selection = %d, want newest (1)", m.benchSel)
	}
	checkView(t, m, 110, 34)
	out := m.View()
	for _, want := range []string{"Bench", "gemma-hereti", "abcdef12", "4.0 GiB", "2.1s", "gen", "prompt", "t/s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("bench view missing %q:\n%s", want, out)
		}
	}
}

func TestBenchFilterCyclesTargets(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabBench)

	m.cycleBenchFilter()
	if m.benchFilter != "gemma-heretic-32k" || len(m.benchRuns) != 1 {
		t.Fatalf("filter = %q, runs = %d", m.benchFilter, len(m.benchRuns))
	}
	m.cycleBenchFilter()
	if m.benchFilter != "gemma-heretic-65k" || len(m.benchRuns) != 1 {
		t.Fatalf("filter = %q, runs = %d", m.benchFilter, len(m.benchRuns))
	}
	m.cycleBenchFilter()
	if m.benchFilter != "" || len(m.benchRuns) != 2 {
		t.Fatalf("filter = %q, runs = %d", m.benchFilter, len(m.benchRuns))
	}
}

func TestBenchMarkAndCompare(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabBench)

	m.benchSel = 0
	m.benchMarkAction()
	if !m.benchMarkSet || !m.benchMarkAt.Equal(time.Unix(100, 0)) {
		t.Fatalf("mark = %v set=%v", m.benchMarkAt, m.benchMarkSet)
	}
	m.benchSel = 1
	m.showBenchCompare()
	if m.overlay == "" {
		t.Fatal("compare did not open an overlay")
	}
	if !strings.Contains(m.overlayTitle, "bench compare") {
		t.Fatalf("compare title = %q", m.overlayTitle)
	}
	for _, want := range []string{
		"config changes", "cache k", "results",
		"gen t/s", "(+10.0%)", "4.0 GiB", "5.0 GiB", "2.1s", "2.3s",
	} {
		if !strings.Contains(m.overlay, want) {
			t.Fatalf("compare missing %q:\n%s", want, m.overlay)
		}
	}

	// Marking the same run again clears it, and comparing without a mark is
	// refused rather than opening an empty overlay.
	m.overlay = ""
	m.benchSel = 0
	m.benchMarkAction()
	if m.benchMarkSet {
		t.Fatal("second space did not clear the mark")
	}
	m.showBenchCompare()
	if m.overlay != "" || !strings.Contains(m.toast, "mark a run") {
		t.Fatalf("compare without mark opened %q (toast %q)", m.overlay, m.toast)
	}
}

func TestBenchConfigDiffUsesSelectedTarget(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabBench)
	m.benchSel = 1
	m.showBenchConfigDiff()
	if !strings.Contains(m.overlayTitle, "config diff · gemma-heretic-65k") {
		t.Fatalf("overlay title = %q (toast %q)", m.overlayTitle, m.toast)
	}
	if m.overlay == "" {
		t.Fatal("expected a config diff overlay")
	}
}

func TestBenchTabKeys(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabDashboard)

	// B jumps to the bench tab and loads history.
	if _, _ = m.handleKey(keyMsg("B")); m.tab != tabBench {
		t.Fatalf("tab = %v, want bench", m.tab)
	}
	if len(m.benchRuns) != 2 {
		t.Fatalf("runs = %d", len(m.benchRuns))
	}
	// space marks, esc clears.
	if _, _ = m.handleKey(keyMsg(" ")); !m.benchMarkSet {
		t.Fatal("space did not mark")
	}
	if _, _ = m.handleKey(keyMsg("esc")); m.benchMarkSet {
		t.Fatal("esc did not clear the mark")
	}
	// 7 jumps to bench from another tab.
	m.setTab(tabDashboard)
	if _, _ = m.handleKey(keyMsg("7")); m.tab != tabBench {
		t.Fatalf("tab = %v, want bench", m.tab)
	}
}
