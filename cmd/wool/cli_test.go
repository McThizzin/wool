package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wool/internal/bench"
	"wool/internal/config"
	"wool/internal/daemon"
	"wool/internal/fit"
	"wool/internal/gguf"
	"wool/internal/llamaserver"
	"wool/internal/measure"
)

func TestParseFitArgsSavePreset(t *testing.T) {
	o, err := parseFitArgs([]string{
		"my-target", "--vram", "7G", "--save-preset", "fit-7g", "--force",
		"--json", "--cache-type=q8_0", "--gpu-layers=32", "--n-cpu-moe", "4",
		"--calibrated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.target != "my-target" || o.vram != "7G" || o.savePreset != "fit-7g" || !o.force || !o.asJSON || !o.calibrated {
		t.Fatalf("options = %+v", o)
	}
	if o.cacheType != "q8_0" || o.gpuLayers == nil || *o.gpuLayers != 32 ||
		o.nCPUMoE == nil || *o.nCPUMoE != 4 {
		t.Fatalf("overrides = %+v", o)
	}
}

func TestParseFitArgsForceNeedsSavePreset(t *testing.T) {
	if _, err := parseFitArgs([]string{"target", "--force"}); err == nil {
		t.Fatal("expected --force without --save-preset to fail")
	}
}

func TestApplyFitCalibration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	store := measure.NewStoreAt(filepath.Join(dir, "wool", "measurements", "runs.jsonl"))
	shape := measure.Record{
		ModelID: "m", Ctx: 4096, CacheTypeK: "f16", CacheTypeV: "f16",
		GPULayers: 99, ModelSize: 8 << 30,
	}
	for i := 0; i < 4; i++ {
		r := shape
		r.BaselineKnown = true
		r.ObservedBytes = 9 << 30
		r.EstimateBytes = 8 << 30
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	res := fit.Result{
		ModelID: "m",
		Budget:  gguf.Budget{Known: true, Available: 10 << 30},
		Recommendation: fit.Recommendation{
			Ctx: 4096, CacheTypeK: "f16", CacheTypeV: "f16", GPULayers: 99,
			Estimate: gguf.Estimate{Total: 8 << 30},
		},
	}
	applyFitCalibration(&res, &config.Config{}, 8<<30, time.Time{})
	rec := res.Recommendation
	if rec.Calibration == nil || rec.Calibration.Samples != 4 {
		t.Fatalf("calibration = %+v", rec.Calibration)
	}
	if !rec.Calibrated || rec.Corrected != 9<<30 {
		t.Fatalf("corrected = %d calibrated=%v, want 9 GiB", rec.Corrected, rec.Calibrated)
	}
	if rec.CorrectedHeadroom != 1<<30 {
		t.Fatalf("corrected headroom = %d, want 1 GiB", rec.CorrectedHeadroom)
	}
	if rec.Estimate.Total != 8<<30 {
		t.Fatalf("raw estimate changed to %d", rec.Estimate.Total)
	}
}

func TestApplyFitCalibrationNeedsThreeSamples(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	store := measure.NewStoreAt(filepath.Join(dir, "wool", "measurements", "runs.jsonl"))
	shape := measure.Record{ModelID: "m", Ctx: 4096, ModelSize: 8 << 30}
	for i := 0; i < 2; i++ {
		r := shape
		r.BaselineKnown = true
		r.ObservedBytes = 9 << 30
		r.EstimateBytes = 8 << 30
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	res := fit.Result{
		ModelID: "m",
		Budget:  gguf.Budget{Known: true, Available: 10 << 30},
		Recommendation: fit.Recommendation{
			Ctx: 4096, Estimate: gguf.Estimate{Total: 8 << 30},
		},
	}
	applyFitCalibration(&res, &config.Config{}, 8<<30, time.Time{})
	rec := res.Recommendation
	if rec.Calibration == nil || rec.Calibration.Samples != 2 {
		t.Fatalf("calibration = %+v", rec.Calibration)
	}
	if rec.Calibrated || rec.Corrected != 0 {
		t.Fatalf("two samples applied a correction: %+v", rec)
	}
}

func TestFitCalibrationText(t *testing.T) {
	below := &measure.Calibration{Samples: 2, ObservedBytes: 9 << 30}
	got := fitCalibrationText(8<<30, below)
	for _, want := range []string{"estimate 8.0 GiB", "observed 9.0 GiB", "n=2", "not calibrated"} {
		if !strings.Contains(got, want) {
			t.Fatalf("below-threshold text missing %q: %q", want, got)
		}
	}
	ok := &measure.Calibration{Samples: 5, Ratio: 1.125, Spread: 0.08, ObservedBytes: 9 << 30}
	got = fitCalibrationText(8<<30, ok)
	for _, want := range []string{"calibrated:", "estimate 8.0 GiB", "corrected 9.0 GiB", "observed 9.0 GiB", "n=5", "±8%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("calibrated text missing %q: %q", want, got)
		}
	}
	ok.Clamped = true
	if got := fitCalibrationText(8<<30, ok); !strings.Contains(got, "clamped") {
		t.Fatalf("clamped text = %q", got)
	}
}

func TestParseBenchArgsDefaults(t *testing.T) {
	o, err := parseBenchArgs([]string{"preset-a", "--json", "--no-warmup"})
	if err != nil {
		t.Fatal(err)
	}
	if o.target != "preset-a" || o.runs != 3 || o.pp != 512 || o.tg != 128 ||
		o.engine != "server" || !o.asJSON || !o.noWarmup {
		t.Fatalf("options = %+v", o)
	}
}

func TestParseBenchArgsURL(t *testing.T) {
	o, err := parseBenchArgs([]string{
		"--url=http://127.0.0.1:8080", "--model", "qwen", "--runs=5", "--pp=0", "--tg=64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.url != "http://127.0.0.1:8080" || o.model != "qwen" || o.runs != 5 || o.pp != 0 || o.tg != 64 {
		t.Fatalf("options = %+v", o)
	}
}

func TestParseBenchArgsRejects(t *testing.T) {
	cases := [][]string{
		{},
		{"target", "--url", "http://127.0.0.1:8080", "--model", "m"},
		{"--url", "http://127.0.0.1:8080"},
		{"--url", "http://127.0.0.1:8080", "--model", "m", "--engine", "llama-bench"},
		{"--url", "127.0.0.1:8080", "--model", "m"},
		{"target", "--model", "m"},
		{"target", "--engine", "other"},
		{"target", "--runs", "0"},
		{"target", "--pp", "-1"},
		{"target", "--tg", "0"},
		{"target", "extra"},
		{"target", "--unknown"},
	}
	for _, args := range cases {
		if _, err := parseBenchArgs(args); err == nil {
			t.Errorf("parseBenchArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestCmdBenchURLWritesBenchRecord(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	var got llamaserver.CompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/completion":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode request: %v", err)
			}
			io.WriteString(w, `{
				"content":"ok",
				"tokens_evaluated":16,
				"tokens_predicted":8,
				"timings":{
					"prompt_n":16,"prompt_ms":8,"prompt_per_second":2000,
					"predicted_n":8,"predicted_ms":4,"predicted_per_second":2000
				}
			}`)
		case "/metrics":
			io.WriteString(w, "")
		case "/props":
			io.WriteString(w, `{"build_info":"b-test"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := `[app]
llama_server = "/nonexistent/llama-server"

[models.m]
path = "/nonexistent/model.gguf"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { os.Stdout = old }()
	os.Stdout = w
	code := cmdBench(cfgPath, []string{
		"--url", srv.URL, "--model", "m", "--runs", "1", "--no-warmup", "--json",
	})
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}

	var rec measure.Record
	if err := json.Unmarshal(out, &rec); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out)
	}
	if rec.Kind != measure.KindBench || rec.Target != "m" || rec.TargetKind != "url" {
		t.Fatalf("record identity = %+v", rec)
	}
	if rec.ModelID != "m" || rec.LlamaServerVersion != "b-test" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Bench == nil || rec.Bench.Engine != "server" || !rec.Bench.Attached {
		t.Fatalf("bench = %+v", rec.Bench)
	}
	if rec.Bench.Runs != 1 || len(rec.Bench.Samples) != 1 {
		t.Fatalf("runs = %+v", rec.Bench)
	}
	if rec.Bench.Median.PromptTPS != 2000 || rec.Bench.Median.GenerationTPS != 2000 {
		t.Fatalf("median = %+v", rec.Bench.Median)
	}
	if got.Temperature != 0 || got.Seed != 42 || got.CachePrompt {
		t.Fatalf("request = %+v", got)
	}

	data, err := os.ReadFile(filepath.Join(stateHome, "wool", "measurements", "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var stored measure.Record
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode store: %v\n%s", err, data)
	}
	if stored.Kind != measure.KindBench || stored.Bench == nil || len(stored.Bench.Samples) != 1 {
		t.Fatalf("stored record = %+v", stored)
	}
}

func TestParseBenchHistoryArgs(t *testing.T) {
	o, err := parseBenchHistoryArgs([]string{"preset-a", "--limit", "5", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if o.target != "preset-a" || o.limit != 5 || !o.asJSON {
		t.Fatalf("options = %+v", o)
	}
	o, err = parseBenchHistoryArgs([]string{"--limit=3"})
	if err != nil || o.limit != 3 || o.target != "" || o.asJSON {
		t.Fatalf("options = %+v, err = %v", o, err)
	}
	if _, err := parseBenchHistoryArgs([]string{"--limit", "0"}); err == nil {
		t.Fatal("expected a non-positive limit to fail")
	}
	if _, err := parseBenchHistoryArgs([]string{"a", "b"}); err == nil {
		t.Fatal("expected two targets to fail")
	}
	if _, err := parseBenchHistoryArgs([]string{"--bogus"}); err == nil {
		t.Fatal("expected an unknown option to fail")
	}
}

func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	code := fn()
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestCmdBenchHistoryJSONAndTable(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	store := measure.NewStore()
	add := func(at int64, target, model string, prompt, gen float64, vram uint64) {
		t.Helper()
		r := measure.Record{
			Version: measure.Version, Kind: measure.KindBench,
			At: time.Unix(at, 0), Target: target, TargetKind: "preset", ModelID: model,
			ConfigHash: "abcdef123456",
			Bench: &measure.Bench{
				Engine: "server", LoadKnown: true, LoadMS: 2100,
				VRAMKnown: true, VRAMDeltaBytes: vram,
				Median: measure.BenchStats{PromptTPS: prompt, GenerationTPS: gen, TotalMS: 1000},
			},
		}
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	add(100, "preset-a", "model-x", 200, 40, 4<<30)
	add(200, "preset-b", "model-y", 220, 44, 5<<30)

	out, code := captureStdout(t, func() int {
		return cmdBench("", []string{"history", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var records []measure.Record
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if len(records) != 2 || records[0].Target != "preset-a" || records[1].Target != "preset-b" {
		t.Fatalf("records = %+v", records)
	}

	out, code = captureStdout(t, func() int {
		return cmdBench("", []string{"history"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	for _, want := range []string{"WHEN", "preset-a", "preset-b", "40.0", "44.0", "4.0 GiB", "2.1s", "abcdef123456"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}

	out, code = captureStdout(t, func() int {
		return cmdBench("", []string{"history", "preset-a"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	if !strings.Contains(out, "preset-a") || strings.Contains(out, "preset-b") {
		t.Fatalf("filter leaked another target:\n%s", out)
	}

	out, code = captureStdout(t, func() int {
		return cmdBench("", []string{"history", "missing"})
	})
	if code != 0 || !strings.Contains(out, "no benchmarks recorded for missing") {
		t.Fatalf("empty history = %q (exit %d)", out, code)
	}
}

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
