package measure

import "time"

// KindUsage is a snapshot of router model usage. Unlike an observation it is
// not tied to a launch shape, and the store keeps only the newest snapshot.
const KindUsage = "usage"

// Usage is the persisted usage of the router's models. It is a snapshot rather
// than a log: every flush replaces the previous one during compaction.
type Usage struct {
	Router RouterUsage           `json:"router"`
	Models map[string]ModelUsage `json:"models,omitempty"`
}

// RouterUsage identifies the router process the counters belong to. Key
// changes when the router restarts, which lets the caller start a fresh metric
// baseline instead of reporting a negative delta.
type RouterUsage struct {
	Key       string    `json:"key,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Restarts  int       `json:"restarts,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// ModelUsage is lifetime usage for one model ID. Loads counts observed loads;
// tokens are summed from /metrics counter deltas.
type ModelUsage struct {
	ID           string    `json:"id"`
	Loads        int       `json:"loads"`
	PromptTokens uint64    `json:"prompt_tokens"`
	GenTokens    uint64    `json:"generated_tokens"`
	LastUsed     time.Time `json:"last_used,omitempty"`
}

// Lookup returns the usage for id, or a zero entry with the ID filled in.
func (u Usage) Lookup(id string) ModelUsage {
	if m, ok := u.Models[id]; ok {
		if m.ID == "" {
			m.ID = id
		}
		return m
	}
	return ModelUsage{ID: id}
}

// AddLoad records one observed load and marks the model as used.
func (u *Usage) AddLoad(id string, at time.Time) {
	m := u.Lookup(id)
	m.ID = id
	m.Loads++
	if !at.IsZero() {
		m.LastUsed = at
	}
	u.put(m)
}

// AddTokens adds one window's token deltas and marks the model as used.
func (u *Usage) AddTokens(id string, prompt, gen uint64, at time.Time) {
	m := u.Lookup(id)
	m.ID = id
	m.PromptTokens += prompt
	m.GenTokens += gen
	if !at.IsZero() {
		m.LastUsed = at
	}
	u.put(m)
}

// Touch updates the last-used time without changing the counters.
func (u *Usage) Touch(id string, at time.Time) {
	if at.IsZero() {
		return
	}
	m := u.Lookup(id)
	m.ID = id
	m.LastUsed = at
	u.put(m)
}

func (u *Usage) put(m ModelUsage) {
	if u.Models == nil {
		u.Models = make(map[string]ModelUsage)
	}
	u.Models[m.ID] = m
}

// AppendUsage writes one usage snapshot. The newest record wins; compaction
// keeps only that record so frequent flushes cannot evict observations or
// benchmarks.
func (s *Store) AppendUsage(u Usage) error {
	return s.Append(Record{Kind: KindUsage, Usage: &u})
}

// LatestUsage returns the newest usage snapshot. A missing snapshot is not an
// error.
func (s *Store) LatestUsage() (Usage, bool, error) {
	records, err := s.All()
	if err != nil {
		return Usage{}, false, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind != KindUsage || r.Usage == nil {
			continue
		}
		u := *r.Usage
		if u.Models == nil {
			u.Models = make(map[string]ModelUsage)
		}
		return u, true, nil
	}
	return Usage{}, false, nil
}
