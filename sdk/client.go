package docsgpt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{},
	}
}

func (c *Client) endpoint() string {
	return c.BaseURL + "/v1/chat/completions"
}

// StreamHandler is called once per SSE chunk as it arrives, with the chunk's
// delta and its finish_reason — the latter empty on every chunk but the last.
type StreamHandler func(delta Delta, finishReason string)

// Send performs a non-streaming chat completion request.
func (c *Client) Send(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &chatResp, nil
}

// SendStream performs a streaming chat completion request, calling onDelta for
// each chunk. It returns the accumulated final response, including the
// conversation id, sources, model and usage the stream carried.
func (c *Client) SendStream(ctx context.Context, req ChatRequest, onDelta StreamHandler) (*ChatResponse, error) {
	req.Stream = true
	if req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var (
		out          ChatResponse
		accumulated  Delta
		finishReason string
	)

	scanner := bufio.NewScanner(resp.Body)
	// A sources chunk carries the retrieved text and easily outgrows the
	// default 64KB line limit.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			ChatResponse
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("server error: %s", chunk.Error.Message)
		}

		meta := chunk.DocsGPT
		if meta.ConversationID != "" {
			out.DocsGPT.ConversationID = meta.ConversationID
		}
		if meta.Model != "" {
			out.DocsGPT.Model = meta.Model
		}
		if len(meta.Sources) > 0 {
			out.DocsGPT.Sources = meta.Sources
		}
		if chunk.Model != "" {
			out.Model = chunk.Model
		}
		if chunk.Usage != nil {
			out.Usage = chunk.Usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		if choice.FinishReason != "" {
			// Don't overwrite "tool_calls" with a later "stop" —
			// the DocsGPT server sends additional chunks after tool_calls.
			if finishReason != "tool_calls" {
				finishReason = choice.FinishReason
			}
		}

		accumulated.Content += delta.Content
		accumulated.ReasoningContent += delta.ReasoningContent

		for _, tc := range delta.ToolCalls {
			for tc.Index >= len(accumulated.ToolCalls) {
				accumulated.ToolCalls = append(accumulated.ToolCalls, ToolCall{})
			}
			existing := &accumulated.ToolCalls[tc.Index]
			if tc.ID != "" {
				existing.ID = tc.ID
			}
			if tc.Type != "" {
				existing.Type = tc.Type
			}
			if tc.Function.Name != "" {
				existing.Function.Name = tc.Function.Name
			}
			existing.Function.Arguments += tc.Function.Arguments
		}

		if onDelta != nil {
			onDelta(delta, choice.FinishReason)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stream: %w", err)
	}

	out.Choices = []Choice{{Message: accumulated, FinishReason: finishReason}}
	return &out, nil
}

// ToolCallHandler is called when the model requests a tool call.
// It receives the tool call and should return the result string.
type ToolCallHandler func(tc ToolCall) string

// RunOptions configures RunWithTools.
type RunOptions struct {
	Tools []Tool
	// Stream selects SSE streaming; OnDelta then sees every chunk, and
	// otherwise each whole response once.
	Stream bool
	// ConversationID continues a stored conversation (from an earlier
	// RunResult); empty starts a new one.
	ConversationID string
	OnDelta        StreamHandler
	OnToolCall     ToolCallHandler
}

// RunResult is the outcome of RunWithTools.
type RunResult struct {
	// Messages is the input history with the assistant and tool turns
	// appended.
	Messages []Message
	// ConversationID identifies the server-side conversation; pass it back
	// in RunOptions to continue it.
	ConversationID string
	// Sources are the documents the answer drew on.
	Sources []Source
	// Model is the LLM that answered, when the server says.
	Model string
	// Usage is summed over every request of the run (one per tool round);
	// nil when the server reported none.
	Usage *Usage
}

// RunWithTools sends a chat request and handles tool call loops.
// When the model returns tool_calls, OnToolCall is invoked for each one,
// and results are sent back in a continuation request. This repeats
// until the model returns finish_reason "stop" (or non-tool_calls).
func (c *Client) RunWithTools(ctx context.Context, messages []Message, opts RunOptions) (*RunResult, error) {
	res := &RunResult{
		Messages:       append([]Message(nil), messages...),
		ConversationID: opts.ConversationID,
	}

	for {
		req := ChatRequest{
			Messages:       res.Messages,
			Tools:          opts.Tools,
			ConversationID: res.ConversationID,
		}

		var resp *ChatResponse
		var err error
		if opts.Stream {
			resp, err = c.SendStream(ctx, req, opts.OnDelta)
		} else if resp, err = c.Send(ctx, req); err == nil && opts.OnDelta != nil && len(resp.Choices) > 0 {
			// Without streaming the handler sees each response whole.
			opts.OnDelta(resp.Choices[0].Message, resp.Choices[0].FinishReason)
		}
		if err != nil {
			return nil, err
		}

		if resp.DocsGPT.ConversationID != "" {
			res.ConversationID = resp.DocsGPT.ConversationID
		}
		if s := ParseSources(resp.DocsGPT.Sources); len(s) > 0 {
			res.Sources = s
		}
		if resp.DocsGPT.Model != "" {
			res.Model = resp.DocsGPT.Model
		}
		if u := resp.Usage; u != nil {
			if res.Usage == nil {
				res.Usage = &Usage{}
			}
			res.Usage.PromptTokens += u.PromptTokens
			res.Usage.CompletionTokens += u.CompletionTokens
			res.Usage.TotalTokens += u.TotalTokens
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("empty response from API")
		}
		choice := resp.Choices[0]

		res.Messages = append(res.Messages, Message{
			Role:      "assistant",
			Content:   choice.Message.Content,
			ToolCalls: choice.Message.ToolCalls,
		})

		if choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) == 0 || opts.OnToolCall == nil {
			return res, nil
		}

		for _, tc := range choice.Message.ToolCalls {
			res.Messages = append(res.Messages, Message{
				Role:       "tool",
				Content:    opts.OnToolCall(tc),
				ToolCallID: tc.ID,
			})
		}
	}
}

// Models lists the models the API key can use (GET /v1/models). DocsGPT
// answers with the key's agent, or 401 for an unknown key, without running
// the agent, which makes this a cheap way to check a key.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var list struct {
		Data []Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return list.Data, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
}
