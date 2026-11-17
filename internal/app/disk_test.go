package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wool/internal/config"
)

func TestModelsDiskView(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.gguf")
	if err := os.WriteFile(shared, bytes.Repeat([]byte("x"), 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	m.cfg.Models = map[string]config.ModelConfig{
		"alpha": {Path: shared, Alias: "Alpha"},
		"beta":  {Path: shared, Alias: "Beta"},
		"gone":  {Path: filepath.Join(dir, "missing.gguf")},
	}
	m.cfg.Presets = map[string]config.PresetConfig{"p": {Model: "alpha"}}
	m.models = m.cfg.ModelIDs()
	m.presets = m.cfg.PresetIDs()
	m.modelSel = 0

	m.setTab(tabModels)
	if len(m.disk.DuplicatePaths) != 1 {
		t.Fatalf("duplicate paths = %+v", m.disk.DuplicatePaths)
	}
	if len(m.disk.Unused) != 2 || m.disk.Missing != 1 || m.disk.TotalBytes != 2048 {
		t.Fatalf("disk = %+v", m.disk)
	}

	m.width, m.height = 120, 40
	out := m.View()
	for _, want := range []string{"2.0 KiB", "2 unused", "1 missing", "duplicate path", "Beta"} {
		if !strings.Contains(out, want) {
			t.Fatalf("models view missing %q:\n%s", want, out)
		}
	}
	checkView(t, m, 120, 40)
	checkView(t, m, 60, 20)
}
