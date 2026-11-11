package llamaserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRatesFirstUpdateOnlySeeds(t *testing.T) {
	var r Rates
	r.Update(Metrics{PromptTokens: 100, PredictedTokens: 200}, time.Unix(1000, 0))
	if !r.Seeded {
		t.Fatal("first update did not seed")
	}
	if r.Window != 0 || r.PromptTPS != 0 || r.GenTPS != 0 || r.Reset {
		t.Fatalf("seed update produced rates: %+v", r)
	}
}

func TestRatesDeltasPreferBusySeconds(t *testing.T) {
	var r Rates
	at := time.Unix(1000, 0)
	r.Update(Metrics{PromptTokens: 1000, PromptSeconds: 2, PredictedTokens: 4000, PredictedSeconds: 8}, at)
	// Wall clock is 10s; the busy counters must win so idle time is not
	// counted against throughput.
	r.Update(Metrics{PromptTokens: 2000, PromptSeconds: 4, PredictedTokens: 8000, PredictedSeconds: 12}, at.Add(10*time.Second))
	if r.Window != 10*time.Second {
		t.Fatalf("window = %v", r.Window)
	}
	if r.PromptTPS != 500 || r.GenTPS != 1000 {
		t.Fatalf("tps = %v/%v, want 500/1000", r.PromptTPS, r.GenTPS)
	}
	if r.PromptTokens != 1000 || r.GenTokens != 4000 {
		t.Fatalf("token deltas = %v/%v", r.PromptTokens, r.GenTokens)
	}
	if r.Reset {
		t.Fatal("steady counters reported a reset")
	}
}

func TestRatesWallClockFallback(t *testing.T) {
	var r Rates
	at := time.Unix(2000, 0)
	r.Update(Metrics{PromptTokens: 100, PredictedTokens: 100}, at)
	// The seconds counters did not move; the wall-clock window is the
	// fallback so a stalled counter cannot zero out real tokens.
	r.Update(Metrics{PromptTokens: 110, PredictedTokens: 120}, at.Add(2*time.Second))
	if r.PromptTPS != 5 || r.GenTPS != 10 {
		t.Fatalf("tps = %v/%v, want 5/10", r.PromptTPS, r.GenTPS)
	}
}

func TestRatesDecreasingCounterReseeds(t *testing.T) {
	var r Rates
	at := time.Unix(3000, 0)
	r.Update(Metrics{PromptTokens: 5000, PredictedTokens: 6000}, at)
	// The server restarted: every counter dropped.
	r.Update(Metrics{PromptTokens: 100, PredictedTokens: 50}, at.Add(time.Second))
	if !r.Reset {
		t.Fatal("decreasing counter did not report a reset")
	}
	if r.PromptTPS != 0 || r.GenTPS != 0 || r.PromptTokens != 0 || r.GenTokens != 0 {
		t.Fatalf("reset produced rates: %+v", r)
	}
	// The next window is measured against the new baseline.
	r.Update(Metrics{PromptTokens: 200, PredictedTokens: 50}, at.Add(2*time.Second))
	if r.Reset {
		t.Fatal("reset flag stuck")
	}
	if r.PromptTPS != 100 {
		t.Fatalf("post-reset tps = %v, want 100", r.PromptTPS)
	}
}

func TestRatesSpecAccept(t *testing.T) {
	var r Rates
	at := time.Unix(4000, 0)
	r.Update(Metrics{SpecDraftTokens: 100, SpecAcceptedTokens: 80}, at)
	r.Update(Metrics{SpecDraftTokens: 400, SpecAcceptedTokens: 200}, at.Add(time.Second))
	if !r.SpecSeen || r.SpecRate != 40 {
		t.Fatalf("window spec rate = %v (seen %v), want 40", r.SpecRate, r.SpecSeen)
	}
	// A window without drafts keeps showing the lifetime rate.
	r.Update(Metrics{SpecDraftTokens: 400, SpecAcceptedTokens: 200}, at.Add(2*time.Second))
	if !r.SpecSeen || r.SpecRate != 50 {
		t.Fatalf("lifetime spec rate = %v (seen %v), want 50", r.SpecRate, r.SpecSeen)
	}
	// No drafts ever: not speculative at all.
	var idle Rates
	idle.Update(Metrics{PromptTokens: 1}, at)
	idle.Update(Metrics{PromptTokens: 2}, at.Add(time.Second))
	if idle.SpecSeen || idle.SpecRate != 0 {
		t.Fatalf("idle spec = %v (seen %v)", idle.SpecRate, idle.SpecSeen)
	}
}

func TestSummarizeSlots(t *testing.T) {
	sum := SummarizeSlots([]Slot{
		{ID: 0, NCtx: 4096, NPast: 1024, IsProcessing: true},
		{ID: 1, NCtx: 4096, NPast: 512},
		{ID: 2, NCtx: 2048, NPast: 512, IsProcessing: true},
	})
	if sum.Total != 3 || sum.Busy != 2 {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.KVUsed != 2048 || sum.KVCap != 10240 {
		t.Fatalf("kv = %d/%d", sum.KVUsed, sum.KVCap)
	}
	if want := 20.0; sum.KVPercent() != want {
		t.Fatalf("kv percent = %v, want %v", sum.KVPercent(), want)
	}
	if got := (SummarizeSlots(nil)).KVPercent(); got != 0 {
		t.Fatalf("empty kv percent = %v, want 0", got)
	}
	if got := (SummarizeSlots([]Slot{{NPast: 5}})).KVPercent(); got != 0 {
		t.Fatalf("contextless kv percent = %v, want 0", got)
	}
}

func TestMetricsDisabled(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":404,"message":"not found"}}`)
	})
	if _, err := c.Metrics(context.Background(), ""); !errors.Is(err, ErrMetricsDisabled) {
		t.Fatalf("err = %v, want ErrMetricsDisabled", err)
	}
}

func TestScrapeInferenceClassifiesFailures(t *testing.T) {
	scrapes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			scrapes++
			if scrapes == 1 {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":{"code":404}}`)
				return
			}
			if scrapes == 2 {
				// Alive endpoint that exposes nothing.
				return
			}
			io.WriteString(w, "llamacpp:prompt_tokens_total 10\n")
		case "/slots":
			io.WriteString(w, `[{"id":0,"n_ctx":4096,"n_past":64,"is_processing":true}]`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	// A metrics-disabled router still reports slot occupancy.
	m, slots, reason := c.ScrapeInference(context.Background(), "a")
	if !strings.Contains(reason, "metrics are disabled") {
		t.Fatalf("reason = %q, want metrics-disabled", reason)
	}
	if m.PromptTokens != 0 {
		t.Fatalf("metrics = %+v, want zero", m)
	}
	if len(slots) != 1 || !slots[0].IsProcessing {
		t.Fatalf("slots = %+v", slots)
	}

	// A live endpoint that answers with an empty body is not metrics.
	_, slots, reason = c.ScrapeInference(context.Background(), "")
	if reason != "no metrics exposed" {
		t.Fatalf("reason = %q", reason)
	}
	if len(slots) != 1 {
		t.Fatalf("slots = %+v", slots)
	}

	// The healthy case returns parsed counters.
	m, slots, reason = c.ScrapeInference(context.Background(), "")
	if reason != "" || m.PromptTokens != 10 || len(slots) != 1 {
		t.Fatalf("scrape = %+v slots=%d reason=%q", m, len(slots), reason)
	}
}
