package target

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/spec"
)

// streamTarget runs a question against POST {base}/stream, the native DocsGPT
// SSE endpoint. Auth is the api_key field inside the JSON body.
type streamTarget struct{}

func (streamTarget) Name() string { return spec.TargetStream }

// streamRequest is the /stream request body.
type streamRequest struct {
	Question    string   `json:"question"`
	APIKey      string   `json:"api_key"`
	Attachments []string `json:"attachments,omitempty"`
}

// streamEvent is one decoded SSE data frame. Polymorphic fields stay raw.
type streamEvent struct {
	Type      string          `json:"type"`
	Answer    json.RawMessage `json:"answer,omitempty"`     // string delta, or object for structured_answer
	Thought   string          `json:"thought,omitempty"`    // thought delta
	Source    json.RawMessage `json:"source,omitempty"`     // full source list (replace)
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"` // final tool-call list
	Error     string          `json:"error,omitempty"`
	ID        string          `json:"id,omitempty"`
}

func (streamTarget) Run(ctx context.Context, req Request) (*Result, error) {
	if req.Timeout <= 0 {
		req.Timeout = spec.DefaultTimeout // never hang unbounded
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	endpoint := strings.TrimRight(req.BaseURL, "/") + "/stream"

	body, err := json.Marshal(streamRequest{
		Question:    req.Question,
		APIKey:      req.APIKey,
		Attachments: req.AttachmentIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("stream target: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("stream target: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stream target: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("stream target: %s returned %d: %s",
			endpoint, resp.StatusCode, truncateBody(b, 300))
	}

	var (
		answer        strings.Builder
		thought       strings.Builder
		sources       []map[string]any
		toolCalls     []ToolCallInfo
		convID        string
		structured    string
		gotStructured bool
		gotContent    bool
		gotEnd        bool
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1 MiB for long frames

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // ignore "id:" frames, comments, blank lines
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			gotEnd = true
			break
		}

		var ev streamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // ignore malformed frames
		}

		switch ev.Type {
		case "answer":
			var s string
			if json.Unmarshal(ev.Answer, &s) == nil {
				answer.WriteString(s)
				gotContent = true
			}
		case "thought":
			thought.WriteString(ev.Thought)
		case "source":
			var s []map[string]any
			if json.Unmarshal(ev.Source, &s) == nil {
				sources = s // full list: replace, not append
			}
		case "tool_calls":
			toolCalls = extractToolCalls(ev.ToolCalls)
		case "structured_answer":
			if v, ok := stringifyStructured(ev.Answer); ok {
				structured = v
				gotStructured = true
				gotContent = true
			}
		case "id":
			if ev.ID != "" {
				convID = ev.ID
			}
		case "error":
			msg := ev.Error
			if msg == "" {
				msg = data
			}
			return nil, fmt.Errorf("stream target: %s server error: %s", endpoint, msg)
		case "end":
			gotEnd = true
		default:
			// message_id, notice, workflow_run, ... -> ignore
		}

		if gotEnd {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		// A read error (including ctx cancellation) is a real failure.
		return nil, fmt.Errorf("stream target: reading %s: %w", endpoint, err)
	}

	finalAnswer := answer.String()
	if gotStructured {
		finalAnswer = structured
	}

	// EOF after data frames is fine as long as we captured something. A stream
	// that closed with no end event and no content at all is a broken run.
	if !gotEnd && !gotContent && len(toolCalls) == 0 && len(sources) == 0 {
		return nil, fmt.Errorf("stream target: %s closed without an answer or end event", endpoint)
	}

	return &Result{
		Answer:         finalAnswer,
		Thought:        thought.String(),
		Sources:        sources,
		ToolCalls:      toolCalls,
		ConversationID: convID,
		Latency:        time.Since(start),
	}, nil
}

// stringifyStructured renders a structured_answer payload as a single string.
// A JSON string is unquoted; a JSON object/array is re-marshalled compactly.
func stringifyStructured(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			return string(b), true
		}
	}
	return string(raw), true
}
