package ui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Shift+Enter sends the same byte as Enter unless the terminal is asked to
// report modified keys. While the Screen runs it asks in both ways there
// are (the kitty keyboard protocol's "disambiguate" flag, and xterm's
// modifyOtherKeys, which tmux forwards with extended-keys on); a terminal
// that knows neither ignores both. ttyInput turns the replies back into the
// legacy bytes bubbletea reads, Shift+Enter becoming Ctrl+J (a new line).
const (
	keysOn  = "\x1b[>1u\x1b[>4;1m"
	keysOff = "\x1b[<u\x1b[>4;0m"
)

var pasteEnd = []byte("\x1b[201~")

// NewlineKey names the key for a new line in the editor: shift+enter, or
// ctrl+j where Shift+Enter cannot be told from Enter (Terminal.app, tmux
// without extended-keys, Windows).
func NewlineKey() string {
	if runtime.GOOS == "windows" || os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return "ctrl+j"
	}
	if os.Getenv("TMUX") != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "tmux", "show", "-gv", "extended-keys").Output(); err == nil && strings.TrimSpace(string(out)) == "off" {
			return "ctrl+j"
		}
	}
	return "shift+enter"
}

// ttyInput is stdin for the Screen, its modified keys translated. It keeps
// stdin's Fd and Name, so bubbletea still switches the terminal to raw mode
// and can cancel a read.
type ttyInput struct {
	*os.File
	held  []byte // an unfinished sequence, completed by the next read
	paste bool   // inside a bracketed paste, passed on as it is
	// blurred takes the terminal's focus reports (CSI I, CSI O), which
	// go no further: bubbletea knows one only when a read holds nothing
	// else.
	blurred *atomic.Bool
}

// newTTYInput returns nil on Windows, where bubbletea reads console events.
func newTTYInput() *ttyInput {
	if runtime.GOOS == "windows" {
		return nil
	}
	return &ttyInput{File: os.Stdin}
}

// Read never returns more than it read: every translation is shorter than
// its sequence.
func (t *ttyInput) Read(p []byte) (int, error) {
	n, err := t.File.Read(p[:max(1, len(p)-len(t.held))])
	data := append(append([]byte(nil), t.held...), p[:n]...)
	if err != nil {
		t.held = nil
		return copy(p, data), err
	}
	out, held := t.translate(data)
	t.held = held
	return copy(p, out), nil
}

// translate rewrites the key sequences of b and returns the rest of an
// unfinished one to hold for the next read.
func (t *ttyInput) translate(b []byte) (out, held []byte) {
	for i := 0; i < len(b); {
		if t.paste {
			j := bytes.Index(b[i:], pasteEnd)
			if j < 0 {
				// Hold a start of the end marker.
				for k := min(len(pasteEnd)-1, len(b)-i); k > 0; k-- {
					if bytes.HasSuffix(b, pasteEnd[:k]) {
						return append(out, b[i:len(b)-k]...), b[len(b)-k:]
					}
				}
				return append(out, b[i:]...), nil
			}
			end := i + j + len(pasteEnd)
			out, i, t.paste = append(out, b[i:end]...), end, false
			continue
		}
		if b[i] != '\x1b' || i+1 >= len(b) || b[i+1] != '[' {
			out = append(out, b[i])
			i++
			continue
		}
		j := i + 2
		for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3f {
			j++
		}
		params := string(b[i+2 : min(j, len(b))])
		if j == len(b) {
			// No final byte yet: a key or mouse report split across reads.
			if params != "" && len(params) < 32 && strings.Trim(strings.TrimPrefix(params, "<"), "0123456789;:") == "" {
				return out, b[i:]
			}
			return append(out, b[i:]...), nil
		}
		seq, final := b[i:j+1], b[j]
		i = j + 1
		switch {
		case final == '~' && params == "200":
			t.paste = true
			out = append(out, seq...)
		case final == 'u':
			out = append(out, legacyKey(params, false)...)
		case final == '~' && strings.HasPrefix(params, "27;"):
			out = append(out, legacyKey(params, true)...)
		case params == "" && (final == 'I' || final == 'O') && t.blurred != nil:
			t.blurred.Store(final == 'O')
		default:
			out = append(out, seq...)
		}
	}
	return out, nil
}

// legacyKey returns the bytes a terminal sends for a key without the
// enhancements, from the parameters of a kitty "CSI code;mods u" or an
// xterm "CSI 27;mods;code ~" sequence; nothing for keys the editor has no
// use for (with Super or Hyper, keypad and media keys).
func legacyKey(params string, xterm bool) []byte {
	f := strings.Split(params, ";")
	code, mods := f[0], ""
	if xterm {
		if len(f) != 3 {
			return nil
		}
		code, mods = f[2], f[1]
	} else if len(f) > 1 {
		mods = f[1]
	}
	code, _, _ = strings.Cut(code, ":") // alternate keys
	mods, _, _ = strings.Cut(mods, ":") // event type
	c, err := strconv.Atoi(code)
	if err != nil {
		return nil
	}
	m := 1
	if mods != "" {
		if m, err = strconv.Atoi(mods); err != nil || m < 1 {
			return nil
		}
	}
	m = (m - 1) &^ (64 | 128) // Caps Lock, Num Lock
	if m&^7 != 0 {
		return nil
	}
	shift, alt, ctrl := m&1 != 0, m&2 != 0, m&4 != 0

	var s string
	switch {
	case c == 13 || c == 57414: // Enter, keypad Enter
		switch {
		case shift:
			return []byte("\n")
		case alt:
			return []byte("\x1b\r")
		}
		return []byte("\r")
	case c == 9:
		if shift {
			return []byte("\x1b[Z")
		}
		s = "\t"
	case c == 127:
		s = "\x7f"
		if ctrl {
			s = "\x08"
		}
	case c == 27:
		s = "\x1b"
	case c == 32:
		s = " "
		if ctrl {
			s = "\x00"
		}
	case c >= 57399 && c <= 57416: // keypad 0-9 . / * - + Enter = ,
		s = string("0123456789./*-+\r=,"[c-57399])
	case c < 32 || c >= 57344 && c <= 63743 || c > 0x10ffff:
		return nil
	case ctrl && (c >= 'a' && c <= 'z' || strings.ContainsRune("@[\\]^_", rune(c))):
		s = string(rune(c & 0x1f))
	case ctrl && c == '?':
		s = "\x7f"
	case ctrl && (c == '-' || c == '/'): // 0x1f, as a terminal sends them (undo)
		s = "\x1f"
	case shift && c >= 'a' && c <= 'z':
		s = string(rune(c - 32))
	default:
		s = string(rune(c))
	}
	if alt {
		s = "\x1b" + s
	}
	return []byte(s)
}
