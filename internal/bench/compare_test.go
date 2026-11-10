package bench

import (
	"strings"
	"testing"

	"wool/internal/measure"
)

func sampleRuns(prompt, gen []float64) []measure.BenchRun {
	n := len(prompt)
	if len(gen) > n {
		n = len(gen)
	}
	out := make([]measure.BenchRun, n)
	for i := 0; i < n; i++ {
		out[i] = measure.BenchRun{Index: i + 1}
		if i < len(prompt) {
			out[i].PromptTPS = prompt[i]
		}
		if i < len(gen) {
			out[i].GenerationTPS = gen[i]
		}
	}
	return out
}

func TestNewSideSummarizesAndSpreads(t *testing.T) {
	side := NewSide(sampleRuns(
		[]float64{100, 200, 300},
		[]float64{40, 44, 48},
	))
	if side.Median.PromptTPS != 200 || side.Min.PromptTPS != 100 || side.Max.PromptTPS != 300 {
		t.Fatalf("prompt summary = %+v / %+v / %+v", side.Min, side.Median, side.Max)
	}
	if side.Median.GenerationTPS != 44 {
		t.Fatalf("gen median = %v", side.Median.GenerationTPS)
	}
	// (300-100)/200 = 100%, (48-40)/44 = 18.18%.
	if got := side.Spread.PromptTPS; got < 0.99 || got > 1.01 {
		t.Fatalf("prompt spread = %v", got)
	}
	if got := side.Spread.GenerationTPS; got < 0.18 || got > 0.19 {
		t.Fatalf("gen spread = %v", got)
	}
}

func TestSideFromRecordFallsBackToStoredSummary(t *testing.T) {
	r := measure.Record{
		ModelID:    "model",
		Target:     "preset",
		TargetKind: "preset",
		ConfigHash: "abc",
		Bench: &measure.Bench{
			Median: measure.BenchStats{PromptTPS: 200, GenerationTPS: 40},
			Min:    measure.BenchStats{PromptTPS: 190, GenerationTPS: 38},
			Max:    measure.BenchStats{PromptTPS: 210, GenerationTPS: 42},
		},
	}
	side := SideFromRecord(r)
	if side.Target != "preset" || side.ModelID != "model" || side.ConfigHash != "abc" {
		t.Fatalf("side identity = %+v", side)
	}
	if side.Median.GenerationTPS != 40 || side.Spread.GenerationTPS != 0.1 {
		t.Fatalf("summary = %+v spread = %+v", side.Median, side.Spread)
	}
}

func TestComparisonWarningsNoise(t *testing.T) {
	cmp := Comparison{
		A: NewSide(sampleRuns([]float64{200, 200, 200}, []float64{30, 40, 50})),
		B: NewSide(sampleRuns([]float64{200, 200, 200}, []float64{44, 44, 44})),
	}
	cmp.BuildWarnings()
	if len(cmp.Warnings) != 1 {
		t.Fatalf("warnings = %v", cmp.Warnings)
	}
	if !strings.Contains(cmp.Warnings[0], "generation t/s spread on A") ||
		!strings.Contains(cmp.Warnings[0], "50.0%") {
		t.Fatalf("warning = %q", cmp.Warnings[0])
	}
}

func TestComparisonWarningsEnvironment(t *testing.T) {
	cmp := Comparison{
		A: Side{GPUName: "GPU A", GPUPCIID: "1002:73ff", GPUDriver: "6.0", LlamaServerVersion: "b1"},
		B: Side{GPUName: "GPU B", GPUPCIID: "1002:744c", GPUDriver: "6.1", LlamaServerVersion: "b2"},
	}
	cmp.BuildWarnings()
	joined := strings.Join(cmp.Warnings, "\n")
	for _, want := range []string{"GPU changed between runs", "GPU driver changed", "llama-server version changed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings missing %q:\n%s", want, joined)
		}
	}
}

func TestComparisonWarningsClean(t *testing.T) {
	cmp := Comparison{
		A: NewSide(sampleRuns([]float64{200, 201, 202}, []float64{40, 40.1, 40.2})),
		B: NewSide(sampleRuns([]float64{220, 221, 222}, []float64{44, 44.1, 44.2})),
	}
	cmp.A.GPUPCIID, cmp.B.GPUPCIID = "1002:73ff", "1002:73ff"
	cmp.A.LlamaServerVersion, cmp.B.LlamaServerVersion = "b1", "b1"
	cmp.BuildWarnings()
	if len(cmp.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", cmp.Warnings)
	}
}

func TestDeltaAndMedian(t *testing.T) {
	if got := Delta(40, 44); got < 9.99 || got > 10.01 {
		t.Fatalf("delta = %v", got)
	}
	if got := Delta(0, 44); got != 0 {
		t.Fatalf("zero-base delta = %v", got)
	}
	if got := Median([]float64{3, 1, 2}); got != 2 {
		t.Fatalf("median = %v", got)
	}
	if got := Median(nil); got != 0 {
		t.Fatalf("empty median = %v", got)
	}
}
