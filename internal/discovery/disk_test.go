package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"wool/internal/config"
)

func TestDiskReportsSizesUsageAndOrphans(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.gguf")
	fresh := filepath.Join(dir, "fresh.gguf")
	writeGGUF(t, shared, "Shared", "llama", 1_000, 4096)
	writeGGUF(t, fresh, "Fresh", "llama", 2_000, 8192)
	sharedInfo, err := os.Stat(shared)
	if err != nil {
		t.Fatal(err)
	}
	freshInfo, err := os.Stat(fresh)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"a":    {Path: shared, Alias: "A"},
			"b":    {Path: shared, Alias: "B"},
			"c":    {Path: fresh, Alias: "C"},
			"gone": {Path: filepath.Join(dir, "missing.gguf")},
		},
		Presets: map[string]config.PresetConfig{
			"p1": {Model: "c"},
			"p2": {Model: "c"},
		},
	}
	rep := Disk(cfg, DiskOptions{SkipContent: true})

	wantTotal := uint64(sharedInfo.Size()) + uint64(freshInfo.Size())
	if rep.TotalBytes != wantTotal || rep.UniqueFiles != 2 {
		t.Fatalf("total = %d over %d files, want %d over 2", rep.TotalBytes, rep.UniqueFiles, wantTotal)
	}
	if rep.Missing != 1 {
		t.Fatalf("missing = %d, want 1", rep.Missing)
	}
	if len(rep.Unused) != 3 || rep.Unused[0] != "a" || rep.Unused[1] != "b" || rep.Unused[2] != "gone" {
		t.Fatalf("unused = %v", rep.Unused)
	}
	if len(rep.DuplicatePaths) != 1 || rep.DuplicatePaths[0].Path != shared {
		t.Fatalf("duplicate paths = %+v", rep.DuplicatePaths)
	}
	if got := rep.DuplicatePaths[0].Models; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("duplicate models = %v", got)
	}
	if len(rep.DuplicateContent) != 0 {
		t.Fatalf("skip-content report checked content: %+v", rep.DuplicateContent)
	}
	if rep.DuplicateDetection != "none" {
		t.Fatalf("detection = %q", rep.DuplicateDetection)
	}

	a, ok := rep.Find("a")
	if !ok || !a.DuplicatePath || a.Size != uint64(sharedInfo.Size()) || a.Status != DiskStatusOK {
		t.Fatalf("a = %+v, ok = %v", a, ok)
	}
	if len(a.Presets) != 0 {
		t.Fatalf("a presets = %v", a.Presets)
	}
	c, _ := rep.Find("c")
	if len(c.Presets) != 2 || c.Presets[0] != "p1" || c.Presets[1] != "p2" {
		t.Fatalf("c presets = %v", c.Presets)
	}
	gone, _ := rep.Find("gone")
	if gone.Status != DiskStatusMissing || gone.Size != 0 {
		t.Fatalf("gone = %+v", gone)
	}
}

func TestDiskContentDuplicatesDefaultAndHash(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.gguf")
	writeGGUF(t, base, "Same Model", "llama", 1_000, 4096)
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	// Same metadata and size, different trailing bytes. The cheap default
	// cannot tell them apart; full hashing can.
	first := filepath.Join(dir, "first.gguf")
	second := filepath.Join(dir, "second.gguf")
	if err := os.WriteFile(first, append(append([]byte{}, data...), []byte("AAAA")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, append(append([]byte{}, data...), []byte("BBBB")...), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Models: map[string]config.ModelConfig{
		"first":  {Path: first},
		"second": {Path: second},
	}}
	rep := Disk(cfg, DiskOptions{})
	if len(rep.DuplicateContent) != 1 || rep.DuplicateDetection != "size+metadata" {
		t.Fatalf("default duplicates = %+v (%s)", rep.DuplicateContent, rep.DuplicateDetection)
	}
	if rep.DuplicateContent[0].Hash != "" {
		t.Fatalf("default report exposed a hash: %+v", rep.DuplicateContent[0])
	}

	rep = Disk(cfg, DiskOptions{Hash: true})
	if len(rep.DuplicateContent) != 0 {
		t.Fatalf("hash flagged different bytes: %+v", rep.DuplicateContent)
	}
	if rep.DuplicateDetection != "sha256" {
		t.Fatalf("detection = %q", rep.DuplicateDetection)
	}
	for _, m := range rep.Models {
		if m.Hash == "" {
			t.Fatalf("model %s has no hash: %+v", m.ID, m)
		}
	}
}

func TestDiskHashGroupsIdenticalFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.gguf")
	writeGGUF(t, first, "Copy", "llama", 1_000, 4096)
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second.gguf")
	if err := os.WriteFile(second, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Models: map[string]config.ModelConfig{
		"first":  {Path: first},
		"second": {Path: second},
	}}
	rep := Disk(cfg, DiskOptions{Hash: true})
	if len(rep.DuplicateContent) != 1 {
		t.Fatalf("duplicates = %+v", rep.DuplicateContent)
	}
	group := rep.DuplicateContent[0]
	if group.Hash == "" || len(group.Models) != 2 || group.Models[0] != "first" || group.Models[1] != "second" {
		t.Fatalf("group = %+v", group)
	}
	if rep.Models[0].Hash != group.Hash {
		t.Fatalf("model hash = %q, group hash = %q", rep.Models[0].Hash, group.Hash)
	}
}

func TestDiskSamePathIsOnlyAPathDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.gguf")
	writeGGUF(t, path, "Shared", "llama", 1_000, 4096)
	cfg := &config.Config{Models: map[string]config.ModelConfig{
		"a": {Path: path},
		"b": {Path: path},
	}}
	rep := Disk(cfg, DiskOptions{})
	if len(rep.DuplicatePaths) != 1 || len(rep.DuplicateContent) != 0 {
		t.Fatalf("paths = %+v, content = %+v", rep.DuplicatePaths, rep.DuplicateContent)
	}
	for _, m := range rep.Models {
		if !m.DuplicatePath || m.DuplicateContent {
			t.Fatalf("model = %+v", m)
		}
	}
}

func TestDiskWarnsOnBadMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.gguf")
	if err := os.WriteFile(path, []byte("not a gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Models: map[string]config.ModelConfig{"broken": {Path: path}}}
	rep := Disk(cfg, DiskOptions{})
	if len(rep.Warnings) != 1 || rep.Warnings[0].Path != path {
		t.Fatalf("warnings = %+v", rep.Warnings)
	}
	if len(rep.DuplicateContent) != 0 {
		t.Fatalf("broken metadata produced duplicates: %+v", rep.DuplicateContent)
	}
}

func TestDiskNilConfig(t *testing.T) {
	rep := Disk(nil, DiskOptions{})
	if rep.Models == nil || rep.Unused == nil {
		t.Fatalf("nil config returned unstable slices: %+v", rep)
	}
}
