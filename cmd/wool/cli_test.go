package main

import (
	"testing"
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
