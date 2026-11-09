package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"wool/internal/llamaserver"
	"wool/internal/measure"
)

func TestParseFitArgsSavePreset(t *testing.T) {
	o, err := parseFitArgs([]string{
		"my-target", "--vram", "7G", "--save-preset", "fit-7g", "--force",
		"--json", "--cache-type=q8_0", "--gpu-layers=32", "--n-cpu-moe", "4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.target != "my-target" || o.vram != "7G" || o.savePreset != "fit-7g" || !o.force || !o.asJSON {
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
