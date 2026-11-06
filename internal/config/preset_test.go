package config

import (
	"errors"
	"strings"
	"testing"
)

func TestSetValuesUpdatesMultipleKeys(t *testing.T) {
	path := writeTempConfig(t, `# top comment
[models.a]
path = "a.gguf"
ctx = 4096

# keep this
[presets.p]
model = "a"
`)
	err := SetValues(path, "models.a", []Value{
		{Key: "ctx", Value: "8192"},
		{Key: "gpu_layers", Value: "32"},
		{Key: "cache_type_k", Value: `"q8_0"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := readString(t, path)
	for _, want := range []string{
		"ctx = 8192",
		"gpu_layers = 32",
		`cache_type_k = "q8_0"`,
		"# top comment",
		"# keep this",
		`path = "a.gguf"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ctx = 4096") {
		t.Fatalf("old value kept:\n%s", text)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("config no longer loads: %v", err)
	}
}

func TestSavePresetWritesAndResolves(t *testing.T) {
	path := writeTempConfig(t, `# top comment
[models.a]
path = "a.gguf"
alias = "A"

# keep this preset
[presets.existing]
model = "a"
ctx = 2048
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fit := PresetFit{
		Ctx:        16384,
		GPULayers:  20,
		NCPUMoE:    2,
		CacheTypeK: "q8_0",
		CacheTypeV: "q8_0",
	}
	if err := cfg.SavePreset(path, "fitted", "a", fit, false); err != nil {
		t.Fatal(err)
	}
	text := readString(t, path)
	for _, want := range []string{
		"# top comment",
		"# keep this preset",
		"[presets.existing]",
		"[presets.fitted]",
		`model = "a"`,
		"ctx = 16384",
		"gpu_layers = 20",
		"n_cpu_moe = 2",
		`cache_type_k = "q8_0"`,
		`cache_type_v = "q8_0"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reloaded.Presets["fitted"]
	if !ok {
		t.Fatal("saved preset missing after reload")
	}
	r, err := reloaded.Resolve("", &p)
	if err != nil {
		t.Fatal(err)
	}
	if r.ModelID != "a" || r.Ctx != 16384 || r.GPULayers != 20 || r.NCPUMoE != 2 ||
		r.CacheTypeK != "q8_0" || r.CacheTypeV != "q8_0" {
		t.Fatalf("resolved preset = %+v", r)
	}

	if err := reloaded.SavePreset(path, "fitted", "a", fit, false); !errors.Is(err, ErrPresetExists) {
		t.Fatalf("overwrite without force = %v, want ErrPresetExists", err)
	}
	fit.Ctx = 4096
	if err := reloaded.SavePreset(path, "fitted", "a", fit, true); err != nil {
		t.Fatal(err)
	}
	after, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p = after.Presets["fitted"]
	if p.Options.Ctx == nil || *p.Options.Ctx != 4096 {
		t.Fatalf("forced ctx = %v, want 4096", p.Options.Ctx)
	}
	if text := readString(t, path); !strings.Contains(text, "# keep this preset") {
		t.Fatalf("comment lost on replace:\n%s", text)
	}
}
