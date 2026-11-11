package app

import (
	"time"

	"wool/internal/llamaserver"
)

// infState classifies why a source can or cannot show inference numbers.
type infState int

const (
	infOK        infState = iota // metrics scrape succeeded
	infNoMetrics                 // instance is alive but the metrics scrape failed
	infNotLoaded                 // the selected router model is not loaded
	infDown                      // no instance is running
)

// infSource is one scrapeable inference target: the standalone server or the
// selected router model.
type infSource struct {
	kind    string // "server" or "router"
	model   string // what it is serving (router model ID, preset or model name)
	url     string
	metrics llamaserver.Metrics
	slots   []llamaserver.Slot
	state   infState
	err     string // scrape failure reason for infNoMetrics
}

// key identifies the target a Rates baseline belongs to. Switching keys
// invalidates the baseline because counters differ between targets.
func (s infSource) key() string {
	if s.kind == "router" {
		return "router/" + s.model
	}
	return "server"
}

// inference is the Inference tab state: the preferred source, the delta
// tracker and the sparkline history.
type inference struct {
	prefer  string // "server" or "router"; "" follows the default order
	key     string // source the rates baseline belongs to
	rates   llamaserver.Rates
	hist    inferenceHistory
	resetAt time.Time
}

// inferenceHistory keeps the same 120-sample window as the hardware graphs.
type inferenceHistory struct {
	Prompt   []float64 // prompt tokens/s
	Gen      []float64 // generation tokens/s
	Requests []float64 // requests processing
	KV       []float64 // aggregate KV cache fill percent
	Spec     []float64 // speculative acceptance percent
}

const infHistoryLen = 120

// pushSeries appends one sample, dropping the oldest beyond the window.
func pushSeries(dst []float64, v float64) []float64 {
	dst = append(dst, v)
	if len(dst) > infHistoryLen {
		dst = dst[len(dst)-infHistoryLen:]
	}
	return dst
}

// push records one scrape. Failed scrapes never reach it, so a gap in the
// sparkline is honest while a zero would not be.
func (h *inferenceHistory) push(src infSource, sum llamaserver.SlotSummary, rates llamaserver.Rates) {
	h.Prompt = pushSeries(h.Prompt, rates.PromptTPS)
	h.Gen = pushSeries(h.Gen, rates.GenTPS)
	h.Requests = pushSeries(h.Requests, src.metrics.RequestsProcessing)
	h.KV = pushSeries(h.KV, sum.KVPercent())
	if rates.SpecSeen {
		h.Spec = pushSeries(h.Spec, rates.SpecRate)
	}
}

// serverSource describes the standalone server from the latest tick.
func (m *Model) serverSource() infSource {
	src := infSource{kind: "server", model: m.server.Model, url: m.server.URL()}
	if m.server.Preset != "" {
		src.model = m.server.Preset
	}
	if !m.hasServer {
		src.state = infDown
		return src
	}
	src.metrics, src.slots = m.srvStats, m.srvSlots
	switch {
	case m.srvStatsErr == "":
		src.state = infOK
	default:
		src.state, src.err = infNoMetrics, m.srvStatsErr
	}
	return src
}

// routerSource describes the selected router model from the latest tick.
func (m *Model) routerSource() infSource {
	src := infSource{kind: "router", url: m.router.URL()}
	if !m.hasRouter {
		src.state = infDown
		return src
	}
	rm, ok := m.selectedRouterModel()
	if !ok {
		src.state = infNotLoaded
		return src
	}
	src.model = rm.ID
	if rm.Status.Value != "loaded" && rm.Status.Value != "sleeping" {
		src.state = infNotLoaded
		return src
	}
	src.metrics, src.slots = m.stats, m.slots
	switch {
	case m.statsID != rm.ID:
		// The selection moved while a scrape was in flight. Keep the zero
		// value rather than attributing another model's counters to this
		// one; the next tick scrapes the new target.
		src.metrics, src.slots = llamaserver.Metrics{}, nil
	case m.statsErr != "":
		src.state, src.err = infNoMetrics, m.statsErr
	default:
		src.state = infOK
	}
	return src
}

// infResolve picks the inference source: the user's choice while its instance
// is alive, otherwise the standalone server, otherwise the router. It never
// mutates the model; applyTick drops stale preferences.
func (m *Model) infResolve() infSource {
	var candidates []infSource
	if m.hasServer {
		candidates = append(candidates, m.serverSource())
	}
	if m.hasRouter {
		candidates = append(candidates, m.routerSource())
	}
	if m.inf.prefer != "" {
		for _, c := range candidates {
			if c.kind == m.inf.prefer {
				return c
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return m.serverSource() // both down; describe the standalone server
}

// updateInference folds the newest scrape into the rate tracker and history
// for the resolved source. It runs on every tick so the graphs stay live
// even when the Inference tab is not focused.
func (m *Model) updateInference(at time.Time) {
	src := m.infResolve()
	if src.key() != m.inf.key {
		// Different target: deltas against the old baseline would lie.
		m.inf = inference{prefer: m.inf.prefer, key: src.key()}
	}
	if src.state != infOK {
		return
	}
	m.inf.rates.Update(src.metrics, at)
	if m.inf.rates.Reset {
		m.inf.resetAt = at
	}
	if m.inf.rates.Window > 0 {
		// The seeding scrape and a counter reset produce no window; pushing a
		// zero would draw a throughput dip that never happened.
		m.inf.hist.push(src, llamaserver.SummarizeSlots(src.slots), m.inf.rates)
	}
}

// cycleInfSource switches the panel between the standalone server and the
// selected router model, refusing targets that are not running.
func (m *Model) cycleInfSource() {
	cur := m.infResolve()
	want := "router"
	if cur.kind == "router" {
		want = "server"
	}
	var toast string
	switch want {
	case "server":
		if !m.hasServer {
			m.setToast("standalone server is not running (s starts it)")
			return
		}
		toast = "inference source: standalone server"
	case "router":
		if !m.hasRouter {
			m.setToast("router is not running (r starts it)")
			return
		}
		if rm, ok := m.selectedRouterModel(); !ok || (rm.Status.Value != "loaded" && rm.Status.Value != "sleeping") {
			m.setToast("no router model is loaded (l loads the selection)")
			return
		}
		toast = "inference source: router " + m.selectedRouterID()
	}
	// Reset immediately so the panel does not show the old source's numbers
	// under the new header until the next tick re-seeds them.
	m.inf = inference{prefer: want}
	m.setToast(toast)
}
