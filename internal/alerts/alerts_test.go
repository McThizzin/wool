package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wool/internal/config"
)

func testConfig() Config {
	return Config{
		Enabled:     true,
		TempC:       90,
		VRAMPercent: 95,
		Cooldown:    5 * time.Minute,
	}
}

func TestFromConfigDefaults(t *testing.T) {
	c := FromConfig(config.AlertsConfig{Enabled: true, Cooldown: "soon"})
	if c.Cooldown != defaultCooldown {
		t.Fatalf("cooldown = %s, want default", c.Cooldown)
	}
	c = FromConfig(config.AlertsConfig{Cooldown: "2m"})
	if c.Cooldown != 2*time.Minute {
		t.Fatalf("cooldown = %s, want 2m", c.Cooldown)
	}
}

func TestTemperatureAndVRAMCooldown(t *testing.T) {
	e := New(testConfig())
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	in := Input{At: at, HasTemp: true, TempC: 92, HasVRAM: true, VRAMPercent: 97}

	got := e.Evaluate(in)
	if len(got) != 2 {
		t.Fatalf("first evaluation = %+v, want temperature and vram", got)
	}
	if got[0].Rule != RuleTemperature || got[1].Rule != RuleVRAM {
		t.Fatalf("rules = %q, %q", got[0].Rule, got[1].Rule)
	}

	in.At = at.Add(4 * time.Minute)
	if got := e.Evaluate(in); len(got) != 0 {
		t.Fatalf("inside cooldown = %+v, want none", got)
	}

	in.At = at.Add(5*time.Minute + time.Second)
	if got := e.Evaluate(in); len(got) != 2 {
		t.Fatalf("after cooldown = %+v, want both rules", got)
	}
}

func TestThresholdsDisabledAndBelow(t *testing.T) {
	e := New(testConfig())
	at := time.Now()
	got := e.Evaluate(Input{At: at, HasTemp: true, TempC: 89, HasVRAM: true, VRAMPercent: 94})
	if len(got) != 0 {
		t.Fatalf("below thresholds = %+v", got)
	}
	if got := New(Config{}).Evaluate(Input{At: at, HasTemp: true, TempC: 120}); len(got) != 0 {
		t.Fatalf("disabled engine = %+v", got)
	}
}

func TestHealthFailureHasGraceAndClears(t *testing.T) {
	e := New(testConfig())
	started := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	inst := Instance{Name: "server", Alive: true, Health: "unreachable", StartedAt: started}

	got := e.Evaluate(Input{At: started.Add(5 * time.Second), Instances: []Instance{inst}})
	if len(got) != 0 {
		t.Fatalf("health alert inside grace = %+v", got)
	}
	got = e.Evaluate(Input{At: started.Add(16 * time.Second), Instances: []Instance{inst}})
	if len(got) != 1 || got[0].Rule != RuleHealth || got[0].Subject != "server" {
		t.Fatalf("health alert after grace = %+v", got)
	}

	ok := inst
	ok.Health = "ok"
	if got := e.Evaluate(Input{At: started.Add(17 * time.Second), Instances: []Instance{ok}}); len(got) != 0 {
		t.Fatalf("healthy evaluation = %+v", got)
	}
	// A failure right after recovery is still inside the cooldown window.
	got = e.Evaluate(Input{At: started.Add(18 * time.Second), Instances: []Instance{inst}})
	if len(got) != 0 {
		t.Fatalf("health alert inside cooldown after recovery = %+v", got)
	}
}

func TestHealthLoadingIsNotAFailure(t *testing.T) {
	e := New(testConfig())
	started := time.Now().Add(-time.Minute)
	for _, status := range []string{"ok", "loading model", "loading", "starting", ""} {
		got := e.Evaluate(Input{At: time.Now(), Instances: []Instance{{
			Name: "server", Alive: true, Health: status, StartedAt: started,
		}}})
		if len(got) != 0 {
			t.Fatalf("%q produced %+v", status, got)
		}
	}
}

func TestOOMDeduplicatesTheSameLine(t *testing.T) {
	e := New(testConfig())
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	line := "llama_init_from_gpt_params: error: CUDA error: out of memory"
	in := Input{At: at, Logs: map[string][]string{"server": {"loading", line}}}

	got := e.Evaluate(in)
	if len(got) != 1 || got[0].Rule != RuleOOM || got[0].Subject != "server" {
		t.Fatalf("oom = %+v", got)
	}
	in.At = at.Add(6 * time.Minute)
	if got := e.Evaluate(in); len(got) != 0 {
		t.Fatalf("same oom line refired = %+v", got)
	}

	// Once the line leaves the tail, a later identical event can fire again.
	in.At = at.Add(7 * time.Minute)
	in.Logs["server"] = []string{"loading"}
	if got := e.Evaluate(in); len(got) != 0 {
		t.Fatalf("cleared log = %+v", got)
	}
	in.At = at.Add(8 * time.Minute)
	in.Logs["server"] = []string{"loading", line}
	if got := e.Evaluate(in); len(got) != 1 {
		t.Fatalf("reappeared oom = %+v", got)
	}
}

func TestModelLoadFailure(t *testing.T) {
	e := New(testConfig())
	at := time.Now()
	got := e.Evaluate(Input{At: at, Models: []Model{{ID: "broken", Status: "failed", Failed: true}}})
	if len(got) != 1 || got[0].Rule != RuleModelLoad || got[0].Subject != "broken" {
		t.Fatalf("model load = %+v", got)
	}
}

func TestRestartLoop(t *testing.T) {
	e := New(testConfig())
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	inst := func(started time.Time) Instance {
		return Instance{Name: "server", Alive: true, Health: "ok", StartedAt: started}
	}

	if got := e.Evaluate(Input{At: at, Instances: []Instance{inst(at)}}); len(got) != 0 {
		t.Fatalf("first observation = %+v", got)
	}
	for i := 1; i <= 2; i++ {
		got := e.Evaluate(Input{At: at.Add(time.Duration(i) * time.Minute), Instances: []Instance{inst(at.Add(time.Duration(i) * time.Minute))}})
		if len(got) != 0 {
			t.Fatalf("restart %d = %+v, want none", i, got)
		}
	}
	got := e.Evaluate(Input{At: at.Add(3 * time.Minute), Instances: []Instance{inst(at.Add(3 * time.Minute))}})
	if len(got) != 1 || got[0].Rule != RuleRestartLoop {
		t.Fatalf("third restart = %+v", got)
	}
}

type fakeNotifier struct {
	mu   sync.Mutex
	seen []Alert
	err  error
}

func (f *fakeNotifier) Notify(_ context.Context, a Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, a)
	return f.err
}

func TestDispatchUsesNotifiers(t *testing.T) {
	f := &fakeNotifier{}
	Dispatch(context.Background(), []Notifier{f}, []Alert{
		{Rule: RuleTemperature, Message: "hot"},
		{Rule: RuleOOM, Message: "oom"},
	})
	if len(f.seen) != 2 || f.seen[0].Rule != RuleTemperature || f.seen[1].Rule != RuleOOM {
		t.Fatalf("dispatched = %+v", f.seen)
	}
}

func TestDesktopNotifierMissingBinary(t *testing.T) {
	err := (DesktopNotifier{Bin: "wool-test-no-such-notifier"}).Notify(context.Background(), Alert{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestWebhookPostsJSONAndHidesURL(t *testing.T) {
	var payload webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	a := Alert{At: time.Now(), Rule: RuleHealth, Severity: SeverityCritical, Subject: "server", Message: "down"}
	if err := (WebhookNotifier{URL: srv.URL}).Notify(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if payload.Version != WebhookVersion || payload.Rule != RuleHealth || payload.Subject != "server" {
		t.Fatalf("payload = %+v", payload)
	}

	// A connection failure must not echo the secret in the URL.
	secret := "http://127.0.0.1:1/hook/secret-token"
	err := (WebhookNotifier{URL: secret}).Notify(context.Background(), a)
	if err == nil {
		t.Fatal("expected webhook error")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked webhook URL: %v", err)
	}
}
