// Package session saves chats, so they can be resumed: one JSONL file per
// chat under ~/.docsgpt/sessions/<cwd slug>-<cwd hash>/, a header line and
// then the messages as they complete (pi's format, without its tree).
package session

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// Entry is one line of a session file: the "session" header first, then
// "message" lines and "state" lines (the server, key or server
// conversation changed).
type Entry struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`

	ID  string `json:"id,omitempty"`  // header
	Cwd string `json:"cwd,omitempty"` // header

	Server         string `json:"server,omitempty"`          // header, state
	Key            string `json:"key,omitempty"`             // header, state
	ConversationID string `json:"conversation_id,omitempty"` // header, state

	Message *docsgpt.Message `json:"message,omitempty"`
	Text    string           `json:"text,omitempty"` // what the user typed; Message adds the context
	Sources []docsgpt.Source `json:"sources,omitempty"`
}

// Session is one saved chat.
type Session struct {
	Path           string
	Cwd            string
	Server         string // the latest
	Key            string // the latest
	ConversationID string // the latest
	Created        time.Time
	Updated        time.Time
	Messages       []Entry // the "message" entries, in order
}

// Dir returns the directory of cwd's sessions: the end of the path made
// readable, and a hash of it, as paths can read the same (/x/a-b, /x/a/b).
func Dir(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	name := hex.EncodeToString(sum[:4])
	slug := strings.Trim(nonSlug.ReplaceAllString(cwd, "-"), "-")
	if slug = strings.TrimLeft(slug[max(0, len(slug)-48):], "-"); slug != "" {
		name = slug + "-" + name
	}
	return filepath.Join(config.Dir(), "sessions", name)
}

var nonSlug = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// New starts a session in memory; its file is written with the first
// messages.
func New(cwd, server, key string) *Session {
	id := make([]byte, 8)
	rand.Read(id)
	now := time.Now().UTC()
	name := strings.NewReplacer(":", "-", ".", "-").Replace(now.Format("2006-01-02T15:04:05.000Z")) + "_" + hex.EncodeToString(id) + ".jsonl"
	return &Session{Path: filepath.Join(Dir(cwd), name), Cwd: cwd, Server: server, Key: key, Created: now, Updated: now}
}

// Record appends messages, noting first whether the server, the key or the
// server conversation changed. The first call writes the header.
func (s *Session) Record(server, key, conversationID string, msgs ...Entry) error {
	now := time.Now().UTC()
	var lines []Entry
	if _, err := os.Stat(s.Path); err != nil {
		id := strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
		id = id[strings.LastIndexByte(id, '_')+1:]
		lines = append(lines, Entry{Type: "session", Time: s.Created, ID: id, Cwd: s.Cwd, Server: server, Key: key, ConversationID: conversationID})
	} else if server != s.Server || key != s.Key || conversationID != s.ConversationID {
		lines = append(lines, Entry{Type: "state", Time: now, Server: server, Key: key, ConversationID: conversationID})
	}
	for _, m := range msgs {
		m.Type, m.Time = "message", now
		lines = append(lines, m)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	s.Server, s.Key, s.ConversationID, s.Updated = server, key, conversationID, now
	s.Messages = append(s.Messages, lines[len(lines)-len(msgs):]...)
	return nil
}

// Saved reports whether the session has a file yet.
func (s *Session) Saved() bool {
	_, err := os.Stat(s.Path)
	return err == nil
}

// Load reads a session file.
func Load(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := &Session{Path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "session":
			s.Cwd, s.Server, s.Created = e.Cwd, e.Server, e.Time
			s.Key, s.ConversationID = e.Key, e.ConversationID
		case "state":
			s.Server, s.Key, s.ConversationID = e.Server, e.Key, e.ConversationID
		case "message":
			if e.Message != nil {
				s.Messages = append(s.Messages, e)
			}
		}
		s.Updated = e.Time
	}
	return s, sc.Err()
}

// List returns cwd's sessions that hold messages, latest first.
func List(cwd string) ([]*Session, error) {
	paths, err := filepath.Glob(filepath.Join(Dir(cwd), "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, p := range paths {
		if s, err := Load(p); err == nil && len(s.Messages) > 0 && s.Cwd == cwd {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Turn is one exchange: what the user typed and the answer.
type Turn struct {
	Question string
	Answer   string // the assistant's texts of the exchange, joined
	Sources  []docsgpt.Source
}

// Turns groups the messages into exchanges.
func (s *Session) Turns() []Turn {
	var turns []Turn
	for _, e := range s.Messages {
		switch m := e.Message; {
		case m.Role == "user":
			turns = append(turns, Turn{Question: e.Text})
			if e.Text == "" {
				turns[len(turns)-1].Question = m.Content
			}
		case m.Role == "assistant" && len(turns) > 0:
			t := &turns[len(turns)-1]
			if strings.TrimSpace(m.Content) != "" {
				if t.Answer != "" {
					t.Answer += "\n\n"
				}
				t.Answer += m.Content
			}
			if len(e.Sources) > 0 {
				t.Sources = e.Sources
			}
		}
	}
	return turns
}

// Title is the first line of the first question.
func (s *Session) Title() string {
	for _, t := range s.Turns() {
		if line, _, _ := strings.Cut(strings.TrimSpace(t.Question), "\n"); line != "" {
			return line
		}
	}
	return "(empty)"
}
