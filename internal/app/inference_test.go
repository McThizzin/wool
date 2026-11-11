package app

import (
	"strings"
	"testing"
	"time"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
)

func serverInstance() daemon.Instance {
	return daemon.Instance{
		State: daemon.State{Name: "server", Kind: daemon.KindServer, Model: "m", Host: "127.0.0.1", Port: 8081},
		Alive: true,
	}
}

func routerInstance() daemon.Instance {
	return daemon.Instance{
		State: daemon.State{Name: "router", Kind: daemon.KindRouter, Host: "127.0.0.1", Port: 8082},
		Alive: true,
	}
}

// serverTick folds one tick worth of standalone-server state into the model.
func serverTick(m *Model, at time.Time, st llamaserver.Metrics, slots []llamaserver.Slot, scrapeErr string) {
	msg := tickMsg{
		at: at, snap: m.snap,
		server:      serverInstance(),
		hasServer:   true,
		srvStats:    st,
		srvSlots:    slots,
		srvStatsErr: scrapeErr,
	}
	m.applyTick(msg)
}

// routerTick folds one tick worth of router state into the model. statsID is
// the model the scrape actually targeted, which may lag the selection.
func routerTick(m *Model, at time.Time, models []llamaserver.Model, stats llamaserver.Metrics, statsID, statsErr string) {
	msg := tickMsg{
		at: at, snap: m.snap,
		router:    routerInstance(),
		hasRouter: true,
		models:    models,
		stats:     stats,
		statsID:   statsID,
		statsErr:  statsErr,
	}
	m.applyTick(msg)
}

// bothTick folds a tick in which both instances are alive: the standalone
// server and a router serving the loaded model b.
func bothTick(m *Model, at time.Time, srvStats, rtStats llamaserver.Metrics) {
	models := []llamaserver.Model{{ID: "b", Status: llamaserver.ModelStatus{Value: "loaded"}}}
	msg := tickMsg{
		at: at, snap: m.snap,
		server:    serverInstance(),
		hasServer: true,
		router:    routerInstance(),
		hasRouter: true,
		models:    models,
		stats:     rtStats,
		statsID:   "b",
		srvStats:  srvStats,
		srvSlots:  []llamaserver.Slot{{ID: 0, NCtx: 2048, NPast: 128, IsProcessing: true}},
	}
	m.applyTick(msg)
}

func inferenceView(m *Model) string {
	m.setTab(tabInference)
	m.width, m.height = 120, 40
	return m.View()
}

func metricsAt(prompt, promptSec, gen, genSec float64) llamaserver.Metrics {
	return llamaserver.Metrics{
		PromptTokens: prompt, PromptSeconds: promptSec,
		PredictedTokens: gen, PredictedSeconds: genSec,
	}
}

func TestInferencePanelShowsRates(t *testing.T) {
	m := testModel(t)
	now := time.Now()
	slots := []llamaserver.Slot{
		{ID: 0, NCtx: 4096, NPast: 1024, IsProcessing: true},
		{ID: 1, NCtx: 4096, NPast: 512},
	}
	serverTick(m, now, metricsAt(1000, 2, 4000, 8), slots, "")
	serverTick(m, now.Add(time.Second), metricsAt(2000, 4, 8000, 12), slots, "")

	out := inferenceView(m)
	for _, want := range []string{
		"server · m",
		"prompt", "500.0 t/s",
		"gen", "1000.0 t/s",
		"requests", "0 proc · 0 defer",
		"SLOTS  1/2 busy",
		"lifetime prompt 500.0 t/s · gen 666.7 t/s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "collecting the first scrape window") || strings.Contains(out, "counter reset") {
		t.Fatalf("panel reported seeding or reset after a healthy window:\n%s", out)
	}
	if len(m.inf.hist.Prompt) != 1 || len(m.inf.hist.KV) != 1 {
		t.Fatalf("history lengths = %d/%d, want one window", len(m.inf.hist.Prompt), len(m.inf.hist.KV))
	}
	if m.inf.hist.KV[0] != (1536.0 / 8192 * 100) {
		t.Fatalf("kv sample = %v", m.inf.hist.KV[0])
	}
}

func TestInferenceSeedingScrapeShowsNoRates(t *testing.T) {
	m := testModel(t)
	serverTick(m, time.Now(), metricsAt(1000, 2, 4000, 8), nil, "")
	out := inferenceView(m)
	if !strings.Contains(out, "collecting the first scrape window...") {
		t.Fatalf("seeding scrape missing its notice:\n%s", out)
	}
	if strings.Contains(out, "t/s") {
		t.Fatalf("seeding scrape presented rates:\n%s", out)
	}
	if len(m.inf.hist.Prompt) != 0 {
		t.Fatalf("seeding scrape pushed history: %v", m.inf.hist.Prompt)
	}
}

func TestInferenceMetricsDisabledIsExplicit(t *testing.T) {
	m := testModel(t)
	now := time.Now()
	serverTick(m, now, llamaserver.Metrics{}, nil, llamaserver.ErrMetricsDisabled.Error())
	serverTick(m, now.Add(time.Second), llamaserver.Metrics{}, nil, llamaserver.ErrMetricsDisabled.Error())

	out := inferenceView(m)
	for _, want := range []string{
		"metrics unavailable",
		"metrics are disabled",
		"restart it with metrics = true to expose counters",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel missing %q:\n%s", want, out)
		}
	}
	// Zeros must not be presented as facts.
	if strings.Contains(out, "0.0 t/s") || strings.Contains(out, "collecting the first scrape window") {
		t.Fatalf("disabled metrics produced numbers:\n%s", out)
	}
	if m.inf.rates.Seeded {
		t.Fatal("a failing scrape seeded the rate tracker")
	}
}

func TestInferenceCounterResetNote(t *testing.T) {
	m := testModel(t)
	now := time.Now()
	serverTick(m, now, metricsAt(5000, 10, 6000, 12), nil, "")
	serverTick(m, now.Add(time.Second), metricsAt(100, 1, 50, 1), nil, "")
	out := inferenceView(m)
	if !strings.Contains(out, "counter reset detected (server restarted); rates re-seeded") {
		t.Fatalf("reset note missing:\n%s", out)
	}
	// Neither zeros nor negatives may be presented as the window's rates.
	if strings.Contains(out, "t/s") {
		t.Fatalf("reset window presented rates:\n%s", out)
	}
	if len(m.inf.hist.Prompt) != 0 {
		t.Fatalf("reset window pushed history: %v", m.inf.hist.Prompt)
	}
	// The next window is measured against the new baseline.
	serverTick(m, now.Add(2*time.Second), metricsAt(200, 2, 150, 2), nil, "")
	out = inferenceView(m)
	if !strings.Contains(out, "100.0 t/s") {
		t.Fatalf("rates did not resume after the reset:\n%s", out)
	}
	if !strings.Contains(out, "counter reset detected") {
		t.Fatalf("the reset note vanished too quickly:\n%s", out)
	}
}

func TestInferenceNothingRunning(t *testing.T) {
	m := testModel(t)
	m.applyTick(tickMsg{at: time.Now(), snap: m.snap})
	out := inferenceView(m)
	for _, want := range []string{"Nothing is serving inference.", "wool stats"} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel missing %q:\n%s", want, out)
		}
	}
}

func TestInferenceRouterNotLoaded(t *testing.T) {
	m := testModel(t)
	models := []llamaserver.Model{
		{ID: "a", Status: llamaserver.ModelStatus{Value: "unloaded"}},
		{ID: "b", Status: llamaserver.ModelStatus{Value: "loaded"}},
	}
	routerTick(m, time.Now(), models, llamaserver.Metrics{}, "", "")
	out := inferenceView(m)
	if !strings.Contains(out, "a is not loaded on the router") {
		t.Fatalf("panel missing not-loaded notice:\n%s", out)
	}
}

func TestInferenceSourceCycling(t *testing.T) {
	m := testModel(t)
	now := time.Now()
	bothTick(m, now, metricsAt(1000, 2, 1000, 2), metricsAt(500, 1, 500, 1))
	bothTick(m, now.Add(time.Second), metricsAt(2000, 4, 2000, 4), metricsAt(1000, 2, 1000, 2))

	// The standalone server wins while nothing is pinned.
	out := inferenceView(m)
	if !strings.Contains(out, "server · m") || !strings.Contains(out, "500.0 t/s") {
		t.Fatalf("default source missing server rates:\n%s", out)
	}

	// m switches to the selected router model and wipes the old source's
	// numbers until the next tick re-seeds them.
	if _, _ = m.handleKey(keyMsg("m")); m.inf.prefer != "router" {
		t.Fatalf("prefer = %q", m.inf.prefer)
	}
	out = inferenceView(m)
	if !strings.Contains(out, "router · b") || !strings.Contains(out, "collecting the first scrape window...") {
		t.Fatalf("router source missing its header or notice:\n%s", out)
	}
	if strings.Contains(out, "500.0 t/s") {
		t.Fatalf("switch kept the server's rates:\n%s", out)
	}
	if !strings.Contains(m.toast, "inference source: router b") {
		t.Fatalf("toast = %q", m.toast)
	}

	// The next ticks re-seed the router source and produce its own rates.
	bothTick(m, now.Add(2*time.Second), metricsAt(3000, 6, 3000, 6), metricsAt(1500, 2, 1500, 2))
	bothTick(m, now.Add(3*time.Second), metricsAt(4000, 8, 4000, 8), metricsAt(2500, 3, 2500, 3))
	out = inferenceView(m)
	if !strings.Contains(out, "1000.0 t/s") {
		t.Fatalf("router rates missing:\n%s", out)
	}
	if strings.Contains(out, "server · m") {
		t.Fatalf("router view still shows the server header:\n%s", out)
	}

	// m switches back.
	if _, _ = m.handleKey(keyMsg("m")); m.inf.prefer != "server" {
		t.Fatalf("prefer = %q", m.inf.prefer)
	}
	if !strings.Contains(m.toast, "inference source: standalone server") {
		t.Fatalf("toast = %q", m.toast)
	}
}

func TestInferenceSourceRefusesDeadInstance(t *testing.T) {
	m := testModel(t)
	m.applyTick(tickMsg{at: time.Now(), snap: m.snap})
	m.setTab(tabInference)
	if _, _ = m.handleKey(keyMsg("m")); m.inf.prefer != "" {
		t.Fatalf("prefer = %q, want unchanged", m.inf.prefer)
	}
	if !strings.Contains(m.toast, "router is not running") {
		t.Fatalf("toast = %q", m.toast)
	}
}

func TestInferenceRouterSelectionKeys(t *testing.T) {
	m := testModel(t)
	models := []llamaserver.Model{
		{ID: "a", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "b", Status: llamaserver.ModelStatus{Value: "sleeping"}},
	}
	m.setTab(tabInference)
	m.routerModels = models
	if _, _ = m.handleKey(keyMsg("j")); m.selectedRouterID() != "b" {
		t.Fatalf("j did not move the selection: %q", m.selectedRouterID())
	}
	if _, _ = m.handleKey(keyMsg("k")); m.selectedRouterID() != "a" {
		t.Fatalf("k did not move the selection: %q", m.selectedRouterID())
	}
}

func TestInferenceIgnoresStaleRouterScrape(t *testing.T) {
	m := testModel(t)
	now := time.Now()
	models := []llamaserver.Model{
		{ID: "a", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "b", Status: llamaserver.ModelStatus{Value: "loaded"}},
	}
	// The selection moves to b while a scrape for a is still in flight.
	routerTick(m, now, models, metricsAt(100, 1, 100, 1), "a", "")
	m.setTab(tabInference)
	if _, _ = m.handleKey(keyMsg("j")); m.selectedRouterID() != "b" {
		t.Fatalf("selection = %q", m.selectedRouterID())
	}
	routerTick(m, now.Add(time.Second), models, metricsAt(999999, 1, 999999, 1), "a", "")
	if src := m.infResolve(); src.state != infOK || src.metrics.PromptTokens != 0 || len(src.slots) != 0 {
		t.Fatalf("stale scrape attributed another model's counters: %+v", src)
	}
	// The next tick scrapes b; the rate is b's own, never a's.
	routerTick(m, now.Add(2*time.Second), models, metricsAt(3000, 3, 3000, 3), "b", "")
	out := inferenceView(m)
	if !strings.Contains(out, "1000.0 t/s") {
		t.Fatalf("model b's rates missing:\n%s", out)
	}
	if strings.Contains(out, "999999") {
		t.Fatalf("model a's counters leaked into b's panel:\n%s", out)
	}
}

func TestInferenceHistoryWindow(t *testing.T) {
	var h inferenceHistory
	src := infSource{metrics: llamaserver.Metrics{RequestsProcessing: 2}}
	for i := 0; i < infHistoryLen+10; i++ {
		h.push(src, llamaserver.SlotSummary{KVUsed: 1, KVCap: 2}, llamaserver.Rates{
			Window: time.Second, PromptTPS: float64(i), GenTPS: 1, SpecSeen: i%2 == 0, SpecRate: 50,
		})
	}
	if len(h.Prompt) != infHistoryLen || len(h.Spec) != infHistoryLen/2+5 {
		t.Fatalf("lengths = %d/%d", len(h.Prompt), len(h.Spec))
	}
	if h.Prompt[0] != 10 || h.Prompt[len(h.Prompt)-1] != float64(infHistoryLen+9) {
		t.Fatalf("window kept the wrong samples: %v..%v", h.Prompt[0], h.Prompt[len(h.Prompt)-1])
	}
	if h.Requests[len(h.Requests)-1] != 2 || h.KV[len(h.KV)-1] != 50 {
		t.Fatalf("requests/kv samples = %v/%v", h.Requests[len(h.Requests)-1], h.KV[len(h.KV)-1])
	}
	// A scrape without speculative activity never touches the spec series.
	spec := len(h.Spec)
	h.push(src, llamaserver.SlotSummary{}, llamaserver.Rates{Window: time.Second})
	if len(h.Spec) != spec {
		t.Fatal("non-speculative scrape pushed the spec series")
	}
}
