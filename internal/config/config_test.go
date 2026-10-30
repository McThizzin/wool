package config

import (
	"path/filepath"
	"testing"
)

func TestLoadExample(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 6 {
		t.Fatalf("models = %d, want 6", len(cfg.Models))
	}
	if len(cfg.Presets) != 12 {
		t.Fatalf("presets = %d, want 12", len(cfg.Presets))
	}

	r, err := cfg.Resolve("qwen35-4b-mtp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ctx != 65536 {
		t.Fatalf("ctx = %d, want 65536", r.Ctx)
	}
	if r.Threads != 16 && r.Threads <= 0 {
		t.Fatalf("threads = %d, want resolved nproc", r.Threads)
	}
	if r.Extra["spec-type"] != "draft-mtp" {
		t.Fatalf("missing MTP extra: %#v", r.Extra)
	}
	if r.Sampling.Temp != 0.7 || r.Sampling.TopP != 0.8 {
		t.Fatalf("sampling = %+v, want temp .7 top_p .8", r.Sampling)
	}
	if r.Sampling.RepeatPenalty != 1.05 {
		t.Fatalf("repeat penalty = %v", r.Sampling.RepeatPenalty)
	}

	preset := cfg.Presets["gemma-heretic-65k"]
	r, err = cfg.Resolve("", &preset)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ctx != 65536 {
		t.Fatalf("preset ctx = %d, want 65536", r.Ctx)
	}
	if r.Sampling.TopK != 64 {
		t.Fatalf("gemma top_k = %d, want 64", r.Sampling.TopK)
	}
}

func TestParseCustomThemes(t *testing.T) {
	cfg, err := Parse([]byte(`
[app]
theme = "dusk"

[themes.dusk]
base = "tokyo"

[themes.dusk.colors]
accent = "#ff9e64"
`), "test")
	if err != nil {
		t.Fatal(err)
	}
	def := cfg.Themes["dusk"]
	if def.Base != "tokyo" || def.Colors["accent"] != "#ff9e64" {
		t.Fatalf("theme = %+v", def)
	}

	if _, err := Parse([]byte(`
[themes.bad]
base = "missing"
`), "test"); err == nil {
		t.Fatal("expected unknown base error")
	}
	if _, err := Parse([]byte(`
[themes.bad.colors]
accent = "not-a-color"
`), "test"); err == nil {
		t.Fatal("expected bad color error")
	}
}

func TestParseRejectsUnknownPresetModel(t *testing.T) {
	_, err := Parse([]byte(`
[models.a]
path = "a.gguf"
[presets.b]
model = "missing"
`), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseMinimal(t *testing.T) {
	cfg, err := Parse([]byte(`
[models.a]
path = "~/models/a.gguf"
ctx = 8192
[models.a.sampling]
temp = 0.3
[presets.a]
model = "a"
`), "test")
	if err != nil {
		t.Fatal(err)
	}
	preset := cfg.Presets["a"]
	r, err := cfg.Resolve("", &preset)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sampling.Temp != 0.3 {
		t.Fatalf("temp = %v", r.Sampling.Temp)
	}
	// Unspecified sampler fields come from defaults, not zero.
	if r.Sampling.TopP != DefaultSampling().TopP {
		t.Fatalf("top_p = %v", r.Sampling.TopP)
	}
	if r.Threads <= 0 {
		t.Fatalf("threads = %d", r.Threads)
	}
}
