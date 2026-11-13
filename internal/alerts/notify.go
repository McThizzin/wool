package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

// ErrUnavailable means the optional notifier is not installed. Callers can
// ignore it: the status bar is still the primary surface.
var ErrUnavailable = errors.New("notifier unavailable")

// Notifier delivers one alert. Implementations must be safe to call off the
// UI goroutine and must not log their configuration.
type Notifier interface {
	Notify(ctx context.Context, a Alert) error
}

// DesktopNotifier shells out to notify-send. A missing binary degrades to
// ErrUnavailable instead of an error the UI would show.
type DesktopNotifier struct {
	Bin string
}

func (n DesktopNotifier) Notify(ctx context.Context, a Alert) error {
	bin := n.Bin
	if bin == "" {
		bin = "notify-send"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "wool: "+a.Subject, a.Message)
	if err := cmd.Run(); err != nil {
		return errors.New("notify-send failed")
	}
	return nil
}

// WebhookNotifier POSTs one JSON object per alert. The URL is never included
// in an error, so a token in the URL cannot leak into a toast or log line.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// WebhookVersion is the payload schema version.
const WebhookVersion = 1

type webhookPayload struct {
	Version  int       `json:"version"`
	At       time.Time `json:"at"`
	Rule     string    `json:"rule"`
	Severity string    `json:"severity"`
	Subject  string    `json:"subject"`
	Message  string    `json:"message"`
}

func (n WebhookNotifier) Notify(ctx context.Context, a Alert) error {
	if n.URL == "" {
		return ErrUnavailable
	}
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	body, err := json.Marshal(webhookPayload{
		Version:  WebhookVersion,
		At:       a.At,
		Rule:     a.Rule,
		Severity: a.Severity,
		Subject:  a.Subject,
		Message:  a.Message,
	})
	if err != nil {
		return errors.New("webhook: encode failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("webhook: invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("webhook: request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Notifiers builds the configured delivery chain. Both are optional and the
// status bar always receives the alert regardless of the result.
func Notifiers(cfg Config) []Notifier {
	var out []Notifier
	if cfg.Notify {
		out = append(out, DesktopNotifier{})
	}
	if cfg.Webhook != "" {
		out = append(out, WebhookNotifier{URL: cfg.Webhook})
	}
	return out
}

// Dispatch sends every alert through every notifier and ignores individual
// failures. The caller has already surfaced the alert in the UI.
func Dispatch(ctx context.Context, ns []Notifier, list []Alert) {
	for _, a := range list {
		for _, n := range ns {
			_ = n.Notify(ctx, a)
		}
	}
}
