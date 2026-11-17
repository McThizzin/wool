package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeAlertsConfig(t *testing.T, enabled bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[alerts]\nenabled = " + strconv.FormatBool(enabled) + "\n\n[models.m]\npath = \"m.gguf\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCmdAlertsDisabled(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfgPath := writeAlertsConfig(t, false)

	out, errs, code := captureOutput(t, func() int { return cmdAlerts(cfgPath, nil) })
	if code != 0 || errs != "" {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
	if !strings.Contains(out, "alerts disabled") {
		t.Fatalf("output = %q", out)
	}

	out, code = captureStdout(t, func() int { return cmdAlerts(cfgPath, []string{"--json"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var snap alertsSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if snap.Version != alertsVersion || snap.Enabled || snap.Alerts == nil || len(snap.Alerts) != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestCmdAlertsNoConditions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfgPath := writeAlertsConfig(t, true)

	out, errs, code := captureOutput(t, func() int { return cmdAlerts(cfgPath, nil) })
	if code != 0 || errs != "" {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
	if strings.TrimSpace(out) != "no alerts" {
		t.Fatalf("output = %q", out)
	}
}

func TestCmdAlertsOOMFromLogTail(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	logPath := filepath.Join(t.TempDir(), "server.log")
	body := "starting server\nllama_init: CUDA error: out of memory\n"
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceStateWithLog(t, stateHome, "server", host, port, "m", logPath)

	cfgPath := writeAlertsConfig(t, true)
	out, code := captureStdout(t, func() int { return cmdAlerts(cfgPath, nil) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	for _, want := range []string{"oom", "out of memory"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	out, code = captureStdout(t, func() int { return cmdAlerts(cfgPath, []string{"--json"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var snap alertsSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if !snap.Enabled || len(snap.Alerts) != 1 || snap.Alerts[0].Rule != "oom" || snap.Alerts[0].Subject != "server" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestCmdAlertsUsage(t *testing.T) {
	out, code := captureStdout(t, func() int { return cmdAlerts("", []string{"--help"}) })
	if code != 0 || !strings.Contains(out, "wool alerts") {
		t.Fatalf("help exit = %d, output = %q", code, out)
	}
	out, errs, code := captureOutput(t, func() int { return cmdAlerts("", []string{"--bogus"}) })
	if code != 2 || !strings.Contains(errs, "unknown option") || out != "" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, out, errs)
	}
}
