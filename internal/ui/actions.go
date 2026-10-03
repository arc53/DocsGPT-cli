package ui

import (
	"fmt"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/attach"

	tea "github.com/charmbracelet/bubbletea"
)

// escWindow is how soon a second Esc makes a pair (see ScreenOptions.EscEsc).
const escWindow = 500 * time.Millisecond

// SelectAction is Select for a list with Actions: it also returns the
// action key that chose the item, "" for Enter.
func (s *Screen) SelectAction(sel Select) (value, action string, err error) {
	if len(sel.Items) == 0 {
		return "", "", fmt.Errorf("ui: select has no items")
	}
	r := s.answer(&panel{sel: newSelectModel(sel)})
	return r.value, r.action, r.err
}

// SetInput puts text in the editor in place of what is there, with the
// files its markers stand for.
func (s *Screen) SetInput(text string, files ...attach.File) {
	s.do(func(m *screenModel) tea.Cmd {
		m.ed.setText(text)
		m.ed.pastes, m.ed.files = map[int]string{}, map[int]attach.File{}
		for _, f := range files {
			m.ed.files[f.ID] = f
		}
		m.ed.changed()
		return nil
	})
}

// PutBack puts a message that was not sent back into the editor, before
// what it holds, as Alt+↑ does with queued ones.
func (s *Screen) PutBack(msg Message) {
	s.do(func(m *screenModel) tea.Cmd {
		m.queue = append([]queued{{Message: msg}}, m.queue...)
		m.requeue()
		return nil
	})
}

// escEsc sends the EscEsc command for the second of two Esc keys on an
// empty input while nothing runs, and reports whether it took the key (the
// first Esc does nothing there anyway). Two Escs read at once arrive as
// alt+esc.
func (m *screenModel) escEsc(key string) bool {
	second := time.Since(m.escAt) < escWindow
	m.escAt = time.Time{}
	if m.escCmd == "" || key != "esc" && key != "alt+esc" || m.cancel != nil || !m.ed.empty() || m.ed.popup != nil {
		return false
	}
	if key == "esc" && !second {
		m.escAt = time.Now()
		return true
	}
	m.follow = true
	msg := Message{Text: m.escCmd, Shown: m.escCmd}
	if m.waiter != nil {
		m.waiter <- msg
		m.waiter = nil
	} else {
		m.queue = append(m.queue, queued{Message: msg})
	}
	return true
}
