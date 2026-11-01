package measure

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// DefaultSettle lists sample offsets from the first successful /health. The
// first sample catches a server that is still faulting weights in; the second
// is used for the recorded observation.
var DefaultSettle = []time.Duration{5 * time.Second, 15 * time.Second}

// Capture takes post-launch samples and appends one record.
type Capture struct {
	Store         *Store
	Record        Record
	Baseline      uint64
	BaselineKnown bool

	// Sample returns used VRAM. Sleep and Now are injectable for tests.
	Sample func() (uint64, bool)
	Sleep  func(time.Duration)
	Now    func() time.Time
	// At lists the offsets from health at which to sample. It defaults to
	// DefaultSettle. Offsets need not be monotonic; a non-increasing offset
	// samples immediately.
	At []time.Duration
}

// Run waits through the settle window, samples, and appends the record. It
// returns an error when the store or sampler is missing; a cancelled context
// aborts before writing.
func (c Capture) Run(ctx context.Context) error {
	if c.Store == nil {
		return errors.New("no measurement store")
	}
	if c.Sample == nil {
		return errors.New("no VRAM sampler")
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	settle := c.At
	if settle == nil {
		settle = DefaultSettle
	}

	rec := c.Record
	rec.Samples = nil
	known := false
	var elapsed time.Duration
	for _, offset := range settle {
		if offset < 0 {
			offset = 0
		}
		if delay := offset - elapsed; delay > 0 {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
			} else {
				sleep(delay)
			}
		}
		if offset > elapsed {
			elapsed = offset
		}
		v, ok := c.Sample()
		if !ok {
			continue
		}
		rec.Samples = append(rec.Samples, v)
		known = true
	}
	rec.SampleCount = len(rec.Samples)
	rec.BaselineBytes = c.Baseline
	rec.BaselineKnown = c.BaselineKnown && known
	if rec.BaselineKnown {
		last := rec.Samples[len(rec.Samples)-1]
		if last >= c.Baseline {
			rec.ObservedBytes = last - c.Baseline
		} else {
			rec.BaselineKnown = false
		}
	}
	rec.At = now()
	return c.Store.Append(rec)
}

// ServerVersion returns the first line of `llama-server --version`.
func ServerVersion(bin string) string {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
