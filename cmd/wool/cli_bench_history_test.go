package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wool/internal/measure"
)

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
