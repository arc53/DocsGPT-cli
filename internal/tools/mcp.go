package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/mcp"
	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// callMCP calls a tool of an MCP server, asking first like a command:
// "Always allow" covers the tool, or every tool of its server.
func (s *Session) callMCP(ctx context.Context, cancel context.CancelFunc, t *mcp.Tool, raw string) docsgpt.ToolResult {
	text := func(s string) docsgpt.ToolResult { return docsgpt.ToolResult{Content: s} }
	server, tool := display.Safe(t.Server), display.Safe(t.Tool.Name)
	title := server + " · " + tool
	args := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return text(s.fail(title, "invalid arguments", "Error: the arguments are not a JSON object: "+err.Error()))
		}
	}
	note := "(MCP)"
	if t.ReadOnly() {
		note = "(MCP, read-only)"
	}
	s.ui().Open(title, note)
	s.ui().Lines(argLines(args))
	if !s.AutoApprove && !s.allowedMCP[t.Server] && !s.allowedMCP[t.Server+"/"+t.Tool.Name] {
		choice, refusal := s.ask(cancel, "call it once", false,
			allowItem("Always allow "+tool, "call "+tool+" without asking this session"),
			ui.Item{Label: "Always allow " + server, Value: "server", Keys: []string{"s"},
				Description: "call any tool of " + server + " without asking this session"})
		if refusal != "" {
			return text(refusal)
		}
		if s.allowedMCP == nil {
			s.allowedMCP = map[string]bool{}
		}
		switch choice {
		case "always":
			s.allowedMCP[t.Server+"/"+t.Tool.Name] = true
		case "server":
			s.allowedMCP[t.Server] = true
		}
	}

	out := s.ui().Output()
	start := time.Now()
	callCtx, stop := context.WithTimeout(ctx, mcp.CallTimeout)
	defer stop()
	res, err := s.MCP.Call(callCtx, t, args)
	took := display.Duration(time.Since(start))
	switch {
	case ctx.Err() != nil:
		s.ui().Close(false, "cancelled")
		return text("The user interrupted the tool call.")
	case errors.Is(err, context.DeadlineExceeded):
		s.ui().Close(false, "timed out after "+timeoutText(mcp.CallTimeout))
		return text("Error: the tool call timed out after " + timeoutText(mcp.CallTimeout) + ".")
	case err != nil:
		s.ui().Close(false, err.Error())
		return text("Error: " + err.Error())
	}
	result, images := mcp.Result(res)
	result = truncateHead(result)
	io.WriteString(out, result)
	status := lines(len(splitLines(result)))
	if len(images) > 0 {
		status += fmt.Sprintf(" · %d %s", len(images), map[bool]string{true: "image", false: "images"}[len(images) == 1])
	}
	if res.IsError {
		s.ui().Close(false, "error · "+took)
		return docsgpt.ToolResult{Content: "[The tool reported an error.]\n" + result, Parts: images}
	}
	if strings.TrimSpace(result) == "" {
		result, status = "(no output)", "no output"
	}
	s.ui().Close(true, status+" · "+took)
	return docsgpt.ToolResult{Content: result, Parts: images}
}

// argLines shows a call's arguments, a line each: strings as they are,
// anything else as JSON, long values cut.
func argLines(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	const maxLines, maxWidth = 10, 200
	var out []string
	for i, k := range keys {
		if i == maxLines {
			out = append(out, fmt.Sprintf("… %d more", len(keys)-maxLines))
			break
		}
		v, ok := args[k].(string)
		if !ok {
			b, _ := json.Marshal(args[k])
			v = string(b)
		}
		v = strings.Join(strings.Fields(v), " ")
		if r := []rune(v); len(r) > maxWidth {
			v = string(r[:maxWidth]) + "…"
		}
		out = append(out, display.Safe(k+": "+v))
	}
	return out
}
