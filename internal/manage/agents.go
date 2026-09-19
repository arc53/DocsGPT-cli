package manage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Identity is the GET /api/user/me document. Token is set only when the
// caller authenticated with a personal access token.
type Identity struct {
	Success    bool       `json:"success"`
	UserID     string     `json:"user_id"`
	Roles      []string   `json:"roles"`
	Email      string     `json:"email,omitempty"`
	Name       string     `json:"name,omitempty"`
	AuthMethod string     `json:"auth_method,omitempty"` // "pat" for a personal access token
	Token      *TokenInfo `json:"token,omitempty"`

	Raw json.RawMessage `json:"-"` // server document, verbatim
}

// TokenInfo describes the presented personal access token.
type TokenInfo struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Scopes         []string            `json:"scopes"`
	ResourceFilter map[string][]string `json:"resource_filter"` // family -> allowed ids; empty = unrestricted
}

// Me calls GET /api/user/me.
func (c *Client) Me(ctx context.Context) (*Identity, error) {
	var id Identity
	raw, err := c.getJSON(ctx, "/api/user/me", nil, &id)
	if err != nil {
		return nil, err
	}
	id.Raw = raw
	return &id, nil
}

// Agent is the subset of an agent document the CLI renders. The full server
// document is available through the raw return of ListAgents.
type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	AgentType   string `json:"agent_type"`
	Status      string `json:"status"`
	Ownership   string `json:"ownership"`
	UpdatedAt   any    `json:"updated_at"`
}

// ListAgents calls GET /api/get_agents (scope agents:read).
func (c *Client) ListAgents(ctx context.Context) ([]Agent, json.RawMessage, error) {
	var out []Agent
	raw, err := c.getJSON(ctx, "/api/get_agents", nil, &out)
	return out, raw, err
}

// ExportAgent calls GET /api/export_agent?id= and returns the YAML document
// (scope agents:read).
func (c *Client) ExportAgent(ctx context.Context, id string) ([]byte, error) {
	return c.getRaw(ctx, "/api/export_agent", url.Values{"id": {id}}, "application/x-yaml, application/json")
}

// DeleteAgent calls DELETE /api/delete_agent?id= (scope agents:write).
func (c *Client) DeleteAgent(ctx context.Context, id string) error {
	_, err := c.doJSON(ctx, http.MethodDelete, "/api/delete_agent", url.Values{"id": {id}}, nil, nil)
	return err
}

// Plan statuses reported by POST /api/import_agent/plan.
const (
	StatusMatched     = "matched"     // source / built-in model id resolved
	StatusMissing     = "missing"     // source name not found for this user
	StatusReuse       = "reuse"       // tool / prompt / custom model already exists
	StatusCreate      = "create"      // will be created from the file
	StatusBuiltin     = "builtin"     // built-in tool available on this instance
	StatusUnavailable = "unavailable" // tool type / model id unknown to this instance
	StatusDefault     = "default"     // prompt: the agent uses the default prompt
)

// Plan is the dry-run resolution report for one agent document.
type Plan struct {
	Target   PlanTarget    `json:"target"`
	Sources  []PlanSource  `json:"sources"`
	Tools    []PlanTool    `json:"tools"`
	Prompt   PlanPrompt    `json:"prompt"`
	Models   []PlanModel   `json:"models"`
	Workflow *PlanWorkflow `json:"workflow"`

	Raw json.RawMessage `json:"-"` // the server `plan` object, verbatim
}

// PlanTarget says whether the document creates an agent or updates one
// (matched by metadata.id, then metadata.slug).
type PlanTarget struct {
	Action    string `json:"action"` // create | update
	AgentID   string `json:"agent_id"`
	MatchedBy string `json:"matched_by"` // id | slug
	Status    string `json:"status"`     // existing agent status (draft/published)
}

// PlanSource is one spec.sources entry.
type PlanSource struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"` // matched | missing
	TargetID string `json:"target_id"`
}

// PlanTool is one spec.tools entry. Key ("tool-N", N = index in the file) is
// how the resolution map addresses it.
type PlanTool struct {
	Key             string   `json:"key"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Builtin         bool     `json:"builtin"`
	Status          string   `json:"status"` // builtin | reuse | create | unavailable
	TargetID        string   `json:"target_id"`
	RequiresSecrets []string `json:"requires_secrets"`
}

// PlanPrompt is the spec.prompt resolution.
type PlanPrompt struct {
	Status string `json:"status"` // reuse | create | default
	Name   string `json:"name"`
}

// PlanModel is one spec.model.available entry: a plain model id (ID set) or a
// custom model (DisplayName set).
type PlanModel struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	Status          string   `json:"status"` // matched | unavailable | reuse | create
	RequiresSecrets []string `json:"requires_secrets"`
}

// Label is the identity the resolution map (and the user) addresses.
func (m PlanModel) Label() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.ID
}

// PlanWorkflow describes the workflow graph change of a workflow agent.
type PlanWorkflow struct {
	Action string `json:"action"` // create | update | delete
	Nodes  int    `json:"nodes"`
	Edges  int    `json:"edges"`
}

// ToolDecision is one entry of Resolution.Tools.
type ToolDecision struct {
	// Decision is "reuse" (link ToolID, which must be the caller's tool),
	// "create" (create a new tool even if a (type, name) match exists),
	// "skip" (leave the tool off the agent), or "" (server default: reuse a
	// (type, name) match, otherwise create).
	Decision string `json:"decision,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	// Secrets are merged into the tool config when the tool is created
	// (config-requirement field name -> value).
	Secrets map[string]string `json:"secrets,omitempty"`
}

// ModelDecision is one entry of Resolution.Models: the API key used to create
// a custom model that does not exist yet.
type ModelDecision struct {
	APIKey string `json:"api_key,omitempty"`
}

// Resolution is the `resolution` object of POST /api/import_agent. Every
// section is optional.
type Resolution struct {
	Sources map[string]string        `json:"sources,omitempty"` // spec source name -> existing source id
	Tools   map[string]ToolDecision  `json:"tools,omitempty"`   // "tool-N" -> decision
	Models  map[string]ModelDecision `json:"models,omitempty"`  // custom model display_name -> api key
}

// Empty reports whether no decision is present.
func (r *Resolution) Empty() bool {
	return r == nil || (len(r.Sources) == 0 && len(r.Tools) == 0 && len(r.Models) == 0)
}

// ApplyResult is the POST /api/import_agent reply.
type ApplyResult struct {
	AgentID   string   `json:"agent_id"`
	Action    string   `json:"action"` // created | updated
	Status    string   `json:"status"` // draft on create; unchanged on update
	AgentType string   `json:"agent_type"`
	Slug      string   `json:"slug"`
	Warnings  []string `json:"warnings"`
}

type importRequest struct {
	YAML       string      `json:"yaml"`
	Resolution *Resolution `json:"resolution,omitempty"`
}

// PlanImport calls POST /api/import_agent/plan with {"yaml": <document>}
// (scope agents:write, like the apply itself). Nothing is written server-side.
func (c *Client) PlanImport(ctx context.Context, yamlDoc string) (*Plan, error) {
	var env struct {
		Plan json.RawMessage `json:"plan"`
	}
	if _, err := c.doJSON(ctx, http.MethodPost, "/api/import_agent/plan", nil, importRequest{YAML: yamlDoc}, &env); err != nil {
		return nil, err
	}
	var plan Plan
	if err := decode(http.MethodPost, "/api/import_agent/plan", env.Plan, &plan); err != nil {
		return nil, err
	}
	plan.Raw = env.Plan
	return &plan, nil
}

// ApplyImport calls POST /api/import_agent with
// {"yaml": <document>, "resolution": {...}} (scope agents:write). A new agent
// is created as a draft; an update keeps the matched agent's status.
func (c *Client) ApplyImport(ctx context.Context, yamlDoc string, res *Resolution) (*ApplyResult, error) {
	body := importRequest{YAML: yamlDoc}
	if !res.Empty() {
		body.Resolution = res
	}
	var out ApplyResult
	if _, err := c.doJSON(ctx, http.MethodPost, "/api/import_agent", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
