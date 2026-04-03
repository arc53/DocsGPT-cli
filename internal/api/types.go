package api

import "encoding/json"

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ChatRequest struct {
	Messages       []Message `json:"messages"`
	Stream         bool      `json:"stream"`
	Tools          []Tool    `json:"tools,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
}

type DocsGPTMeta struct {
	ConversationID string `json:"conversation_id,omitempty"`
}

type ChatResponse struct {
	Choices []Choice    `json:"choices"`
	DocsGPT DocsGPTMeta `json:"docsgpt,omitempty"`
}

type Choice struct {
	Delta        Delta  `json:"delta,omitempty"`
	Message      Delta  `json:"message,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type Delta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}
