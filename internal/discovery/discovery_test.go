package discovery

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wool/internal/config"
)

type kv struct {
	key string
	typ uint32
	val any
}

// writeGGUF creates a small but valid GGUF header with complete attention
// metadata. File data is not needed because discovery only reads metadata.
func writeGGUF(t *testing.T, path, name, arch string, params uint64, ctx int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	w32 := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	w64 := func(v uint64) { binary.Write(&b, binary.LittleEndian, v) }
	wstr := func(s string) { w64(uint64(len(s))); b.WriteString(s) }
	kvs := []kv{
		{"general.name", 8, name},
		{"general.architecture", 8, arch},
		{arch + ".block_count", 4, uint32(2)},
		{arch + ".embedding_length", 4, uint32(8)},
		{arch + ".attention.head_count", 4, uint32(2)},
		{arch + ".context_length", 4, uint32(ctx)},
	}
	if params > 0 {
		kvs = append(kvs, kv{"general.parameter_count", 10, params})
	}
	w32(0x46554747)
	w32(3)
	w64(0)
	w64(uint64(len(kvs)))
	for _, item := range kvs {
		wstr(item.key)
		w32(item.typ)
		switch item.typ {
		case 8:
			wstr(item.val.(string))
		case 4:
			w32(item.val.(uint32))
		case 10:
			w64(item.val.(uint64))
		}
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsEntriesAndSkipsPartialFiles(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "Llama 3.gguf")
	second := filepath.Join(root, "nested", "Other-Model.gguf")
	writeGGUF(t, first, "Llama 3 Test", "llama", 7_000_000_000, 8192)
	writeGGUF(t, second, "", "qwen3", 0, 32768)
	if err := os.WriteFile(filepath.Join(root, "download.gguf.part"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{App: config.AppConfig{ModelDirs: []string{root}}}
	rep := Scan(cfg, Options{Workers: 1})
	if len(rep.Entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(rep.Entries), rep.Entries)
	}
	byPath := map[string]Entry{}
	for _, e := range rep.Entries {
		byPath[e.Path] = e
	}
	e := byPath[first]
	if e.SuggestedID != "llama-3" || e.SuggestedAlias != "Llama 3 Test" {
		t.Fatalf("first entry = %+v", e)
	}
	if e.Architecture != "llama" || e.Parameters != 7_000_000_000 || e.CtxTrain != 8192 {
		t.Fatalf("first metadata = %+v", e)
	}
	if e.Size == 0 {
		t.Fatal("entry size was not recorded")
	}
	e = byPath[second]
	if e.SuggestedID != "other-model" || e.SuggestedAlias != "other-model" {
		t.Fatalf("second entry = %+v", e)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0].Message, "partial") {
		t.Fatalf("warnings = %+v", rep.Warnings)
	}
	if rep.Dirs[0] != root {
		t.Fatalf("dirs = %v", rep.Dirs)
	}
}

func TestScanGroupsConfiguredAndFlagsDuplicates(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared.gguf")
	fresh := filepath.Join(root, "fresh.gguf")
	writeGGUF(t, shared, "Shared", "llama", 1_000, 4096)
	writeGGUF(t, fresh, "Fresh", "llama", 1_000, 4096)
	missing := filepath.Join(root, "missing.gguf")

	cfg := &config.Config{
		App: config.AppConfig{ModelDirs: []string{root}},
		Models: map[string]config.ModelConfig{
			"a":    {Path: shared, Alias: "A"},
			"b":    {Path: shared, Alias: "B"},
			"gone": {Path: missing},
		},
	}
	rep := Scan(cfg, Options{Workers: 2})
	if len(rep.Configured) != 3 {
		t.Fatalf("configured = %+v", rep.Configured)
	}
	byID := map[string]Configured{}
	for _, c := range rep.Configured {
		byID[c.ID] = c
	}
	if !byID["a"].Duplicate || !byID["b"].Duplicate {
		t.Fatalf("duplicates not flagged: %+v", byID)
	}
	if !byID["a"].Exists || !byID["a"].Discovered {
		t.Fatalf("shared model not discovered: %+v", byID["a"])
	}
	if byID["gone"].Exists || byID["gone"].Discovered {
		t.Fatalf("missing model reported present: %+v", byID["gone"])
	}
	for _, e := range rep.Entries {
		switch e.Path {
		case shared:
			if len(e.Configured) != 2 || !e.Duplicate {
				t.Fatalf("shared entry = %+v", e)
			}
		case fresh:
			if len(e.Configured) != 0 || e.Duplicate {
				t.Fatalf("fresh entry = %+v", e)
			}
		}
	}
}

func TestScanSkipsUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission tests do not work as root")
	}
	root := t.TempDir()
	writeGGUF(t, filepath.Join(root, "ok.gguf"), "OK", "llama", 1, 4096)
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGGUF(t, filepath.Join(locked, "hidden.gguf"), "Hidden", "llama", 1, 4096)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	cfg := &config.Config{App: config.AppConfig{ModelDirs: []string{root}}}
	rep := Scan(cfg, Options{})
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "OK" {
		t.Fatalf("entries = %+v", rep.Entries)
	}
	found := false
	for _, warning := range rep.Warnings {
		if warning.Path == locked {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing permission warning: %+v", rep.Warnings)
	}
}

func TestScanWarnsOnInvalidAndEmptyGGUF(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.gguf"), []byte("not a gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.gguf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{App: config.AppConfig{ModelDirs: []string{root}}}
	rep := Scan(cfg, Options{})
	if len(rep.Entries) != 0 {
		t.Fatalf("entries = %+v", rep.Entries)
	}
	if len(rep.Warnings) != 2 {
		t.Fatalf("warnings = %+v", rep.Warnings)
	}
}

func TestScanSymlinksAreOptIn(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	writeGGUF(t, filepath.Join(external, "external.gguf"), "External", "llama", 1, 4096)
	if err := os.Symlink(external, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{App: config.AppConfig{ModelDirs: []string{root}}}

	rep := Scan(cfg, Options{})
	if len(rep.Entries) != 0 {
		t.Fatalf("symlink was followed by default: %+v", rep.Entries)
	}

	rep = Scan(cfg, Options{FollowSymlinks: true})
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "External" {
		t.Fatalf("follow entries = %+v", rep.Entries)
	}
}

func TestWorkerCount(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{-1, DefaultWorkers},
		{0, DefaultWorkers},
		{1, 1},
		{32, 32},
		{100, 32},
	} {
		if got := workerCount(tc.in); got != tc.want {
			t.Errorf("workerCount(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestInspectSuggestsUniqueIDAndAlias(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Qwen 3 4B.gguf")
	writeGGUF(t, path, "Qwen3 4B", "qwen3", 4_000_000_000, 32768)
	taken := map[string]bool{"qwen-3-4b": true}
	e, err := Inspect(path, taken)
	if err != nil {
		t.Fatal(err)
	}
	if e.SuggestedID != "qwen-3-4b-2" {
		t.Fatalf("suggested ID = %q", e.SuggestedID)
	}
	if e.SuggestedAlias != "Qwen3 4B" {
		t.Fatalf("suggested alias = %q", e.SuggestedAlias)
	}
	if !taken[e.SuggestedID] {
		t.Fatal("Inspect did not reserve the suggested ID")
	}
}
