package ui

import (
	"fmt"
	"time"

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

// SetInput puts text in the editor in place of what is there.
func (s *Screen) SetInput(text string) {
	s.do(func(m *screenModel) tea.Cmd {
		m.ed.setText(text)
		m.ed.pastes = map[int]string{}
		m.ed.changed()
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
	if m.waiter != nil {
		m.waiter <- [2]string{m.escCmd, m.escCmd}
		m.waiter = nil
	} else {
		m.queue = append(m.queue, [2]string{m.escCmd, m.escCmd})
	}
	return true
}
