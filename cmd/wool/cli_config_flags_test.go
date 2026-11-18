package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCmdConfigCheckWarnsAboutUnknownFlagButPasses(t *testing.T) {
	path := writeCheckConfig(t, "[defaults.extra]\nbrand-new = \"1\"\n")
	out, stderr, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check"})
	})
	if code != 0 || !strings.Contains(out, "config OK") {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	if !strings.Contains(stderr, "brand-new") || !strings.Contains(stderr, "not in the llama-server flag catalog") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestCmdConfigCheckJSONIncludesFlagWarnings(t *testing.T) {
	path := writeCheckConfig(t, "[defaults.extra]\nmodel-alias = \"legacy\"\n")
	out, _, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got configCheckOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || len(got.Warnings) != 1 {
		t.Fatalf("output = %+v", got)
	}
	if got.Warnings[0].Kind != "deprecated" || !strings.Contains(got.Warnings[0].Message, "--alias") {
		t.Fatalf("warning = %+v", got.Warnings[0])
	}
}

func TestCmdConfigCheckStrictPromotesFlagWarnings(t *testing.T) {
	path := writeCheckConfig(t, "[defaults.extra]\nbrand-new = \"1\"\n")
	_, stderr, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check", "--strict"})
	})
	if code != 1 || !strings.Contains(stderr, "brand-new") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestCmdConfigCheckConflictStaysAdvisoryInStrictMode(t *testing.T) {
	path := writeCheckConfig(t, "[defaults.extra]\nctx-size = \"4096\"\n")
	_, stderr, code := captureOutput(t, func() int {
		return cmdConfig(path, []string{"check", "--strict"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "duplicates first-class option") {
		t.Fatalf("stderr = %q", stderr)
	}
}
