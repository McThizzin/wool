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
// would exceed models_max or leave less than the memory margin; the API does
// not expose the LRU order, so the victim list is an approximation.
type routerEviction struct {
	Candidate      string
	CandidateBytes uint64
	CandidateKnown bool
	Resident       bool
	Victims        []string
	OverLimit      int
	EstimatesKnown bool
}

// routerMemoryBudget is the router's memory pressure state. Enabled is true
// when the router reports per-model memory, which it only does when a positive
// --models-memory-margin was passed. Free is current free device memory.
type routerMemoryBudget struct {
	Enabled bool
	Known   bool
	Free    uint64
	Margin  uint64
}

// routerMemoryEnabled reports whether the router enforces a memory margin.
func routerMemoryEnabled(models []llamaserver.Model) bool {
	for _, rm := range models {
		if rm.Memory != nil {
			return true
		}
	}
	return false
}

// routerModelBytes prefers the router's own measurement (the running
// instance's allocation, then the measured minimum) and falls back to wool's
// estimate.
func routerModelBytes(rm llamaserver.Model, fallback uint64, fallbackKnown bool) (uint64, bool) {
	if rm.Memory != nil {
		if rm.Memory.Effective != nil {
			if b, ok := rm.Memory.Effective.SelfBytes(); ok {
				return b, true
			}
		}
		if rm.Memory.Min != nil {
			if b, ok := rm.Memory.Min.SelfBytes(); ok {
				return b, true
			}
		}
	}
	return fallback, fallbackKnown
}

// predictRouterEviction removes approximate-LRU residents until the candidate
// fits both the count limit and, when the router reports memory, the margin.
// A resident that is busy is never evicted, which the API cannot tell us.
func predictRouterEviction(resident []routerResidentModel, candidate string,
	candidateBytes uint64, candidateKnown bool, limit int, memory routerMemoryBudget) routerEviction {
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
	}
	if limit > 0 {
		plan.OverLimit = len(resident) + 1 - limit
		if plan.OverLimit < 0 {
			plan.OverLimit = 0
		}
	}

	// Memory pressure needs a candidate size and current free memory. Without
	// either, eviction cannot be ruled out; keep count-only victims but label
	// the prediction unknown.
	memoryCheck := memory.Enabled
	if memoryCheck && (!memory.Known || !candidateKnown) {
		plan.EstimatesKnown = false
		memoryCheck = false
	}

	ordered := append([]routerResidentModel(nil), resident...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.LoadedAt.IsZero() != b.LoadedAt.IsZero() {
			return !a.LoadedAt.IsZero()
		}
		return a.LoadedAt.Before(b.LoadedAt)
	})

	free := memory.Free
	if memoryCheck {
		if candidateBytes >= free {
			free = 0
		} else {
			free -= candidateBytes
		}
	}
	victims := 0
	for {
		countExceeded := limit > 0 && len(resident)-victims+1 > limit
		memoryExceeded := memoryCheck && free < memory.Margin
		if !countExceeded && !memoryExceeded {
			break
		}
		if victims >= len(ordered) {
			break
		}
		victim := ordered[victims]
		if memoryCheck && !victim.Known {
			// The router will evict this model, but without its size we cannot
			// tell whether that frees enough memory.
			plan.EstimatesKnown = false
			memoryCheck = false
		} else if memoryCheck {
			free += victim.Bytes
		}
		plan.Victims = append(plan.Victims, victim.ID)
		victims++
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
