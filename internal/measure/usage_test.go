package measure

import (
	"testing"
	"time"
)

func TestUsageRoundTrip(t *testing.T) {
	s := testStore(t)
	at := time.Unix(1_700_000_000, 0)
	u := Usage{
		Router: RouterUsage{Key: "1/2", StartedAt: at, Restarts: 1},
		Models: map[string]ModelUsage{
			"alpha": {ID: "alpha", Loads: 2, PromptTokens: 1200, GenTokens: 345, LastUsed: at},
		},
	}
	if err := s.AppendUsage(u); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LatestUsage()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no usage snapshot")
	}
	if got.Router.Key != "1/2" || got.Router.Restarts != 1 {
		t.Fatalf("router = %+v", got.Router)
	}
	m := got.Lookup("alpha")
	if m.Loads != 2 || m.PromptTokens != 1200 || m.GenTokens != 345 {
		t.Fatalf("alpha = %+v", m)
	}
	if !m.LastUsed.Equal(at) {
		t.Fatalf("last used = %v, want %v", m.LastUsed, at)
	}
}

func TestUsageKeepsLatestDuringCompaction(t *testing.T) {
	old := maxRecords
	maxRecords = 3
	defer func() { maxRecords = old }()

	s := testStore(t)
	for i := 0; i < 5; i++ {
		r := Record{
			Version: Version, Kind: KindObservation, ModelID: "m",
			At: time.Unix(int64(i), 0),
		}
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		u := Usage{Models: map[string]ModelUsage{"alpha": {ID: "alpha", Loads: i}}}
		if err := s.AppendUsage(u); err != nil {
			t.Fatal(err)
		}
	}

	records, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("records = %d, want 3 observations + 1 usage", len(records))
	}
	var observations int
	for _, r := range records {
		if r.Kind == KindObservation {
			observations++
		}
	}
	if observations != 3 {
		t.Fatalf("observations = %d, want 3", observations)
	}
	got, ok, err := s.LatestUsage()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Lookup("alpha").Loads != 3 {
		t.Fatalf("latest usage = %+v, ok = %v", got, ok)
	}
}

func TestUsageRecordWithoutModelID(t *testing.T) {
	s := testStore(t)
	if err := s.AppendUsage(Usage{Models: map[string]ModelUsage{"alpha": {ID: "alpha", Loads: 1}}}); err != nil {
		t.Fatal(err)
	}
	records, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != KindUsage {
		t.Fatalf("records = %+v", records)
	}
	if records[0].ModelID != "" {
		t.Fatalf("usage record model ID = %q, want empty", records[0].ModelID)
	}
}
