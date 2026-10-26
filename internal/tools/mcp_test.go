package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/mcp"
	"github.com/arc53/DocsGPT-cli/internal/mcp/mcptest"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

func TestMain(m *testing.M) { mcptest.Main(m) }

func TestCallMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := mcp.Start([]mcp.Server{mcptest.Server("test"), mcptest.Server("other")}, "test")
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	u := &recordingUI{choice: "deny"}
	s := &Session{UI: u, Timeout: time.Minute, MCP: m}
	call := func(name, args string) docsgpt.ToolResult {
		return s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: name, Arguments: args}})
	}

	if got := call("mcp__test__echo", `{"text":"hi"}`).Content; got != "The user denied this tool call." {
		t.Errorf("denied: %q", got)
	}
	if got := u.offered[0]; strings.Join(got, "|") != "Approve|Always allow echo|Always allow test|Always approve|Deny" {
		t.Errorf("choices %q", got)
	}
	if u.titles[0] != "test · echo (MCP, read-only)" {
		t.Errorf("title %q", u.titles[0])
	}

	// "Always allow" the tool: that tool runs unasked, others still ask.
	u.choice = "always"
	if got := call("mcp__test__echo_ct2", `{"text":"hi"}`).Content; got != "echo: hi" { // renamed by the server
		t.Errorf("echo = %q", got)
	}
	asked := len(u.offered)
	call("mcp__test__echo", `{"text":"again"}`)
	if len(u.offered) != asked {
		t.Error("asked again about an allowed tool")
	}
	call("mcp__test__fail", `{}`)
	if len(u.offered) != asked+1 {
		t.Error("another tool ran unasked")
	}

	// "Always allow" the server: all its tools, not the other server's.
	u.choice = "server"
	if res := call("mcp__test__image", ``); len(res.Parts) != 1 || !strings.Contains(res.Content, "a pixel") {
		t.Errorf("image: %+v", res)
	}
	asked = len(u.offered)
	if got := call("mcp__test__fail", `{}`).Content; got != "[The tool reported an error.]\nit broke" {
		t.Errorf("fail = %q", got)
	}
	if got := call("mcp__test__big", `{}`).Content; !strings.HasPrefix(got, "line 1\n") || !strings.HasSuffix(got, "[Output truncated: showing the first 2000 of 3000 lines.]") {
		t.Errorf("big result ends %q", got[len(got)-80:])
	}
	if len(u.offered) != asked {
		t.Error("asked about a tool of an allowed server")
	}
	call("mcp__other__echo", `{"text":"x"}`)
	if len(u.offered) != asked+1 {
		t.Error("the other server's tool ran unasked")
	}
	if status := u.status[len(u.status)-1]; !strings.HasPrefix(status, "1 line · ") {
		t.Errorf("status %q", status)
	}

	if got := call("mcp__test__echo", `[1]`).Content; !strings.HasPrefix(got, "Error: the arguments are not a JSON object") {
		t.Errorf("bad arguments: %q", got)
	}
}
