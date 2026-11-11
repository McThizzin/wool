package llamaserver

import "time"

// Rates converts the cumulative /metrics counters into rates for the window
// between two scrapes. Call Update once per scrape, in order.
//
// Tokens per second prefer the server's own busy counters
// (prompt_seconds_total, tokens_predicted_seconds_total) so idle time is not
// counted against throughput; the wall-clock window is the fallback when a
// seconds counter did not move. A counter that decreases means the server
// restarted: the baseline is re-seeded instead of producing a negative rate,
// and Reset reports it for one update.
type Rates struct {
	prev   Metrics
	prevAt time.Time

	// Seeded is true once one scrape has established the baseline. Rates
	// become meaningful from the second update on.
	Seeded bool

	// Results of the most recent window.
	PromptTPS    float64
	GenTPS       float64
	PromptTokens float64 // prompt-token delta over the window
	GenTokens    float64 // generated-token delta over the window
	SpecDraft    float64 // draft-token delta over the window
	SpecAccepted float64 // accepted-token delta over the window
	SpecRate     float64 // accepted/draft percent (window, else lifetime)
	SpecSeen     bool    // speculative decoding has produced tokens
	Window       time.Duration
	Reset        bool
}

// Update folds one scrape into the tracker. The first call only seeds the
// baseline; rates become available from the second scrape on.
func (r *Rates) Update(m Metrics, at time.Time) {
	if !r.Seeded {
		r.prev, r.prevAt, r.Seeded = m, at, true
		r.Reset = false
		r.apply(m, 0, 0, 0, 0, 0, 0, 0)
		return
	}
	window := at.Sub(r.prevAt)
	if window < 0 {
		window = 0
	}
	dPrompt := m.PromptTokens - r.prev.PromptTokens
	dGen := m.PredictedTokens - r.prev.PredictedTokens
	dPromptSec := m.PromptSeconds - r.prev.PromptSeconds
	dGenSec := m.PredictedSeconds - r.prev.PredictedSeconds
	dDraft := m.SpecDraftTokens - r.prev.SpecDraftTokens
	dAccepted := m.SpecAcceptedTokens - r.prev.SpecAcceptedTokens
	r.prev, r.prevAt = m, at
	if dPrompt < 0 || dGen < 0 || dPromptSec < 0 || dGenSec < 0 || dDraft < 0 || dAccepted < 0 {
		// A counter went backwards: the server restarted.
		r.Reset = true
		r.apply(m, 0, 0, 0, 0, 0, 0, 0)
		return
	}
	r.Reset = false
	r.apply(m, window, dPrompt, dGen, dPromptSec, dGenSec, dDraft, dAccepted)
}

// apply records one window's result. m is the newer scrape.
func (r *Rates) apply(m Metrics, window time.Duration, dPrompt, dGen, dPromptSec, dGenSec, dDraft, dAccepted float64) {
	r.Window = window
	r.PromptTokens, r.GenTokens = dPrompt, dGen
	r.PromptTPS = tokenRate(dPrompt, dPromptSec, window)
	r.GenTPS = tokenRate(dGen, dGenSec, window)
	r.SpecDraft, r.SpecAccepted = dDraft, dAccepted
	switch {
	case dDraft > 0:
		r.SpecRate = dAccepted / dDraft * 100
		r.SpecSeen = true
	case m.SpecDraftTokens > 0:
		// No drafts in this window; keep showing the lifetime rate.
		r.SpecRate = m.SpecAcceptRate()
		r.SpecSeen = true
	default:
		r.SpecRate, r.SpecSeen = 0, false
	}
}

// tokenRate prefers the server's busy seconds, falling back to the wall-clock
// window, so a stalled seconds counter cannot zero out real tokens.
func tokenRate(tokens, seconds float64, window time.Duration) float64 {
	switch {
	case tokens <= 0:
		return 0
	case seconds > 0:
		return tokens / seconds
	case window > 0:
		return tokens / window.Seconds()
	default:
		return 0
	}
}

// SlotSummary aggregates a /slots response into occupancy numbers.
type SlotSummary struct {
	Total  int
	Busy   int
	KVUsed int // sum of n_past
	KVCap  int // sum of n_ctx
}

// SummarizeSlots counts busy slots and aggregate KV cache fill.
func SummarizeSlots(slots []Slot) SlotSummary {
	s := SlotSummary{Total: len(slots)}
	for _, sl := range slots {
		if sl.IsProcessing {
			s.Busy++
		}
		s.KVUsed += sl.NPast
		s.KVCap += sl.NCtx
	}
	return s
}

// KVPercent is the aggregate KV cache fill, 0..100. It is 0 when the slots
// report no context.
func (s SlotSummary) KVPercent() float64 {
	if s.KVCap <= 0 {
		return 0
	}
	return float64(s.KVUsed) / float64(s.KVCap) * 100
}
