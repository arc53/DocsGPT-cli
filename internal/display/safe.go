package display

import (
	"fmt"
	"strings"
	"unicode"
)

// Safe makes text the model or a server controls (a command, a path, a
// source title) safe to print: control characters become visible symbols
// (␛ for ESC, ␍ for CR, ␊ for a newline), other invisible format runes
// (bidi overrides, zero-width) are shown as \uXXXX and invalid UTF-8 as �,
// so nothing can move the cursor, recolour or hide part of what the user
// is asked to approve. Tabs become spaces.
func Safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20:
			b.WriteRune(0x2400 + r)
		case r == 0x7f:
			b.WriteRune('␡')
		case unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r) // invalid UTF-8 arrives as utf8.RuneError (�)
		}
	}
	return b.String()
}

// StripControls removes terminal control sequences from text meant to be
// read as text (an answer, a saved chat): escape sequences whole, so no
// "[31m" is left behind, and every other control character but newline and
// tab. Unlike Safe it keeps the text as it reads, line breaks included.
func StripControls(s string) string {
	var f controlFilter
	return f.clean(s)
}

// controlFilter is StripControls for text that arrives in pieces: a
// sequence split across pieces is still removed whole. ESC sequences
// (CSI, OSC, DCS, …) and their C1 forms go; a string sequence (OSC, DCS,
// SOS, PM, APC) lasts until BEL or ST, as on a terminal.
type controlFilter struct{ state uint8 }

const (
	inText      = iota
	inEsc       // after ESC
	inEscInter  // after ESC and intermediate bytes, until the final byte
	inCSI       // control sequence, until its final byte
	inString    // OSC, DCS, SOS, PM, APC, until BEL or ST
	inStringEsc // ESC inside a string: ESC \ is ST
)

func (f *controlFilter) clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		f.step(r, &b)
	}
	return b.String()
}

func (f *controlFilter) step(r rune, b *strings.Builder) {
	switch f.state {
	case inEsc:
		switch {
		case r == '[':
			f.state = inCSI
		case r == ']' || r == 'P' || r == 'X' || r == '^' || r == '_':
			f.state = inString
		case r >= 0x20 && r <= 0x2f:
			f.state = inEscInter
		default:
			f.state = inText
		}
		return
	case inEscInter:
		if r < 0x20 || r > 0x2f {
			f.state = inText
		}
		return
	case inCSI:
		if r >= 0x20 && r <= 0x3f {
			return
		}
		f.state = inText
		if r >= 0x40 && r <= 0x7e {
			return
		}
	case inString:
		switch r {
		case 0x07, 0x9c, 0x18, 0x1a:
			f.state = inText
		case 0x1b:
			f.state = inStringEsc
		}
		return
	case inStringEsc: // ESC \ ends the string, any other ESC aborts it
		f.state = inEsc
		if r == '\\' {
			f.state = inText
		} else {
			f.step(r, b)
		}
		return
	}
	switch {
	case r == 0x1b:
		f.state = inEsc
	case r == 0x9b:
		f.state = inCSI
	case r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f:
		f.state = inString
	case r == '\n' || r == '\t' || r >= 0x20 && r < 0x7f || r >= 0xa0:
		b.WriteRune(r)
	}
}
