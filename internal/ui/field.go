package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// field is a single-line editor shared by Input and the Select filter.
type field struct {
	text []rune
	pos  int
}

func (f *field) set(s string) { f.text, f.pos = []rune(s), len([]rune(s)) }

func (f *field) String() string { return string(f.text) }

// update applies an editing key and reports whether the text changed.
func (f *field) update(k tea.KeyMsg) bool {
	before := string(f.text)
	switch k.String() {
	case "left", "ctrl+b":
		f.pos = max(0, f.pos-1)
	case "right", "ctrl+f":
		f.pos = min(len(f.text), f.pos+1)
	case "alt+left", "alt+b", "ctrl+left":
		f.pos = f.wordStart()
	case "alt+right", "alt+f", "ctrl+right":
		f.pos = f.wordEnd()
	case "home", "ctrl+a":
		f.pos = 0
	case "end", "ctrl+e":
		f.pos = len(f.text)
	case "backspace", "ctrl+h":
		if f.pos > 0 {
			f.text = append(f.text[:f.pos-1], f.text[f.pos:]...)
			f.pos--
		}
	case "delete", "ctrl+d":
		if f.pos < len(f.text) {
			f.text = append(f.text[:f.pos], f.text[f.pos+1:]...)
		}
	case "ctrl+u":
		f.text, f.pos = f.text[f.pos:], 0
	case "ctrl+k":
		f.text = f.text[:f.pos]
	case "ctrl+w", "alt+backspace":
		start := f.wordStart()
		f.text, f.pos = append(f.text[:start], f.text[f.pos:]...), start
	default:
		if k.Type != tea.KeyRunes && k.Type != tea.KeySpace {
			return false
		}
		var ins []rune
		for _, r := range k.Runes {
			if r == '\t' {
				r = ' '
			}
			if unicode.IsPrint(r) {
				ins = append(ins, r)
			}
		}
		f.text = append(f.text[:f.pos], append(ins, f.text[f.pos:]...)...)
		f.pos += len(ins)
	}
	return string(f.text) != before
}

func (f *field) wordStart() int {
	i := f.pos
	for i > 0 && unicode.IsSpace(f.text[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(f.text[i-1]) {
		i--
	}
	return i
}

func (f *field) wordEnd() int {
	i := f.pos
	for i < len(f.text) && unicode.IsSpace(f.text[i]) {
		i++
	}
	for i < len(f.text) && !unicode.IsSpace(f.text[i]) {
		i++
	}
	return i
}

// view renders the text in width columns with a block cursor, scrolling
// horizontally to keep the cursor visible.
func (f *field) view(width int, mask bool, placeholder string) string {
	width = max(1, width)
	if len(f.text) == 0 {
		ph := []rune(ansi.Truncate(placeholder, width-1, ""))
		if len(ph) == 0 {
			return reverse(" ")
		}
		return reverse(fg(Colors.Dim).Render(string(ph[:1]))) + fg(Colors.Dim).Render(string(ph[1:]))
	}
	shown := f.text
	if mask {
		shown = []rune(strings.Repeat("•", len(f.text)))
	}
	// Columns needed up to and including the cursor cell.
	start, used := f.pos, 1
	for start > 0 && used+ansi.StringWidth(string(shown[start-1])) <= width {
		start--
		used += ansi.StringWidth(string(shown[start]))
	}
	var b strings.Builder
	cols := 0
	for i := start; i <= len(shown); i++ {
		c := " "
		if i < len(shown) {
			c = string(shown[i])
		}
		w := ansi.StringWidth(c)
		if cols+w > width {
			break
		}
		cols += w
		if i == f.pos {
			b.WriteString(reverse(c))
		} else if i < len(shown) {
			b.WriteString(c)
		}
	}
	return b.String()
}
