package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"wool/internal/bench"
	"wool/internal/measure"
	"wool/internal/ui"
)

// loadBenchHistory reloads the benchmark store and keeps the current selection
// when it is still visible. The list is oldest first, so the default selection
// is the newest run.
func (m *Model) loadBenchHistory() {
	if m.meas == nil {
		return
	}
	// The store is capped at a few thousand records, but re-parsing it on
	// every poll tick is still wasteful. Reload only when the file changes.
	stamp := "missing"
	if info, err := os.Stat(m.meas.Path); err == nil {
		stamp = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	} else if !errors.Is(err, os.ErrNotExist) {
		m.benchErr = err.Error()
		return
	}
	if !m.benchLoaded || stamp != m.benchStamp {
		all, err := m.meas.BenchHistory("", 0)
		if err != nil {
			m.benchErr = err.Error()
			return
		}
		m.benchAll = all
		m.benchStamp = stamp
		m.benchLoaded = true
	}
	m.benchErr = ""
	m.benchTargets = measure.BenchTargets(m.benchAll)

	prev, hadPrev := m.selectedBenchAt()
	runs := measure.FilterBench(m.benchAll, m.benchFilter)
	m.benchRuns = runs

	idx := -1
	if hadPrev {
		for i, r := range runs {
			if r.At.Equal(prev) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		idx = len(runs) - 1
	}
	m.benchSel = clamp(idx, 0, maxInt(0, len(runs)-1))

	// A mark is scoped to the visible filter. Hiding it would leave the
	// compare action pointing at a row the user cannot see.
	if m.benchMarkSet {
		found := false
		for _, r := range runs {
			if r.At.Equal(m.benchMarkAt) {
				found = true
				break
			}
		}
		if !found {
			m.benchMarkSet = false
		}
	}
}

func (m *Model) selectedBench() (measure.Record, bool) {
	if m.benchSel >= 0 && m.benchSel < len(m.benchRuns) {
		return m.benchRuns[m.benchSel], true
	}
	return measure.Record{}, false
}

func (m *Model) selectedBenchAt() (time.Time, bool) {
	r, ok := m.selectedBench()
	return r.At, ok
}

func (m *Model) markedBench() (measure.Record, bool) {
	if !m.benchMarkSet {
		return measure.Record{}, false
	}
	for _, r := range m.benchRuns {
		if r.At.Equal(m.benchMarkAt) {
			return r, true
		}
	}
	return measure.Record{}, false
}

// benchMarkAction marks or unmarks the selected run. A mark is the first half
// of the two-run comparison.
func (m *Model) benchMarkAction() {
	r, ok := m.selectedBench()
	if !ok {
		m.setToast("nothing selected to mark")
		return
	}
	if m.benchMarkSet && m.benchMarkAt.Equal(r.At) {
		m.benchMarkSet = false
		m.setToast("mark cleared")
		return
	}
	m.benchMarkAt, m.benchMarkSet = r.At, true
	m.setToast("marked " + benchRunLabel(r) + " · enter compares")
}

// clearBenchMark drops a mark without changing the selection.
func (m *Model) clearBenchMark() bool {
	if !m.benchMarkSet {
		return false
	}
	m.benchMarkSet = false
	m.setToast("mark cleared")
	return true
}

// cycleBenchFilter moves through "all targets" and every target in the store.
func (m *Model) cycleBenchFilter() {
	options := append([]string{""}, m.benchTargets...)
	idx := -1
	for i, t := range options {
		if t == m.benchFilter {
			idx = i
			break
		}
	}
	m.benchFilter = options[(idx+1)%len(options)]
	m.benchSel = -1
	m.loadBenchHistory()
	if m.benchFilter == "" {
		m.setToast("bench filter: all targets")
	} else {
		m.setToast("bench filter: " + m.benchFilter)
	}
}

// showBenchCompare renders the marked and selected runs side by side, reusing
// the two-column field layout of the M1.4 config diff.
func (m *Model) showBenchCompare() {
	base, ok := m.markedBench()
	if !ok {
		m.setToast("mark a run with space first")
		return
	}
	next, ok := m.selectedBench()
	if !ok {
		m.setToast("nothing selected to compare")
		return
	}
	if base.At.Equal(next.At) {
		m.setToast("pick a different run to compare")
		return
	}
	m.showBenchCompareRecords(base, next)
}

// showBenchCompareRecords is the shared comparison presentation. It is used by
// the mark/enter flow and by /bench compare A B.
func (m *Model) showBenchCompareRecords(base, next measure.Record) {
	a := bench.SideFromRecord(base)
	b := bench.SideFromRecord(next)
	cmp := bench.Comparison{Version: bench.ComparisonVersion, A: a, B: b}
	cmp.BuildWarnings()

	lines := []string{
		"marked   " + benchRunLabel(base),
		"selected " + benchRunLabel(next),
		"",
		"config changes",
	}
	changes := 0
	field := func(name, a, b string) {
		if a == b {
			return
		}
		changes++
		lines = append(lines, fmt.Sprintf("  %-14s %-18s →  %s", name, a, b))
	}
	field("target", measure.BenchTargetName(base), measure.BenchTargetName(next))
	field("model", fallbackDash(base.ModelID), fallbackDash(next.ModelID))
	field("ctx", fmt.Sprint(base.Ctx), fmt.Sprint(next.Ctx))
	field("cache k", fallbackDash(base.CacheTypeK), fallbackDash(next.CacheTypeK))
	field("cache v", fallbackDash(base.CacheTypeV), fallbackDash(next.CacheTypeV))
	field("gpu layers", fmt.Sprint(base.GPULayers), fmt.Sprint(next.GPULayers))
	field("n_cpu_moe", fmt.Sprint(base.NCPUMoE), fmt.Sprint(next.NCPUMoE))
	field("engine", benchEngine(base), benchEngine(next))
	field("config hash", fallbackDash(base.ConfigHash), fallbackDash(next.ConfigHash))
	if changes == 0 {
		lines = append(lines, "  launch options and config hash are identical")
	}

	lines = append(lines, "", "results")
	lines = append(lines,
		fmt.Sprintf("  %-14s %-18s %-18s %s", "metric", "A", "B", "delta"),
		fmt.Sprintf("  %-14s %-18s %-18s %s", "prompt t/s",
			benchStat(a.Median.PromptTPS, a.Spread.PromptTPS),
			benchStat(b.Median.PromptTPS, b.Spread.PromptTPS),
			benchDelta(a.Median.PromptTPS, b.Median.PromptTPS)),
		fmt.Sprintf("  %-14s %-18s %-18s %s", "gen t/s",
			benchStat(a.Median.GenerationTPS, a.Spread.GenerationTPS),
			benchStat(b.Median.GenerationTPS, b.Spread.GenerationTPS),
			benchDelta(a.Median.GenerationTPS, b.Median.GenerationTPS)),
		fmt.Sprintf("  %-14s %-18s %-18s %s", "total",
			fmt.Sprintf("%.0f ms", a.Median.TotalMS),
			fmt.Sprintf("%.0f ms", b.Median.TotalMS),
			benchDelta(a.Median.TotalMS, b.Median.TotalMS)),
		fmt.Sprintf("  %-14s %-18s %-18s", "vram", benchVRAM(base.Bench), benchVRAM(next.Bench)),
		fmt.Sprintf("  %-14s %-18s %-18s", "load", benchLoad(base.Bench), benchLoad(next.Bench)),
	)

	if len(a.Samples) > 0 || len(b.Samples) > 0 {
		lines = append(lines, "", "raw runs (gen t/s)")
		if len(a.Samples) > 0 {
			lines = append(lines, "  A  "+sampleList(a.Samples))
		}
		if len(b.Samples) > 0 {
			lines = append(lines, "  B  "+sampleList(b.Samples))
		}
	}

	lines = append(lines, "", "warnings")
	if len(cmp.Warnings) == 0 {
		lines = append(lines, "  none")
	} else {
		for _, warning := range cmp.Warnings {
			lines = append(lines, "  - "+warning)
		}
	}

	m.overlayTitle = "bench compare · " + measure.BenchTargetName(next)
	m.overlayScroll = 0
	m.overlay = strings.Join(lines, "\n")
}

// benchCompareCommand opens a comparison for the newest recorded run of each
// named target.
func (m *Model) benchCompareCommand(names []string) {
	if len(names) != 2 {
		m.setToast("bench compare: want two targets")
		return
	}
	m.setTab(tabBench)
	a, ok := m.latestBench(names[0])
	if !ok {
		m.setToast("bench: no runs for " + names[0])
		return
	}
	b, ok := m.latestBench(names[1])
	if !ok {
		m.setToast("bench: no runs for " + names[1])
		return
	}
	m.benchFilter = ""
	m.loadBenchHistory()
	m.benchMarkAt, m.benchMarkSet = a.At, true
	for i, r := range m.benchRuns {
		if r.At.Equal(b.At) {
			m.benchSel = i
			break
		}
	}
	m.showBenchCompareRecords(a, b)
}

// latestBench returns the newest recorded run for a target name or model ID.
func (m *Model) latestBench(name string) (measure.Record, bool) {
	var out measure.Record
	found := false
	for _, r := range m.benchAll {
		if r.Kind != measure.KindBench {
			continue
		}
		if measure.BenchTargetName(r) == name || r.ModelID == name {
			out, found = r, true
		}
	}
	return out, found
}

// showBenchConfigDiff opens the M1.4 diff for the selected run's target when
// that preset or model still exists in the current config.
func (m *Model) showBenchConfigDiff() {
	r, ok := m.selectedBench()
	if !ok {
		m.setToast("nothing selected to diff")
		return
	}
	name := measure.BenchTargetName(r)
	if _, ok := m.cfg.Presets[name]; ok {
		m.showConfigDiffFor(name, "preset")
		return
	}
	if _, ok := m.cfg.Models[name]; ok {
		m.showConfigDiffFor(name, "model")
		return
	}
	if r.ModelID != "" {
		if _, ok := m.cfg.Models[r.ModelID]; ok {
			m.showConfigDiffFor(r.ModelID, "model")
			return
		}
	}
	m.setToast("bench: " + name + " is no longer in the config")
}

func benchRunLabel(r measure.Record) string {
	when := r.At.Local().Format("2006-01-02 15:04")
	label := when + " · " + measure.BenchTargetName(r)
	if r.Bench != nil {
		label += fmt.Sprintf(" · %.1f gen t/s", r.Bench.Median.GenerationTPS)
	}
	return label
}

func benchEngine(r measure.Record) string {
	if r.Bench == nil {
		return "-"
	}
	engine := fallbackDash(r.Bench.Engine)
	if r.Bench.Attached {
		engine += " attached"
	}
	return engine
}

func benchVRAM(b *measure.Bench) string {
	if b == nil {
		return "-"
	}
	switch {
	case b.VRAMKnown:
		return ui.HumanBytes(b.VRAMDeltaBytes)
	case b.PeakBytes > 0:
		return "~" + ui.HumanBytes(b.PeakBytes)
	default:
		return "-"
	}
}

func benchLoad(b *measure.Bench) string {
	if b == nil || !b.LoadKnown {
		return "-"
	}
	return fmt.Sprintf("%.1fs", b.LoadMS/1000)
}

func fallbackDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func benchDelta(base, next float64) string {
	if base <= 0 {
		return ""
	}
	return fmt.Sprintf("(%+.1f%%)", bench.Delta(base, next))
}

func benchStat(median, spread float64) string {
	return fmt.Sprintf("%.1f ±%.1f%%", median, spread*100)
}

func sampleList(samples []measure.BenchRun) string {
	parts := make([]string, len(samples))
	for i, run := range samples {
		parts[i] = fmt.Sprintf("%.1f", run.GenerationTPS)
	}
	return strings.Join(parts, "  ")
}
