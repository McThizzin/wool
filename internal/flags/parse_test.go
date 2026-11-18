package flags

import (
	"os"
	"strings"
	"testing"
)

const (
	baselineHelp = "../../tools/flagsgen/data/llama-server-help.txt"
	olderHelp    = "../../tools/flagsgen/testdata/help-older.txt"
)

func TestParseHelpAcrossVersions(t *testing.T) {
	cases := []struct {
		path    string
		version string
		want    []string
	}{
		{baselineHelp, "b6000", []string{"model", "ctx-size", "gpu-layers", "jinja", "models-preset", "verbose"}},
		{olderHelp, "b4000", []string{"model", "ctx-size", "threads", "temp", "seed", "verbose"}},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			b, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			c, err := ParseHelp(string(b), CatalogSource{Commit: "testcommit"})
			if err != nil {
				t.Fatal(err)
			}
			if c.Source.Version != tc.version || c.Source.Commit != "testcommit" {
				t.Fatalf("source = %+v", c.Source)
			}
			names := c.Names()
			for _, want := range tc.want {
				found := false
				for _, name := range names {
					if name == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s not parsed from %s; got %v", want, tc.path, names)
				}
			}
			for _, f := range c.Flags {
				if f.Source.Version != tc.version || f.Source.Commit != "testcommit" {
					t.Fatalf("flag %q source = %+v", f.Name, f.Source)
				}
				if f.Value == "" || f.Description == "" || f.Category == "" {
					t.Fatalf("flag %q has incomplete metadata: %+v", f.Name, f)
				}
			}
		})
	}
}

func TestParseHelpMetadataAndAliases(t *testing.T) {
	b, err := os.ReadFile(baselineHelp)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseHelp(string(b), CatalogSource{Version: "b6000", Commit: "a1b2c3d"})
	if err != nil {
		t.Fatal(err)
	}

	model, ok := c.Lookup("--model")
	if !ok {
		t.Fatal("model flag missing")
	}
	if model.Value != ValuePath {
		t.Fatalf("model value = %q", model.Value)
	}
	if len(model.Aliases) != 1 || model.Aliases[0] != "m" {
		t.Fatalf("model aliases = %q", model.Aliases)
	}
	if model.Default != "models/7B/ggml-model-f16.gguf" {
		t.Fatalf("model default = %q", model.Default)
	}

	alias, ok := c.Lookup("alias")
	if !ok {
		t.Fatal("alias flag missing")
	}
	if alias.Env != "LLAMA_ARG_MODEL_ALIAS" {
		t.Fatalf("alias env = %q", alias.Env)
	}
	if strings.Contains(alias.Description, "LLAMA_ARG") {
		t.Fatalf("env metadata leaked into description: %q", alias.Description)
	}

	ctx, ok := c.Lookup("ctx-size")
	if !ok || len(ctx.Aliases) != 1 || ctx.Aliases[0] != "c" {
		t.Fatalf("ctx-size aliases = %+v, %v", ctx.Aliases, ok)
	}
	if ctx.Value != ValueInt {
		t.Fatalf("ctx-size value = %q", ctx.Value)
	}
	if DetectVersion("llama-server b1234 (commit)") != "b1234" {
		t.Fatal("DetectVersion missed a build tag")
	}
}

func TestParseHelpIgnoresProse(t *testing.T) {
	const text = `llama-server (b6000)

options:
  -m, --model FNAME        model path
This prose mentions --not-a-flag but is not an option row.
      --jinja              use jinja templating
`
	c, err := ParseHelp(text, CatalogSource{Version: "b6000", Commit: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Flags) != 2 {
		t.Fatalf("flags = %v", c.Names())
	}
	if _, ok := c.Lookup("not-a-flag"); ok {
		t.Fatal("parsed a flag from prose")
	}
	if _, err := ParseHelp("just prose", CatalogSource{}); err == nil {
		t.Fatal("ParseHelp accepted text with no options")
	}
}
