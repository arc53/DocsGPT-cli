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

	"docsgpt-cli/internal/api"
	"docsgpt-cli/internal/bench/spec"
)

// v1Target runs a question against POST {base}/v1/chat/completions, the
// OpenAI-compatible endpoint, with Bearer auth and stream:false.
type v1Target struct{}

func (v1Target) Name() string { return spec.TargetV1 }

func (v1Target) Run(ctx context.Context, req Request) (*Result, error) {
	if req.Timeout <= 0 {
		req.Timeout = spec.DefaultTimeout // never hang unbounded
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(req.BaseURL, "/") + "/v1/chat/completions"

	chatReq := api.ChatRequest{
		Model:    "docsgpt", // ignored for agent selection; the key selects the agent
		Messages: []api.Message{{Role: "user", Content: req.Question}},
		Stream:   false,
	}
	if len(req.AttachmentIDs) > 0 {
		chatReq.DocsGPT = &api.DocsGPTRequest{Attachments: req.AttachmentIDs}
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("v1 target: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("v1 target: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)

	start := time.Now()
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("v1 target: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("v1 target: read response from %s: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("v1 target: %s returned %d: %s",
			endpoint, resp.StatusCode, truncateBody(respBody, 300))
	}

	var chatResp api.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("v1 target: decode response from %s: %w", endpoint, err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("v1 target: %s returned no choices: %s",
			endpoint, truncateBody(respBody, 300))
	}

	msg := chatResp.Choices[0].Message
	var sources []map[string]any
	if len(chatResp.DocsGPT.Sources) > 0 {
		// Best-effort: a surprising sources shape must not fail the run.
		_ = json.Unmarshal(chatResp.DocsGPT.Sources, &sources)
	}
	result := &Result{
		Answer:         msg.Content,
		Thought:        msg.ReasoningContent,
		ConversationID: chatResp.DocsGPT.ConversationID,
		Sources:        sources,
		ToolCalls:      extractToolCalls(chatResp.DocsGPT.ToolCalls),
		Latency:        latency,
	}
	if u := chatResp.Usage; u != nil {
		result.Usage = &Usage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
		}
	}
	return result, nil
}
