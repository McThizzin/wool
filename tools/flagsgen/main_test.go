package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"wool/internal/flags"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunRegeneratesCheckedInArtifacts(t *testing.T) {
	root := testRoot(t)
	help := filepath.Join(root, "tools", "flagsgen", "data", "llama-server-help.txt")
	overrides := filepath.Join(root, "tools", "flagsgen", "data", "overrides.json")

	wantCatalog, err := os.ReadFile(filepath.Join(root, "internal", "flags", "data", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantMarkdown, err := os.ReadFile(filepath.Join(root, "docs", "flags.md"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	markdownPath := filepath.Join(dir, "flags.md")
	if code := run([]string{
		"-help", help,
		"-overrides", overrides,
		"-out", catalogPath,
		"-markdown", markdownPath,
	}); code != 0 {
		t.Fatalf("run exit = %d", code)
	}

	gotCatalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotCatalog, wantCatalog) {
		t.Fatalf("generated catalog differs from internal/flags/data/catalog.json")
	}
	gotMarkdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotMarkdown, wantMarkdown) {
		t.Fatalf("generated reference differs from docs/flags.md")
	}
}

func TestApplyOverridesMarksDeprecatedFlags(t *testing.T) {
	root := testRoot(t)
	ov, err := loadOverrides(filepath.Join(root, "tools", "flagsgen", "data", "overrides.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := &flags.Catalog{Flags: []flags.Flag{{
		Name:        "model-alias",
		Value:       flags.ValueString,
		Description: "deprecated: use --alias",
		Category:    "general",
	}}}
	applyOverrides(c, ov, ov.Source)
	f, ok := c.Lookup("model-alias")
	if !ok || !f.Deprecated || f.Replacement != "--alias" {
		t.Fatalf("override not applied: %+v, %v", f, ok)
	}
	if f.Source != ov.Source {
		t.Fatalf("source = %+v, want %+v", f.Source, ov.Source)
	}
}

func TestVersionFromHelpFixtures(t *testing.T) {
	root := testRoot(t)
	for _, tc := range []struct {
		path string
		want string
	}{
		{filepath.Join(root, "tools", "flagsgen", "data", "llama-server-help.txt"), "b6000"},
		{filepath.Join(root, "tools", "flagsgen", "testdata", "help-older.txt"), "b4000"},
	} {
		b, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := versionFromHelp(string(b)); got != tc.want {
			t.Fatalf("%s: version = %q, want %q", tc.path, got, tc.want)
		}
	}
}
