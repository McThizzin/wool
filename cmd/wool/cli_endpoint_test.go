package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wool/internal/endpoint"
)

func TestParseEndpointArgs(t *testing.T) {
	o, err := parseEndpointArgs([]string{"router", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if o.target != "router" || !o.asJSON {
		t.Fatalf("options = %+v", o)
	}
	o, err = parseEndpointArgs(nil)
	if err != nil || o.target != "" || o.asJSON {
		t.Fatalf("options = %+v, err = %v", o, err)
	}
	for _, args := range [][]string{
		{"a", "b"},
		{"--bogus"},
	} {
		if _, err := parseEndpointArgs(args); err == nil {
			t.Errorf("parseEndpointArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestCmdEndpointServerTextAndJSON(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"served-model"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "server", host, port, "configured-model")

	out, errs, code := captureOutput(t, func() int { return cmdEndpoint("", nil) })
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	for _, want := range []string{
		"server · " + srv.URL,
		"base URL  " + srv.URL,
		"API base  " + srv.URL + "/v1",
		"model     served-model",
		"curl", "openai-python", "continue", "open-webui",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"Bearer ", "sk-", "Authorization:"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output contains %q:\n%s", bad, out)
		}
	}

	raw, errs, code := captureOutput(t, func() int {
		return cmdEndpoint("", []string{"server", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	var ep endpoint.Endpoint
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, raw)
	}
	if ep.Version != endpoint.Version || ep.Source != "server" || ep.Model != "served-model" {
		t.Fatalf("endpoint = %+v", ep)
	}
	if ep.BaseURL != srv.URL || ep.APIBase != srv.URL+"/v1" {
		t.Fatalf("URLs = %q / %q", ep.BaseURL, ep.APIBase)
	}
	if len(ep.Snippets) != 4 {
		t.Fatalf("snippets = %d", len(ep.Snippets))
	}
}

func TestCmdEndpointFallsBackToRecordedModel(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "server", host, port, "recorded-model")

	raw, errs, code := captureOutput(t, func() int {
		return cmdEndpoint("", []string{"--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	var ep endpoint.Endpoint
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, raw)
	}
	if ep.Model != "recorded-model" {
		t.Fatalf("model = %q, want the recorded model", ep.Model)
	}
}

func TestCmdEndpointRouterModel(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			io.WriteString(w, `{"data":[
				{"id":"a","status":{"value":"unloaded"}},
				{"id":"b","status":{"value":"loaded"}}
			]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "router", host, port, "")

	raw, errs, code := captureOutput(t, func() int {
		return cmdEndpoint("", []string{"router", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	var ep endpoint.Endpoint
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, raw)
	}
	if ep.Source != "router" || ep.Model != "b" || ep.BaseURL != srv.URL {
		t.Fatalf("endpoint = %+v", ep)
	}

	// Without a target the router is used when no standalone server exists.
	raw, errs, code = captureOutput(t, func() int {
		return cmdEndpoint("", []string{"--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, raw)
	}
	if ep.Model != "b" {
		t.Fatalf("model = %q, want b", ep.Model)
	}

	// An explicit unloaded model is still a valid endpoint.
	raw, errs, code = captureOutput(t, func() int {
		return cmdEndpoint("", []string{"a", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if err := json.Unmarshal([]byte(raw), &ep); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, raw)
	}
	if ep.Model != "a" {
		t.Fatalf("model = %q, want a", ep.Model)
	}

	_, errs, code = captureOutput(t, func() int {
		return cmdEndpoint("", []string{"missing"})
	})
	if code != 1 || !strings.Contains(errs, "router does not list missing") {
		t.Fatalf("stderr = %q (exit %d)", errs, code)
	}
}

func TestCmdEndpointNeedsARunningInstance(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, errs, code := captureOutput(t, func() int {
		return cmdEndpoint("", nil)
	})
	if code != 1 || !strings.Contains(errs, "no server is running") {
		t.Fatalf("stderr = %q, stdout = %q (exit %d)", errs, out, code)
	}
}
