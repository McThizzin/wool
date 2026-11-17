package llamaserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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

// NewClientURL builds a client for an explicit base URL. It has no timeout
// because a benchmark request is bounded by its context.
func NewClientURL(base string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		HTTP:    &http.Client{},
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

// CompletionRequest is the subset of /completion options a benchmark needs.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	if err := c.get(ctx, "/health", &h); err != nil {
		return h, err
	}
	return h, nil
}

// Complete sends one non-streaming completion request.
func (c *Client) Complete(ctx context.Context, req CompletionRequest) (Completion, error) {
	var out Completion
	if err := c.post(ctx, "/completion", req, &out); err != nil {
		return out, err
	}
	return out, nil
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

// OpenAIModels lists the model IDs the OpenAI-compatible /v1/models endpoint
// reports. It works for a standalone server and a router.
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
