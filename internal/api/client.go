package api

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
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &chatResp, nil
}

// SendStream performs a streaming chat completion request.
// onDelta is called for each SSE chunk with the delta and finish_reason.
// Returns the accumulated final response.
func (c *Client) SendStream(ctx context.Context, req ChatRequest, onDelta func(Delta, string)) (*ChatResponse, error) {
	req.Stream = true
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
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Accumulate the full response
	var accumulated Delta
	var finishReason string
	var conversationID string
	var accToolCalls []ToolCall

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			break
		}

		var chunk ChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Try parsing as a docsgpt metadata chunk
			var meta struct {
				DocsGPT struct {
					ConversationID string `json:"conversation_id"`
				} `json:"docsgpt"`
			}
			if json.Unmarshal([]byte(data), &meta) == nil && meta.DocsGPT.ConversationID != "" {
				conversationID = meta.DocsGPT.ConversationID
			}
			continue
		}

		// Also check for conversation_id in standard response chunks
		if chunk.DocsGPT.ConversationID != "" {
			conversationID = chunk.DocsGPT.ConversationID
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

		// Accumulate content
		accumulated.Content += delta.Content
		accumulated.ReasoningContent += delta.ReasoningContent

		// Accumulate tool calls
		for _, tc := range delta.ToolCalls {
			for tc.Index >= len(accToolCalls) {
				accToolCalls = append(accToolCalls, ToolCall{})
			}
			existing := &accToolCalls[tc.Index]
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

	accumulated.ToolCalls = accToolCalls

	return &ChatResponse{
		Choices: []Choice{
			{
				Message:      accumulated,
				FinishReason: finishReason,
			},
		},
		DocsGPT: DocsGPTMeta{ConversationID: conversationID},
	}, nil
}

// ToolCallHandler is called when the model requests a tool call.
// It receives the tool call and should return the result string.
type ToolCallHandler func(tc ToolCall) string

// RunWithTools sends a chat request and handles tool call loops.
// When the model returns tool_calls, onToolCall is invoked for each one,
// and results are sent back in a continuation request. This repeats
// until the model returns finish_reason "stop" (or non-tool_calls).
func (c *Client) RunWithTools(
	ctx context.Context,
	messages []Message,
	tools []Tool,
	stream bool,
	onDelta func(Delta, string),
	onToolCall ToolCallHandler,
) ([]Message, error) {
	history := make([]Message, len(messages))
	copy(history, messages)
	var conversationID string

	for {
		req := ChatRequest{
			Messages:       history,
			Tools:          tools,
			ConversationID: conversationID,
		}

		var resp *ChatResponse
		var err error

		if stream {
			resp, err = c.SendStream(ctx, req, onDelta)
		} else {
			resp, err = c.Send(ctx, req)
		}
		if err != nil {
			return history, err
		}

		// Track conversation_id for continuation requests
		if resp.DocsGPT.ConversationID != "" {
			conversationID = resp.DocsGPT.ConversationID
		}

		if len(resp.Choices) == 0 {
			return history, fmt.Errorf("empty response from API")
		}

		choice := resp.Choices[0]

		// Append the assistant message to history
		assistantMsg := Message{
			Role:      "assistant",
			Content:   choice.Message.Content,
			ToolCalls: choice.Message.ToolCalls,
		}
		history = append(history, assistantMsg)

		// If no tool calls, we're done
		if choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) == 0 {
			return history, nil
		}

		// Process each tool call
		for _, tc := range choice.Message.ToolCalls {
			result := onToolCall(tc)
			history = append(history, Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}

		// Loop continues — sends history with tool results back to API
	}
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
}
