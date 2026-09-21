package manage

import (
	"context"
	"encoding/json"
)

// Prompt is one GET /api/get_prompts entry. Type is "public" for the built-in
// prompts, "private" for the caller's own and "team" for shared ones.
type Prompt struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ListPrompts calls GET /api/get_prompts (scope prompts:read).
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, json.RawMessage, error) {
	var out []Prompt
	raw, err := c.getJSON(ctx, "/api/get_prompts", nil, &out)
	return out, raw, err
}

// Tool is the subset of a GET /api/get_tools entry the CLI renders. Name is
// the tool type (e.g. "brave"); CustomName is the user's label.
type Tool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CustomName  string `json:"customName"`
	DisplayName string `json:"displayName"`
	Status      bool   `json:"status"`
	Builtin     bool   `json:"builtin"`
	Default     bool   `json:"default"`
	Ownership   string `json:"ownership"`
}

// ListTools calls GET /api/get_tools (scope tools:read). raw is the server's
// `tools` array.
func (c *Client) ListTools(ctx context.Context) ([]Tool, json.RawMessage, error) {
	var env struct {
		Tools json.RawMessage `json:"tools"`
	}
	if _, err := c.getJSON(ctx, "/api/get_tools", nil, &env); err != nil {
		return nil, nil, err
	}
	var out []Tool
	if len(env.Tools) > 0 {
		if err := decode("GET", "/api/get_tools", env.Tools, &out); err != nil {
			return nil, nil, err
		}
	}
	return out, env.Tools, nil
}
