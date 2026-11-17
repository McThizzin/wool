package main

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseStatsArgs(t *testing.T) {
	o, err := parseStatsArgs([]string{"--window", "2s", "mymodel", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if o.model != "mymodel" || o.window != 2*time.Second || !o.asJSON {
		t.Fatalf("options = %+v", o)
	}
	o, err = parseStatsArgs([]string{"--window=500ms"})
	if err != nil || o.window != 500*time.Millisecond || o.model != "" || o.asJSON {
		t.Fatalf("options = %+v, err = %v", o, err)
	}
	for _, args := range [][]string{
		{"--window"},         // missing value
		{"--window", "0"},    // non-positive
		{"--window", "soon"}, // not a duration
		{"--window", "-1s"},  // negative
		{"a", "b"},           // two models
		{"--bogus"},          // unknown option
	} {
		if _, err := parseStatsArgs(args); err == nil {
			t.Errorf("parseStatsArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestCmdStatsServerTextAndJSON(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	scrapes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			scrapes++
			if scrapes%2 == 1 {
				io.WriteString(w, `# HELP llamacpp:prompt_tokens_total docs
llamacpp:prompt_tokens_total 1000
llamacpp:prompt_seconds_total 2
llamacpp:tokens_predicted_total 2000
llamacpp:tokens_predicted_seconds_total 4
llamacpp:spec_decode_num_draft_tokens_total 100
llamacpp:spec_decode_num_accepted_tokens_total 60
llamacpp:requests_processing 2
llamacpp:requests_deferred 1
`)
				return
			}
			io.WriteString(w, `# HELP llamacpp:prompt_tokens_total docs
llamacpp:prompt_tokens_total 3000
llamacpp:prompt_seconds_total 6
llamacpp:tokens_predicted_total 4000
llamacpp:tokens_predicted_seconds_total 8
llamacpp:spec_decode_num_draft_tokens_total 250
llamacpp:spec_decode_num_accepted_tokens_total 160
llamacpp:requests_processing 3
llamacpp:requests_deferred 0
`)
		case "/slots":
			io.WriteString(w, `[{"id":0,"n_ctx":4096,"n_past":12,"is_processing":true},{"id":1,"n_ctx":4096,"n_past":18}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "server", host, port, "m")

	out, code := captureStdout(t, func() int {
		return cmdStats("", []string{"--window", "20ms"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	for _, want := range []string{
		"server m · http://",
		"prompt:      500.0 t/s (2000 tokens)",
		"generation: 500.0 t/s (2000 tokens)",
		"lifetime:    500.0 prompt · 500.0 gen t/s",
		"requests:   3 processing · 0 deferred",
		"slots:       1/2 busy · KV 30/8.2k tok (0%)",
		"speculative: 67% accepted",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if scrapes != 2 {
		t.Fatalf("scrapes = %d, want 2", scrapes)
	}

	out, code = captureStdout(t, func() int {
		return cmdStats("", []string{"--json", "--window", "20ms"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var snap statsSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if snap.Version != statsVersion || snap.Source != "server" || snap.Model != "m" {
		t.Fatalf("identity = %+v", snap)
	}
	if snap.PromptTPS != 500 || snap.GenTPS != 500 || snap.PromptTokens != 2000 || snap.GenTokens != 2000 {
		t.Fatalf("rates = %+v", snap)
	}
	if snap.LifetimePromptTPS != 500 || snap.LifetimeGenTPS != 500 {
		t.Fatalf("lifetime = %+v", snap)
	}
	if snap.RequestsProcessing != 3 || snap.RequestsDeferred != 0 {
		t.Fatalf("requests = %+v", snap)
	}
	if snap.Slots.Total != 2 || snap.Slots.Busy != 1 || snap.Slots.KVUsed != 30 || snap.Slots.KVCap != 8192 {
		t.Fatalf("slots = %+v", snap)
	}
	if !snap.Speculative || math.Abs(snap.SpecAcceptPercent-66.6667) > 1e-3 {
		t.Fatalf("spec = %+v", snap)
	}
	if snap.CounterReset || snap.WindowSeconds <= 0 {
		t.Fatalf("window/reset = %v/%v", snap.WindowSeconds, snap.CounterReset)
	}
}

func TestCmdStatsRouterModel(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			io.WriteString(w, `{"data":[
				{"id":"a","status":{"value":"unloaded"}},
				{"id":"b","status":{"value":"sleeping"}}
			]}`)
		case "/metrics":
			if r.URL.Query().Get("model") != "b" {
				t.Errorf("metrics model query = %q", r.URL.Query().Get("model"))
			}
			io.WriteString(w, "llamacpp:prompt_tokens_total 100\nllamacpp:prompt_seconds_total 1\n")
		case "/slots":
			if r.URL.Query().Get("model") != "b" {
				t.Errorf("slots model query = %q", r.URL.Query().Get("model"))
			}
			io.WriteString(w, `[{"id":0,"n_ctx":1024,"n_past":256,"is_processing":true}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "router", host, port, "")

	out, code := captureStdout(t, func() int {
		return cmdStats("", []string{"b", "--window", "20ms", "--json"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out)
	}
	var snap statsSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out)
	}
	if snap.Source != "router" || snap.Model != "b" || snap.Slots.Busy != 1 || snap.Slots.KVFill != 25 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// An explicit model the router does not list is an error, not zeros.
	out, errs, code := captureOutput(t, func() int {
		return cmdStats("", []string{"missing", "--window", "20ms"})
	})
	if code != 1 || !strings.Contains(errs, "router does not list missing") {
		t.Fatalf("missing model stderr = %q (exit %d, stdout %q)", errs, code, out)
	}
}

func TestCmdStatsMetricsDisabledFails(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			w.WriteHeader(http.StatusNotFound)
		case "/slots":
			io.WriteString(w, `[{"id":0,"n_ctx":4096,"n_past":64}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	writeInstanceState(t, stateHome, "server", host, port, "m")

	out, errs, code := captureOutput(t, func() int {
		return cmdStats("", []string{"--window", "20ms"})
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr = %s", code, errs)
	}
	if !strings.Contains(errs, "metrics are disabled") {
		t.Fatalf("stderr = %q", errs)
	}
	// Zeros must not be presented as facts on stdout.
	if strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
}

func TestCmdStatsNeedsARunningServer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out, errs, code := captureOutput(t, func() int {
		return cmdStats("", nil)
	})
	if code != 1 || !strings.Contains(errs, "no server is running") {
		t.Fatalf("stderr = %q, stdout = %q (exit %d)", errs, out, code)
	}
}
