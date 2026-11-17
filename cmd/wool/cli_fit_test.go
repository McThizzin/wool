package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wool/internal/config"
	"wool/internal/fit"
	"wool/internal/gguf"
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

func TestParseSmokeTimeout(t *testing.T) {
	if got := parseSmokeTimeout("5s"); got != 5*time.Second {
		t.Fatalf("timeout = %s, want 5s", got)
	}
	for _, raw := range []string{"", "soon", "-1s", "0s"} {
		if got := parseSmokeTimeout(raw); got != measure.DefaultSmokeTimeout {
			t.Fatalf("%q: timeout = %s, want default", raw, got)
		}
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
