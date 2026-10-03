package ui

import (
	"os"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// The terminal's title is saved on its stack before the chat sets one, and
// comes back from it when the chat ends (xterm's window operations 22 and
// 23; terminals without a stack ignore both).
const (
	titlePush = "\x1b[22;0t"
	titlePop  = "\x1b[23;0t"
	titleMax  = 80 // characters
)

// Title sets the terminal's (tab) title while the screen runs; the one
// before comes back when it ends. Control characters are dropped.
func (s *Screen) Title(title string) {
	s.do(func(m *screenModel) tea.Cmd {
		if title = cleanTitle(title); title != m.title {
			m.title = title
			m.retitle(!m.titled)
		}
		return nil
	})
}

// retitle writes the title, saving the terminal's own first when push.
func (m *screenModel) retitle(push bool) {
	if m.out == nil || m.title == "" {
		return
	}
	seq := "\x1b]0;" + m.title + "\a"
	if push {
		seq, m.titled = titlePush+seq, true
	}
	m.out.WriteString(seq)
}

// cleanTitle keeps a title to one line of printable characters, cut to
// titleMax.
func cleanTitle(s string) string {
	s = strings.Join(strings.FieldsFunc(strings.ToValidUTF8(s, ""), func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }), " ")
	if r := []rune(s); len(r) > titleMax {
		s = strings.TrimSpace(string(r[:titleMax-1])) + "…"
	}
	return s
}

// Notify tells the user something waits for them (an answer, an approval)
// when the terminal has reported losing focus: a desktop notification
// where the terminal posts one, else the bell. Without focus reports
// (focus-events off in tmux, say) it never fires.
func (s *Screen) Notify(title, body string) {
	s.do(func(m *screenModel) tea.Cmd {
		if m.out != nil && m.blurred.Load() {
			m.out.WriteString(notification(title, body))
		}
		return nil
	})
}

// notification is the sequence that posts a notification on the terminal
// at hand: OSC 9 for iTerm2, OSC 99 for kitty, OSC 777 for the terminals
// known to take it, and the bell elsewhere (tmux passes it on, Terminal.app
// bounces the Dock icon).
func notification(title, body string) string {
	title, body = cleanNote(title), cleanNote(body)
	prog, term := os.Getenv("TERM_PROGRAM"), os.Getenv("TERM")
	switch {
	case os.Getenv("TMUX") != "" || os.Getenv("STY") != "":
		return "\a"
	case prog == "iTerm.app":
		return "\x1b]9;" + title + ": " + body + "\a"
	case os.Getenv("KITTY_WINDOW_ID") != "" || term == "xterm-kitty":
		return "\x1b]99;i=1:d=0;" + title + "\x1b\\\x1b]99;i=1:p=body;" + body + "\x1b\\"
	case prog == "ghostty", prog == "WezTerm", strings.HasPrefix(term, "foot"), strings.HasPrefix(term, "rxvt"), os.Getenv("VTE_VERSION") != "":
		return "\x1b]777;notify;" + title + ";" + body + "\a"
	}
	return "\a"
}

// cleanNote keeps a notification's text printable and free of the ";"
// OSC 777 separates its fields with.
func cleanNote(s string) string {
	return strings.ReplaceAll(cleanTitle(s), ";", ",")
}
