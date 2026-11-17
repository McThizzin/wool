package llamaserver

import (
	"context"
	"fmt"
)

func (c *Client) OpenAIModels(ctx context.Context) ([]string, error) {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/v1/models", &body); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
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
