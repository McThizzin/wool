package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetValueUpdatesExisting(t *testing.T) {
	path := writeTempConfig(t, `# top comment
[models.a]
path = "a.gguf"
ctx = 4096 # keep me? no
batch_size = 512

[presets.p]
model = "a"
`)
	if err := SetValue(path, "models.a", "ctx", "8192"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	if !strings.Contains(text, "ctx = 8192\n") || strings.Contains(text, "4096") {
		t.Fatalf("ctx not updated:\n%s", text)
	}
	if !strings.Contains(text, "# top comment") {
		t.Fatal("comment lost")
	}
	if !strings.Contains(text, "batch_size = 512") {
		t.Fatal("neighbor key lost")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestSetValueInsertsInSection(t *testing.T) {
	path := writeTempConfig(t, `[models.a]
path = "a.gguf"

# sampling follows
[models.a.sampling]
temp = 1.0
`)
	if err := SetValue(path, "models.a", "gpu_layers", "99"); err != nil {
		t.Fatal(err)
	}
	text := readString(t, path)
	if !strings.Contains(text, "gpu_layers = 99\n\n# sampling follows") {
		t.Fatalf("insert location wrong:\n%s", text)
	}
	if strings.Index(text, "gpu_layers") > strings.Index(text, "[models.a.sampling]") {
		t.Fatalf("inserted into wrong section:\n%s", text)
	}
}

func TestSetValueCreatesSection(t *testing.T) {
	path := writeTempConfig(t, `[models.a]
path = "a.gguf"`)
	if err := SetValue(path, "presets.new", "ctx", "2048"); err != nil {
		t.Fatal(err)
	}
	text := readString(t, path)
	if !strings.HasSuffix(text, "[presets.new]\nctx = 2048") {
		t.Fatalf("section not appended cleanly: %q", text)
	}
}

func TestSetValueNestedSampling(t *testing.T) {
	path := writeTempConfig(t, `[presets.p]
model = "a"
ctx = 4096
`)
	if err := SetValue(path, "presets.p.sampling", "temp", "0.5"); err != nil {
		t.Fatal(err)
	}
	if err := SetValue(path, "presets.p.sampling", "top_p", "0.9"); err != nil {
		t.Fatal(err)
	}
	text := readString(t, path)
	if !strings.Contains(text, "[presets.p.sampling]\ntemp = 0.5\ntop_p = 0.9") {
		t.Fatalf("sampling section wrong:\n%s", text)
	}
}

func TestSetValueRejectsBadNames(t *testing.T) {
	path := writeTempConfig(t, "")
	for _, tc := range []struct{ table, key, value string }{
		{"bad[table]", "x", "1"},
		{"ok", "bad key", "1"},
		{"ok", "x", "a\nb"},
	} {
		if err := SetValue(path, tc.table, tc.key, tc.value); err == nil {
			t.Errorf("%+v should fail", tc)
		}
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
