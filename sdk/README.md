# DocsGPT Go SDK

A Go client for the DocsGPT chat API: the OpenAI-compatible
`/v1/chat/completions` endpoint, with streaming, tool calls, sources and
conversation ids. It is the client `docsgpt-cli` is built on.

```bash
go get github.com/arc53/DocsGPT-cli/sdk
```

The import path ends in `/sdk`; the package is `docsgpt`.

```go
import docsgpt "github.com/arc53/DocsGPT-cli/sdk"

client := docsgpt.NewClient("https://gptcloud.arc53.com", apiKey)
resp, err := client.Send(ctx, docsgpt.ChatRequest{
	Messages: []docsgpt.Message{{Role: "user", Content: "What is DocsGPT?"}},
})
```

`apiKey` is an agent API key (DocsGPT → Agent settings → API key).

## Streaming and tools

`RunWithTools` streams the answer, runs tool calls through your handler and
sends the results back until the model is done:

```go
res, err := client.RunWithTools(ctx, messages, docsgpt.RunOptions{
	Stream: true,
	Tools:  tools,
	OnDelta: func(d docsgpt.Delta, _ string) { fmt.Print(d.Content) },
	OnToolCall: func(tc docsgpt.ToolCall) string {
		return runTool(tc) // the result the model sees
	},
})
// res.Messages: the history with the new turns
// res.ConversationID: pass it in RunOptions to continue the conversation
// res.Sources, res.Model, res.Usage
```

| API | What |
|---|---|
| `Send`, `SendStream` | One request, whole or streamed (`StreamHandler` per delta) |
| `RunWithTools` | The tool-call loop, streamed or not |
| `Models` | The key's agent (`GET /v1/models`); a cheap way to check a key |
| `APIError` | A non-2xx reply: status code, body, `Message()` (the server's message, one line), `Code()`, `RetryAfter` |
| `RetryPolicy` | `Client.Retry`: how failed chat requests are sent again |

## Retries

`NewClient` retries a chat request up to 3 times (after 2s, 4s, 8s) when it
failed before any of the answer arrived: 408, 429, 502, 503, 504, or a
connection refused, reset or timed out. A `Retry-After` is honoured, up to
`MaxDelay` (a longer one returns the error at once), and so is
`x-should-retry: false`; a spent usage limit (`error_code` ending in
`limit-reached`) is not retried. A stream that fails once it has started is
never retried, so an answer never comes twice.

```go
client.Retry.OnRetry = func(ev docsgpt.RetryEvent) {
	log.Printf("retrying (%d/%d) in %s: %v", ev.Attempt, ev.MaxRetries, ev.Delay, ev.Err)
}
client.Retry = docsgpt.RetryPolicy{} // never retry
```

Cancelling the context ends a wait at once.

## Versioning

The module has no dependencies beyond the standard library. It is versioned
independently of the CLI, under `sdk/vX.Y.Z` tags, and is below v1, so the API
may still change.
