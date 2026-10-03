// Package session saves chats, so they can be resumed: one JSONL file per
// chat under ~/.docsgpt/sessions/<cwd slug>-<cwd hash>/, a header line and
// then the messages as they complete (pi's format, without its tree: a
// fork is a new file starting with a copy of the messages it keeps).
package session

import (
	"bufio"
	"bytes"
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

	"github.com/arc53/DocsGPT-cli/internal/attach"
	"github.com/arc53/DocsGPT-cli/internal/config"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// Entry is one line of a session file: the "session" header first, then
// "message" lines, "state" lines (the server, key or server conversation
// changed) and "name" lines (the chat was named, or the name cleared).
type Entry struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`

	ID     string `json:"id,omitempty"`     // header
	Cwd    string `json:"cwd,omitempty"`    // header
	Parent string `json:"parent,omitempty"` // header: the file this chat was forked from
	Name   string `json:"name,omitempty"`   // name

	Server         string `json:"server,omitempty"`          // header, state
	Key            string `json:"key,omitempty"`             // header, state
	ConversationID string `json:"conversation_id,omitempty"` // header, state

	Message *docsgpt.Message `json:"message,omitempty"`
	Text    string           `json:"text,omitempty"` // what the user typed; Message adds the context
	// Attachments are the files a user message carried: their paths and
	// hashes, not their bytes, which the server keeps.
	Attachments []attach.File    `json:"attachments,omitempty"`
	Sources     []docsgpt.Source `json:"sources,omitempty"`
	Usage       *docsgpt.Usage   `json:"usage,omitempty"` // an exchange's last message: the tokens it took

	// Where a message went: the header's or the latest state line's.
	server, key, conv string
}

// Session is one saved chat.
type Session struct {
	Path           string
	Cwd            string
	Name           string // given with /name; "" for none
	Parent         string // the file this chat was forked from
	Server         string // the latest
	Key            string // the latest
	ConversationID string // the latest
	Created        time.Time
	Updated        time.Time
	Messages       []Entry // the "message" entries, in order

	pending []Entry // what a fork copies, written with its first record
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
// server conversation changed. The first call writes the header (and what
// a fork copies).
func (s *Session) Record(server, key, conversationID string, msgs ...Entry) error {
	now := time.Now().UTC()
	var lines []Entry
	if !s.Saved() {
		lines = s.pending
		if lines == nil {
			lines = []Entry{{Type: "session", Time: s.Created, ID: s.id(), Cwd: s.Cwd, Parent: s.Parent, Server: server, Key: key, ConversationID: conversationID}}
			s.Server, s.Key, s.ConversationID = server, key, conversationID
		}
		if s.Name != "" {
			lines = append(lines, Entry{Type: "name", Time: now, Name: s.Name})
		}
	}
	if server != s.Server || key != s.Key || conversationID != s.ConversationID {
		lines = append(lines, Entry{Type: "state", Time: now, Server: server, Key: key, ConversationID: conversationID})
	}
	for _, m := range msgs {
		m.Type, m.Time = "message", now
		m.server, m.key, m.conv = server, key, conversationID
		lines = append(lines, m)
	}
	if err := s.append(lines); err != nil {
		return err
	}
	s.pending = nil
	s.Server, s.Key, s.ConversationID, s.Updated = server, key, conversationID, now
	s.Messages = append(s.Messages, lines[len(lines)-len(msgs):]...)
	return nil
}

// append writes lines at the end of the file, creating it.
func (s *Session) append(lines []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// One write, so chats recording to the same file do not interleave.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	_, err = f.Write(buf.Bytes())
	return err
}

// id is the session's id: the end of its file name.
func (s *Session) id() string {
	id := strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
	return id[strings.LastIndexByte(id, '_')+1:]
}

// Saved reports whether the session has a file yet.
func (s *Session) Saved() bool {
	_, err := os.Stat(s.Path)
	return err == nil
}

// SetName names the chat, "" clearing the name. A chat not saved yet
// writes it with its first record.
func (s *Session) SetName(name string) error {
	if s.Saved() {
		if err := s.append([]Entry{{Type: "name", Time: time.Now().UTC(), Name: name}}); err != nil {
			return err
		}
	}
	s.Name = name
	return nil
}

// Delete removes the session's file.
func (s *Session) Delete() error { return os.Remove(s.Path) }

// Fork starts a new session holding a copy of the exchanges before turn
// (counted from 0), so the chat can go on from there while this one stays
// as it is. Its file is written with its first record.
func (s *Session) Fork(turn int) *Session {
	f := New(s.Cwd, s.Server, s.Key)
	f.Parent = s.Path
	n, users := len(s.Messages), 0
	for i, e := range s.Messages {
		if e.Message.Role != "user" {
			continue
		}
		if users == turn {
			n = i
			break
		}
		users++
	}
	for i, e := range s.Messages[:n] {
		if i == 0 {
			f.pending = []Entry{{Type: "session", Time: f.Created, ID: f.id(), Cwd: f.Cwd, Parent: f.Parent, Server: e.server, Key: e.key, ConversationID: e.conv}}
		} else if e.server != f.Server || e.key != f.Key || e.conv != f.ConversationID {
			f.pending = append(f.pending, Entry{Type: "state", Time: e.Time, Server: e.server, Key: e.key, ConversationID: e.conv})
		}
		f.Server, f.Key, f.ConversationID = e.server, e.key, e.conv
		f.pending = append(f.pending, e)
		f.Messages = append(f.Messages, e)
	}
	return f
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
			s.Cwd, s.Server, s.Created, s.Parent = e.Cwd, e.Server, e.Time, e.Parent
			s.Key, s.ConversationID = e.Key, e.ConversationID
		case "state":
			s.Server, s.Key, s.ConversationID = e.Server, e.Key, e.ConversationID
		case "message":
			if e.Message != nil {
				e.server, e.key, e.conv = s.Server, s.Key, s.ConversationID
				s.Messages = append(s.Messages, e)
			}
		case "name":
			s.Name = e.Name
			continue // naming a chat does not make it recent
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
	Files    []attach.File // attached to the question
	Answer   string        // the assistant's texts of the exchange, joined
	Sources  []docsgpt.Source
	Usage    *docsgpt.Usage // nil when the server reported none

	// Where the exchange took place: the server, the key and the server
	// conversation ("" when the server named none), and the exchange's
	// index in that conversation.
	Server, Key, ConversationID string
	Index                       int
}

// Turns groups the messages into exchanges.
func (s *Session) Turns() []Turn {
	var turns []Turn
	conv, index := "", 0
	for _, e := range s.Messages {
		switch m := e.Message; {
		case m.Role == "user":
			// The server numbers a conversation's exchanges from 0; one
			// started over from the messages counts again.
			if index++; len(turns) == 0 || e.conv != conv {
				conv, index = e.conv, 0
			}
			turns = append(turns, Turn{Question: e.Text, Files: e.Attachments, Server: e.server, Key: e.key, ConversationID: e.conv, Index: index})
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
			if e.Usage != nil {
				t.Usage = e.Usage
			}
		}
	}
	return turns
}

// Usage returns the tokens of the last exchange that reported them, and
// of all of them.
func (s *Session) Usage() (last, total docsgpt.Usage) {
	for _, t := range s.Turns() {
		if u := t.Usage; u != nil {
			last = *u
			total.PromptTokens += u.PromptTokens
			total.CompletionTokens += u.CompletionTokens
			total.TotalTokens += u.TotalTokens
		}
	}
	return last, total
}

// Title is the chat's name, else the first line of its first question.
func (s *Session) Title() string {
	if s.Name != "" {
		return s.Name
	}
	for _, t := range s.Turns() {
		if line, _, _ := strings.Cut(strings.TrimSpace(t.Question), "\n"); line != "" {
			return line
		}
	}
	return "(empty)"
}
