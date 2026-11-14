package app

import (
	"strings"
	"testing"
	"time"

	"wool/internal/gguf"
	"wool/internal/llamaserver"
)

func TestPredictRouterEvictionAtLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	resident := []routerResidentModel{
		{ID: "old", LoadedAt: now.Add(-time.Hour), Bytes: 4 << 30, Known: true},
		{ID: "new", LoadedAt: now.Add(-time.Minute), Bytes: 5 << 30, Known: true},
	}
	plan := predictRouterEviction(resident, "candidate", 6<<30, true, 2)
	if plan.OverLimit != 1 {
		t.Fatalf("over limit = %d, want 1", plan.OverLimit)
	}
	if len(plan.Victims) != 1 || plan.Victims[0] != "old" {
		t.Fatalf("victims = %v, want [old]", plan.Victims)
	}
	if !plan.EstimatesKnown {
		t.Fatal("plan should be known")
	}
}

func TestPredictRouterEvictionOverLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	resident := []routerResidentModel{
		{ID: "old", LoadedAt: now.Add(-3 * time.Hour), Known: true},
		{ID: "middle", LoadedAt: now.Add(-2 * time.Hour), Known: true},
		{ID: "new", LoadedAt: now.Add(-time.Hour), Known: true},
	}
	plan := predictRouterEviction(resident, "candidate", 0, true, 1)
	if plan.OverLimit != 3 {
		t.Fatalf("over limit = %d, want 3", plan.OverLimit)
	}
	want := []string{"old", "middle", "new"}
	if strings.Join(plan.Victims, ",") != strings.Join(want, ",") {
		t.Fatalf("victims = %v, want %v", plan.Victims, want)
	}
}

func TestPredictRouterEvictionEmpty(t *testing.T) {
	plan := predictRouterEviction(nil, "", 0, false, 1)
	if plan.Candidate != "" || len(plan.Victims) != 0 || plan.Resident {
		t.Fatalf("plan = %+v", plan)
	}
	plan = predictRouterEviction(nil, "first", 4<<30, true, 1)
	if plan.OverLimit != 0 || len(plan.Victims) != 0 {
		t.Fatalf("first load should not evict: %+v", plan)
	}
}

func TestPredictRouterEvictionCandidateResident(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	resident := []routerResidentModel{{ID: "loaded", LoadedAt: now, Known: true}}
	plan := predictRouterEviction(resident, "loaded", 4<<30, true, 1)
	if !plan.Resident {
		t.Fatal("candidate should be reported as resident")
	}
	if len(plan.Victims) != 0 {
		t.Fatalf("victims = %v, want none", plan.Victims)
	}
}

func TestPredictRouterEvictionUnknownEstimate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	resident := []routerResidentModel{{ID: "old", LoadedAt: now, Known: false}}
	plan := predictRouterEviction(resident, "candidate", 0, false, 1)
	if plan.EstimatesKnown {
		t.Fatal("plan should be unknown")
	}
	if len(plan.Victims) != 1 || plan.Victims[0] != "old" {
		t.Fatalf("count-limited victim should still be known: %+v", plan)
	}
}

func TestPredictRouterEvictionZeroLoadedAtSortsLast(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	resident := []routerResidentModel{
		{ID: "unknown", Known: true},
		{ID: "known", LoadedAt: now, Known: true},
	}
	plan := predictRouterEviction(resident, "candidate", 0, true, 2)
	if len(plan.Victims) != 1 || plan.Victims[0] != "known" {
		t.Fatalf("victims = %v, want [known]", plan.Victims)
	}
}

func TestRouterSummaryAtLimit(t *testing.T) {
	m := testModel(t)
	m.cfg.Router.ModelsMax = 1
	now := time.Unix(1_700_000_000, 0)
	m.routerLoadedAt = map[string]time.Time{"gemma-4-heretic": now}
	m.routerModels = []llamaserver.Model{
		{ID: "gemma-4-heretic", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "qwen3-4b-cyber", Status: llamaserver.ModelStatus{Value: "unloaded"}},
	}
	m.routerSel = 1
	m.fitCache = map[string]fitInfo{
		"model:gemma-4-heretic": {Ready: true, Est: gguf.Estimate{Total: 4 << 30}},
		"model:qwen3-4b-cyber":  {Ready: true, Est: gguf.Estimate{Total: 5 << 30}},
	}

	out := strings.Join(m.routerSummary(100), "\n")
	for _, want := range []string{
		"resident 1/1",
		"margin 512 MiB",
		"est 4.0 GiB",
		"predicted eviction: gemma-4-heretic",
		"LRU approx.",
		"if loading qwen3-4b-cyber (5.0 GiB)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestRouterSummaryOverLimit(t *testing.T) {
	m := testModel(t)
	m.cfg.Router.ModelsMax = 1
	now := time.Unix(1_700_000_000, 0)
	m.routerLoadedAt = map[string]time.Time{
		"gemma-4-heretic": now.Add(-time.Hour),
		"qwen3-4b-cyber":  now.Add(-time.Minute),
	}
	m.routerModels = []llamaserver.Model{
		{ID: "gemma-4-heretic", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "qwen3-4b-cyber", Status: llamaserver.ModelStatus{Value: "sleeping"}},
		{ID: "lfm3-1-7b", Status: llamaserver.ModelStatus{Value: "unloaded"}},
	}
	m.routerSel = 2
	m.fitCache = map[string]fitInfo{
		"model:gemma-4-heretic": {Ready: true, Est: gguf.Estimate{Total: 4 << 30}},
		"model:qwen3-4b-cyber":  {Ready: true, Est: gguf.Estimate{Total: 5 << 30}},
		"model:lfm3-1-7b":       {Ready: true, Est: gguf.Estimate{Total: 2 << 30}},
	}

	out := strings.Join(m.routerSummary(120), "\n")
	for _, want := range []string{
		"resident 2/1 (over limit)",
		"predicted eviction: gemma-4-heretic, qwen3-4b-cyber",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestRouterSummaryEmpty(t *testing.T) {
	m := testModel(t)
	m.routerModels = nil
	out := strings.Join(m.routerSummary(80), "\n")
	if !strings.Contains(out, "resident 0/1") {
		t.Fatalf("summary missing resident count:\n%s", out)
	}
	if !strings.Contains(out, "no models reported by the router") {
		t.Fatalf("summary missing empty state:\n%s", out)
	}
}

func TestRouterSummaryUnknownEstimate(t *testing.T) {
	m := testModel(t)
	m.cfg.Router.ModelsMax = 1
	m.routerModels = []llamaserver.Model{
		{ID: "not-configured-a", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "not-configured-b", Status: llamaserver.ModelStatus{Value: "unloaded"}},
	}
	m.routerSel = 1
	m.fitCache = map[string]fitInfo{}

	out := strings.Join(m.routerSummary(100), "\n")
	if !strings.Contains(out, "est unknown") {
		t.Fatalf("summary missing unknown estimate:\n%s", out)
	}
	if !strings.Contains(out, "predicted eviction: unknown (estimate missing)") {
		t.Fatalf("summary missing unknown prediction:\n%s", out)
	}
}

func TestTrackRouterResidency(t *testing.T) {
	m := testModel(t)
	now := time.Unix(1_700_000_000, 0)
	m.routerModels = []llamaserver.Model{
		{ID: "a", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "b", Status: llamaserver.ModelStatus{Value: "unloaded"}},
		{ID: "c", Status: llamaserver.ModelStatus{Value: "sleeping"}},
	}
	m.trackRouterResidency(now)
	if got := m.routerLoadedAt["a"]; !got.Equal(now) {
		t.Fatalf("a loaded at %s, want %s", got, now)
	}
	if got := m.routerLoadedAt["c"]; !got.Equal(now) {
		t.Fatalf("c loaded at %s, want %s", got, now)
	}
	if _, ok := m.routerLoadedAt["b"]; ok {
		t.Fatal("unloaded model should not be tracked")
	}

	later := now.Add(time.Minute)
	m.routerModels = []llamaserver.Model{
		{ID: "a", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "b", Status: llamaserver.ModelStatus{Value: "loaded"}},
	}
	m.trackRouterResidency(later)
	if got := m.routerLoadedAt["a"]; !got.Equal(now) {
		t.Fatalf("a timestamp changed to %s", got)
	}
	if got := m.routerLoadedAt["b"]; !got.Equal(later) {
		t.Fatalf("b loaded at %s, want %s", got, later)
	}
	if _, ok := m.routerLoadedAt["c"]; ok {
		t.Fatal("no-longer-resident model should be dropped")
	}
}

func TestRouterViewShowsEvictionSummary(t *testing.T) {
	m := testModel(t)
	m.hasRouter = true
	m.routerModels = []llamaserver.Model{
		{ID: "gemma-4-heretic", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "qwen3-4b-cyber", Status: llamaserver.ModelStatus{Value: "unloaded"}},
	}
	m.routerSel = 1
	m.fitCache = map[string]fitInfo{
		"model:gemma-4-heretic": {Ready: true, Est: gguf.Estimate{Total: 4 << 30}},
		"model:qwen3-4b-cyber":  {Ready: true, Est: gguf.Estimate{Total: 5 << 30}},
	}
	m.setTab(tabRouter)
	for _, size := range [][2]int{{100, 30}, {80, 24}, {40, 12}} {
		checkView(t, m, size[0], size[1])
	}
	if !strings.Contains(m.View(), "resident 1/1") {
		t.Fatalf("router view missing eviction summary:\n%s", m.View())
	}
}
