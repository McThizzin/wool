package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCmdFlagsJSONHasStableSchema(t *testing.T) {
	out, stderr, code := captureOutput(t, func() int {
		return cmdFlags("", []string{"--json"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var got flagsOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != flagsSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got.Version, flagsSchemaVersion)
	}
	if got.Source.Version == "" || got.Source.Commit == "" {
		t.Fatalf("source = %+v", got.Source)
	}
	if len(got.Flags) == 0 {
		t.Fatal("no flags in JSON output")
	}
	found := false
	for _, f := range got.Flags {
		if f.Name == "" || f.Value == "" || f.Description == "" || f.Category == "" {
			t.Fatalf("incomplete flag: %+v", f)
		}
		if f.Aliases == nil || f.Source != got.Source {
			t.Fatalf("flag %q has unstable metadata: %+v", f.Name, f)
		}
		if f.Name == "ctx-size" {
			found = true
		}
	}
	if !found {
		t.Fatal("ctx-size missing from JSON output")
	}

	out2, _, code := captureOutput(t, func() int {
		return cmdFlags("", []string{"--json"})
	})
	if code != 0 || out2 != out {
		t.Fatal("wool flags --json is not byte-stable")
	}
}

func TestCmdFlagsSearchAndTextOutput(t *testing.T) {
	out, _, code := captureOutput(t, func() int {
		return cmdFlags("", []string{"--json", "--search", "gpu"})
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got flagsOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Flags) == 0 || got.Flags[0].Name != "gpu-layers" {
		t.Fatalf("search results = %+v", got.Flags)
	}

	text, _, code := captureOutput(t, func() int {
		return cmdFlags("", []string{"--search", "ctx"})
	})
	if code != 0 || !strings.Contains(text, "--ctx-size") {
		t.Fatalf("exit = %d, output = %q", code, text)
	}

	_, stderr, code := captureOutput(t, func() int {
		return cmdFlags("", []string{"--search", "zzzzzzzzzzzzzzzz"})
	})
	if code != 1 || !strings.Contains(stderr, "no flags match") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestCmdFlagsCheckReportsDriftAndStrictFails(t *testing.T) {
	dir := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	help, err := os.ReadFile(filepath.Join(root, "tools", "flagsgen", "data", "llama-server-help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "llama-server")
	script := "#!/bin/sh\ncase \"$1\" in\n  --version) echo 'version: b6000' ;;\n  --help) cat <<'HELP'\n" +
		string(help) + "      --brand-new N              a newly added flag\nHELP\n  ;;\n  *) exit 2 ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf("[app]\nllama_server = %s\n", strconv.Quote(bin))), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stderr, code := captureOutput(t, func() int {
		return cmdFlags(cfgPath, []string{"--check", "--json"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var got flagsCheckOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != flagsSchemaVersion {
		t.Fatalf("schema version = %d", got.Version)
	}
	if got.Result.OK {
		t.Fatal("check reported no drift")
	}
	if len(got.Result.New) != 1 || got.Result.New[0] != "brand-new" {
		t.Fatalf("new = %q", got.Result.New)
	}
	if got.Result.InstalledSource.Version != "b6000" {
		t.Fatalf("installed source = %+v", got.Result.InstalledSource)
	}

	text, _, code := captureOutput(t, func() int {
		return cmdFlags(cfgPath, []string{"--check"})
	})
	if code != 0 || !strings.Contains(text, "new: brand-new") {
		t.Fatalf("exit = %d, output = %q", code, text)
	}

	_, _, code = captureOutput(t, func() int {
		return cmdFlags(cfgPath, []string{"--check", "--json", "--strict"})
	})
	if code != 1 {
		t.Fatalf("strict exit = %d, want 1", code)
	}
}

func TestCmdFlagsStrictRequiresCheck(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int {
		return cmdFlags("", []string{"--strict"})
	})
	if code != 2 || !strings.Contains(stderr, "--strict requires --check") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}
