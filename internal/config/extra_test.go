package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeExtraFlag(t *testing.T) {
	for raw, want := range map[string]string{
		"spec-type":   "spec-type",
		"--spec-type": "spec-type",
		"  ---foo  ":  "foo",
	} {
		got, err := NormalizeExtraFlag(raw)
		if err != nil || got != want {
			t.Errorf("NormalizeExtraFlag(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "-", "foo bar", "foo.bar", "foo=bar"} {
		if _, err := NormalizeExtraFlag(raw); err == nil {
			t.Errorf("NormalizeExtraFlag(%q) should fail", raw)
		}
	}
}

func TestExtraProvenanceAndDiff(t *testing.T) {
	cfg, err := Parse([]byte(`
[defaults.extra]
shared = "base"

[models.a]
path = "a.gguf"

[models.a.extra]
--shared = "model"
bare = ""
ctx-size = "12345"

[presets.p]
model = "a"

[presets.p.extra]
--shared = "preset"
`), "test")
	if err != nil {
		t.Fatal(err)
	}

	r, sources, err := cfg.ResolveDetailed("", "p")
	if err != nil {
		t.Fatal(err)
	}
	if r.Extra["shared"] != "preset" || r.Extra["bare"] != "" || r.Extra["ctx-size"] != "12345" {
		t.Fatalf("resolved extras = %+v", r.Extra)
	}
	if got := sources["extra.shared"]; got.Source != SourcePreset || got.Layer != "presets.p" {
		t.Fatalf("extra.shared source = %+v", got)
	}
	if got := sources["extra.bare"]; got.Source != SourceModel || got.Layer != "models.a" {
		t.Fatalf("extra.bare source = %+v", got)
	}
	if FieldValue(r, "extra.bare") != "" {
		t.Fatalf("bare value = %#v", FieldValue(r, "extra.bare"))
	}

	fields := FieldNames(r)
	tail := fields[len(fields)-3:]
	wantTail := []string{"extra.bare", "extra.ctx-size", "extra.shared"}
	for i := range wantTail {
		if tail[i] != wantTail[i] {
			t.Fatalf("extra field order = %v, want %v", tail, wantTail)
		}
	}

	defaults := cfg.ResolveDefaults()
	if defaults.Extra["shared"] != "base" {
		t.Fatalf("defaults extras = %+v", defaults.Extra)
	}
	baseSources := cfg.sourcesFor(defaults, "", "")
	if got := baseSources["extra.shared"]; got.Source != SourceDefaults || got.Layer != "defaults" {
		t.Fatalf("defaults extra source = %+v", got)
	}

	presetDiff, err := cfg.Diff("", "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(presetDiff.Fields) != 1 || presetDiff.Fields[0].Field != "extra.shared" {
		t.Fatalf("preset diff = %+v", presetDiff.Fields)
	}
	shared := presetDiff.Fields[0]
	if shared.Target.Value != "preset" || shared.Target.Source != SourcePreset || shared.Target.Layer != "presets.p" {
		t.Fatalf("preset diff target = %+v", shared.Target)
	}
	if shared.Against.Value != "model" || shared.Against.Source != SourceModel || shared.Against.Layer != "models.a" {
		t.Fatalf("preset diff against = %+v", shared.Against)
	}

	modelDiff, err := cfg.Diff("a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	byField := map[string]DiffField{}
	for _, f := range modelDiff.Fields {
		byField[f.Field] = f
	}
	if len(byField) != 3 {
		t.Fatalf("model diff fields = %+v", modelDiff.Fields)
	}
	if got := byField["extra.shared"]; got.Target.Value != "model" || got.Against.Value != "base" || got.Against.Source != SourceDefaults {
		t.Fatalf("shared diff = %+v", got)
	}
	if got := byField["extra.bare"]; got.Target.Value != "" || got.Against.Value != nil {
		t.Fatalf("bare diff = %+v", got)
	}
	if got := byField["extra.ctx-size"]; got.Target.Value != "12345" || got.Target.Layer != "models.a" {
		t.Fatalf("ctx-size diff = %+v", got)
	}
}

func TestExtraConflictWarnings(t *testing.T) {
	extra := map[string]string{"--ctx-size": "65536", "gpu_layers": "10"}
	conflicts := ExtraConflicts(extra)
	if len(conflicts) != 2 || conflicts[0].Flag != "ctx-size" || conflicts[0].Field != "ctx" ||
		conflicts[1].Flag != "gpu_layers" || conflicts[1].Field != "gpu_layers" {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	warnings := ExtraConflictWarnings(extra)
	if !strings.Contains(warnings[0], "appended last and win") {
		t.Fatalf("warning = %q", warnings[0])
	}
	if got := DisplayFieldValue("extra.bare", ""); got != `"" (bare)` {
		t.Fatalf("display = %q", got)
	}
}

func TestSetAndRemoveExtraPreservesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "# top comment\n[models.a]\npath = \"a.gguf\"\n\n[models.a.extra]\n# keep me\nspec-type = \"draft-mtp\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetValue(path, "models.a.extra", "spec-mode", `"fast"`); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "# top comment") || !strings.Contains(string(text), "# keep me") {
		t.Fatalf("comments lost:\n%s", text)
	}
	if !strings.Contains(string(text), "spec-mode = \"fast\"") {
		t.Fatalf("extra missing:\n%s", text)
	}
	if err := RemoveValue(path, "models.a.extra", "spec-mode"); err != nil {
		t.Fatal(err)
	}
	text, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "spec-mode") || !strings.Contains(string(text), "# keep me") {
		t.Fatalf("remove did not preserve the table:\n%s", text)
	}
}
