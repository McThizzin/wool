package llamaserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeGroupClient struct {
	loaded  map[string]Model
	loadErr map[string]error
	noAdd   map[string]bool
	keep    map[string]bool
	loads   []string
}

func (f *fakeGroupClient) Load(_ context.Context, model string) error {
	f.loads = append(f.loads, model)
	if err := f.loadErr[model]; err != nil {
		return err
	}
	if f.noAdd[model] || f.keep[model] {
		return nil
	}
	if f.loaded == nil {
		f.loaded = map[string]Model{}
	}
	f.loaded[model] = Model{ID: model, Status: ModelStatus{Value: "loaded"}}
	return nil
}

func (f *fakeGroupClient) Models(context.Context, bool) ([]Model, error) {
	out := make([]Model, 0, len(f.loaded))
	for _, m := range f.loaded {
		out = append(out, m)
	}
	return out, nil
}

func TestLoadGroupLoadsInOrder(t *testing.T) {
	client := &fakeGroupClient{loadErr: map[string]error{}}
	results := LoadGroup(context.Background(), client, []string{"a", "b", "c"},
		50*time.Millisecond, time.Millisecond)
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for i, want := range []string{"a", "b", "c"} {
		if results[i].Model != want || results[i].Err != nil || results[i].Status != "loaded" {
			t.Fatalf("result %d = %+v", i, results[i])
		}
	}
	if strings.Join(client.loads, ",") != "a,b,c" {
		t.Fatalf("loads = %v, want a,b,c", client.loads)
	}
}

func TestLoadGroupContinuesAfterFailure(t *testing.T) {
	client := &fakeGroupClient{
		loadErr: map[string]error{"rejected": errors.New("not accepted")},
		loaded: map[string]Model{
			"failed": {ID: "failed", Status: ModelStatus{Value: "unloaded", Failed: true}},
		},
		keep: map[string]bool{"failed": true},
	}
	results := LoadGroup(context.Background(), client,
		[]string{"rejected", "failed", "ok"}, 50*time.Millisecond, time.Millisecond)
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "not accepted") {
		t.Fatalf("rejected result = %+v", results[0])
	}
	if results[1].Err == nil || !strings.Contains(results[1].Err.Error(), "load failed") {
		t.Fatalf("failed result = %+v", results[1])
	}
	if results[2].Err != nil || results[2].Status != "loaded" {
		t.Fatalf("ok result = %+v", results[2])
	}
	if strings.Join(client.loads, ",") != "rejected,failed,ok" {
		t.Fatalf("loads = %v, want all three attempted", client.loads)
	}
}

func TestLoadGroupAcceptsSleeping(t *testing.T) {
	client := &fakeGroupClient{
		loaded: map[string]Model{
			"a": {ID: "a", Status: ModelStatus{Value: "sleeping"}},
		},
		loadErr: map[string]error{},
	}
	results := LoadGroup(context.Background(), client, []string{"a"},
		50*time.Millisecond, time.Millisecond)
	if results[0].Err != nil || results[0].Status != "sleeping" {
		t.Fatalf("result = %+v", results[0])
	}
	if len(client.loads) != 0 {
		t.Fatalf("loads = %v, want none for an already resident model", client.loads)
	}
}

func TestLoadGroupSkipsResidentModels(t *testing.T) {
	client := &fakeGroupClient{
		loaded: map[string]Model{
			"a": {ID: "a", Status: ModelStatus{Value: "loaded"}},
			"b": {ID: "b", Status: ModelStatus{Value: "unloaded"}},
		},
		loadErr: map[string]error{},
	}
	results := LoadGroup(context.Background(), client, []string{"a", "b"},
		50*time.Millisecond, time.Millisecond)
	if results[0].Err != nil || results[0].Status != "loaded" {
		t.Fatalf("resident result = %+v", results[0])
	}
	if results[1].Err != nil || results[1].Status != "loaded" {
		t.Fatalf("loaded result = %+v", results[1])
	}
	if strings.Join(client.loads, ",") != "b" {
		t.Fatalf("loads = %v, want only b", client.loads)
	}
}

func TestLoadGroupUnknownModel(t *testing.T) {
	client := &fakeGroupClient{loadErr: map[string]error{}, noAdd: map[string]bool{"missing": true}}
	results := LoadGroup(context.Background(), client, []string{"missing"},
		50*time.Millisecond, time.Millisecond)
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "not in the router list") {
		t.Fatalf("result = %+v", results[0])
	}
}

func TestLoadGroupTimeout(t *testing.T) {
	client := &fakeGroupClient{
		loaded: map[string]Model{
			"slow": {ID: "slow", Status: ModelStatus{Value: "loading"}},
		},
		loadErr: map[string]error{},
		keep:    map[string]bool{"slow": true},
	}
	results := LoadGroup(context.Background(), client, []string{"slow"},
		20*time.Millisecond, time.Millisecond)
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "timed out") {
		t.Fatalf("result = %+v", results[0])
	}
}

func TestLoadGroupContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &fakeGroupClient{loadErr: map[string]error{}}
	results := LoadGroup(ctx, client, []string{"a", "b"}, time.Second, time.Millisecond)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("result = %+v, want context canceled", r)
		}
	}
	if len(client.loads) != 0 {
		t.Fatalf("loads = %v, want none after cancel", client.loads)
	}
}
