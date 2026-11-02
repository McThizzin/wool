package config

import "testing"

func diffConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(`
[defaults]
ctx = 8192
cache_type_k = "f16"

[models.a]
path = "a.gguf"
gpu_layers = 42

[models.a.sampling]
temp = 0.4

[models.plain]
path = "plain.gguf"

[presets.p]
model = "a"
ctx = 16384

[presets.p.sampling]
top_p = 0.5
`), "test")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDiffPresetAgainstModel(t *testing.T) {
	cfg := diffConfig(t)
	got, err := cfg.Diff("", "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Against != SourceModel {
		t.Fatalf("against = %q, want model", got.Against)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("fields = %+v, want ctx and sampling.top_p", got.Fields)
	}
	byField := map[string]DiffField{}
	for _, f := range got.Fields {
		byField[f.Field] = f
	}
	ctx, ok := byField["ctx"]
	if !ok {
		t.Fatalf("ctx missing from %+v", got.Fields)
	}
	if ctx.Target.Value != 16384 || ctx.Target.Source != SourcePreset || ctx.Target.Layer != "presets.p" {
		t.Errorf("ctx target = %+v", ctx.Target)
	}
	if ctx.Against.Value != 8192 || ctx.Against.Source != SourceDefaults || ctx.Against.Layer != "defaults" {
		t.Errorf("ctx against = %+v", ctx.Against)
	}
	topP, ok := byField["sampling.top_p"]
	if !ok {
		t.Fatalf("sampling.top_p missing from %+v", got.Fields)
	}
	if topP.Target.Value != 0.5 || topP.Target.Source != SourcePreset {
		t.Errorf("top_p target = %+v", topP.Target)
	}
	if topP.Against.Source != SourceBuiltin || topP.Against.Layer != "built-in" {
		t.Errorf("top_p against = %+v", topP.Against)
	}
	if _, ok := byField["sampling.temp"]; ok {
		t.Error("sampling.temp should match the model layer")
	}
}

func TestDiffModelAgainstDefaults(t *testing.T) {
	cfg := diffConfig(t)
	got, err := cfg.Diff("a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Against != SourceDefaults {
		t.Fatalf("against = %q, want defaults", got.Against)
	}
	var fields []string
	for _, f := range got.Fields {
		fields = append(fields, f.Field)
	}
	want := []string{"gpu_layers", "sampling.temp"}
	if len(fields) != len(want) || fields[0] != want[0] || fields[1] != want[1] {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
	if got.Fields[0].Target.Source != SourceModel || got.Fields[0].Target.Layer != "models.a" {
		t.Errorf("gpu_layers target = %+v", got.Fields[0].Target)
	}
}

func TestDiffNoChanges(t *testing.T) {
	cfg := diffConfig(t)
	got, err := cfg.Diff("plain", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fields) != 0 {
		t.Fatalf("fields = %+v, want none", got.Fields)
	}
	if got.Fields == nil {
		t.Fatal("fields must be an empty slice so JSON is []")
	}
}

func TestDiffRejectsBadAgainst(t *testing.T) {
	cfg := diffConfig(t)
	if _, err := cfg.Diff("a", "", SourceModel); err == nil {
		t.Fatal("model against model should fail")
	}
	if _, err := cfg.Diff("a", "", "preset"); err == nil {
		t.Fatal("unknown layer should fail")
	}
}

func TestDefaultDiffAgainst(t *testing.T) {
	if got := DefaultDiffAgainst("p"); got != SourceModel {
		t.Fatalf("preset default = %q, want model", got)
	}
	if got := DefaultDiffAgainst(""); got != SourceDefaults {
		t.Fatalf("model default = %q, want defaults", got)
	}
}
