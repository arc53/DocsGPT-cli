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
