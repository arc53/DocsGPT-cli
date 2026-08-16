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

// answerTarget runs a question against POST {base}/api/answer, the native
// synchronous JSON endpoint: same auth and options as /stream (api_key in the
// body, model_id, conversation_id) but a single JSON reply
// {conversation_id, answer, sources, tool_calls, thought}. It has no
// attachments parameter and cannot observe time-to-first-token.
type answerTarget struct{}

func (answerTarget) Name() string { return spec.TargetAnswer }

type answerRequest struct {
	Question       string `json:"question"`
	APIKey         string `json:"api_key"`
	ConversationID string `json:"conversation_id,omitempty"`
	ModelID        string `json:"model_id,omitempty"`
}

func (answerTarget) Run(ctx context.Context, req Request) (*Result, error) {
	if req.Timeout <= 0 {
		req.Timeout = spec.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(req.BaseURL, "/") + "/api/answer"

	body, err := json.Marshal(answerRequest{
		Question:       req.Question,
		APIKey:         req.APIKey,
		ConversationID: req.ConversationID,
		ModelID:        req.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("answer target: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("answer target: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	setBenchHeaders(httpReq, req.RunTag)

	start := time.Now()
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("answer target: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("answer target: read response from %s: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &ServerError{
			Status:  resp.StatusCode,
			Message: errorMessage(respBody),
			Body:    truncateBody(respBody, 300),
			Where:   "answer target: " + endpoint,
		}
	}
	if !gjson.ValidBytes(respBody) {
		return nil, fmt.Errorf("answer target: %s returned non-JSON body: %s", endpoint, truncateBody(respBody, 300))
	}
	doc := gjson.ParseBytes(respBody)
	// A 200 with an error field is still a server-reported failure.
	if e := doc.Get("error"); e.Exists() && e.String() != "" {
		return nil, &ServerError{
			Status:  resp.StatusCode,
			Message: errorMessage(respBody),
			Body:    truncateBody(respBody, 300),
			Where:   "answer target: " + endpoint,
		}
	}
	ans := doc.Get("answer")
	if !ans.Exists() {
		return nil, fmt.Errorf("answer target: %s response has no answer field: %s", endpoint, truncateBody(respBody, 300))
	}
	result := &Result{
		Answer:         stringifyAnswer(ans),
		Thought:        doc.Get("thought").String(),
		ConversationID: doc.Get("conversation_id").String(),
		Latency:        latency,
	}
	if s := doc.Get("sources"); s.IsArray() {
		_ = json.Unmarshal([]byte(s.Raw), &result.Sources)
	}
	if tc := doc.Get("tool_calls"); tc.IsArray() {
		result.ToolCalls = extractToolCalls(json.RawMessage(tc.Raw))
	}
	return result, nil
}

// stringifyAnswer renders a structured (object/array) answer compactly and
// returns string answers verbatim.
func stringifyAnswer(r gjson.Result) string {
	if r.Type == gjson.String {
		return r.String()
	}
	if r.Type == gjson.Null {
		return ""
	}
	return r.Raw
}
