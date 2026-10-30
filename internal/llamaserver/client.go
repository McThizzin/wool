package llamaserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to either a standalone llama-server or a router.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewClient builds a client for host:port, mapping wildcard listen addresses
// to loopback.
func NewClient(host string, port int) *Client {
	return &Client{
		BaseURL: BaseURL(host, port),
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// BaseURL returns an HTTP URL suitable for dialing a server that may be
// listening on a wildcard address.
func BaseURL(host string, port int) string {
	h := strings.TrimSpace(strings.Split(host, ",")[0])
	switch h {
	case "", "0.0.0.0", "*":
		h = "127.0.0.1"
	case "::", "[::]":
		h = "::1"
	}
	return "http://" + net.JoinHostPort(h, strconv.Itoa(port))
}

// APIError is a structured error from llama-server.
type APIError struct {
	Status  int
	Code    int    `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("llama-server: %s (code %d)", e.Message, e.Code)
	}
	return fmt.Sprintf("llama-server: HTTP %d", e.Status)
}

// Health is the /health response.
type Health struct {
	Status string `json:"status"`
}

// ModelStatus is the router's per-model status block.
type ModelStatus struct {
	Value    string   `json:"value"`
	Args     []string `json:"args"`
	Preset   string   `json:"preset"`
	ExitCode *int     `json:"exit_code"`
	Failed   bool     `json:"failed"`
}

// Model is one entry from GET /models.
type Model struct {
	ID      string      `json:"id"`
	Aliases []string    `json:"aliases"`
	Tags    []string    `json:"tags"`
	Object  string      `json:"object"`
	OwnedBy string      `json:"owned_by"`
	Created int64       `json:"created"`
	Status  ModelStatus `json:"status"`
}

// Routed reports whether the model has been loaded at least once.
func (m Model) Routed() bool {
	return m.Status.Value == "loaded" || m.Status.Value == "sleeping" || len(m.Status.Args) > 0
}

// Slot is one entry from /slots.
type Slot struct {
	ID               int  `json:"id"`
	NCtx             int  `json:"n_ctx"`
	NPast            int  `json:"n_past"`
	NPromptTokens    int  `json:"n_prompt_tokens"`
	NGeneratedTokens int  `json:"n_generated_tokens"`
	IsProcessing     bool `json:"is_processing"`
	Speculative      bool `json:"speculative"`
}

// Metrics holds the counters exposed by /metrics. Fields absent from the
// response remain zero.
type Metrics struct {
	PromptTokens       float64
	PromptTokensCached float64
	PromptSeconds      float64
	PredictedTokens    float64
	PredictedSeconds   float64
	NDecode            float64
	NTokensMax         float64
	SpecDraftTokens    float64
	SpecAcceptedTokens float64
	RequestsProcessing float64
	RequestsDeferred   float64
	BusySlotsPerDecode float64
	Raw                map[string]float64
}

// PromptTPS is prompt tokens per second, averaged over the server lifetime.
func (m Metrics) PromptTPS() float64 {
	if m.PromptSeconds <= 0 {
		return 0
	}
	return m.PromptTokens / m.PromptSeconds
}

// PredictedTPS is generated tokens per second.
func (m Metrics) PredictedTPS() float64 {
	if m.PredictedSeconds <= 0 {
		return 0
	}
	return m.PredictedTokens / m.PredictedSeconds
}

// SpecAcceptRate is accepted/draft tokens for speculative decoding.
func (m Metrics) SpecAcceptRate() float64 {
	if m.SpecDraftTokens <= 0 {
		return 0
	}
	return m.SpecAcceptedTokens / m.SpecDraftTokens * 100
}

// Health calls GET /health.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	if err := c.get(ctx, "/health", &h); err != nil {
		return h, err
	}
	return h, nil
}

// WaitReady polls /health until it reports ok or the timeout expires.
func (c *Client) WaitReady(ctx context.Context, timeout, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var last error
	for {
		attempt, cancel := context.WithTimeout(ctx, minDuration(interval, 2*time.Second))
		h, err := c.Health(attempt)
		cancel()
		if err == nil && h.Status == "ok" {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("health status %q", h.Status)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server did not become ready in %s: %w", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Models lists router models. With reload, the router re-reads its INI first.
func (c *Client) Models(ctx context.Context, reload bool) ([]Model, error) {
	path := "/models"
	if reload {
		path += "?reload=1"
	}
	var body struct {
		Data    []Model   `json:"data"`
		Success bool      `json:"success"`
		Error   *APIError `json:"error"`
	}
	if err := c.get(ctx, path, &body); err != nil {
		return nil, err
	}
	if body.Error != nil {
		return nil, body.Error
	}
	return body.Data, nil
}

// ReloadModels asks the router to reload its preset file.
func (c *Client) ReloadModels(ctx context.Context) error {
	_, err := c.Models(ctx, true)
	return err
}

// Load asks the router to load a model. It returns as soon as the request is
// accepted; poll Models for the resulting status.
func (c *Client) Load(ctx context.Context, model string) error {
	return c.action(ctx, "/models/load", model)
}

// Unload asks the router to unload a model.
func (c *Client) Unload(ctx context.Context, model string) error {
	return c.action(ctx, "/models/unload", model)
}

func (c *Client) action(ctx context.Context, path, model string) error {
	var body struct {
		Success bool      `json:"success"`
		Error   *APIError `json:"error"`
	}
	if err := c.post(ctx, path, map[string]string{"model": model}, &body); err != nil {
		return err
	}
	if body.Error != nil {
		return body.Error
	}
	if !body.Success {
		return fmt.Errorf("llama-server: %s was not accepted", path)
	}
	return nil
}

// Props returns the raw /props object. Router and single server differ.
func (c *Client) Props(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/props", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Slots returns slot state. A router needs the model ID; a standalone server
// ignores it.
func (c *Client) Slots(ctx context.Context, model string) ([]Slot, error) {
	path := "/slots"
	if model != "" {
		path += "?model=" + url.QueryEscape(model)
	}
	var raw json.RawMessage
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	var list []Slot
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var wrapper struct {
		Slots []Slot `json:"slots"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("decode /slots: %w", err)
	}
	return wrapper.Slots, nil
}

// Metrics fetches and parses /metrics. A router needs the model ID.
func (c *Client) Metrics(ctx context.Context, model string) (Metrics, error) {
	path := "/metrics"
	if model != "" {
		path += "?model=" + url.QueryEscape(model)
	}
	text, err := c.getText(ctx, path)
	if err != nil {
		return Metrics{}, err
	}
	return ParseMetrics(text), nil
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

func (c *Client) get(ctx context.Context, path string, out any) error {
	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := checkStatus(status, body); err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) getText(ctx context.Context, path string) (string, error) {
	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	if err := checkStatus(status, body); err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	body, status, err := c.do(ctx, http.MethodPost, path, data)
	if err != nil {
		return err
	}
	if err := checkStatus(status, body); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func checkStatus(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	apiErr := &APIError{Status: status}
	if err := json.Unmarshal(body, apiErr); err == nil && apiErr.Message != "" {
		apiErr.Status = status
		return apiErr
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	apiErr.Message = msg
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
