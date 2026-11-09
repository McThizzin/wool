package measure

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return NewStoreAt(filepath.Join(t.TempDir(), "measurements", "runs.jsonl"))
}

func TestStoreAppendAndAll(t *testing.T) {
	s := testStore(t)
	want := Record{Version: Version, ModelID: "gemma", Ctx: 65536, ObservedBytes: 3 << 30}
	if err := s.Append(want); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(Record{Version: Version, ModelID: "ornith", Ctx: 32768}); err != nil {
		t.Fatal(err)
	}

	// A corrupt line must be skipped, not fail the store.
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].ModelID != "gemma" || got[0].ObservedBytes != 3<<30 {
		t.Fatalf("first record = %+v", got[0])
	}
}

func TestStoreSkipsUnknownVersion(t *testing.T) {
	s := testStore(t)
	if err := s.Append(Record{Version: 99, ModelID: "future"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unknown version should be skipped, got %+v", got)
	}
}

func TestStoreMissingFile(t *testing.T) {
	s := testStore(t)
	got, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("missing file returned %d records", len(got))
	}
}

func TestSummaryMatchesShapeAndCounts(t *testing.T) {
	s := testStore(t)
	base := Record{
		Version: Version, ModelID: "m", Ctx: 4096,
		CacheTypeK: "q4_0", CacheTypeV: "q4_0", GPULayers: 999,
	}
	first := base
	first.At = time.Now().Add(-time.Hour)
	first.ObservedBytes = 1 << 30
	second := base
	second.At = time.Now()
	second.ObservedBytes = 2 << 30
	other := base
	other.Ctx = 8192
	other.ObservedBytes = 9 << 30
	for _, r := range []Record{first, second, other} {
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	sum, ok := s.Summary(base.Shape())
	if !ok {
		t.Fatal("no summary for matching shape")
	}
	if sum.Count != 2 {
		t.Fatalf("count = %d, want 2", sum.Count)
	}
	if sum.ObservedBytes != 2<<30 {
		t.Fatalf("latest observed = %d, want %d", sum.ObservedBytes, 2<<30)
	}
	if _, ok := s.Summary(Shape{ModelID: "nope"}); ok {
		t.Fatal("unexpected summary for unknown model")
	}
}

func TestStoreBenchKindAndSummaryIgnoresBench(t *testing.T) {
	s := testStore(t)
	shape := Record{
		Version: Version, ModelID: "m", Ctx: 4096,
		CacheTypeK: "q4_0", CacheTypeV: "q4_0", GPULayers: 999,
	}
	observation := shape
	observation.ObservedBytes = 1 << 30
	if err := s.Append(observation); err != nil {
		t.Fatal(err)
	}
	bench := shape
	bench.Kind = KindBench
	bench.ObservedBytes = 99 << 30
	bench.Bench = &Bench{
		Engine: "server",
		Runs:   2,
		Samples: []BenchRun{
			{Index: 1, PromptTPS: 100, GenerationTPS: 10},
			{Index: 2, PromptTPS: 120, GenerationTPS: 12},
		},
		Median: BenchStats{PromptTPS: 110, GenerationTPS: 11},
	}
	if err := s.Append(bench); err != nil {
		t.Fatal(err)
	}
	plain := shape
	plain.At = time.Now()
	plain.ObservedBytes = 2 << 30
	if err := s.Append(plain); err != nil {
		t.Fatal(err)
	}

	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("records = %d", len(all))
	}
	if all[0].Kind != KindObservation || all[1].Kind != KindBench || all[2].Kind != KindObservation {
		t.Fatalf("kinds = %q %q %q", all[0].Kind, all[1].Kind, all[2].Kind)
	}
	if all[1].Bench == nil || len(all[1].Bench.Samples) != 2 {
		t.Fatalf("bench payload = %+v", all[1].Bench)
	}

	sum, ok := s.Summary(shape.Shape())
	if !ok {
		t.Fatal("no summary")
	}
	if sum.Count != 2 || sum.ObservedBytes != 2<<30 {
		t.Fatalf("summary = %+v (bench record must not be counted)", sum)
	}
}

func TestCaptureRecordsDelta(t *testing.T) {
	s := testStore(t)
	values := []uint64{150, 160}
	i := 0
	cap := Capture{
		Store:         s,
		Record:        Record{Version: Version, ModelID: "m", Ctx: 1024},
		Baseline:      100,
		BaselineKnown: true,
		Sample: func() (uint64, bool) {
			v := values[i]
			i++
			return v, true
		},
		Now: func() time.Time { return time.Unix(100, 0) },
		At:  []time.Duration{0, 0},
	}
	if err := cap.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	r := got[0]
	if !r.BaselineKnown || r.ObservedBytes != 60 {
		t.Fatalf("observed = %d (known %v), want 60 known", r.ObservedBytes, r.BaselineKnown)
	}
	if r.SampleCount != 2 || len(r.Samples) != 2 {
		t.Fatalf("sample count = %d samples %v, want 2", r.SampleCount, r.Samples)
	}
	if !r.At.Equal(time.Unix(100, 0)) {
		t.Fatalf("at = %v", r.At)
	}
}

func TestCaptureSamplesAtAbsoluteOffsets(t *testing.T) {
	s := testStore(t)
	var slept []time.Duration
	cap := Capture{
		Store:         s,
		Record:        Record{Version: Version, ModelID: "m"},
		Baseline:      100,
		BaselineKnown: true,
		Sample:        func() (uint64, bool) { return 110, true },
		Sleep:         func(d time.Duration) { slept = append(slept, d) },
		At:            []time.Duration{5 * time.Second, 15 * time.Second},
	}
	if err := cap.Run(nil); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second}
	if !reflect.DeepEqual(slept, want) {
		t.Fatalf("slept %v, want %v (15s total, not 20s)", slept, want)
	}
	got, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ObservedBytes != 10 {
		t.Fatalf("records = %+v", got)
	}
}

func TestCaptureUnknownBaseline(t *testing.T) {
	s := testStore(t)
	cap := Capture{
		Store:         s,
		Record:        Record{Version: Version, ModelID: "m"},
		BaselineKnown: false,
		Sample:        func() (uint64, bool) { return 500, true },
		At:            []time.Duration{0},
	}
	if err := cap.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.All()
	if len(got) != 1 {
		t.Fatalf("got %d records", len(got))
	}
	if got[0].BaselineKnown || got[0].ObservedBytes != 0 {
		t.Fatalf("unknown baseline should not claim an observation: %+v", got[0])
	}
}

func TestCaptureCancelledBeforeWrite(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cap := Capture{
		Store:  s,
		Record: Record{Version: Version, ModelID: "m"},
		Sample: func() (uint64, bool) { return 1, true },
		At:     []time.Duration{time.Hour},
	}
	if err := cap.Run(ctx); err == nil {
		t.Fatal("expected context error")
	}
	got, _ := s.All()
	if len(got) != 0 {
		t.Fatalf("cancelled capture wrote %d records", len(got))
	}
}
