package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddModelWritesResolvableEntry(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(modelsDir, "old.gguf")
	newPath := filepath.Join(modelsDir, "new model.gguf")
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte("GGUF"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := "# keep this comment\n[models.old]\npath = " + quote(oldPath) + "\nalias = \"Old\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddModel(cfgPath, "new", newPath, "New Alias"); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep this comment", "[models.new]", "path = " + quote(newPath), `alias = "New Alias"`} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}
	reloaded, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := reloaded.Models["new"]
	if !ok || m.Path != newPath || m.Alias != "New Alias" {
		t.Fatalf("model = %+v, ok = %v", m, ok)
	}
	if _, err := reloaded.Resolve("new", nil); err != nil {
		t.Fatalf("added model does not resolve: %v", err)
	}
}

func TestAddModelRejectsDuplicateIDAndPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	modelPath := filepath.Join(dir, "a.gguf")
	if err := os.WriteFile(modelPath, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("[models.a]\npath = "+quote(modelPath)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddModel(cfgPath, "a", filepath.Join(dir, "b.gguf"), ""); !errors.Is(err, ErrModelExists) {
		t.Fatalf("duplicate ID error = %v", err)
	}
	if err := cfg.AddModel(cfgPath, "b", modelPath, ""); !errors.Is(err, ErrModelPathExists) {
		t.Fatalf("duplicate path error = %v", err)
	}

	link := filepath.Join(dir, "link.gguf")
	if err := os.Symlink(modelPath, link); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddModel(cfgPath, "c", link, ""); !errors.Is(err, ErrModelPathExists) {
		t.Fatalf("symlink duplicate path error = %v", err)
	}
}

func TestAddModelRejectsBadID(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := cfg.AddModel(cfgPath, "bad id", "a.gguf", ""); err == nil {
		t.Fatal("expected invalid ID error")
	}
}

func TestValidIDAndCanonicalPath(t *testing.T) {
	if !ValidID("qwen3-4b") || ValidID("has space") || ValidID("-leading") {
		t.Fatal("ValidID returned the wrong result")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.gguf")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.gguf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalPath(link); got != target {
		t.Fatalf("CanonicalPath(link) = %q, want %q", got, target)
	}
	if got := CanonicalPath(""); got != "" {
		t.Fatalf("CanonicalPath(empty) = %q", got)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
