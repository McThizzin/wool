package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExampleEmbedMatchesRoot(t *testing.T) {
	root, err := os.ReadFile(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, ExampleTOML) {
		t.Fatal("internal/config/example.toml is out of sync with config.example.toml")
	}
}

func TestWriteExample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := WriteExample(path, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if err := WriteExample(path, false); err == nil {
		t.Fatal("expected existing error")
	}
	if err := WriteExample(path, true); err != nil {
		t.Fatal(err)
	}
}
