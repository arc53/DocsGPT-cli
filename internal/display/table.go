package display

import (
	"regexp"
	"slices"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
	east "github.com/yuin/goldmark-emoji/ast"
	"github.com/yuin/goldmark-emoji/definition"
	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Tables are laid out like pi's: a box drawn in the dim tone, the header
// bold, a rule between rows, and each column as wide as its widest cell
// while the table fits; when it does not, the columns shrink towards their
// longest word (at most maxWordWidth) and the cells wrap at spaces. Too
// narrow for even a character per column, the table stays raw markdown.

// maxWordWidth caps the word a column is kept wide enough for: a longer
// one (a URL, a hash) is cut rather than widen its column.
const maxWordWidth = 30

// tableCell is a cell rendered on one line, and how it aligns.
type tableCell struct {
	text  string
	align extast.Alignment
}

// renderTable lays out md, one GFM table and nothing else, at width. ok is
// false when md is not that (the caller renders it as prose then).
func (m *markdown) renderTable(md string, width int) (out string, ok bool) {
	src := []byte(md)
	var refs []linkRef
	pc := parser.NewContext()
	pc.Set(linksKey, &refs)
	doc := m.prose.Parser().Parse(text.NewReader(src), parser.WithContext(pc))
	table, ok := doc.FirstChild().(*extast.Table)
	if !ok || table.NextSibling() != nil || table.FirstChild() == nil {
		return "", false
	}

	var rows [][]tableCell
	next := 0 // the first of refs the next cell's links take
	for r := table.FirstChild(); r != nil; r = r.NextSibling() {
		var row []tableCell
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cell, _ := c.(*extast.TableCell)
			if cell == nil {
				continue
			}
			row = append(row, tableCell{m.renderCell(cell, src, refs, &next, r.Kind() == extast.KindTableHeader), cell.Alignment})
		}
		rows = append(rows, row)
	}
	cols := len(rows[0])
	if cols == 0 {
		return "", false
	}
	for i, row := range rows { // the header sets the columns
		for len(row) < cols {
			row = append(row, tableCell{})
		}
		rows[i] = row[:cols]
	}

	// "│ " + cells joined by " │ " + " │"
	overhead := 3*cols + 1
	widths := columnWidths(rows, width-overhead)
	if widths == nil {
		return rawTable(md, width), true
	}

	var lines []string
	rule := func(left, mid, right string) {
		segs := make([]string, cols)
		for i, w := range widths {
			segs[i] = strings.Repeat("─", w)
		}
		lines = append(lines, T.Dim.Render(left+"─"+strings.Join(segs, "─"+mid+"─")+"─"+right))
	}
	bar := T.Dim.Render("│")
	rule("┌", "┬", "┐")
	for i, row := range rows {
		if i > 0 {
			rule("├", "┼", "┤")
		}
		wrapped := make([][]string, cols)
		height := 1
		for c, cell := range row {
			wrapped[c] = wrapCell(cell.text, widths[c])
			height = max(height, len(wrapped[c]))
		}
		for k := range height {
			parts := make([]string, cols)
			for c := range row {
				var s string
				if k < len(wrapped[c]) {
					s = wrapped[c][k]
				}
				parts[c] = align(s, widths[c], row[c].align)
			}
			lines = append(lines, bar+" "+strings.Join(parts, " "+bar+" ")+" "+bar)
		}
	}
	rule("└", "┴", "┘")
	return strings.Join(lines, "\n"), true
}

// cellBreak stands for a <br> in a cell while it renders: a noncharacter,
// like the link markers.
const cellBreak = '\uFDD2'

// lineBreak reports whether n is a <br> tag, which models put in cells for
// a line break that markdown tables have no other way to write.
func lineBreak(n ast.Node, src []byte) bool {
	h, ok := n.(*ast.RawHTML)
	if !ok {
		return false
	}
	var tag []byte
	for i := 0; i < h.Segments.Len(); i++ {
		seg := h.Segments.At(i)
		tag = append(tag, seg.Value(src)...)
	}
	return brTag.Match(tag)
}

var brTag = regexp.MustCompile(`(?i)^<br\s*/?>$`)

// renderCell renders the inline markdown of cell on one line (a <br> starts
// another), its links
// placed (the cell's markers take refs from *next on), bold in the header.
func (m *markdown) renderCell(cell *extast.TableCell, src []byte, refs []linkRef, next *int, header bool) string {
	doc := ast.NewDocument()
	p := ast.NewParagraph()
	doc.AppendChild(doc, p)
	var into ast.Node = p
	if header && !Colorless() { // plain, glamour would write **text**
		strong := ast.NewEmphasis(2)
		p.AppendChild(p, strong)
		into = strong
	}
	for n := cell.FirstChild(); n != nil; {
		after := n.NextSibling()
		if lineBreak(n, src) { // glamour drops raw HTML
			n = east.NewEmoji(nil, &definition.Emoji{Unicode: []rune{cellBreak}})
		}
		into.AppendChild(into, n)
		n = after
	}
	var b strings.Builder
	if err := m.prose.Renderer().Render(&b, src, doc); err != nil {
		return Safe(plainText(cell, src))
	}
	out := strings.Join(strings.Split(tidy(b.String()), "\n"), " ")
	out = strings.ReplaceAll(out, string(cellBreak), "\n")
	n := strings.Count(out, string(linkStart))
	from := min(*next, len(refs))
	*next += n
	return placeLinks(out, refs[from:min(*next, len(refs))])
}

// columnWidths shares avail columns out among the table's columns, as pi
// does: each as wide as its widest cell when they all fit; else each at
// least as wide as its longest word (capped at maxWordWidth; when even
// those do not fit, each as wide as its widest character and the rest
// shared by how much wider those words are), and what is left shared by
// how much wider each would like to be. Nil when even the characters do
// not fit.
func columnWidths(rows [][]tableCell, avail int) []int {
	cols := len(rows[0])
	natural, words, floor := make([]int, cols), make([]int, cols), make([]int, cols)
	for _, row := range rows {
		for c, cell := range row {
			for _, line := range strings.Split(cell.text, "\n") {
				natural[c] = max(natural[c], xansi.StringWidth(line))
			}
			words[c] = max(words[c], 1, longestWord(cell.text))
			floor[c] = max(floor[c], 1, widestGrapheme(cell.text))
		}
	}
	sum := func(s []int) (n int) {
		for _, v := range s {
			n += v
		}
		return n
	}
	if sum(floor) > avail {
		return nil
	}

	least := words
	if sum(words) > avail {
		least = slices.Clone(floor)
		spare, weight := avail-sum(floor), 0
		for c, w := range words {
			weight += w - floor[c]
		}
		for c, w := range words {
			if weight > 0 {
				least[c] += (w - floor[c]) * spare / weight
			}
		}
		for c := 0; sum(least) < avail && c < cols; c++ {
			least[c]++
		}
	}

	widths := make([]int, cols)
	if sum(natural) <= avail {
		for c := range widths {
			widths[c] = max(natural[c], least[c])
		}
		return widths
	}
	grow, extra := 0, max(0, avail-sum(least))
	for c := range natural {
		grow += max(0, natural[c]-least[c])
	}
	for c := range widths {
		widths[c] = least[c]
		if grow > 0 {
			widths[c] += max(0, natural[c]-least[c]) * extra / grow
		}
	}
	for left := avail - sum(widths); left > 0; {
		grew := false
		for c := 0; c < cols && left > 0; c++ {
			if widths[c] < natural[c] {
				widths[c]++
				left--
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	return widths
}

// longestWord is the width of the widest space-separated word of the
// styled text s, at most maxWordWidth.
func longestWord(s string) int {
	n := 0
	for _, w := range strings.Fields(xansi.Strip(s)) {
		n = max(n, xansi.StringWidth(w))
	}
	return min(n, maxWordWidth)
}

// widestGrapheme is the width of the widest character of the styled text
// s: 2 for a CJK character or an emoji.
func widestGrapheme(s string) int {
	n, state := 0, -1
	for rest := xansi.Strip(s); rest != ""; {
		var w int
		_, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
		n = max(n, w)
	}
	return n
}

// wrapCell breaks a cell's styled text into rows at most width wide, at
// its line breaks and spaces, each row closing the styles and links it
// opens.
func wrapCell(s string, width int) []string {
	var rows []string
	for _, line := range strings.Split(s, "\n") {
		rows = append(rows, wrapWords(line, width)...)
	}
	for i, r := range rows {
		r = relink(r)
		if strings.Contains(r, "\x1b[") && !strings.HasSuffix(r, "\x1b[0m") {
			r += "\x1b[0m"
		}
		rows[i] = r
	}
	return rows
}

// align pads s to width: left unless the column says otherwise.
func align(s string, width int, a extast.Alignment) string {
	gap := max(0, width-xansi.StringWidth(s))
	switch a {
	case extast.AlignRight:
		return strings.Repeat(" ", gap) + s
	case extast.AlignCenter:
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}

// rawTable is a table too narrow to draw: its markdown, wrapped at width.
func rawTable(md string, width int) string {
	var rows []string
	for _, line := range strings.Split(strings.TrimRight(md, "\n"), "\n") {
		rows = append(rows, wrapWords(strings.TrimRight(line, " \t"), width)...)
	}
	return strings.Join(rows, "\n")
}
