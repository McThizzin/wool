package llamaserver

import (
	"context"
	"fmt"
	"time"
)

// GroupClient is the router surface needed to load a warm set: submit a load
// and poll for the final state. *Client implements it.
type GroupClient interface {
	Load(ctx context.Context, model string) error
	Models(ctx context.Context, reload bool) ([]Model, error)
}

// GroupResult is one model's outcome in a warm-set load.
type GroupResult struct {
	Model  string
	Status string
	Err    error
}

// LoadGroup loads models in order, waiting for each to reach loaded or
// sleeping before starting the next. A failure is recorded and the remaining
// models are still attempted. timeout bounds each model's wait; poll defaults
// to 400ms when zero or negative.
func LoadGroup(ctx context.Context, client GroupClient, models []string,
	timeout, poll time.Duration) []GroupResult {
	if poll <= 0 {
		poll = 400 * time.Millisecond
	}
	results := make([]GroupResult, 0, len(models))
	for _, id := range models {
		result := GroupResult{Model: id}
		if err := ctx.Err(); err != nil {
			result.Err = err
			results = append(results, result)
			continue
		}
		// Loading an already resident model is an error in the router API, so
		// skip it and make a repeated group load idempotent.
		current, found, err := groupModel(ctx, client, id)
		if err != nil {
			result.Err = err
			results = append(results, result)
			continue
		}
		if found && (current.Status.Value == "loaded" || current.Status.Value == "sleeping") {
			result.Status = current.Status.Value
			results = append(results, result)
			continue
		}
		// A model that is still loading from an earlier request needs no second
		// load request; just wait for it.
		if !found || current.Status.Value != "loading" {
			if err := client.Load(ctx, id); err != nil {
				result.Err = err
				results = append(results, result)
				continue
			}
		}
		model, err := waitGroupModel(ctx, client, id, timeout, poll)
		result.Status = model.Status.Value
		result.Err = err
		results = append(results, result)
	}
	return results
}

// groupModel returns one model's current state, or found=false when the router
// does not list it.
func groupModel(ctx context.Context, client GroupClient, id string) (Model, bool, error) {
	models, err := client.Models(ctx, false)
	if err != nil {
		return Model{}, false, err
	}
	for _, m := range models {
		if m.ID == id {
			return m, true, nil
		}
	}
	return Model{}, false, nil
}

// waitGroupModel polls until the model is resident or reports a failure.
func waitGroupModel(ctx context.Context, client GroupClient, id string,
	timeout, poll time.Duration) (Model, error) {
	deadline := time.Now().Add(timeout)
	var last Model
	for {
		models, err := client.Models(ctx, false)
		if err != nil {
			return last, err
		}
		found := false
		for _, m := range models {
			if m.ID != id {
				continue
			}
			found = true
			last = m
			if m.Status.Failed {
				msg := fmt.Sprintf("%s (load failed", m.Status.Value)
				if m.Status.ExitCode != nil {
					msg += fmt.Sprintf(", exit %d", *m.Status.ExitCode)
				}
				return m, fmt.Errorf("%s)", msg)
			}
			if m.Status.Value == "loaded" || m.Status.Value == "sleeping" {
				return m, nil
			}
			break
		}
		if !found {
			return last, fmt.Errorf("model %q is not in the router list", id)
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("timed out in state %q, want loaded", last.Status.Value)
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}
