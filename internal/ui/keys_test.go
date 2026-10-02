package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTranslateKeys(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"\x1b[13;2u", "\n"},                   // kitty Shift+Enter
		{"\x1b[27;2;13~", "\n"},                // xterm / tmux Shift+Enter
		{"\x1b[13;6u", "\n"},                   // Ctrl+Shift+Enter
		{"\x1b[13;3u", "\x1b\r"},               // Alt+Enter
		{"\x1b[13;5u", "\r"},                   // Ctrl+Enter
		{"\x1b[57414u", "\r"},                  // keypad Enter
		{"\x1b[99;5u", "\x03"},                 // Ctrl+C
		{"\x1b[100;5u", "\x04"},                // Ctrl+D
		{"\x1b[106;5u", "\n"},                  // Ctrl+J
		{"\x1b[99;69u", "\x03"},                // Ctrl+C with Caps Lock
		{"\x1b[27u", "\x1b"},                   // Esc
		{"\x1b[98;3u", "\x1bb"},                // Alt+B
		{"\x1b[98;4u", "\x1bB"},                // Alt+Shift+B
		{"\x1b[127;3u", "\x1b\x7f"},            // Alt+Backspace
		{"\x1b[9;2u", "\x1b[Z"},                // Shift+Tab
		{"\x1b[32;5u", "\x00"},                 // Ctrl+Space
		{"\x1b[97;9u", ""},                     // Super+A
		{"\x1b[57399u", "0"},                   // keypad 0
		{"\x1b[57441u", ""},                    // a modifier key alone
		{"\x1b[?1u", ""},                       // a flags reply
		{"a\x1b[13;2ub", "a\nb"},               // among text
		{"\x1b[1;5A\x1b[A", "\x1b[1;5A\x1b[A"}, // arrows untouched
		{"\x1b[3~", "\x1b[3~"},                 // Delete untouched
		{"\x1b[24;1R", "\x1b[24;1R"},           // no cursor reply expected
		{"\x1b[200~\x1b[13;2u\x1b[201~\x1b[13;2u", "\x1b[200~\x1b[13;2u\x1b[201~\n"}, // pasted text stays
	} {
		in := &ttyInput{}
		out, held := in.translate([]byte(c.in))
		if string(out) != c.want || held != nil {
			t.Errorf("%q → %q (held %q), want %q", c.in, out, held, c.want)
		}
	}
}

func TestTranslateSplitReads(t *testing.T) {
	in := &ttyInput{}
	out, held := in.translate([]byte("x\x1b[13;"))
	if string(out) != "x" || string(held) != "\x1b[13;" {
		t.Fatalf("first half: %q, held %q", out, held)
	}
	if out, held = in.translate(append(held, "2u"...)); string(out) != "\n" || held != nil {
		t.Fatalf("second half: %q, held %q", out, held)
	}
	// A lone Esc or Alt+[ is not held.
	if out, held = in.translate([]byte("\x1b[")); string(out) != "\x1b[" || held != nil {
		t.Fatalf("alt+[: %q, held %q", out, held)
	}
	// The end of a paste split across reads ends it.
	in.translate([]byte("\x1b[200~text\x1b[2"))
	if !in.paste {
		t.Fatal("not in the paste")
	}
	out, held = in.translate([]byte("text\x1b[20"))
	if string(out) != "text" || string(held) != "\x1b[20" {
		t.Fatalf("paste end held: %q, %q", out, held)
	}
	if out, _ = in.translate(append(held, "1~\x1b[13;2u"...)); string(out) != "\x1b[201~\n" || in.paste {
		t.Fatalf("after the paste: %q, paste %v", out, in.paste)
	}
}

func TestTranslateCursorReply(t *testing.T) {
	var got []tea.Msg
	in := &ttyInput{send: func(m tea.Msg) { got = append(got, m) }}
	in.cursor.Store(true)
	out, _ := in.translate([]byte("hi\x1b[12;1R\x1b[3;4R"))
	if string(out) != "hi\x1b[3;4R" || len(got) != 1 || got[0] != (cursorMsg{12}) {
		t.Fatalf("out %q, msgs %v", out, got)
	}
}
