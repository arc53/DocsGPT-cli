// Package target sends one benchmark question to a DocsGPT agent through one
// of three protocols: the OpenAI-compatible /v1/chat/completions endpoint,
// the native /stream SSE endpoint, or an agent incoming webhook (async, with
// /api/task_status polling).
package target

import (
	"context"
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
}

// Request carries everything a target needs for one run.
type Request struct {
	Question      string
	AttachmentIDs []string // server-side attachment ids (see UploadAttachments)
	BaseURL       string
	APIKey        string
	WebhookURL    string        // webhook target only
	Timeout       time.Duration // whole-run budget, including webhook polling
	PollInterval  time.Duration // webhook polling cadence
}

// Target runs one request against one protocol.
type Target interface {
	Name() string
	Run(ctx context.Context, req Request) (*Result, error)
}

// ForName maps a spec target name (spec.TargetV1, ...) to an implementation.
// It is defined in forname.go alongside the three implementations.
