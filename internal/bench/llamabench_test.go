package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLlamaBenchCommandArgs(t *testing.T) {
	args := LlamaBenchOptions{
		ModelPath:        "/models/m.gguf",
		Runs:             2,
		PromptTokens:     256,
		GenerationTokens: 64,
		Threads:          8,
		GPULayers:        32,
		NCPUMoE:          4,
		BatchSize:        2048,
		UBatchSize:       512,
		CacheTypeK:       "q8_0",
		CacheTypeV:       "q8_0",
		FlashAttn:        "on",
		NoWarmup:         true,
	}.CommandArgs()
	for _, want := range [][]string{
		{"-m", "/models/m.gguf"},
		{"-p", "256"},
		{"-n", "64"},
		{"-r", "2"},
		{"-o", "json"},
		{"-t", "8"},
		{"-ngl", "32"},
		{"-ncmoe", "4"},
		{"-b", "2048"},
		{"-ub", "512"},
		{"-ctk", "q8_0"},
		{"-ctv", "q8_0"},
		{"-fa", "on"},
		{"--no-warmup"},
	} {
		if len(want) == 1 {
			if !hasFlag(args, want[0]) {
				t.Errorf("missing %v in %v", want, args)
			}
			continue
		}
		if !hasPair(args, want[0], want[1]) {
			t.Errorf("missing %v in %v", want, args)
		}
	}

	// 999 is wool's "all layers" sentinel; llama-bench defaults to -1 already.
	args = LlamaBenchOptions{ModelPath: "m", GPULayers: 999}.CommandArgs()
	if hasPair(args, "-ngl", "999") {
		t.Fatalf("all-layers sentinel should be omitted: %v", args)
	}
}

func hasPair(args []string, name, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func TestParseLlamaBenchJSON(t *testing.T) {
	data := []byte(`[
		{"build_commit":"abc123","build_number":42,"n_prompt":256,"n_gen":0,
		 "avg_ns":1000000,"avg_ts":250,"samples_ns":[900000,1000000,1100000],"samples_ts":[260,250,240]},
		{"build_commit":"abc123","build_number":42,"n_prompt":0,"n_gen":64,
		 "avg_ns":2000000,"avg_ts":32,"samples_ns":[1900000,2000000,2100000],"samples_ts":[33,32,31]}
	]`)
	entries, err := parseLlamaBenchJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	runs, version, err := llamaBenchRuns(entries, LlamaBenchOptions{PromptTokens: 256, GenerationTokens: 64})
	if err != nil {
		t.Fatal(err)
	}
	if version != "llama-bench abc123 (build 42)" {
		t.Fatalf("version = %q", version)
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %d", len(runs))
	}
	first := runs[0]
	if first.Source != "llama-bench" || first.PromptTokens != 256 || first.GenerationTokens != 64 {
		t.Fatalf("first run = %+v", first)
	}
	if first.PromptTPS != 260 || first.GenerationTPS != 33 {
		t.Fatalf("first rates = %+v", first)
	}
	if first.PromptMS != 0.9 || first.GenerationMS != 1.9 || first.TotalMS != 2.8 {
		t.Fatalf("first times = %+v", first)
	}
	median, min, max := Summarize(runs)
	if median.PromptTPS != 250 || min.PromptTPS != 240 || max.PromptTPS != 260 {
		t.Fatalf("prompt stats = %+v %+v %+v", median, min, max)
	}
	if median.GenerationTPS != 32 || min.GenerationTPS != 31 || max.GenerationTPS != 33 {
		t.Fatalf("generation stats = %+v %+v %+v", median, min, max)
	}
}

func TestParseLlamaBenchJSONRejectsEmpty(t *testing.T) {
	if _, err := parseLlamaBenchJSON([]byte(`[]`)); err == nil {
		t.Fatal("expected empty output to fail")
	}
	if _, err := parseLlamaBenchJSON([]byte(`not json`)); err == nil {
		t.Fatal("expected invalid output to fail")
	}
}

func TestRunLlamaBenchExecutesAndParses(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "llama-bench")
	script := `#!/bin/sh
cat <<'EOF'
[{"build_commit":"deadbeef","build_number":7,"n_prompt":16,"n_gen":0,"avg_ns":1000000,"avg_ts":100,"samples_ns":[1000000],"samples_ts":[100]},
 {"build_commit":"deadbeef","build_number":7,"n_prompt":0,"n_gen":8,"avg_ns":2000000,"avg_ts":20,"samples_ns":[2000000],"samples_ts":[20]}]
EOF
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := RunLlamaBench(context.Background(), LlamaBenchOptions{
		Bin:              bin,
		ModelPath:        "m.gguf",
		Runs:             1,
		PromptTokens:     16,
		GenerationTokens: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Runs) != 1 || res.Runs[0].PromptTPS != 100 || res.Runs[0].GenerationTPS != 20 {
		t.Fatalf("result = %+v", res)
	}
	if res.Version != "llama-bench deadbeef (build 7)" {
		t.Fatalf("version = %q", res.Version)
	}
	if !hasPair(res.Args, "-m", "m.gguf") || !hasPair(res.Args, "-o", "json") {
		t.Fatalf("args = %v", res.Args)
	}
}

func TestRunLlamaBenchReportsStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "llama-bench")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'no such model' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := RunLlamaBench(context.Background(), LlamaBenchOptions{Bin: bin, ModelPath: "missing.gguf"})
	if err == nil || !strings.Contains(err.Error(), "no such model") {
		t.Fatalf("err = %v", err)
	}
}

func TestLlamaBenchRunsRequiresTests(t *testing.T) {
	entries := []llamaBenchEntry{{NPrompt: 16, NGen: 0, AvgTS: 100}}
	if _, _, err := llamaBenchRuns(entries, LlamaBenchOptions{PromptTokens: 16, GenerationTokens: 8}); err == nil {
		t.Fatal("expected missing generation test to fail")
	}
	if _, _, err := llamaBenchRuns(nil, LlamaBenchOptions{PromptTokens: 16, GenerationTokens: 8}); err == nil {
		t.Fatal("expected empty entries to fail")
	}
}
