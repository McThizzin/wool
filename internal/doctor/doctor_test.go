package doctor

import (
	"strings"
	"testing"

	"wool/internal/config"
	"wool/internal/gguf"
)

func TestEstimateLine(t *testing.T) {
	const mib = 1 << 20
	e := gguf.Estimate{WeightsBytes: 2048 * mib, KVBytes: 512 * mib, Overhead: 512 * mib, Total: 3072 * mib}
	line := estimateLine(e, 1024*mib, "weights", true)
	for _, want := range []string{"weights 2.0 GiB", "kv 512.0 MiB", "overhead 512.0 MiB", "= 3.0 GiB", "limit weights", "headroom 1.0 GiB"} {
		if !strings.Contains(line, want) {
			t.Fatalf("estimate line missing %q: %q", want, line)
		}
	}
	over := estimateLine(e, -512*mib, "weights", true)
	if !strings.Contains(over, "over by 512.0 MiB") {
		t.Fatalf("over line = %q", over)
	}
	if got := estimateLine(e, 0, "weights", false); strings.Contains(got, "headroom") {
		t.Fatalf("unknown budget should omit headroom: %q", got)
	}
}

func TestFitNoteUsesBudget(t *testing.T) {
	const mib = 1 << 20
	est := gguf.Estimate{Total: 4096 * mib}
	budget := gguf.ComputeBudget(8<<30, 2<<30, 512<<20, 0, false)
	if note := fitNote(est, config.Resolved{Ctx: 4096}, budget); note != "" {
		t.Fatalf("fitting estimate should not warn: %q", note)
	}
	est.Total = 8 << 30
	note := fitNote(est, config.Resolved{Ctx: 4096, NCPUMoE: 8}, budget)
	for _, want := range []string{"exceeds the", "budget by", "n_cpu_moe"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note missing %q: %q", want, note)
		}
	}
	if note := fitNote(est, config.Resolved{Ctx: 4096}, gguf.Budget{}); note != "" {
		t.Fatalf("unknown budget should not warn: %q", note)
	}
}

func TestReportPrintShowsBreakdownAndBudget(t *testing.T) {
	const mib = 1 << 20
	budget := gguf.ComputeBudget(8<<30, 2<<30, 512<<20, 0, false)
	est := gguf.Estimate{WeightsBytes: 4096 * mib, KVBytes: 1024 * mib, Overhead: 512 * mib, Total: 5632 * mib}
	rep := Report{
		ConfigPath: "/tmp/config.toml",
		Budget:     budget,
		Checks:     []Check{{Level: LevelOK, Name: "llama-server", Detail: "/bin/llama-server"}},
		Models: []ModelReport{{
			ID: "m", Path: "/tmp/m.gguf", Exists: true, Size: 8 << 30,
			Ctx: 4096, HasEst: true, Est: est,
			Headroom: budget.Headroom(est), Limiting: est.LimitingTerm(),
			Presets: []PresetFit{{
				ID: "p", Ctx: 65536, Known: true,
				Estimate: gguf.Estimate{WeightsBytes: 4096 * mib, KVBytes: 6144 * mib, Overhead: 512 * mib, Total: 10752 * mib},
				Fits:     false, Note: "MoE CPU offload not included",
			}},
		}},
	}
	rep.Models[0].Presets[0].Headroom = budget.Headroom(rep.Models[0].Presets[0].Estimate)
	rep.Models[0].Presets[0].Limiting = rep.Models[0].Presets[0].Estimate.LimitingTerm()
	out := rep.Print()
	for _, want := range []string{
		"vram: 8.0 GiB total, 2.0 GiB used, 512.0 MiB reserve",
		"weights 4.0 GiB + kv 1.0 GiB + overhead 512.0 MiB = 5.5 GiB",
		"limit weights",
		"headroom",
		"over",
		"MoE CPU offload not included",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(Report{}.Print(), "budget unknown") {
		t.Fatal("unknown budget not reported")
	}
}
