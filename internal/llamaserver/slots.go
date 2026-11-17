package llamaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

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

// ErrMetricsDisabled reports a 404 from /metrics, which llama-server answers
// with when the server was started without --metrics.
