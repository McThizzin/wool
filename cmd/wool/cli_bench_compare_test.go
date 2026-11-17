package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wool/internal/bench"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/measure"
)

func TestParseBenchCompareArgs(t *testing.T) {
	o, names, err := parseBenchCompareArgs([]string{
		"a", "b", "--runs", "4", "--pp=256", "--tg", "64", "--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("targets = %v", names)
	}
	if o.runs != 4 || o.pp != 256 || o.tg != 64 || !o.asJSON {
		t.Fatalf("options = %+v", o)
	}

	bad := [][]string{
		{"a"},
		{"a", "b", "c"},
		{"a", "a"},
		{"a", "b", "--url", "http://127.0.0.1:8080"},
		{"a", "b", "--model", "m"},
		{"a", "b", "--bogus"},
	}
	for _, args := range bad {
		if _, _, err := parseBenchCompareArgs(args); err == nil {
			t.Fatalf("%v: expected an error", args)
		}
	}
}

func TestInterleavedOrder(t *testing.T) {
	got := interleavedOrder(3)
	want := []int{0, 1, 1, 0, 0, 1}
	if len(got) != len(want) {
		t.Fatalf("order = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestPrintBenchCompare(t *testing.T) {
	cmp := bench.Comparison{
		Version:          bench.ComparisonVersion,
		Runs:             3,
		PromptTokens:     512,
		GenerationTokens: 128,
		Seed:             42,
		Interleaved:      true,
		A:                bench.NewSide(compareRuns(200, 40, 3)),
		B:                bench.NewSide(compareRuns(220, 44, 3)),
	}
	cmp.A.Target, cmp.A.ModelID, cmp.A.Engine = "preset-a", "model", bench.EngineServer
	cmp.B.Target, cmp.B.ModelID, cmp.B.Engine = "preset-b", "model", bench.EngineServer
	cmp.A.ConfigHash, cmp.B.ConfigHash = "aaaa1111", "bbbb2222"
	cmp.A.LlamaServerVersion, cmp.B.LlamaServerVersion = "b-test", "b-test"
	cmp.A.LoadKnown, cmp.A.LoadMS = true, 2100
	cmp.B.LoadKnown, cmp.B.LoadMS = true, 2300
	cmp.A.VRAMKnown, cmp.A.VRAMDeltaBytes = true, 4<<30
	cmp.B.VRAMKnown, cmp.B.VRAMDeltaBytes = true, 5<<30
	cmp.BuildWarnings()

	out, code := captureStdout(t, func() int {
		printBenchCompare(cmp, "test.jsonl")
		return 0
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		"A/B comparison", "3 rounds per target", "interleaved",
		"FIELD", "preset-a", "preset-b", "aaaa1111", "bbbb2222",
		"METRIC", "A MEDIAN", "A SPREAD", "generation tok/s",
		"+10.0%", "raw runs", "40.0", "44.0",
		"4.0 GiB", "5.0 GiB", "2.1s", "2.3s",
		"no noise or environment warnings", "stored: test.jsonl",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func compareRuns(prompt, gen float64, n int) []measure.BenchRun {
	out := make([]measure.BenchRun, n)
	for i := range out {
		out[i] = measure.BenchRun{Index: i + 1, PromptTPS: prompt, GenerationTPS: gen, TotalMS: 1000}
	}
	return out
}

func TestCmdBenchCompareInterleavesAndStores(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := `[app]
llama_server = "/nonexistent/llama-server"

[models.m]
path = "` + modelPath + `"

[presets.a]
model = "m"
ctx = 32768

[presets.b]
model = "m"
ctx = 65536
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var order []string
	orig := runCompareRound
	defer func() { runCompareRound = orig }()
	runCompareRound = func(ctx context.Context, cfg *config.Config, target compareTarget, opts benchOptions, serverBin string, mgr *daemon.Manager, lbBin string) (benchRound, error) {
		order = append(order, target.name)
		gen := 40.0
		if target.name == "b" {
			gen = 44.0
		}
		return benchRound{
			run: measure.BenchRun{
				Index: 1, PromptTokens: opts.pp, GenerationTokens: opts.tg,
				PromptTPS: 200, GenerationTPS: gen, TotalMS: 1000,
			},
			loadMS: 2000, loadKnown: true,
			version: "b-test", configHash: "hash-" + target.name,
			baseline: 1 << 30, baselineKnown: true,
			peak: 5 << 30, peakKnown: true, delta: 4 << 30, deltaKnown: true,
		}, nil
	}

	out, code := captureStdout(t, func() int {
		return cmdBenchCompare(cfgPath, []string{"a", "b", "--runs", "2", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	if got := strings.Join(order, ","); got != "a,b,b,a" {
		t.Fatalf("round order = %s", got)
	}

	var cmp bench.Comparison
	if err := json.Unmarshal([]byte(out), &cmp); err != nil {
		t.Fatalf("decode comparison: %v\n%s", err, out)
	}
	if cmp.Version != bench.ComparisonVersion || !cmp.Interleaved || cmp.Runs != 2 {
		t.Fatalf("comparison header = %+v", cmp)
	}
	if cmp.A.Target != "a" || cmp.B.Target != "b" || cmp.A.ModelID != "m" {
		t.Fatalf("sides = %+v / %+v", cmp.A, cmp.B)
	}
	if len(cmp.A.Samples) != 2 || len(cmp.B.Samples) != 2 {
		t.Fatalf("samples = %d / %d", len(cmp.A.Samples), len(cmp.B.Samples))
	}
	if cmp.A.Spread.GenerationTPS != 0 || cmp.B.Spread.GenerationTPS != 0 {
		t.Fatalf("spread = %+v / %+v", cmp.A.Spread, cmp.B.Spread)
	}

	records, err := measure.NewStore().BenchHistory("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("stored records = %d", len(records))
	}
	if records[0].Target != "a" || records[1].Target != "b" {
		t.Fatalf("stored targets = %q, %q", records[0].Target, records[1].Target)
	}
	if len(records[0].Bench.Samples) != 2 {
		t.Fatalf("stored samples = %d", len(records[0].Bench.Samples))
	}
}
