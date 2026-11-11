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
		// Three runs with a small, deterministic wobble so the comparison has
		// raw runs and a non-zero spread without tripping the noise warning.
		samples := make([]measure.BenchRun, 3)
		for i := range samples {
			offset := float64(i-1) * 0.5
			samples[i] = measure.BenchRun{
				Index:            i + 1,
				PromptTokens:     512,
				GenerationTokens: 128,
				PromptTPS:        prompt,
				GenerationTPS:    gen + offset,
				TotalMS:          1000,
			}
		}
		r := measure.Record{
			Version: measure.Version, Kind: measure.KindBench,
			At: time.Unix(at, 0), Target: target, TargetKind: "preset", ModelID: model,
			Ctx: ctx, CacheTypeK: cache, CacheTypeV: cache, GPULayers: 999,
			ConfigHash: hash,
			Bench: &measure.Bench{
				Engine: "server", LoadKnown: true, LoadMS: loadMS,
				VRAMKnown: true, VRAMDeltaBytes: vram,
				Samples: samples,
				Median:  measure.BenchStats{PromptTPS: prompt, GenerationTPS: gen, TotalMS: 1000},
				Min:     measure.BenchStats{PromptTPS: prompt, GenerationTPS: gen - 0.5, TotalMS: 1000},
				Max:     measure.BenchStats{PromptTPS: prompt, GenerationTPS: gen + 0.5, TotalMS: 1000},
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
		"raw runs (gen t/s)", "39.5  40.0  40.5", "43.5  44.0  44.5",
		"warnings", "none",
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

func TestBenchCompareCommandSlash(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabDashboard)

	m.runCommand("/bench compare gemma-heretic-32k gemma-heretic-65k")
	if m.tab != tabBench {
		t.Fatalf("tab = %v, want bench", m.tab)
	}
	if !strings.Contains(m.overlayTitle, "bench compare") {
		t.Fatalf("compare title = %q (toast %q)", m.overlayTitle, m.toast)
	}
	if !strings.Contains(m.overlay, "raw runs (gen t/s)") {
		t.Fatalf("compare missing raw runs:\n%s", m.overlay)
	}
}

func TestBenchCompareCommandUsesNewestRun(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabDashboard)

	m.benchCompareCommand([]string{"gemma-heretic-32k", "gemma-heretic-65k"})
	if m.tab != tabBench {
		t.Fatalf("tab = %v, want bench", m.tab)
	}
	if !strings.Contains(m.overlayTitle, "bench compare") {
		t.Fatalf("compare title = %q (toast %q)", m.overlayTitle, m.toast)
	}
	if !m.benchMarkSet || !m.benchMarkAt.Equal(time.Unix(100, 0)) {
		t.Fatalf("mark = %v set=%v", m.benchMarkAt, m.benchMarkSet)
	}
	if m.benchSel != 1 {
		t.Fatalf("selection = %d, want newest (1)", m.benchSel)
	}
	if !strings.Contains(m.overlay, "gemma-heretic-65k") {
		t.Fatalf("compare missing target:\n%s", m.overlay)
	}
}

func TestBenchCompareCommandMissingTarget(t *testing.T) {
	m := testModel(t)
	seedBench(t, m)
	m.setTab(tabDashboard)

	m.benchCompareCommand([]string{"gemma-heretic-32k", "nope"})
	if m.overlay != "" {
		t.Fatalf("compare opened an overlay: %q", m.overlay)
	}
	if !strings.Contains(m.toast, "no runs for nope") {
		t.Fatalf("toast = %q", m.toast)
	}
	m.benchCompareCommand([]string{"gemma-heretic-32k"})
	if !strings.Contains(m.toast, "want two targets") {
		t.Fatalf("toast = %q", m.toast)
	}
}

func TestBenchCompareWarnsOnNoise(t *testing.T) {
	m := testModel(t)
	r := measure.Record{
		Version: measure.Version, Kind: measure.KindBench,
		At: time.Unix(100, 0), Target: "noisy", TargetKind: "preset", ModelID: "model",
		Bench: &measure.Bench{
			Engine: "server",
			Samples: []measure.BenchRun{
				{Index: 1, PromptTPS: 200, GenerationTPS: 30, TotalMS: 1000},
				{Index: 2, PromptTPS: 200, GenerationTPS: 40, TotalMS: 1000},
				{Index: 3, PromptTPS: 200, GenerationTPS: 50, TotalMS: 1000},
			},
		},
	}
	other := measure.Record{
		Version: measure.Version, Kind: measure.KindBench,
		At: time.Unix(200, 0), Target: "steady", TargetKind: "preset", ModelID: "model",
		Bench: &measure.Bench{
			Engine: "server",
			Samples: []measure.BenchRun{
				{Index: 1, PromptTPS: 220, GenerationTPS: 44, TotalMS: 1000},
				{Index: 2, PromptTPS: 220, GenerationTPS: 44, TotalMS: 1000},
				{Index: 3, PromptTPS: 220, GenerationTPS: 44, TotalMS: 1000},
			},
		},
	}
	m.showBenchCompareRecords(r, other)
	if !strings.Contains(m.overlay, "generation t/s spread on A") {
		t.Fatalf("compare missing noise warning:\n%s", m.overlay)
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
	// 8 jumps to bench from another tab.
	m.setTab(tabDashboard)
	if _, _ = m.handleKey(keyMsg("8")); m.tab != tabBench {
		t.Fatalf("tab = %v, want bench", m.tab)
	}
}
