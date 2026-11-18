package config

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseReportsDecodePosition(t *testing.T) {
	src := "[server\nport = 8080\n"
	_, err := Parse([]byte(src), "broken.toml")
	if err == nil {
		t.Fatal("malformed TOML parsed successfully")
	}
	errs := AsErrors(err)
	if len(errs) != 1 {
		t.Fatalf("errors = %d: %v", len(errs), err)
	}
	e := errs[0]
	if e.File != "broken.toml" || e.Line < 1 || e.Column < 1 {
		t.Fatalf("error = %+v", e)
	}
	wantPrefix := "broken.toml:" + strconv.Itoa(e.Line) + ":" + strconv.Itoa(e.Column) + ": "
	if !strings.HasPrefix(e.Error(), wantPrefix) {
		t.Fatalf("error = %q, want prefix %q", e.Error(), wantPrefix)
	}
	if e.Message == "" {
		t.Fatalf("empty message: %+v", e)
	}
}

func TestStrictRejectsUnknownKey(t *testing.T) {
	src := "[server]\nport = 8080\nprot = 9090\n"
	_, err := ParseWithOptions([]byte(src), "typo.toml", ParseOptions{Strict: true})
	if err == nil {
		t.Fatal("strict mode accepted an unknown key")
	}
	errs := AsErrors(err)
	if len(errs) != 1 {
		t.Fatalf("errors = %d: %v", len(errs), err)
	}
	e := errs[0]
	if e.Line != 3 || e.Column < 1 {
		t.Fatalf("position = %d:%d, want line 3", e.Line, e.Column)
	}
	if !strings.Contains(e.Key, "prot") || !strings.Contains(e.Error(), "prot") {
		t.Fatalf("unknown key not named: %+v / %s", e, e.Error())
	}
	if e.File != "typo.toml" {
		t.Fatalf("file = %q", e.File)
	}
}

func TestStrictReportsEveryUnknownKey(t *testing.T) {
	src := "[server]\nprot = 9090\nporrt = 8080\n"
	_, err := ParseWithOptions([]byte(src), "typos.toml", ParseOptions{Strict: true})
	if err == nil {
		t.Fatal("strict mode accepted unknown keys")
	}
	errs := AsErrors(err)
	if len(errs) != 2 {
		t.Fatalf("errors = %d: %v", len(errs), err)
	}
	if errs[0].Line != 2 || errs[1].Line != 3 {
		t.Fatalf("lines = %d, %d", errs[0].Line, errs[1].Line)
	}
	if !strings.Contains(errs[0].Error(), "prot") || !strings.Contains(errs[1].Error(), "porrt") {
		t.Fatalf("errors = %v", errs)
	}
	if len(ErrorLines(err)) != 2 {
		t.Fatalf("error lines = %v", ErrorLines(err))
	}
}

func TestStrictAllowsMapFields(t *testing.T) {
	src := `
[models.a]
path = "a.gguf"

[models.a.extra]
spec-type = "draft-mtp"
anything-goes = "yes"

[presets.p]
model = "a"

[presets.p.extra]
custom = "value"

[themes.custom]
base = "mocha"

[themes.custom.colors]
accent = "#ffffff"
red = "#123456"
`
	cfg, err := ParseWithOptions([]byte(src), "maps.toml", ParseOptions{Strict: true})
	if err != nil {
		t.Fatalf("strict mode rejected map fields: %v", err)
	}
	if cfg.Models["a"].Extra["spec-type"] != "draft-mtp" || cfg.Models["a"].Extra["anything-goes"] != "yes" {
		t.Fatalf("model extra = %+v", cfg.Models["a"].Extra)
	}
	if cfg.Presets["p"].Extra["custom"] != "value" {
		t.Fatalf("preset extra = %+v", cfg.Presets["p"].Extra)
	}
	if cfg.Themes["custom"].Colors["red"] != "#123456" {
		t.Fatalf("theme colors = %+v", cfg.Themes["custom"].Colors)
	}
}

func TestStrictLoadsExampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.toml")
	if _, err := LoadWithOptions(path, ParseOptions{Strict: true}); err != nil {
		t.Fatalf("strict mode rejected the shipped config: %v", err)
	}
}

func TestNonStrictKeepsLoadingUnknownKeys(t *testing.T) {
	src := "[server]\nport = 8080\nprot = 9090\n"
	cfg, err := Parse([]byte(src), "typo.toml")
	if err != nil {
		t.Fatalf("non-strict mode rejected an unknown key: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
}

func TestValidationErrorsAreStructured(t *testing.T) {
	src := "[server]\nport = 99999\n"
	_, err := Parse([]byte(src), "invalid.toml")
	if err == nil {
		t.Fatal("invalid config parsed successfully")
	}
	errs := AsErrors(err)
	if len(errs) != 1 || errs[0].File != "invalid.toml" || errs[0].Line != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	if !strings.Contains(errs[0].Error(), "port must be between") {
		t.Fatalf("error = %s", errs[0].Error())
	}
}
