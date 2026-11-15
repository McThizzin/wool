package app

import (
	"path/filepath"
	"testing"
	"time"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/measure"
)

func usageRouter(pid int, ticks uint64) daemon.Instance {
	return daemon.Instance{
		State: daemon.State{
			Name: "router", Kind: daemon.KindRouter,
			Host: "127.0.0.1", Port: 8082, PID: pid, StartTicks: ticks,
		},
		Alive: true,
	}
}

func loadedModel(id string) llamaserver.Model {
	return llamaserver.Model{ID: id, Status: llamaserver.ModelStatus{Value: "loaded"}}
}

func sleepingModel(id string) llamaserver.Model {
	return llamaserver.Model{ID: id, Status: llamaserver.ModelStatus{Value: "sleeping"}}
}

func usageTick(m *Model, at time.Time, inst daemon.Instance, models []llamaserver.Model, metrics map[string]llamaserver.Metrics) {
	m.applyTick(tickMsg{
		at: at, snap: m.snap,
		router: inst, hasRouter: true,
		models: models, modelsOK: true, usage: metrics,
	})
}

func usageStore(t *testing.T) *measure.Store {
	t.Helper()
	return measure.NewStoreAt(filepath.Join(t.TempDir(), "measurements", "runs.jsonl"))
}

func TestUsageTracksLoadsAndTokens(t *testing.T) {
	m := testModel(t)
	store := usageStore(t)
	m.meas = store
	inst := usageRouter(100, 1)
	t0 := time.Unix(1_700_000_000, 0)

	// Seed the tracker with an empty router, then observe the load.
	usageTick(m, t0, inst, nil, nil)
	alpha := loadedModel("alpha")
	usageTick(m, t0.Add(time.Second), inst, []llamaserver.Model{alpha}, nil)
	usageTick(m, t0.Add(2*time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1000, PredictedTokens: 100}})
	usageTick(m, t0.Add(3*time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1200, PredictedTokens: 150}})

	life := m.usage.life.Lookup("alpha")
	if life.Loads != 1 {
		t.Fatalf("lifetime loads = %d, want 1", life.Loads)
	}
	if life.PromptTokens != 200 || life.GenTokens != 50 {
		t.Fatalf("lifetime tokens = %d/%d, want 200/50", life.PromptTokens, life.GenTokens)
	}
	session := m.usage.session.Lookup("alpha")
	if session.Loads != 1 || session.PromptTokens != 200 || session.GenTokens != 50 {
		t.Fatalf("session usage = %+v", session)
	}

	m.usage.flushNow(store, t0.Add(4*time.Second))
	got, ok, err := store.LatestUsage()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("usage was not persisted")
	}
	saved := got.Lookup("alpha")
	if saved.Loads != 1 || saved.PromptTokens != 200 || saved.GenTokens != 50 {
		t.Fatalf("persisted usage = %+v", saved)
	}
}

func TestUsageSurvivesRestartWithoutDoubleCounting(t *testing.T) {
	store := usageStore(t)
	inst := usageRouter(100, 1)
	t0 := time.Unix(1_700_000_000, 0)
	alpha := loadedModel("alpha")

	m := testModel(t)
	m.meas = store
	usageTick(m, t0, inst, nil, nil)
	usageTick(m, t0.Add(time.Second), inst, []llamaserver.Model{alpha}, nil)
	usageTick(m, t0.Add(2*time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1000, PredictedTokens: 100}})
	usageTick(m, t0.Add(3*time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1200, PredictedTokens: 150}})
	m.usage.flushNow(store, t0.Add(4*time.Second))

	// A new TUI attaches to the same router. The already-resident model must
	// not count as a new load, and the counters must continue from the
	// persisted total rather than replaying the router's lifetime counters.
	m2 := testModel(t)
	m2.meas = store
	usageTick(m2, t0.Add(time.Minute), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1200, PredictedTokens: 150}})
	usageTick(m2, t0.Add(time.Minute+time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1300, PredictedTokens: 180}})

	life := m2.usage.life.Lookup("alpha")
	if life.Loads != 1 {
		t.Fatalf("lifetime loads = %d, want 1 (no extra load on restart)", life.Loads)
	}
	if life.PromptTokens != 300 || life.GenTokens != 80 {
		t.Fatalf("lifetime tokens = %d/%d, want 300/80", life.PromptTokens, life.GenTokens)
	}
	session := m2.usage.session.Lookup("alpha")
	if session.Loads != 0 || session.PromptTokens != 100 || session.GenTokens != 30 {
		t.Fatalf("session usage = %+v", session)
	}
}

func TestUsageRouterRestartResetsCounters(t *testing.T) {
	store := usageStore(t)
	first := usageRouter(100, 1)
	second := usageRouter(200, 2)
	t0 := time.Unix(1_700_000_000, 0)
	alpha := loadedModel("alpha")

	m := testModel(t)
	m.meas = store
	usageTick(m, t0, first, nil, nil)
	usageTick(m, t0.Add(time.Second), first, []llamaserver.Model{alpha}, nil)
	usageTick(m, t0.Add(2*time.Second), first, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1000, PredictedTokens: 100}})
	usageTick(m, t0.Add(3*time.Second), first, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1200, PredictedTokens: 150}})

	// The router restarted: counters begin again at a lower value and the
	// model is loaded by the new process.
	usageTick(m, t0.Add(4*time.Second), second, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 50, PredictedTokens: 5}})
	usageTick(m, t0.Add(5*time.Second), second, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 80, PredictedTokens: 15}})

	life := m.usage.life.Lookup("alpha")
	if life.Loads != 2 {
		t.Fatalf("lifetime loads = %d, want 2", life.Loads)
	}
	if life.PromptTokens != 230 || life.GenTokens != 60 {
		t.Fatalf("lifetime tokens = %d/%d, want 230/60", life.PromptTokens, life.GenTokens)
	}
	if m.usage.life.Router.Restarts != 1 {
		t.Fatalf("restarts = %d, want 1", m.usage.life.Router.Restarts)
	}
	if m.usage.session.Router.Restarts != 1 {
		t.Fatalf("session restarts = %d, want 1", m.usage.session.Router.Restarts)
	}

	m.usage.flushNow(store, t0.Add(6*time.Second))
	got, ok, err := store.LatestUsage()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("usage was not persisted")
	}
	if got.Router.Restarts != 1 {
		t.Fatalf("persisted restarts = %d, want 1", got.Router.Restarts)
	}
	saved := got.Lookup("alpha")
	if saved.Loads != 2 || saved.PromptTokens != 230 || saved.GenTokens != 60 {
		t.Fatalf("persisted usage = %+v", saved)
	}
}

func TestUsageSleepingModelKeepsLastUsed(t *testing.T) {
	m := testModel(t)
	inst := usageRouter(100, 1)
	t0 := time.Unix(1_700_000_000, 0)
	alpha := loadedModel("alpha")
	asleep := sleepingModel("alpha")

	usageTick(m, t0, inst, nil, nil)
	usageTick(m, t0.Add(time.Second), inst, []llamaserver.Model{alpha}, nil)
	// Seed the counters, then observe a busy request on the next poll.
	usageTick(m, t0.Add(2*time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1000, PredictedTokens: 100, RequestsProcessing: 1}})
	usageTick(m, t0.Add(3*time.Second), inst, []llamaserver.Model{alpha},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1000, PredictedTokens: 100, RequestsProcessing: 1}})
	if got := m.usage.life.Lookup("alpha").LastUsed; !got.Equal(t0.Add(3 * time.Second)) {
		t.Fatalf("last used while loaded = %v", got)
	}

	// Cached metrics keep the old request gauge. Sleeping must not refresh
	// last-used, or a model that never wakes would look recently used.
	usageTick(m, t0.Add(time.Minute), inst, []llamaserver.Model{asleep},
		map[string]llamaserver.Metrics{"alpha": {PromptTokens: 1000, PredictedTokens: 100, RequestsProcessing: 1}})
	if got := m.usage.life.Lookup("alpha").LastUsed; !got.Equal(t0.Add(3 * time.Second)) {
		t.Fatalf("last used while sleeping = %v, want %v", got, t0.Add(3*time.Second))
	}
}

func TestUsageIgnoresFailedModelsPoll(t *testing.T) {
	m := testModel(t)
	inst := usageRouter(100, 1)
	t0 := time.Unix(1_700_000_000, 0)
	alpha := loadedModel("alpha")

	usageTick(m, t0, inst, nil, nil)
	usageTick(m, t0.Add(time.Second), inst, []llamaserver.Model{alpha}, nil)
	if got := m.usage.life.Lookup("alpha").Loads; got != 1 {
		t.Fatalf("loads = %d, want 1", got)
	}

	// A failed /models scrape is not an empty resident set. It must not clear
	// the tracker or count the model as loaded again on the next success.
	m.applyTick(tickMsg{
		at: t0.Add(2 * time.Second), snap: m.snap,
		router: inst, hasRouter: true, routerErr: "boom",
	})
	usageTick(m, t0.Add(3*time.Second), inst, []llamaserver.Model{alpha}, nil)
	if got := m.usage.life.Lookup("alpha").Loads; got != 1 {
		t.Fatalf("loads after failed poll = %d, want 1", got)
	}
}
