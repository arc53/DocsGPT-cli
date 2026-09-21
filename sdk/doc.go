// Package docsgpt is a Go client for the DocsGPT chat API.
//
// It talks to the OpenAI-compatible /v1/chat/completions endpoint of a DocsGPT
// server, with the streaming, tool-calling and conversation-id handling the
// DocsGPT extensions add on top.
//
//	client := docsgpt.NewClient("https://gptcloud.arc53.com", apiKey)
//	resp, err := client.Send(ctx, docsgpt.ChatRequest{
//		Messages: []docsgpt.Message{{Role: "user", Content: "What is DocsGPT?"}},
//	})
//
// The import path ends in /sdk while the package is named docsgpt, so imports
// are written as:
//
//	import "github.com/arc53/DocsGPT-cli/sdk"
//
// and the package is referred to as docsgpt.
//
// This module is versioned independently of the docsgpt-cli command that lives
// in the same repository: its tags are prefixed, as in sdk/v0.1.0. While it is
// below v1 the API may still change.
package docsgpt
