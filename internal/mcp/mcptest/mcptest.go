// Package mcptest runs a small MCP server over stdio for tests: the test
// binary itself, started again. A package using it calls Main from its
// TestMain.
package mcptest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const env = "DOCSGPT_MCPTEST_SERVER"

// Main serves when the binary was started as the server, else runs the
// tests.
func Main(m *testing.M) {
	if os.Getenv(env) == "1" {
		serve()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Server is the test server as a configured one, named name.
func Server(name string) mcp.Server {
	dir, _ := os.Getwd()
	return mcp.Server{Name: name, Command: os.Args[0], Args: []string{"-test.run=^$"}, Env: map[string]string{env: "1"}, Dir: dir}
}

// A 1×1 PNG.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")

func serve() {
	// What a careless server prints first, which must not break the protocol.
	fmt.Println("Booting the test server")
	fmt.Fprintln(os.Stderr, "hello from stderr")

	s := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{Instructions: "Call echo to echo."})
	text := func(t string) *sdk.CallToolResult {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: t}}}
	}
	type echoArgs struct {
		Text string `json:"text" jsonschema:"what to echo"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "echo", Description: "Echo the text.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *sdk.CallToolRequest, a echoArgs) (*sdk.CallToolResult, any, error) {
			return text("echo: " + a.Text), nil, nil
		})
	type none struct{}
	add := func(name string, h func(ctx context.Context) *sdk.CallToolResult) {
		sdk.AddTool(s, &sdk.Tool{Name: name, Description: "Test tool " + name + "."},
			func(ctx context.Context, _ *sdk.CallToolRequest, _ none) (*sdk.CallToolResult, any, error) {
				return h(ctx), nil, nil
			})
	}
	add("image", func(context.Context) *sdk.CallToolResult {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "a pixel"}, &sdk.ImageContent{MIMEType: "image/png", Data: png}}}
	})
	add("fail", func(context.Context) *sdk.CallToolResult {
		r := text("it broke")
		r.IsError = true
		return r
	})
	add("crash", func(context.Context) *sdk.CallToolResult {
		fmt.Fprintln(os.Stderr, "crashing")
		os.Exit(3)
		return nil
	})
	add("big", func(context.Context) *sdk.CallToolResult {
		var b strings.Builder
		for i := 1; i <= 3000; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return text(b.String())
	})
	add("slow", func(ctx context.Context) *sdk.CallToolResult {
		select {
		case <-ctx.Done():
		case <-time.After(time.Minute):
		}
		return text("done")
	})
	add("spawn", func(context.Context) *sdk.CallToolResult {
		name, args := "sleep", []string{"60"}
		if runtime.GOOS == "windows" {
			name, args = "ping", []string{"-n", "60", "127.0.0.1"}
		}
		cmd := exec.Command(name, args...)
		if err := cmd.Start(); err != nil {
			return text(err.Error())
		}
		return text(fmt.Sprint(cmd.Process.Pid))
	})
	s.Run(context.Background(), &sdk.StdioTransport{})
}
