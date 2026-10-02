package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// WebhookPath is the path of an agent's incoming webhook, up to its token.
const WebhookPath = "/api/webhooks/agents/"

// Webhook is an agent's incoming webhook URL, split into the base URL the API
// is served from and the secret token that ends the path. It never renders the
// token: String and Redacted show "<base>/api/webhooks/agents/...".
type Webhook struct {
	base  string
	token string
}

// ParseWebhookURL accepts http(s)://host[/prefix]/api/webhooks/agents/<token>.
// The error never contains raw, which is a secret.
func ParseWebhookURL(raw string) (*Webhook, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("the webhook URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the webhook URL is not an http(s) URL")
	}
	// The escaped path keeps the token byte for byte as it was given.
	path := u.EscapedPath()
	i := strings.LastIndex(path, WebhookPath)
	token := ""
	if i >= 0 {
		token = path[i+len(WebhookPath):]
	}
	if i < 0 || token == "" || strings.Contains(token, "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("the webhook URL does not look like an agent webhook (%s://%s%s<token>)",
			u.Scheme, u.Host, WebhookPath)
	}
	return &Webhook{base: u.Scheme + "://" + u.Host + path[:i], token: token}, nil
}

// BaseURL is the API base URL the webhook lives under; /api/task_status for
// its runs is served there too.
func (w *Webhook) BaseURL() string { return w.base }

// URL is the full webhook URL, token included. Never print it.
func (w *Webhook) URL() string { return w.base + WebhookPath + w.token }

// Redacted is the URL with the token replaced by "...", safe for logs.
func (w *Webhook) Redacted() string { return w.base + WebhookPath + "..." }

// String keeps %v and %s from ever printing the token.
func (w *Webhook) String() string { return w.Redacted() }

// Redact replaces every occurrence of the token (as given, or unescaped) in s.
func (w *Webhook) Redact(s string) string {
	if w == nil || w.token == "" {
		return s
	}
	s = strings.ReplaceAll(s, w.token, "...")
	if raw, err := url.PathUnescape(w.token); err == nil && raw != w.token && raw != "" {
		s = strings.ReplaceAll(s, raw, "...")
	}
	return s
}

// SameOrigin reports whether the webhook is served from the same scheme and
// host as baseURL, i.e. whether a token meant for baseURL may be sent there.
func (w *Webhook) SameOrigin(baseURL string) bool {
	a, errA := url.Parse(w.base)
	b, errB := url.Parse(strings.TrimSpace(baseURL))
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// AgentWebhook calls GET /api/agent_webhook?id= (scope agents:keys) and returns
// the agent's incoming webhook. The server creates the webhook token if the
// agent has none yet. The server builds the URL from its API_URL setting,
// which is not always the address this client reaches it at (a self-hosted
// default is http://localhost:7091), so the token is re-rooted on c.BaseURL.
func (c *Client) AgentWebhook(ctx context.Context, agentID string) (*Webhook, error) {
	const path = "/api/agent_webhook"
	var out struct {
		WebhookURL string `json:"webhook_url"`
	}
	if _, err := c.getJSON(ctx, path, url.Values{"id": {agentID}}, &out); err != nil {
		return nil, err
	}
	w, err := ParseWebhookURL(out.WebhookURL)
	if err != nil {
		return nil, fmt.Errorf("GET %s: unexpected webhook_url: %w", path, err)
	}
	return &Webhook{base: c.BaseURL, token: w.token}, nil
}

// TriggerResult is the webhook's reply.
type TriggerResult struct {
	Success bool   `json:"success"`
	TaskID  string `json:"task_id"`
}

// TriggerWebhook POSTs payload (JSON, sent verbatim) to the agent's incoming
// webhook; the agent runs asynchronously as task TaskID. idempotencyKey, when
// non-empty, is sent as the Idempotency-Key header: a repeat within the
// server's dedup window returns the original task instead of running again.
//
// The request never carries the client's token: the webhook authenticates by
// the secret in its URL, and the server refuses personal access tokens there.
// Errors name the redacted URL only.
func (c *Client) TriggerWebhook(ctx context.Context, w *Webhook, payload []byte, idempotencyKey string) (*TriggerResult, error) {
	if len(idempotencyKey) > IdempotencyKeyMaxLen {
		return nil, fmt.Errorf("trigger: idempotency key exceeds %d characters", IdempotencyKeyMaxLen)
	}
	anon := *c
	anon.Token = ""
	anon.BaseURL = w.base
	path := WebhookPath + "..." // what errors show instead of the token

	ctx, cancel := withTimeout(ctx, c.Timeout, DefaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build POST %s%s: %w", w.base, path, unwrapURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	body, err := anon.send(req, path)
	if err != nil {
		return nil, err
	}
	var out TriggerResult
	if err := decode(http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	if out.TaskID == "" {
		return nil, fmt.Errorf("POST %s: response has no task_id: %s", path, truncate(body, 300))
	}
	return &out, nil
}

// AgentRun is what a finished webhook task reports:
// {"status":"success","result":{"answer","sources","tool_calls","thought"}}.
type AgentRun struct {
	Answer    string
	ToolCalls int
}

// AgentRunFailedError is a webhook task that finished as SUCCESS but whose
// result reports a failed run: the worker returns, rather than raises, errors
// that a retry cannot fix ({"status":"quota_exceeded","error":…}) and the
// idempotency guard's give-up ({"success":false,"error":…}).
type AgentRunFailedError struct {
	TaskID string
	Status string
	Detail string
}

func (e *AgentRunFailedError) Error() string {
	msg := fmt.Sprintf("the agent run (task %s) did not succeed", e.TaskID)
	if e.Status != "" {
		msg += fmt.Sprintf(" (%s)", e.Status)
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// AgentRunResult decodes the result of a SUCCESS webhook task.
func AgentRunResult(taskID string, t *TaskStatus) (*AgentRun, error) {
	var res struct {
		Status  string `json:"status"`
		Success *bool  `json:"success"`
		Error   any    `json:"error"`
		Message any    `json:"message"`
		Result  struct {
			Answer    string            `json:"answer"`
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"result"`
	}
	if len(t.Result) == 0 || json.Unmarshal(t.Result, &res) != nil {
		return &AgentRun{}, nil // not the documented shape: nothing to report
	}
	failed := res.Success != nil && !*res.Success
	if res.Status != "" && !strings.EqualFold(res.Status, "success") {
		failed = true
	}
	if failed {
		detail := textOf(res.Error)
		if detail == "" {
			detail = textOf(res.Message)
		}
		if detail == "" {
			detail = truncate(t.Result, 300)
		}
		return nil, &AgentRunFailedError{TaskID: taskID, Status: res.Status, Detail: detail}
	}
	return &AgentRun{Answer: res.Result.Answer, ToolCalls: len(res.Result.ToolCalls)}, nil
}

func textOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, _ := json.Marshal(t)
		return truncate(b, 300)
	}
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}
