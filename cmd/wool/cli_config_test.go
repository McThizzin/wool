package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wool/internal/config"
)

func writeCheckConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCmdConfigCheckValid(t *testing.T) {
	path := writeCheckConfig(t, "[server]\nport = 8080\n")
	out, stderr, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(out, "config OK") {
		t.Fatalf("output = %q", out)
	}
}

func TestCmdConfigCheckMalformedReportsPosition(t *testing.T) {
	path := writeCheckConfig(t, "[server\nport = 8080\n")
	_, stderr, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check"})
	})
	if code == 0 {
		t.Fatalf("exit = %d", code)
	}
	pattern := regexp.MustCompile(`(?m)^wool: ` + regexp.QuoteMeta(path) + `:\d+:\d+: `)
	if !pattern.MatchString(stderr) {
		t.Fatalf("stderr missing path:line:col: %s", stderr)
	}
}

func TestCmdConfigCheckStrictUnknownKey(t *testing.T) {
	path := writeCheckConfig(t, "[server]\nport = 8080\nprot = 9090\n")
	_, stderr, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("non-strict exit = %d, stderr = %s", code, stderr)
	}

	_, stderr, code = captureOutput(t, func() int {
		return cmdConfig(path, []string{"check", "--strict"})
	})
	if code == 0 || !strings.Contains(stderr, "prot") {
		t.Fatalf("strict exit = %d, stderr = %s", code, stderr)
	}
	pattern := regexp.MustCompile(`(?m)^wool: ` + regexp.QuoteMeta(path) + `:\d+:\d+: .*prot`)
	if !pattern.MatchString(stderr) {
		t.Fatalf("strict stderr missing key with position: %s", stderr)
	}
}

func TestCmdConfigCheckJSON(t *testing.T) {
	path := writeCheckConfig(t, "[server]\nport = 8080\nprot = 9090\n")
	out, _, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check", "--strict", "--json"})
	})
	if code != 1 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var result configCheckOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if result.OK || !result.Strict || len(result.Errors) != 1 {
		t.Fatalf("result = %+v", result)
	}
	e := result.Errors[0]
	if e.File != path || e.Line != 3 || e.Column < 1 || !strings.Contains(e.Key, "prot") || e.Message == "" {
		t.Fatalf("error = %+v", e)
	}

	out, _, code = captureOutput(t, func() int {
		return cmdConfig(path, []string{"check", "--json"})
	})
	if code != 0 {
		t.Fatalf("non-strict exit = %d, output = %s", code, out)
	}
	result = configCheckOutput{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !result.OK || result.Strict || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestPrintConfigErrorFormatsStructured(t *testing.T) {
	err := &config.Error{File: "/tmp/config.toml", Line: 4, Column: 9, Key: "server.prt", Message: "unknown field"}
	_, stderr, _ := captureOutput(t, func() int {
		printConfigError("/tmp/config.toml", err)
		return 0
	})
	if !strings.Contains(stderr, "wool: /tmp/config.toml:4:9: unknown field \"server.prt\"\n") {
		t.Fatalf("stderr = %q", stderr)
	}
}
