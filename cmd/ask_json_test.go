package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

// TestAskJSON: --json fills in the answer of every round, the sources,
// the conversation, model, usage and tool calls.
func TestAskJSON(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0o644)
	saved := []bool{globalNoStdin, globalNoContext, globalAutoApprove}
	globalNoStdin, globalNoContext, globalAutoApprove = true, true, true
	t.Cleanup(func() { globalNoStdin, globalNoContext, globalAutoApprove = saved[0], saved[1], saved[2] })

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		frames := []string{`{"docsgpt":{"type":"id","conversation_id":"conv-1"}}`}
		if calls == 1 {
			frames = append(frames,
				`{"choices":[{"delta":{"content":"Let me look."}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"notes.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
		} else {
			if !strings.Contains(string(b), `hello\n`) {
				t.Errorf("the tool result was not sent: %s", b)
			}
			frames = append(frames,
				`{"docsgpt":{"type":"source","sources":[{"title":"Guide","source":"https://docs/guide","text":"…"}]}}`,
				`{"docsgpt":{"type":"model","model":"gpt-x"}}`,
				`{"choices":[{"delta":{"content":"It says hello."}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}}`)
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	globalURL = srv.URL
	t.Setenv(config.EnvAPIKey, "k")

	out := &askResult{Sources: []askSource{}, ToolCalls: []askToolCall{}}
	if err := runAsk([]string{"what", "is", "in", "notes?"}, out); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	for _, want := range []string{
		`"answer":"Let me look.\n\nIt says hello."`,
		`"sources":[{"title":"Guide","url":"https://docs/guide"}]`,
		`"conversation_id":"conv-1"`, `"model":"gpt-x"`,
		`"usage":{"prompt_tokens":30,"completion_tokens":5,"total_tokens":35}`,
		`"tool_calls":[{"name":"read_file","arguments":{"path":"notes.txt"},"result":"hello\n","approved":true}]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s\nlacks %s", b, want)
		}
	}
	if strings.Contains(string(b), `"error"`) {
		t.Errorf("an error in %s", b)
	}

	// An error keeps the shape and says what went wrong.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 401, `{"error":{"message":"Invalid API key"}}`)
	})
	out = &askResult{Sources: []askSource{}, ToolCalls: []askToolCall{}}
	err := runAsk([]string{"q"}, out)
	if err == nil || !strings.Contains(err.Error(), "rejected the key in "+config.EnvAPIKey) {
		t.Fatalf("err = %v", err)
	}
}
