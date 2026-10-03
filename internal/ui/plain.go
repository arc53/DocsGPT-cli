package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// Plain tells how a line of a Block copies as text.
type Plain struct {
	Indent int    // columns of decoration it starts with (padding, a quote's bar), left out
	Wrap   bool   // it goes on from the line above, wrapped for the width
	Sep    string // what the wrap took out between the two (a space)
}

// Plainer is a Block whose lines do not copy as they show: Plain returns
// one Plain per line of Lines(width) (none: as shown).
type Plainer interface{ Plain(width int) []Plain }

// endCol is past the end of any line.
const endCol = 1 << 30

// Unwrap returns the text of lines from column a of the first to column b
// (not included) of the last as it copies: plain, the lines wrapped for the
// width joined again (see Plain), no spaces at their ends.
func Unwrap(lines []string, plain []Plain, a, b int) string {
	var out []string
	var cur string
	for k, l := range lines {
		from, to := 0, endCol
		if k == 0 {
			from = a
		}
		if k == len(lines)-1 {
			to = b
		}
		p := Plain{}
		if k < len(plain) {
			p = plain[k]
		}
		piece := strings.TrimRight(cells(ansi.Strip(l), max(from, p.Indent), to), " ")
		if k > 0 && p.Wrap {
			cur += p.Sep + piece
			continue
		}
		if k > 0 {
			out = append(out, cur)
		}
		cur = piece
	}
	out = append(out, cur)
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// cells returns the characters of plain on columns a to b (b not included),
// with the whole of a wide one that is cut.
func cells(plain string, a, b int) string {
	var out strings.Builder
	col := 0
	g := uniseg.NewGraphemes(plain)
	for g.Next() && col < b {
		w := g.Width()
		if col+w > a || w == 0 && col >= a {
			out.WriteString(g.Str())
		}
		col += w
	}
	return out.String()
}
