package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/session"
	"github.com/arc53/DocsGPT-cli/internal/ui"
)

// editEarlier picks one of the user's messages (Esc twice, /edit), takes
// the chat back to before it and puts it in the input to change and send
// again.
func (s *chatSession) editEarlier(string) {
	turns := s.sess.Turns()
	if len(turns) == 0 {
		s.note("No message of yours to edit yet.")
		return
	}
	items := make([]ui.Item, 0, len(turns))
	for i := len(turns) - 1; i >= 0; i-- {
		first, rest, _ := strings.Cut(strings.TrimSpace(turns[i].Question), "\n")
		desc := fmt.Sprintf("message %d", i+1)
		if rest != "" {
			desc += " · " + lineCount(turns[i].Question)
		}
		items = append(items, ui.Item{Label: display.Safe(first), Value: strconv.Itoa(i), Description: desc})
	}
	v, err := s.scr.Select(ui.Select{Title: "Edit a message (the chat goes back to before it)", Items: items, Filter: len(items) > 1})
	if err != nil {
		return
	}
	i, _ := strconv.Atoi(v)
	s.fork(i, fmt.Sprintf("── edited from message %d ──", i+1))
	s.scr.SetInput(turns[i].Question)
}

// retry sends the last message again: one that failed or was stopped, else
// the last question, the chat going back to before it.
func (s *chatSession) retry(string) {
	if q := s.unanswered; q[0] != "" {
		s.send(q[0], q[1])
		return
	}
	turns := s.sess.Turns()
	if len(turns) == 0 {
		s.fail("No message to send again yet.")
		return
	}
	i := len(turns) - 1
	q := turns[i].Question
	s.fork(i, fmt.Sprintf("── message %d again ──", i+1))
	s.send(q, q)
}

// fork takes the chat back to before exchange i: it goes on in a new
// session holding a copy of the exchanges before it (the old session stays
// whole, for /resume), and in a new server conversation that starts from
// their messages. The transcript is drawn again, divider last.
func (s *chatSession) fork(i int, divider string) {
	turns := s.adopt(s.sess.Fork(i))
	s.conversationID = ""
	s.top()
	s.showTurns(turns)
	s.note(divider)
	s.footer()
}

// adopt makes sess the chat's session: its messages the history, its
// tokens the footer's. It returns its exchanges.
func (s *chatSession) adopt(sess *session.Session) []session.Turn {
	s.reset()
	s.sess, s.said = sess, true
	for _, e := range sess.Messages {
		s.history = append(s.history, *e.Message)
		if c := e.Message.Content; e.Message.Role == "user" && strings.HasPrefix(c, "<context>") {
			if i := strings.Index(c, "</context>"); i > 0 {
				s.sentContext = c[:i+len("</context>")]
			}
		}
	}
	s.conversationID = sess.ConversationID
	s.lastUsage, s.totalUsage = sess.Usage()
	turns := sess.Turns()
	if n := len(turns); n > 0 {
		s.lastAnswer = display.StripControls(turns[n-1].Answer)
	}
	return turns
}

// showTurns adds the exchanges to the transcript.
func (s *chatSession) showTurns(turns []session.Turn) {
	for _, t := range turns {
		s.scr.Add(display.User(t.Question))
		s.scr.Add(display.Markdown(t.Answer))
		s.scr.Add(display.Sources(t.Sources))
	}
}

// nameChat names the chat (/name title), or asks for the name, an empty
// one clearing it.
func (s *chatSession) nameChat(arg string) {
	name := arg
	if name == "" {
		v, err := s.scr.Input(ui.Input{Title: "Name this chat", Value: s.sess.Name, Placeholder: "empty to clear the name"})
		if err != nil {
			return
		}
		name = v
	}
	s.setName(s.sess, name)
}

// setName names sess (a chat in the /resume list, or this one) and says so.
func (s *chatSession) setName(sess *session.Session, name string) {
	name = strings.Join(strings.Fields(display.StripControls(name)), " ")
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	if err := sess.SetName(name); err != nil {
		s.fail("Could not name the chat: " + err.Error())
		return
	}
	if sess.Path == s.sess.Path {
		s.sess.Name = name
	}
	if name == "" {
		s.note("The chat has no name now.")
		return
	}
	s.scr.Add(display.Done("Named the chat “" + display.Safe(name) + "”"))
}

// renameSession asks for a new name for a chat of the /resume list.
func (s *chatSession) renameSession(sess *session.Session) {
	v, err := s.scr.Input(ui.Input{Title: "Rename " + display.Safe(sess.Title()), Value: sess.Name, Placeholder: "empty to clear the name"})
	if err == nil {
		s.setName(sess, v)
	}
}

// deleteSession deletes a chat of the /resume list, once confirmed; not
// the one going on.
func (s *chatSession) deleteSession(sess *session.Session) {
	if sess.Path == s.sess.Path {
		s.fail("That is this chat; start another (/new) to delete it.")
		return
	}
	ok, err := ui.ConfirmWith(s.scr, "Delete “"+display.Safe(sess.Title())+"”?", false)
	if err != nil || !ok {
		return
	}
	if err := sess.Delete(); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail("Could not delete the chat: " + err.Error())
		return
	}
	s.scr.Add(display.Done("Deleted “" + display.Safe(sess.Title()) + "”"))
}
