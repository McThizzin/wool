package measure

import (
	"context"
	"errors"
	"strings"
	"time"

	"wool/internal/llamaserver"
)

// DefaultSmokeTimeout bounds the optional post-health generation check.
const DefaultSmokeTimeout = 30 * time.Second

// SmokePrompt is deliberately tiny: it exercises tokenization, the chat
// template and one generation step without needing a real conversation.
const SmokePrompt = "Reply with the single word: ok"

// SmokeResult is one verification attempt. Err is a short user-facing reason.
type SmokeResult struct {
	At      time.Time
	Latency time.Duration
	Content string
	Err     string
}

// OK reports whether the server produced a non-empty response.
func (r SmokeResult) OK() bool { return r.Err == "" }

// RunSmoke sends one tiny completion request. The timeout covers the whole
// request, including connection setup and generation.
func RunSmoke(ctx context.Context, client *llamaserver.Client, timeout time.Duration) SmokeResult {
	if client == nil {
		return SmokeResult{Err: "no client"}
	}
	if timeout <= 0 {
		timeout = DefaultSmokeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	completion, err := client.Complete(ctx, llamaserver.CompletionRequest{
		Prompt:      SmokePrompt,
		NPredict:    8,
		Temperature: 0,
		Seed:        42,
		CachePrompt: false,
		Stream:      false,
	})
	res := SmokeResult{At: start, Latency: time.Since(start)}
	switch {
	case err != nil:
		if errors.Is(err, context.DeadlineExceeded) {
			res.Err = "timeout"
		} else {
			res.Err = shortError(err.Error())
		}
	case strings.TrimSpace(completion.Content) == "":
		res.Err = "empty response"
	default:
		res.Content = completion.Content
	}
	return res
}

func shortError(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 120
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// Verification is the newest smoke-test result for one launch shape.
type Verification struct {
	At        time.Time `json:"at"`
	LatencyMS float64   `json:"latency_ms,omitempty"`
	Err       string    `json:"error,omitempty"`
}

// OK reports whether the recorded attempt succeeded.
func (v Verification) OK() bool { return v.Err == "" }

// LatestVerification returns the newest smoke-test attempt for shape. Records
// that never ran a smoke test are ignored.
func (s *Store) LatestVerification(shape Shape) (Verification, bool) {
	records, err := s.All()
	if err != nil {
		return Verification{}, false
	}
	var latest Record
	found := false
	for _, r := range records {
		if r.Kind != KindObservation || r.Shape() != shape {
			continue
		}
		if r.VerifiedAt.IsZero() && r.SmokeError == "" {
			continue
		}
		if !found || r.At.After(latest.At) {
			latest, found = r, true
		}
	}
	if !found {
		return Verification{}, false
	}
	at := latest.VerifiedAt
	if at.IsZero() {
		at = latest.At
	}
	return Verification{At: at, LatencyMS: latest.SmokeMS, Err: latest.SmokeError}, true
}
