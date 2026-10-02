package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/session"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// TestHandleDecidesOnWhatWasShown: a collapsed paste starting with ! or /
// is a message, never a shell or slash command.
func TestHandleDecidesOnWhatWasShown(t *testing.T) {
	isolateConfig(t)
	oldCtx, oldStream := globalNoContext, globalNoStream
	globalNoContext, globalNoStream = true, true
	t.Cleanup(func() { globalNoContext, globalNoStream = oldCtx, oldStream })

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		jsonReply(w, 500, `{"error":"no"}`)
	}))
	defer srv.Close()
	s := &chatSession{client: docsgpt.NewClient(srv.URL, "k"), tools: &tools.Session{}, sess: session.New(t.TempDir(), srv.URL, "k")}

	marker := filepath.Join(t.TempDir(), "ran")
	pasted := "!touch " + marker + "\n" + strings.Repeat("more\n", 12)
	s.handle(pasted, "[paste #1 +13 lines]")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a pasted !command ran")
	}
	s.handle("/new and then some\n"+strings.Repeat("x\n", 12), "[paste #1 +13 lines]")
	if len(bodies) != 2 || !strings.Contains(bodies[0], "touch") || !strings.Contains(bodies[1], "/new and then some") {
		t.Fatalf("pastes were not sent as messages: %q", bodies)
	}

	s.handle("!touch "+marker, "!touch "+marker)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a typed !command did not run")
	}
	if s.handle("/nosuchcommand", "/nosuchcommand"); len(bodies) != 2 {
		t.Fatal("a typed /command was sent")
	}
}
