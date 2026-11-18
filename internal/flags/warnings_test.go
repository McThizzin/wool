package flags

import (
	"strings"
	"testing"

	"wool/internal/config"
)

func warningFixture() *Catalog {
	return &Catalog{
		Source: CatalogSource{Version: "b1", Commit: "c1"},
		Flags: []Flag{
			{Name: "ctx-size", Value: ValueInt, Description: "context size", Category: "context"},
			{Name: "model-alias", Value: ValueString, Description: "alias", Category: "model", Deprecated: true, Replacement: "--alias"},
			{Name: "jinja", Value: ValueNone, Description: "use jinja", Category: "templating"},
		},
	}
}

func TestCheckExtrasAndStrictPolicy(t *testing.T) {
	c := warningFixture()
	extra := map[string]string{
		"--ctx-size":  "4096",
		"model-alias": "legacy",
		"brand-new":   "1",
	}
	warnings := CheckExtras(extra, c)
	if len(warnings) != 3 {
		t.Fatalf("warnings = %+v", warnings)
	}
	kinds := map[string]string{}
	for _, w := range warnings {
		kinds[w.Flag] = w.Kind
		if w.Message == "" {
			t.Fatalf("warning %q has no message", w.Flag)
		}
	}
	if kinds["brand-new"] != "unknown" || kinds["model-alias"] != "deprecated" || kinds["ctx-size"] != "conflict" {
		t.Fatalf("kinds = %+v", kinds)
	}

	err := StrictError(warnings)
	if err == nil {
		t.Fatal("strict mode accepted unknown and deprecated flags")
	}
	for _, want := range []string{"brand-new", "model-alias", "--alias"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("strict error missing %q: %s", want, err)
		}
	}
	if StrictError(CheckExtras(map[string]string{"ctx-size": "4096"}, c)) != nil {
		t.Fatal("strict mode rejected an advisory conflict")
	}
	if got := WarningMessages(warnings); len(got) != 3 {
		t.Fatalf("messages = %q", got)
	}
}

func TestConfigWarningsUseLayers(t *testing.T) {
	cfg, err := config.Parse([]byte(`
[models.m]
path = "m.gguf"

[presets.p]
model = "m"

[defaults.extra]
brand-new = "1"

[models.m.extra]
ctx-size = "4096"

[presets.p.extra]
model-alias = "legacy"
`), "layers.toml")
	if err != nil {
		t.Fatal(err)
	}
	warnings := ConfigWarnings(cfg, warningFixture())
	if len(warnings) != 3 {
		t.Fatalf("warnings = %+v", warnings)
	}
	wantLayers := []string{"defaults", "models.m", "presets.p"}
	for i, want := range wantLayers {
		if warnings[i].Layer != want {
			t.Fatalf("warning %d layer = %q, want %q", i, warnings[i].Layer, want)
		}
	}
	messages := WarningMessages(warnings)
	for _, want := range []string{"defaults: ", "models.m: ", "presets.p: "} {
		found := false
		for _, msg := range messages {
			if strings.Contains(msg, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("messages missing %q: %q", want, messages)
		}
	}
}
