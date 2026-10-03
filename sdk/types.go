package docsgpt

import "encoding/json"

// Message is one chat message. Content is its text; Parts add images and
// files to a user message (see ContentPart), and the message then goes out
// with the OpenAI content array: Content as its first text part, then
// Parts. A message read with a content array has all its parts in Parts
// and Content empty; Text joins its text either way.
type Message struct {
	Role       string
	Content    string
	Parts      []ContentPart
	ToolCalls  []ToolCall
	ToolCallID string
}

type ChatRequest struct {
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	// StreamOptions is set by SendStream, which always asks for usage.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
	Tools         []Tool         `json:"tools,omitempty"`
	// ConversationID continues a conversation the server stored: it then
	// takes the history from that conversation instead of from Messages.
	ConversationID string          `json:"conversation_id,omitempty"`
	DocsGPT        *DocsGPTRequest `json:"docsgpt,omitempty"`
}

// StreamOptions mirrors the OpenAI stream_options object.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// DocsGPTRequest is the request-side "docsgpt" extension object. It currently
// carries server-side attachment ids for the /v1 endpoint.
type DocsGPTRequest struct {
	Attachments []string `json:"attachments,omitempty"`
}

// Usage mirrors the OpenAI usage object returned by /v1/chat/completions.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// DocsGPTMeta is the response-side "docsgpt" extension object. Sources and
// ToolCalls stay raw so a surprising shape can never fail the whole response
// decode (which would drop an otherwise valid answer); see ParseSources.
type DocsGPTMeta struct {
	// Type names the kind of a streamed extension chunk ("id", "source",
	// "model", ...); it is empty on a non-streaming response.
	Type           string `json:"type,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	// Model is the LLM that answered (the top-level model is the agent).
	Model     string          `json:"model,omitempty"`
	Sources   json.RawMessage `json:"sources,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

type ChatResponse struct {
	Model   string      `json:"model,omitempty"`
	Choices []Choice    `json:"choices"`
	DocsGPT DocsGPTMeta `json:"docsgpt,omitempty"`
	Usage   *Usage      `json:"usage,omitempty"`
}

// Source is one retrieved document an answer drew on.
type Source struct {
	Title    string `json:"title,omitempty"`
	Source   string `json:"source,omitempty"` // URL or path, when known
	Filename string `json:"filename,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ParseSources decodes a raw "sources" value, skipping entries of an
// unexpected shape instead of failing.
func ParseSources(raw json.RawMessage) []Source {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []Source
	for _, item := range items {
		var s Source
		if json.Unmarshal(item, &s) == nil {
			out = append(out, s)
		}
	}
	return out
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

// Model is an entry of GET /v1/models. On DocsGPT it describes the agent the
// API key belongs to: ID is the agent id and Name its display name.
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
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
