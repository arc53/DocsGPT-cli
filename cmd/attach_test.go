package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/attach"
	"github.com/arc53/DocsGPT-cli/internal/session"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// TestChatAttachments: a message's files (a marker's, an @path's) go as
// content parts once; the history and the session keep the text and the
// files' paths and hashes, not their bytes; a resumed chat shows markers.
func TestChatAttachments(t *testing.T) {
	s, agent := testChat(t)
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("notes.md", []byte("# Notes"), 0o644)
	shot := filepath.Join(dir, "shot.png")
	os.WriteFile(shot, pngBytes, 0o644)
	img, _ := attach.Open(shot)
	img.ID, img.Clipboard = 1, true

	s.send("what is [image #1 · 16 B] and @notes.md?", "what is [image #1 · 16 B] and @notes.md?", img)
	s.send("and now?", "and now?")

	if len(agent.requests) != 2 {
		t.Fatalf("%d requests", len(agent.requests))
	}
	first := agent.requests[0].Messages[0]
	if first.Text() != "what is [image #1 · 16 B] and @notes.md?" || len(first.Parts) != 3 ||
		first.Parts[1].Type != "image_url" || first.Parts[2].File == nil || first.Parts[2].File.Filename != "notes.md" {
		t.Fatalf("first message: %+v", first)
	}
	for _, m := range agent.requests[1].Messages {
		if len(m.Parts) > 0 {
			t.Fatalf("the files went again: %+v", m)
		}
	}

	b, _ := os.ReadFile(s.sess.Path)
	if strings.Contains(string(b), "base64") || !strings.Contains(string(b), `"sha256":"`) {
		t.Fatalf("session: %s", b)
	}
	loaded, _ := session.Load(s.sess.Path)
	turn := loaded.Turns()[0]
	if len(turn.Files) != 2 || turn.Files[1].Ref != "@notes.md" || turn.Files[1].ID != 2 {
		t.Fatalf("files: %+v", turn.Files)
	}
	if got := attach.Show(turn.Question, turn.Files); got != "what is [image #1 · 16 B] and [file #2 · notes.md · 7 B]?" {
		t.Fatalf("shown again: %q", got)
	}
	if got := attached(turn.Files); len(got) != 1 || got[0].Path != shot {
		t.Fatalf("attached again: %+v", got)
	}

	// A file that cannot go is not sent, and the message is put back.
	os.WriteFile("app.bin", []byte{0, 1, 2}, 0o644)
	s.send("see @app.bin", "see @app.bin")
	if len(agent.requests) != 2 {
		t.Fatal("sent with a file that cannot be attached")
	}
}
