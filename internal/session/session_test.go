package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

func TestRecordAndLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := "/work/repo"
	if got := filepath.Base(Dir(cwd)); got != "--work-repo--" {
		t.Errorf("Dir = %q", got)
	}
	s := New(cwd, "https://example.com", "test")
	if s.Saved() {
		t.Fatal("a new session must not be written before its first message")
	}
	user := docsgpt.Message{Role: "user", Content: "<context>\n…\n</context>\n\nhello"}
	answer := docsgpt.Message{Role: "assistant", Content: "Hi!"}
	if err := s.Record("test", "conv-1", Entry{Message: &user, Text: "hello"}, Entry{Message: &answer, Sources: []docsgpt.Source{{Title: "Doc"}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	second := New(cwd, "https://example.com", "other")
	q2 := docsgpt.Message{Role: "user", Content: "second chat"}
	second.Record("other", "conv-2", Entry{Message: &q2, Text: "second chat"})
	s.Record("other", "conv-3", Entry{Message: &q2, Text: "again"})

	if fi, err := os.Stat(s.Path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode: %v %v", fi, err)
	}
	if fi, _ := os.Stat(Dir(cwd)); fi.Mode().Perm() != 0o700 {
		t.Errorf("session dir mode: %v", fi.Mode())
	}
	list, err := List(cwd)
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %d sessions, %v", len(list), err)
	}
	got := list[0] // updated last
	if got.Path != s.Path || got.Key != "other" || got.ConversationID != "conv-3" || got.Server != "https://example.com" {
		t.Fatalf("latest = %+v", got)
	}
	turns := got.Turns()
	if len(turns) != 2 || turns[0].Question != "hello" || turns[0].Answer != "Hi!" || turns[0].Sources[0].Title != "Doc" || turns[1].Question != "again" {
		t.Fatalf("turns = %+v", turns)
	}
	if got.Title() != "hello" || len(got.Messages) != 3 || got.Messages[0].Message.Content != user.Content {
		t.Errorf("title %q, %d messages", got.Title(), len(got.Messages))
	}
}
