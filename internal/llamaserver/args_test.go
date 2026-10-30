package llamaserver

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wool/internal/config"
)

func loadExample(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func TestSingleArgs(t *testing.T) {
	cfg := loadExample(t)
	r, err := cfg.Resolve("qwen35-4b-mtp", nil)
	if err != nil {
		t.Fatal(err)
	}
	args := SingleArgs(r, "127.0.0.1", 8080)
	for _, p := range [][2]string{
		{"--model", r.ModelPath},
		{"--host", "127.0.0.1"},
		{"--port", "8080"},
		{"--alias", "qwen3.5-MTP"},
		{"-c", "65536"},
		{"--gpu-layers", "999"},
		{"--threads", strconv.Itoa(r.Threads)},
		{"--flash-attn", "on"},
		{"--cache-type-k", "q4_0"},
		{"--temp", "0.7"},
		{"--top-p", "0.8"},
		{"--top-k", "20"},
		{"--repeat-penalty", "1.05"},
		{"--mirostat", "0"},
		{"--seed", "-1"},
		{"--spec-type", "draft-mtp"},
		{"--spec-draft-n-max", "1"},
	} {
		if !hasPair(args, p[0], p[1]) {
			t.Errorf("missing %s %s in %v", p[0], p[1], args)
		}
	}
	// n_cpu_moe = 0 means "disabled", so it should be omitted.
	if hasFlag(args, "--n-cpu-moe") {
		t.Error("--n-cpu-moe 0 should be omitted")
	}
	if !hasFlag(args, "--metrics") || !hasFlag(args, "--jinja") {
		t.Errorf("expected metrics and jinja: %v", args)
	}

	// Extra flags are appended after generated flags so they can win.
	if n := len(args); n < 2 || args[n-2] != "--spec-type" || args[n-1] != "draft-mtp" {
		t.Fatalf("extra flags should be at the end: %v", args)
	}
}

func TestSingleArgsJinjaOff(t *testing.T) {
	cfg := loadExample(t)
	r, err := cfg.Resolve("gemma-4-heretic", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Jinja = false
	args := SingleArgs(r, "127.0.0.1", 8080)
	if !hasFlag(args, "--no-jinja") || hasFlag(args, "--jinja") {
		t.Fatalf("jinja flags wrong: %v", args)
	}
}

func TestRouterArgs(t *testing.T) {
	cfg := loadExample(t)
	ini := "/tmp/models.ini"
	args := RouterArgs(cfg, "127.0.0.1", 8080, ini)
	for _, p := range [][2]string{
		{"--host", "127.0.0.1"},
		{"--port", "8080"},
		{"--models-preset", ini},
		{"--models-max", "1"},
		{"--fit-target", "512"},
	} {
		if !hasPair(args, p[0], p[1]) {
			t.Errorf("missing %s %s in %v", p[0], p[1], args)
		}
	}
	if !hasFlag(args, "--no-models-autoload") {
		t.Errorf("autoload should be disabled: %v", args)
	}
	if !hasFlag(args, "--metrics") {
		t.Errorf("metrics should be enabled: %v", args)
	}
	if hasFlag(args, "--models-dir") {
		t.Errorf("must not mix --models-dir with --models-preset: %v", args)
	}
	if hasFlag(args, "-c") {
		t.Errorf("ctx must come from the INI, not the CLI: %v", args)
	}
}

func TestRenderRouterINI(t *testing.T) {
	cfg := loadExample(t)
	data, err := RenderRouterINI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	if !strings.Contains(text, "[*]\n") {
		t.Fatal("missing global section")
	}
	if !strings.Contains(text, "\n[qwen35-4b-mtp]\n") {
		t.Fatal("missing model section")
	}
	if strings.Contains(text, "models-dir") {
		t.Fatal("router INI must not mention models-dir")
	}
	if !strings.Contains(text, "spec-type = draft-mtp") {
		t.Fatal("per-model extra options missing")
	}
	if !strings.Contains(text, "ctx-size = 65536") {
		t.Fatal("qwen ctx missing")
	}
	// Globals must use the defaults layer, not one model's overrides.
	if !strings.Contains(text, "ctx-size = 32768") {
		t.Fatal("default ctx missing from [*]")
	}

	sections := parseINI(t, text)
	if len(sections) != len(cfg.Models)+1 {
		t.Fatalf("sections = %d, want %d\n%s", len(sections), len(cfg.Models)+1, text)
	}
	qwen := sections["qwen35-4b-mtp"]
	for k, want := range map[string]string{
		"model":     config.AbsPath(cfg.Models["qwen35-4b-mtp"].Path),
		"alias":     "qwen3.5-MTP",
		"ctx-size":  "65536",
		"temp":      "0.7",
		"top-p":     "0.8",
		"top-k":     "20",
		"spec-type": "draft-mtp",
	} {
		if qwen[k] != want {
			t.Errorf("%s = %q, want %q", k, qwen[k], want)
		}
	}
	// Flags with a dash in their name keep the exact INI key.
	if _, ok := qwen["ctx_size"]; ok {
		t.Error("INI keys must use dashes, not underscores")
	}
}

func parseINI(t *testing.T, text string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	current := ""
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if _, dup := out[current]; dup {
				t.Fatalf("line %d: duplicate section %q", i+1, current)
			}
			out[current] = map[string]string{}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			t.Fatalf("line %d: bad INI line %q", i+1, line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if _, dup := out[current][key]; dup {
			t.Fatalf("line %d: duplicate key %q in %q", i+1, key, current)
		}
		out[current][key] = val
	}
	return out
}

func TestWriteRouterINI(t *testing.T) {
	cfg := loadExample(t)
	path := filepath.Join(t.TempDir(), "router", "models.ini")
	if err := WriteRouterINI(cfg, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestCommandLine(t *testing.T) {
	got := CommandLine("llama-server", []string{"--model", "/tmp/a b.gguf", "--alias", "x"})
	want := "llama-server --model '/tmp/a b.gguf' --alias x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
