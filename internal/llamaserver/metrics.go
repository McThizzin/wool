package llamaserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var ErrMetricsDisabled = errors.New("metrics are disabled on this server (start it with --metrics)")

// Metrics fetches and parses /metrics. A router needs the model ID.
func (c *Client) Metrics(ctx context.Context, model string) (Metrics, error) {
	path := "/metrics"
	if model != "" {
		path += "?model=" + url.QueryEscape(model)
	}
	text, err := c.getText(ctx, path)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return Metrics{}, ErrMetricsDisabled
		}
		return Metrics{}, err
	}
	return ParseMetrics(text), nil
}

// ScrapeMetrics fetches /metrics for one target and classifies a failure for
// display. It does not touch /slots, so it is safe for a sleeping router model:
// llama-server serves cached metrics while sleeping, but /slots asks the model
// to wake up.
func (c *Client) ScrapeMetrics(ctx context.Context, model string) (Metrics, string) {
	var m Metrics
	var reason string
	st, err := c.Metrics(ctx, model)
	switch {
	case err != nil:
		reason = strings.TrimSpace(err.Error())
	case len(st.Raw) == 0:
		reason = "no metrics exposed"
	default:
		m = st
	}
	return m, reason
}

// ScrapeInference fetches /metrics and /slots for one target and classifies
// failures for display. The returned reason is empty when the metrics scrape
// succeeded; when it failed, slots are still returned if /slots works, so a
// metrics-disabled server can show occupancy. Do not call it for a sleeping
// router model: /slots wakes the model.
func (c *Client) ScrapeInference(ctx context.Context, model string) (Metrics, []Slot, string) {
	m, reason := c.ScrapeMetrics(ctx, model)
	var slots []Slot
	if sl, err := c.Slots(ctx, model); err == nil {
		slots = sl
	}
	return m, slots, reason
}

// ParseMetrics parses the Prometheus text exposition format used by
// llama-server. Labels are ignored and duplicate names are summed.
func ParseMetrics(text string) Metrics {
	m := Metrics{Raw: map[string]float64{}}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := parseMetricLine(line)
		if !ok {
			continue
		}
		m.Raw[name] += value
	}
	get := func(names ...string) float64 {
		for _, n := range names {
			if v, ok := m.Raw[n]; ok {
				return v
			}
			if v, ok := m.Raw["llamacpp:"+n]; ok {
				return v
			}
		}
		return 0
	}
	m.PromptTokens = get("prompt_tokens_total")
	m.PromptTokensCached = get("prompt_tokens_cached_total")
	m.PromptSeconds = get("prompt_seconds_total")
	m.PredictedTokens = get("tokens_predicted_total", "predicted_tokens_total")
	m.PredictedSeconds = get("tokens_predicted_seconds_total", "predicted_seconds_total")
	m.NDecode = get("n_decode_total")
	m.NTokensMax = get("n_tokens_max")
	m.SpecDraftTokens = get("spec_decode_num_draft_tokens_total")
	m.SpecAcceptedTokens = get("spec_decode_num_accepted_tokens_total")
	m.RequestsProcessing = get("requests_processing")
	m.RequestsDeferred = get("requests_deferred")
	m.BusySlotsPerDecode = get("n_busy_slots_per_decode")
	return m
}

func parseMetricLine(line string) (string, float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0, false
	}
	name := fields[0]
	if i := strings.IndexByte(name, '{'); i >= 0 {
		name = name[:i]
	}
	v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	if err != nil {
		return "", 0, false
	}
	return name, v, true
}
