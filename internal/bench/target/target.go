// Package target sends one benchmark question to a DocsGPT agent through one
// of four protocols: the OpenAI-compatible /v1/chat/completions endpoint
// (single JSON response or SSE), the native /stream SSE endpoint, the native
// /api/answer JSON endpoint, or an agent incoming webhook (async, with
// /api/task_status polling).
package target

import (
	"context"
	"fmt"
	"time"
)

// Usage mirrors the OpenAI usage object ( /v1 target only).
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ToolCallInfo is one tool invocation reported by the server.
type ToolCallInfo struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// Result is the normalized outcome of one agent run, whatever the protocol.
type Result struct {
	Answer         string           `json:"answer"`
	Thought        string           `json:"thought,omitempty"`
	Sources        []map[string]any `json:"sources,omitempty"`
	ToolCalls      []ToolCallInfo   `json:"tool_calls,omitempty"`
	ConversationID string           `json:"conversation_id,omitempty"`
	Usage          *Usage           `json:"usage,omitempty"` // nil unless the target reports it (v1 only)
	Latency        time.Duration    `json:"latency_ns"`
	// FirstOutput is the time from request start to the first answer content
	// (stream: first `answer`/`structured_answer` frame; v1 streamed: first
	// content delta). Zero when the protocol cannot observe it.
	FirstOutput time.Duration `json:"first_output_ns,omitempty"`

	// SSE integrity (stream target only; nil/false elsewhere).
	Frames     []string `json:"frames,omitempty"` // distinct frame types in order of first appearance
	EndFrame   bool     `json:"end_frame,omitempty"`
	ErrorFrame bool     `json:"error_frame,omitempty"`
}

// Exchange is one completed user/assistant pair of an earlier turn, replayed
// by stateless targets (v1) to continue a conversation.
type Exchange struct {
	Question string
	Answer   string
}

// InlineFile is an attachment sent inline as a base64 content part (v1 only).
type InlineFile struct {
	Name string // filename shown to the model
	Path string // local file path, read at request time
}

// Request carries everything a target needs for one run.
type Request struct {
	Question      string
	AttachmentIDs []string     // server-side attachment ids (see UploadAttachments)
	InlineFiles   []InlineFile // v1 inline attachments (attachments_mode: inline)
	Model         string       // model id (stream/answer: model_id, v1: model); "" = agent default
	Stream        bool         // v1: request SSE streaming instead of a single JSON response

	// Multi-turn support. ConversationID continues an existing conversation
	// (stream/answer targets); History is replayed as prior messages by
	// stateless targets (v1). The runner fills whichever the target uses.
	ConversationID string
	History        []Exchange

	BaseURL      string
	APIKey       string
	WebhookURL   string        // webhook target only
	Timeout      time.Duration // whole-run budget, including webhook polling
	PollInterval time.Duration // webhook polling cadence
	RunTag       string        // sent as X-DocsGPT-Bench-Tag: bench:<tag>
}

// Target runs one request against one protocol.
type Target interface {
	Name() string
	Run(ctx context.Context, req Request) (*Result, error)
}

// ServerError is returned when the server answered but reported a failure:
// a non-2xx HTTP status, an SSE `error` frame, or an agent-level failure in
// a task result. Negative cases (expect.error) assert against it; anything
// else (transport failures, timeouts, malformed responses) stays a plain
// error and is reported as a run error.
type ServerError struct {
	Status  int    // HTTP status of the response (0 when not applicable)
	Message string // extracted error message (error.message, error, or body)
	Body    string // raw (truncated) response body / frame, for diagnostics
	Where   string // endpoint or protocol label
}

func (e *ServerError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Body
	}
	if e.Status >= 400 {
		return fmt.Sprintf("%s returned %d: %s", e.Where, e.Status, msg)
	}
	return fmt.Sprintf("%s: %s", e.Where, msg)
}

// ForName maps a spec target name (spec.TargetV1, ...) to an implementation.
// It is defined in forname.go alongside the implementations.
