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

// exchange records a question and its answer in conversation conv.
func exchange(t *testing.T, s *Session, key, conv, q string, usage *docsgpt.Usage) {
	t.Helper()
	u, a := docsgpt.Message{Role: "user", Content: q}, docsgpt.Message{Role: "assistant", Content: "answer to " + q}
	if err := s.Record("https://example.com", key, conv, Entry{Message: &u, Text: q}, Entry{Message: &a, Usage: usage}); err != nil {
		t.Fatal(err)
	}
}

// TestName: a name given before the first record is written with it, a
// later one is appended, "" clears it, and naming does not make a chat
// recent.
func TestName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New("/w", "https://example.com", "k")
	if err := s.SetName("early"); err != nil || s.Saved() {
		t.Fatalf("SetName before a record: %v, saved %v", err, s.Saved())
	}
	exchange(t, s, "k", "c", "how do I deploy?", nil)
	if got, _ := Load(s.Path); got.Name != "early" || got.Title() != "early" {
		t.Fatalf("name %q, title %q", got.Name, got.Title())
	}
	updated := s.Updated
	time.Sleep(10 * time.Millisecond)
	s.SetName("deploy notes")
	got, _ := Load(s.Path)
	if got.Name != "deploy notes" || !got.Updated.Equal(updated) {
		t.Fatalf("renamed: %q, updated %v (was %v)", got.Name, got.Updated, updated)
	}
	got.SetName("")
	if got, _ = Load(s.Path); got.Name != "" || got.Title() != "how do I deploy?" {
		t.Fatalf("cleared: name %q, title %q", got.Name, got.Title())
	}
	if got.Delete() != nil || got.Saved() {
		t.Fatal("Delete left the file")
	}
}

// TestUsage: each exchange keeps its tokens; the totals come back on load.
func TestUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New("/w", "https://example.com", "k")
	exchange(t, s, "k", "c", "one", &docsgpt.Usage{PromptTokens: 1000, CompletionTokens: 20, TotalTokens: 1020})
	exchange(t, s, "k", "c", "two", nil) // a server that reported none
	exchange(t, s, "k", "c", "three", &docsgpt.Usage{PromptTokens: 2000, CompletionTokens: 5, TotalTokens: 2005})
	got, _ := Load(s.Path)
	last, total := got.Usage()
	if last.PromptTokens != 2000 || last.CompletionTokens != 5 || total.PromptTokens != 3000 || total.CompletionTokens != 25 || total.TotalTokens != 3025 {
		t.Fatalf("last %+v, total %+v", last, total)
	}
	if turns := got.Turns(); turns[1].Usage != nil || turns[0].Usage.TotalTokens != 1020 {
		t.Fatalf("turn usage: %+v / %+v", turns[0].Usage, turns[1].Usage)
	}
}

// TestQuestionIndex: an exchange's index counts from 0 in its server
// conversation, and again in one that started over (an error, another
// key, a fork); a resumed chat goes on counting.
func TestQuestionIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New("/w", "https://example.com", "k")
	exchange(t, s, "k", "c1", "a", nil)
	exchange(t, s, "k", "c1", "b", nil)
	exchange(t, s, "k", "c2", "c", nil) // started over
	exchange(t, s, "other", "c3", "d", nil)
	resumed, _ := Load(s.Path)
	exchange(t, resumed, "other", "c3", "e", nil)
	exchange(t, resumed, "other", "", "f", nil) // no conversation from the server

	got, _ := Load(s.Path)
	want := []struct {
		conv, key string
		index     int
	}{{"c1", "k", 0}, {"c1", "k", 1}, {"c2", "k", 0}, {"c3", "other", 0}, {"c3", "other", 1}, {"", "other", 0}}
	turns := got.Turns()
	if len(turns) != len(want) {
		t.Fatalf("%d turns", len(turns))
	}
	for i, w := range want {
		if tr := turns[i]; tr.ConversationID != w.conv || tr.Key != w.key || tr.Index != w.index || tr.Server != "https://example.com" {
			t.Errorf("turn %d = %s/%s #%d, want %s/%s #%d", i, tr.Key, tr.ConversationID, tr.Index, w.key, w.conv, w.index)
		}
	}
}

// TestFork: a fork copies the exchanges before the one picked, with where
// each took place, into a new file written only with its first record;
// the original stays whole.
func TestFork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := New("/w", "https://example.com", "k")
	s.SetName("original")
	exchange(t, s, "k", "c1", "a", &docsgpt.Usage{PromptTokens: 10, CompletionTokens: 1})
	exchange(t, s, "k", "c2", "b", &docsgpt.Usage{PromptTokens: 20, CompletionTokens: 2})
	exchange(t, s, "k", "c2", "c", nil)

	f := s.Fork(2)
	if f.Path == s.Path || f.Saved() || f.Parent != s.Path || f.Name != "" {
		t.Fatalf("fork %+v", f)
	}
	if turns := f.Turns(); len(turns) != 2 || turns[1].Question != "b" || turns[1].ConversationID != "c2" {
		t.Fatalf("fork turns before its record: %+v", turns)
	}
	exchange(t, f, "k", "c9", "c, again", nil) // the server conversation starts over

	got, err := Load(f.Path)
	if err != nil || got.Parent != s.Path || got.Cwd != "/w" {
		t.Fatalf("fork file: %+v, %v", got, err)
	}
	turns := got.Turns()
	if len(turns) != 3 || turns[2].Question != "c, again" {
		t.Fatalf("fork turns: %+v", turns)
	}
	for i, w := range []struct {
		conv  string
		index int
	}{{"c1", 0}, {"c2", 0}, {"c9", 0}} {
		if turns[i].ConversationID != w.conv || turns[i].Index != w.index {
			t.Errorf("fork turn %d: %s #%d, want %s #%d", i, turns[i].ConversationID, turns[i].Index, w.conv, w.index)
		}
	}
	if _, total := got.Usage(); total.PromptTokens != 30 {
		t.Errorf("fork usage total %+v", total)
	}
	if orig, _ := Load(s.Path); len(orig.Turns()) != 3 || orig.Name != "original" {
		t.Errorf("original changed: %d turns, name %q", len(orig.Turns()), orig.Name)
	}

	// Forking at the first message keeps nothing: a plain new chat.
	first := s.Fork(0)
	if len(first.Messages) != 0 {
		t.Fatalf("fork(0) kept %d messages", len(first.Messages))
	}
	exchange(t, first, "k", "c10", "a2", nil)
	if got, _ := Load(first.Path); got.Parent != s.Path || len(got.Turns()) != 1 {
		t.Fatalf("fork(0) file: %+v", got)
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
