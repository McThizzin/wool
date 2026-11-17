package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wool/internal/config"
	"wool/internal/discovery"
)

func writeModelGGUF(t *testing.T, path, name, arch string, params uint64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	type kv struct {
		key string
		typ uint32
		val any
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
		{arch + ".context_length", 4, uint32(8192)},
		{"general.parameter_count", 10, params},
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

func writeModelsConfig(t *testing.T, modelDirs ...string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	quoted := make([]string, len(modelDirs))
	for i, d := range modelDirs {
		quoted[i] = strconv.Quote(d)
	}
	body := "# model discovery config\n[app]\nmodel_dirs = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestCmdModelsScanJSON(t *testing.T) {
	dir := t.TempDir()
	writeModelGGUF(t, filepath.Join(dir, "Llama 3.gguf"), "Llama 3", "llama", 7_000_000_000)
	writeModelGGUF(t, filepath.Join(dir, "nested", "qwen.gguf"), "Qwen", "qwen3", 4_000_000_000)
	if err := os.WriteFile(filepath.Join(dir, "partial.gguf.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeModelsConfig(t, dir)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddModel(cfgPath, "existing", filepath.Join(dir, "Llama 3.gguf"), "Existing"); err != nil {
		t.Fatal(err)
	}

	out, code := captureStdout(t, func() int {
		return cmdModels(cfgPath, []string{"scan", "--json", "--jobs", "2"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var rep discovery.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries = %d: %+v", len(rep.Entries), rep.Entries)
	}
	if len(rep.Configured) != 1 || rep.Configured[0].ID != "existing" || !rep.Configured[0].Discovered {
		t.Fatalf("configured = %+v", rep.Configured)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0].Message, "partial") {
		t.Fatalf("warnings = %+v", rep.Warnings)
	}
}

func TestCmdModelsAddWritesAndRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "My Model.gguf")
	writeModelGGUF(t, path, "My Model", "llama", 3_000_000_000)
	cfgPath := writeModelsConfig(t, dir)

	out, code := captureStdout(t, func() int {
		return cmdModels(cfgPath, []string{"add", path, "--id", "my-model", "--alias", "My Alias", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var added addedModel
	if err := json.Unmarshal([]byte(out), &added); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if added.ID != "my-model" || added.Path != path || added.Alias != "My Alias" {
		t.Fatalf("added = %+v", added)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := cfg.Models["my-model"]; !ok || m.Path != path || m.Alias != "My Alias" {
		t.Fatalf("stored model = %+v, ok = %v", m, ok)
	}
	if _, err := cfg.Resolve("my-model", nil); err != nil {
		t.Fatalf("added model does not resolve: %v", err)
	}

	_, stderr, code := captureOutput(t, func() int {
		return cmdModels(cfgPath, []string{"add", path, "--id", "other"})
	})
	if code == 0 || !strings.Contains(stderr, "already") {
		t.Fatalf("duplicate path exit = %d, stderr = %s", code, stderr)
	}

	other := filepath.Join(dir, "other.gguf")
	writeModelGGUF(t, other, "Other", "llama", 1_000)
	_, stderr, code = captureOutput(t, func() int {
		return cmdModels(cfgPath, []string{"add", other, "--id", "my-model"})
	})
	if code == 0 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("duplicate ID exit = %d, stderr = %s", code, stderr)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("duplicate add changed config: %+v", cfg.Models)
	}
}

func TestCmdModelsAddDefaultsFromMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Qwen 3 4B.gguf")
	writeModelGGUF(t, path, "Qwen3 4B Instruct", "qwen3", 4_000_000_000)
	cfgPath := writeModelsConfig(t, dir)

	out, code := captureStdout(t, func() int {
		return cmdModels(cfgPath, []string{"add", path})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	if !strings.Contains(out, "added models.qwen-3-4b") {
		t.Fatalf("output = %s", out)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := cfg.Models["qwen-3-4b"]
	if !ok || m.Alias != "Qwen3 4B Instruct" {
		t.Fatalf("model = %+v, ok = %v", m, ok)
	}
}

func TestCmdModelsAddRejectsMissingPath(t *testing.T) {
	cfgPath := writeModelsConfig(t, t.TempDir())
	_, stderr, code := captureOutput(t, func() int {
		return cmdModels(cfgPath, []string{"add", filepath.Join(t.TempDir(), "missing.gguf")})
	})
	if code == 0 || !strings.Contains(stderr, "no such file") {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
}
