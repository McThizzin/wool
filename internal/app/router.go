package app

import (
	"sort"
	"time"

	"wool/internal/llamaserver"
)

// routerResident reports whether a router model occupies a models_max slot.
// The TUI treats loaded and sleeping models as resident; a model that is still
// loading appears on the next poll.
func routerResident(rm llamaserver.Model) bool {
	return rm.Status.Value == "loaded" || rm.Status.Value == "sleeping"
}

// routerResidentModel is one resident model with wool's memory estimate and
// the time it was first seen loaded. LoadedAt is only a proxy for the router's
// internal last-used clock, which the API does not expose.
type routerResidentModel struct {
	ID       string
	LoadedAt time.Time
	Bytes    uint64
	Known    bool
}

// routerEviction is a best-effort prediction of what loading Candidate does to
// the resident set. The router evicts idle models in LRU order when the load
// would exceed models_max; the API does not expose that order, so the victim
// list is an approximation.
type routerEviction struct {
	Candidate      string
	CandidateBytes uint64
	CandidateKnown bool
	Resident       bool
	Victims        []string
	OverLimit      int
	EstimatesKnown bool
}

// predictRouterEviction counts the slots a new load needs and picks that many
// residents in approximate LRU order. It deliberately models only the count
// limit: the pinned router does not evict on memory pressure, and a model that
// is busy is never evicted, which the API cannot tell us.
func predictRouterEviction(resident []routerResidentModel, candidate string,
	candidateBytes uint64, candidateKnown bool, limit int) routerEviction {
	plan := routerEviction{
		Candidate:      candidate,
		CandidateBytes: candidateBytes,
		CandidateKnown: candidateKnown,
		EstimatesKnown: candidateKnown,
	}
	if candidate == "" {
		return plan
	}
	for _, r := range resident {
		if r.ID == candidate {
			plan.Resident = true
			plan.EstimatesKnown = true
			return plan
		}
		if !r.Known {
			plan.EstimatesKnown = false
		}
	}
	if limit > 0 {
		plan.OverLimit = len(resident) + 1 - limit
		if plan.OverLimit < 0 {
			plan.OverLimit = 0
		}
	}

	ordered := append([]routerResidentModel(nil), resident...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.LoadedAt.IsZero() != b.LoadedAt.IsZero() {
			return !a.LoadedAt.IsZero()
		}
		return a.LoadedAt.Before(b.LoadedAt)
	})
	n := plan.OverLimit
	if n > len(ordered) {
		n = len(ordered)
	}
	for i := 0; i < n; i++ {
		plan.Victims = append(plan.Victims, ordered[i].ID)
	}
	return plan
}

// trackRouterResidency keeps the approximate load order in sync with the
// router's current model list. Unloaded or failed models are dropped so a
// later load gets a fresh timestamp.
func (m *Model) trackRouterResidency(now time.Time) {
	if m.routerLoadedAt == nil {
		m.routerLoadedAt = make(map[string]time.Time)
	}
	seen := make(map[string]bool, len(m.routerModels))
	for _, rm := range m.routerModels {
		if !routerResident(rm) {
			continue
		}
		seen[rm.ID] = true
		if _, ok := m.routerLoadedAt[rm.ID]; !ok {
			m.routerLoadedAt[rm.ID] = now
		}
	}
	for id := range m.routerLoadedAt {
		if !seen[id] {
			delete(m.routerLoadedAt, id)
		}
	}
}
