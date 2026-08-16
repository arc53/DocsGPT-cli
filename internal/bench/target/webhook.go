package target

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/spec"

	"github.com/tidwall/gjson"
)

// webhookTarget POSTs the question to an agent incoming webhook, then polls
// /api/task_status until the async run finishes. Latency covers the full round
// trip including polling.
type webhookTarget struct{}

func (webhookTarget) Name() string { return spec.TargetWebhook }

func (webhookTarget) Run(ctx context.Context, req Request) (*Result, error) {
	if req.WebhookURL == "" {
		return nil, fmt.Errorf("webhook target: webhook URL is required")
	}
	if req.Timeout <= 0 {
		req.Timeout = spec.DefaultTimeout // never poll unbounded
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// The URL's last path segment is the secret token; error messages must
	// only ever carry the redacted form (CI logs are often world-readable).
	safeURL := redactWebhookURL(req.WebhookURL)

	start := time.Now()

	// The worker serializes the whole payload as the agent query, so a bare
	// {"question": ...} object is the accepted convention.
	reqBody, err := json.Marshal(map[string]string{"question": req.Question})
	if err != nil {
		return nil, fmt.Errorf("webhook target: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.WebhookURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("webhook target: build request for %s: %w", safeURL, unwrapURLError(err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	setBenchHeaders(httpReq, req.RunTag)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("webhook target: POST %s: %w", safeURL, unwrapURLError(err))
	}
	postBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("webhook target: read response from %s: %w", safeURL, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &ServerError{
			Status:  resp.StatusCode,
			Message: errorMessage(postBody),
			Body:    truncateBody(postBody, 300),
			Where:   "webhook target: " + safeURL,
		}
	}
	if s := gjson.GetBytes(postBody, "success"); s.Exists() && !s.Bool() {
		return nil, &ServerError{
			Status:  resp.StatusCode,
			Message: errorMessage(postBody),
			Body:    truncateBody(postBody, 300),
			Where:   "webhook target: " + safeURL,
		}
	}

	taskID := gjson.GetBytes(postBody, "task_id").String()
	if taskID == "" {
		return nil, fmt.Errorf("webhook target: %s response missing task_id: %s",
			safeURL, truncateBody(postBody, 300))
	}

	statusBody, err := pollTaskStatus(ctx, req.BaseURL, taskID, req.PollInterval)
	if err != nil {
		return nil, fmt.Errorf("webhook target: %w", err)
	}

	// SUCCESS payload: {"status":"SUCCESS","result":{"status":"success",
	//   "result":{"answer":..,"sources":..,"tool_calls":..,"thought":..}}}
	// Celery-level SUCCESS can still wrap an agent-level failure.
	if s := gjson.GetBytes(statusBody, "result.status").String(); s != "" && !strings.EqualFold(s, "success") {
		msg := gjson.GetBytes(statusBody, "result.error").String()
		if msg == "" {
			msg = gjson.GetBytes(statusBody, "result.message").String()
		}
		if msg == "" {
			msg = truncateBody(statusBody, 300)
		}
		return nil, &ServerError{
			Message: msg,
			Body:    truncateBody(statusBody, 300),
			Where:   fmt.Sprintf("webhook target: agent run reported %q", s),
		}
	}
	inner := gjson.GetBytes(statusBody, "result.result")
	result := &Result{
		Answer:         inner.Get("answer").String(),
		Thought:        inner.Get("thought").String(),
		ConversationID: inner.Get("conversation_id").String(),
		Latency:        time.Since(start),
	}
	if s := inner.Get("sources"); s.IsArray() {
		_ = json.Unmarshal([]byte(s.Raw), &result.Sources)
	}
	if tc := inner.Get("tool_calls"); tc.IsArray() {
		result.ToolCalls = extractToolCalls(json.RawMessage(tc.Raw))
	}
	return result, nil
}
