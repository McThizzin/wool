package llamaserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestHealthAndWaitReady(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"status":"loading model"}`)
			return
		}
		io.WriteString(w, `{"status":"ok"}`)
	})
	if err := c.WaitReady(context.Background(), 2*time.Second, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestModelsAndActions(t *testing.T) {
	var sawReload, sawLoadBody bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/models":
			sawReload = r.URL.Query().Get("reload") == "1"
			io.WriteString(w, `{"data":[{"id":"a","aliases":["aa"],"status":{"value":"loaded","args":["--model","x"]}},{"id":"b","status":{"value":"unloaded","failed":true,"exit_code":1}}]}`)
		case r.URL.Path == "/models/load":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			sawLoadBody = body["model"] == "a"
			io.WriteString(w, `{"success":true}`)
		case r.URL.Path == "/models/unload":
			io.WriteString(w, `{"success":true}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	models, err := c.Models(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !sawReload {
		t.Error("reload query missing")
	}
	if len(models) != 2 || models[0].ID != "a" || models[1].Status.ExitCode == nil || *models[1].Status.ExitCode != 1 {
		t.Fatalf("models = %+v", models)
	}
	if err := c.Load(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if !sawLoadBody {
		t.Error("load body missing model")
	}
	if err := c.Unload(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
}

func TestActionRejected(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":400,"message":"model is not running","type":"invalid_request_error"}}`)
	})
	err := c.Unload(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "model is not running") {
		t.Fatalf("err = %v", err)
	}
}

func TestSlotsAndMetrics(t *testing.T) {
	var slotQuery, metricQuery string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots":
			slotQuery = r.URL.RawQuery
			io.WriteString(w, `[{"id":0,"n_ctx":4096,"n_past":12,"is_processing":true}]`)
		case "/metrics":
			metricQuery = r.URL.RawQuery
			io.WriteString(w, `# HELP llamacpp:prompt_tokens_total docs
llamacpp:prompt_tokens_total 100
llamacpp:prompt_tokens_cached_total 20
llamacpp:prompt_seconds_total 2
llamacpp:tokens_predicted_total 200
llamacpp:tokens_predicted_seconds_total 4
llamacpp:spec_decode_num_draft_tokens_total 50
llamacpp:spec_decode_num_accepted_tokens_total 40
llamacpp:requests_processing{slot="0"} 1
`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	slots, err := c.Slots(context.Background(), "a b")
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].NPast != 12 || !slots[0].IsProcessing {
		t.Fatalf("slots = %+v", slots)
	}
	if slotQuery != "model=a+b" {
		t.Fatalf("slot query = %q", slotQuery)
	}

	m, err := c.Metrics(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if metricQuery != "model=a" {
		t.Fatalf("metric query = %q", metricQuery)
	}
	if m.PromptTPS() != 50 || m.PredictedTPS() != 50 {
		t.Fatalf("tps = %v/%v", m.PromptTPS(), m.PredictedTPS())
	}
	if m.SpecAcceptRate() != 80 {
		t.Fatalf("accept = %v", m.SpecAcceptRate())
	}
	if m.RequestsProcessing != 1 || m.PromptTokensCached != 20 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestParseMetricsToleratesGarbage(t *testing.T) {
	m := ParseMetrics("nonsense\n# comment\nllamacpp:x_total not-a-number\nllamacpp:x_total 5\n")
	if m.Raw["llamacpp:x_total"] != 5 {
		t.Fatalf("raw = %v", m.Raw)
	}
}

func TestBaseURL(t *testing.T) {
	for host, want := range map[string]string{
		"0.0.0.0":            "http://127.0.0.1:8080",
		"::":                 "http://[::1]:8080",
		"::1":                "http://[::1]:8080",
		"localhost,10.0.0.2": "http://localhost:8080",
	} {
		if got := BaseURL(host, 8080); got != want {
			t.Errorf("BaseURL(%q) = %q, want %q", host, got, want)
		}
	}
}
