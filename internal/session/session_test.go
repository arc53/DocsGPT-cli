package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

func TestRecordAndLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := "/work/repo"
	if got := filepath.Base(Dir(cwd)); !strings.HasPrefix(got, "work-repo-") || len(got) != len("work-repo-")+8 {
		t.Errorf("Dir = %q", got)
	}
	s := New(cwd, "https://example.com", "test")
	if s.Saved() {
		t.Fatal("a new session must not be written before its first message")
	}
	user := docsgpt.Message{Role: "user", Content: "<context>\n…\n</context>\n\nhello"}
	answer := docsgpt.Message{Role: "assistant", Content: "Hi!"}
	if err := s.Record("https://example.com", "test", "conv-1", Entry{Message: &user, Text: "hello"}, Entry{Message: &answer, Sources: []docsgpt.Source{{Title: "Doc"}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	second := New(cwd, "https://example.com", "other")
	q2 := docsgpt.Message{Role: "user", Content: "second chat"}
	second.Record("https://example.com", "other", "conv-2", Entry{Message: &q2, Text: "second chat"})
	// Resumed on another server: the state line records it.
	s.Record("https://other.example", "other", "conv-3", Entry{Message: &q2, Text: "again"})

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
	if got.Path != s.Path || got.Key != "other" || got.ConversationID != "conv-3" || got.Server != "https://other.example" {
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

// TestConcurrentRecords: two chats on one session file (two -c) keep each
// record's lines whole and together.
func TestConcurrentRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first := New("/w", "https://example.com", "k")
	q := docsgpt.Message{Role: "user", Content: "hi"}
	first.Record("https://example.com", "k", "c", Entry{Message: &q})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _ := Load(first.Path)
			for i := range 20 {
				m := docsgpt.Message{Role: "user", Content: fmt.Sprintf("%d-%d %s", g, i, strings.Repeat("x", 3000))}
				s.Record("https://example.com", "k", "c", Entry{Message: &m}, Entry{Message: &m})
			}
		}()
	}
	wg.Wait()
	s, err := Load(first.Path)
	if err != nil || len(s.Messages) != 1+8*20*2 {
		t.Fatalf("%d messages after concurrent records, %v", len(s.Messages), err)
	}
	for i := 1; i < len(s.Messages); i += 2 {
		if a, b := s.Messages[i].Message.Content, s.Messages[i+1].Message.Content; a != b {
			t.Fatalf("records interleaved at %d: %.6s / %.6s", i, a, b)
		}
	}
}

// TestDirsDoNotCollide: paths that read alike keep their own chats, and a
// session file in the wrong directory is not listed.
func TestDirsDoNotCollide(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if Dir("/x/a-b") == Dir("/x/a/b") || Dir(`C:\x`) == Dir("/C/x") {
		t.Fatal("different paths share a session directory")
	}
	if got := filepath.Base(Dir("/")); len(got) != 8 {
		t.Errorf("Dir(/) = %q", got)
	}
	long := "/" + strings.Repeat("deep/", 40) + "project"
	if got := filepath.Base(Dir(long)); len(got) > 60 || !strings.HasSuffix(got[:len(got)-9], "project") {
		t.Errorf("Dir(long) = %q", got)
	}

	q := docsgpt.Message{Role: "user", Content: "hi"}
	other := New("/x/a/b", "https://example.com", "k")
	other.Path = filepath.Join(Dir("/x/a-b"), filepath.Base(other.Path)) // as the old encoding did
	if err := other.Record("https://example.com", "k", "conv", Entry{Message: &q}); err != nil {
		t.Fatal(err)
	}
	if list, _ := List("/x/a-b"); len(list) != 0 {
		t.Fatalf("listed another directory's chat: %+v", list[0])
	}
}
