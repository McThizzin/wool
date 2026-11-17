package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wool/internal/llamaserver"
)

func TestCmdRouterGroups(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := `
[models.a]
path = "a.gguf"
[models.b]
path = "b.gguf"
[groups.coding]
models = ["a", "b"]
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := captureStdout(t, func() int {
		return cmdRouter(cfgPath, []string{"groups"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	for _, want := range []string{"coding", "a, b"} {
		if !strings.Contains(out, want) {
			t.Fatalf("groups output missing %q:\n%s", want, out)
		}
	}
}

func TestCmdRouterLoadGroupPartialFailure(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	states := map[string]llamaserver.Model{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/load":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode load body: %v", err)
			}
			if body["model"] == "bad" {
				exit := 1
				states["bad"] = llamaserver.Model{ID: "bad", Status: llamaserver.ModelStatus{
					Value: "unloaded", Failed: true, ExitCode: &exit,
				}}
			} else {
				states[body["model"]] = llamaserver.Model{ID: body["model"], Status: llamaserver.ModelStatus{Value: "loaded"}}
			}
			io.WriteString(w, `{"success":true}`)
		case "/models":
			data := make([]llamaserver.Model, 0, len(states))
			for _, m := range states {
				data = append(data, m)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "router", host, port, "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := `
[models.a]
path = "a.gguf"
[models.bad]
path = "bad.gguf"
[groups.coding]
models = ["a", "bad"]
[router]
models_max = 1
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, code := captureOutput(t, func() int {
		return cmdRouter(cfgPath, []string{"load-group", "--timeout", "200ms", "coding"})
	})
	if code != 1 {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, out, errs)
	}
	for _, want := range []string{"a", "loaded", "bad", "failed", "group coding: 1/2 loaded", "failed: bad"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errs, "models_max is 1") || !strings.Contains(errs, "evicted") {
		t.Fatalf("missing over-limit warning:\n%s", errs)
	}
}

func TestCmdRouterLoadGroupAllLoaded(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	states := map[string]llamaserver.Model{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/load":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			states[body["model"]] = llamaserver.Model{ID: body["model"], Status: llamaserver.ModelStatus{Value: "loaded"}}
			io.WriteString(w, `{"success":true}`)
		case "/models":
			data := make([]llamaserver.Model, 0, len(states))
			for _, m := range states {
				data = append(data, m)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "router", host, port, "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := `
[models.a]
path = "a.gguf"
[groups.single]
models = ["a"]
[router]
models_max = 1
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, code := captureOutput(t, func() int {
		return cmdRouter(cfgPath, []string{"load-group", "--timeout", "200ms", "single"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, out, errs)
	}
	if !strings.Contains(out, "group single: 1/1 loaded") {
		t.Fatalf("stdout = %s", out)
	}
	if strings.Contains(errs, "warning:") {
		t.Fatalf("unexpected warning: %s", errs)
	}
}

func TestCmdRouterLoadGroupUnknown(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := `
[models.a]
path = "a.gguf"
[groups.coding]
models = ["a"]
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errs, code := captureOutput(t, func() int {
		return cmdRouter(cfgPath, []string{"load-group", "missing"})
	})
	if code != 1 {
		t.Fatalf("exit = %d, stdout = %s", code, out)
	}
	if !strings.Contains(errs, "unknown group") || !strings.Contains(errs, "coding") {
		t.Fatalf("stderr = %s", errs)
	}
}
