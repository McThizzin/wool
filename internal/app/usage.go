package app

import (
	"fmt"
	"time"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/measure"
)

// usageFlushInterval bounds how often a dirty usage snapshot is written. Token
// deltas accumulate in memory between flushes; a clean exit flushes the final
// snapshot.
const usageFlushInterval = 15 * time.Second

// usageBaseline is the last cumulative /metrics counters seen for one model.
type usageBaseline struct {
	prompt float64
	gen    float64
	seeded bool
}

// usageTracker turns the poll loop into per-model usage. Life is the persisted
// total across TUI sessions; session is the part observed by this process.
type usageTracker struct {
	loaded    bool
	life      measure.Usage
	session   measure.Usage
	resident  map[string]bool
	base      map[string]usageBaseline
	seeded    bool
	dirty     bool
	lastFlush time.Time
}

// ensure loads the persisted snapshot once. The store is passed in rather than
// captured so tests can swap it after the model is built.
func (u *usageTracker) ensure(store *measure.Store) {
	if u.loaded {
		return
	}
	u.loaded = true
	if store != nil {
		if life, ok, err := store.LatestUsage(); err == nil && ok {
			u.life = life
		}
	}
	if u.life.Models == nil {
		u.life.Models = make(map[string]measure.ModelUsage)
	}
	if u.session.Models == nil {
		u.session.Models = make(map[string]measure.ModelUsage)
	}
	if u.resident == nil {
		u.resident = make(map[string]bool)
	}
	if u.base == nil {
		u.base = make(map[string]usageBaseline)
	}
}

// track folds one successful /models poll into the tracker. A failed poll is
// not passed here because an empty list would look like every model unloaded.
func (u *usageTracker) track(store *measure.Store, at time.Time, inst daemon.Instance,
	models []llamaserver.Model, metrics map[string]llamaserver.Metrics) {
	u.ensure(store)
	restarted := u.observeRouter(inst)

	first := !u.seeded
	u.seeded = true
	if first && !restarted {
		// A TUI started while models were already resident. Seed them without
		// counting a load; otherwise every TUI restart would inflate the count.
		for _, rm := range models {
			if routerResident(rm) {
				u.resident[rm.ID] = true
			}
		}
	}

	for _, rm := range models {
		id := rm.ID
		if !routerResident(rm) {
			if u.resident[id] {
				delete(u.resident, id)
				u.dirty = true
			}
			delete(u.base, id)
			continue
		}
		if !u.resident[id] {
			u.resident[id] = true
			u.life.AddLoad(id, at)
			u.session.AddLoad(id, at)
			u.dirty = true
		}
		m, ok := metrics[id]
		if !ok {
			continue
		}
		b := u.base[id]
		if !b.seeded {
			u.base[id] = usageBaseline{prompt: m.PromptTokens, gen: m.PredictedTokens, seeded: true}
			continue
		}
		dPrompt := m.PromptTokens - b.prompt
		dGen := m.PredictedTokens - b.gen
		b.prompt, b.gen = m.PromptTokens, m.PredictedTokens
		u.base[id] = b
		if dPrompt < 0 || dGen < 0 {
			// The model reloaded and its counters started over. Reseed above
			// instead of recording a negative delta.
			continue
		}
		if dPrompt > 0 || dGen > 0 {
			u.life.AddTokens(id, uint64(dPrompt), uint64(dGen), at)
			u.session.AddTokens(id, uint64(dPrompt), uint64(dGen), at)
			u.dirty = true
		}
		// A sleeping model serves cached metrics. Its stale request gauge must
		// not keep refreshing last-used while it is asleep.
		if rm.Status.Value == "loaded" && m.RequestsProcessing > 0 {
			u.life.Touch(id, at)
			u.session.Touch(id, at)
			u.dirty = true
		}
	}
}

// observeRouter notices a new router process. A restart clears the resident
// and counter baselines so the new process's models count as fresh loads and
// its counters are seeded instead of producing negative deltas. The first
// observation of a persisted key is not a restart.
func (u *usageTracker) observeRouter(inst daemon.Instance) (restarted bool) {
	key := routerInstanceKey(inst)
	if key == "" || key == u.life.Router.Key {
		return false
	}
	restarted = u.life.Router.Key != ""
	if restarted {
		u.life.Router.Restarts++
		u.session.Router.Restarts++
		u.resident = make(map[string]bool)
		u.base = make(map[string]usageBaseline)
	}
	u.life.Router.Key = key
	u.life.Router.StartedAt = inst.StartedAt
	u.session.Router.Key = key
	u.session.Router.StartedAt = inst.StartedAt
	u.dirty = true
	return restarted
}

// flush writes a dirty snapshot when the debounce interval has elapsed.
func (u *usageTracker) flush(store *measure.Store, at time.Time) {
	if !u.loaded || !u.dirty || store == nil {
		return
	}
	if !u.lastFlush.IsZero() && at.Sub(u.lastFlush) < usageFlushInterval {
		return
	}
	u.write(store, at)
}

// flushNow writes a dirty snapshot regardless of the debounce interval. It is
// used on exit so the last window survives a restart.
func (u *usageTracker) flushNow(store *measure.Store, at time.Time) {
	if !u.loaded || !u.dirty || store == nil {
		return
	}
	u.write(store, at)
}

func (u *usageTracker) write(store *measure.Store, at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	u.life.Router.UpdatedAt = at
	if err := store.AppendUsage(u.life); err != nil {
		// Usage is best-effort; the next tick retries.
		return
	}
	u.dirty = false
	u.lastFlush = at
}

// routerInstanceKey identifies a router process. PID plus start ticks survives
// a host/port change and changes when the process restarts.
func routerInstanceKey(inst daemon.Instance) string {
	if !inst.Alive {
		return ""
	}
	switch {
	case inst.StartTicks > 0:
		return fmt.Sprintf("%d/%d", inst.PID, inst.StartTicks)
	case inst.PID > 0:
		return fmt.Sprintf("pid/%d", inst.PID)
	case !inst.StartedAt.IsZero():
		return fmt.Sprintf("at/%d", inst.StartedAt.UnixNano())
	default:
		return ""
	}
}
