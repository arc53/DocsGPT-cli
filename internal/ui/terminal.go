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

// tabClear is the session status that clears the tab's subtitle and dot.
const tabClear = "status=;indicator=;status-color="

// TabStatus shows text under the tab's title, and a dot of color
// (#rrggbb) on the tab, while the screen runs: iTerm2's session status
// (OSC 21337), which other terminals ignore. "" clears it; it is cleared
// when the screen ends and while it is suspended. Inside tmux or screen,
// which drop the sequence, and on the Linux console it is only sent when
// force: through tmux's passthrough then (allow-passthrough on; screen
// still drops it).
func (s *Screen) TabStatus(text, color string, force bool) {
	s.do(func(m *screenModel) tea.Cmd {
		m.tab = ""
		if text = cleanNote(text); text != "" && (force || tabStatusHere()) {
			m.tab = "status=" + text + ";indicator=" + color + ";status-color=" + color
		}
		m.retab()
		return nil
	})
}

// retab writes the tab status asked for when the terminal shows another.
func (m *screenModel) retab() {
	if m.out == nil || m.tab == m.tabShown {
		return
	}
	body := m.tab
	if body == "" {
		body = tabClear
	}
	m.out.WriteString(tabStatusSeq(body))
	m.tabShown = m.tab
}

// ClearTabStatus clears a tab status left by an earlier chat that could
// not clear it (killed, say), before this one shows any: written once,
// where TabStatus with force would send it.
func (s *Screen) ClearTabStatus(force bool) {
	s.do(func(m *screenModel) tea.Cmd {
		if m.out != nil && m.tab == "" && m.tabShown == "" && (force || tabStatusHere()) {
			m.out.WriteString(tabStatusSeq(tabClear))
		}
		return nil
	})
}

// untab clears the tab status the terminal shows (the screen is handed
// back or gone); retab shows it again.
func (m *screenModel) untab() {
	if m.tabShown != "" {
		m.out.WriteString(tabStatusSeq(tabClear))
		m.tabShown = ""
	}
}

// tabStatusHere reports whether the tab status reaches the terminal
// without being asked to: not through tmux or screen, which drop it, nor
// on the Linux console, which knows no such sequence.
func tabStatusHere() bool {
	return os.Getenv("TMUX") == "" && os.Getenv("STY") == "" && os.Getenv("TERM") != "linux"
}

// tabStatusSeq is the sequence that sets the tab status to body, wrapped
// for tmux's passthrough inside tmux.
func tabStatusSeq(body string) string {
	seq := "\x1b]21337;" + body + "\a"
	if os.Getenv("TMUX") != "" {
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
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
