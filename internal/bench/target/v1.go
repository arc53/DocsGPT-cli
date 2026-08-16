package target

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/spec"

	"github.com/tidwall/gjson"
)

// v1Target runs a question against POST {base}/v1/chat/completions, the
// OpenAI-compatible endpoint, with Bearer auth. By default it requests a
// single JSON response (stream:false); with Request.Stream it consumes the
// SSE chunk stream instead (stamping time-to-first-token and reading the
// final usage chunk).
type v1Target struct{}

func (v1Target) Name() string { return spec.TargetV1 }

// defaultV1Model is the placeholder sent when no model is configured: the
// API key selects the agent, and the server treats an unknown alias as
// "agent default".
const defaultV1Model = "docsgpt"

// v1Message is one chat message; Content is a string or a parts array.
type v1Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// v1Request is the request body. It is built locally rather than through
// internal/api so bench can send parts-array content (inline files) without
// touching the interactive chat plumbing.
type v1Request struct {
	Model         string        `json:"model"`
	Messages      []v1Message   `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions *v1StreamOpts `json:"stream_options,omitempty"`
	DocsGPT       *v1DocsGPTExt `json:"docsgpt,omitempty"`
}

type v1StreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type v1DocsGPTExt struct {
	Attachments []string `json:"attachments,omitempty"`
}

func (v1Target) Run(ctx context.Context, req Request) (*Result, error) {
	if req.Timeout <= 0 {
		req.Timeout = spec.DefaultTimeout // never hang unbounded
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(req.BaseURL, "/") + "/v1/chat/completions"

	messages, err := buildV1Messages(req)
	if err != nil {
		return nil, fmt.Errorf("v1 target: %w", err)
	}
	chatReq := v1Request{
		Model:    req.Model,
		Messages: messages,
		Stream:   req.Stream,
	}
	if chatReq.Model == "" {
		chatReq.Model = defaultV1Model
	}
	if req.Stream {
		chatReq.StreamOptions = &v1StreamOpts{IncludeUsage: true}
	}
	if len(req.AttachmentIDs) > 0 {
		chatReq.DocsGPT = &v1DocsGPTExt{Attachments: req.AttachmentIDs}
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
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	setBenchHeaders(httpReq, req.RunTag)

	start := time.Now()
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("v1 target: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &ServerError{
			Status:  resp.StatusCode,
			Message: errorMessage(respBody),
			Body:    truncateBody(respBody, 300),
			Where:   "v1 target: " + endpoint,
		}
	}

	if req.Stream {
		return readV1Stream(resp.Body, endpoint, start)
	}

	respBody, err := io.ReadAll(resp.Body)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("v1 target: read response from %s: %w", endpoint, err)
	}
	return parseV1Response(respBody, endpoint, latency)
}

// buildV1Messages renders the prior exchanges plus the current question. With
// inline files the last user message becomes a parts array.
func buildV1Messages(req Request) ([]v1Message, error) {
	var msgs []v1Message
	for _, ex := range req.History {
		msgs = append(msgs, v1Message{Role: "user", Content: ex.Question})
		msgs = append(msgs, v1Message{Role: "assistant", Content: ex.Answer})
	}
	if len(req.InlineFiles) == 0 {
		return append(msgs, v1Message{Role: "user", Content: req.Question}), nil
	}
	parts := []map[string]any{{"type": "text", "text": req.Question}}
	for _, f := range req.InlineFiles {
		part, err := inlineFilePart(f)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return append(msgs, v1Message{Role: "user", Content: parts}), nil
}

// inlineFilePart base64-encodes a file into an OpenAI content part: images
// become image_url parts, everything else a file part with a data URI.
func inlineFilePart(f InlineFile) (map[string]any, error) {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("inline attachment %s: %w", f.Path, err)
	}
	name := f.Name
	if name == "" {
		name = filepath.Base(f.Path)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = mimeType[:i] // drop charset params
	}
	dataURI := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	if strings.HasPrefix(mimeType, "image/") {
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURI}}, nil
	}
	return map[string]any{"type": "file", "file": map[string]any{"filename": name, "file_data": dataURI}}, nil
}

// parseV1Response decodes a non-streaming chat completion.
func parseV1Response(respBody []byte, endpoint string, latency time.Duration) (*Result, error) {
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage   *Usage `json:"usage"`
		DocsGPT struct {
			ConversationID string          `json:"conversation_id"`
			Sources        json.RawMessage `json:"sources"`
			ToolCalls      json.RawMessage `json:"tool_calls"`
		} `json:"docsgpt"`
	}
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
	return &Result{
		Answer:         msg.Content,
		Thought:        msg.ReasoningContent,
		ConversationID: chatResp.DocsGPT.ConversationID,
		Sources:        sources,
		ToolCalls:      extractToolCalls(chatResp.DocsGPT.ToolCalls),
		Usage:          chatResp.Usage,
		Latency:        latency,
	}, nil
}

// readV1Stream consumes OpenAI-style chat.completion.chunk SSE frames. Content
// and reasoning deltas are accumulated; DocsGPT extension chunks carry
// sources, the conversation id, and completed tool calls; the optional final
// usage chunk (stream_options.include_usage) fills Usage; a `{"error": ...}`
// frame becomes a ServerError.
func readV1Stream(body io.Reader, endpoint string, start time.Time) (*Result, error) {
	var (
		answer, thought strings.Builder
		sources         []map[string]any
		toolCalls       []ToolCallInfo
		seenCalls       = map[string]bool{}
		convID          string
		usage           *Usage
		firstOutput     time.Duration
		gotDone         bool
		gotChunk        bool
	)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			gotDone = true
			break
		}
		if !gjson.Valid(data) {
			return nil, fmt.Errorf("v1 target: %s sent a malformed SSE chunk: %s", endpoint, truncateBody([]byte(data), 200))
		}
		chunk := gjson.Parse(data)
		if e := chunk.Get("error"); e.Exists() {
			return nil, &ServerError{
				Status:  http.StatusOK,
				Message: errorMessage([]byte(data)),
				Body:    truncateBody([]byte(data), 300),
				Where:   "v1 target: " + endpoint + " stream error",
			}
		}
		gotChunk = true

		if u := chunk.Get("usage"); u.IsObject() {
			usage = &Usage{
				PromptTokens:     int(u.Get("prompt_tokens").Int()),
				CompletionTokens: int(u.Get("completion_tokens").Int()),
				TotalTokens:      int(u.Get("total_tokens").Int()),
			}
		}
		if ext := chunk.Get("docsgpt"); ext.IsObject() {
			switch ext.Get("type").String() {
			case "id":
				if id := ext.Get("conversation_id").String(); id != "" {
					convID = id
				}
			case "source":
				var s []map[string]any
				if json.Unmarshal([]byte(ext.Get("sources").Raw), &s) == nil {
					sources = s
				}
			case "tool_call":
				tc := ext.Get("data")
				if tc.IsObject() && tc.Get("status").String() == "completed" {
					key := tc.Get("call_id").String()
					if key == "" || !seenCalls[key] {
						seenCalls[key] = true
						toolCalls = append(toolCalls, extractToolCalls(json.RawMessage("["+tc.Raw+"]"))...)
					}
				}
			}
		}
		if id := chunk.Get("docsgpt.conversation_id"); id.Exists() && id.String() != "" {
			convID = id.String()
		}

		delta := chunk.Get("choices.0.delta")
		if !delta.Exists() {
			continue
		}
		if c := delta.Get("content").String(); c != "" {
			if firstOutput == 0 {
				firstOutput = time.Since(start)
			}
			answer.WriteString(c)
		}
		if r := delta.Get("reasoning_content").String(); r != "" {
			thought.WriteString(r)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("v1 target: reading %s: %w", endpoint, err)
	}
	if !gotDone && !gotChunk {
		return nil, fmt.Errorf("v1 target: %s stream closed without any chunk", endpoint)
	}
	return &Result{
		Answer:         answer.String(),
		Thought:        thought.String(),
		Sources:        sources,
		ToolCalls:      toolCalls,
		ConversationID: convID,
		Usage:          usage,
		Latency:        time.Since(start),
		FirstOutput:    firstOutput,
		EndFrame:       gotDone,
	}, nil
}
