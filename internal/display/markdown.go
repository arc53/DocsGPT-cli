package display

import (
	"regexp"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdown renders markdown for one width. Glamour wraps text badly (its
// word wrapper miscounts hyphens and a second pass re-wraps the overlong
// rows, so words and punctuation end up alone on a row, quote rows lose
// their bar), so prose is rendered unwrapped and rewrap wraps it; tables
// are laid out by glamour at the width.
type markdown struct {
	prose  goldmark.Markdown
	tables *glamour.TermRenderer
}

// newMarkdown returns a markdown renderer for the active theme at width.
// Nil when glamour refuses the options.
func newMarkdown(width int) *markdown {
	style := markdownStyle(width)
	tables, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.ColorProfile()),
	)
	if err != nil {
		return nil
	}
	// As glamour.NewTermRenderer, plus layout and without wrapping.
	prose := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithASTTransformers(util.Prioritized(layout{}, 0))),
	)
	prose.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(
		ansi.NewRenderer(ansi.Options{ColorProfile: termenv.ColorProfile(), Styles: style}), 1000))))
	return &markdown{prose: prose, tables: tables}
}

// layout prepares the tree for rendering without wrapping: soft line breaks
// become spaces (glamour keeps them as line ends then), the paragraphs of a
// list item go on lines of their own (glamour runs them together) and the
// items of a loose list are a blank line apart, as when they stream in.
type layout struct{}

func (layout) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source() // the renderer reads the text from it too
	breakAfter := func(n ast.Node) {
		if k := n.Kind(); k == ast.KindParagraph || k == ast.KindTextBlock {
			br := ast.NewTextSegment(text.NewSegment(0, 0))
			br.SetHardLineBreak(true)
			n.AppendChild(n, br)
		}
	}
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			if s := n.Segment; n.SoftLineBreak() && s.Stop < len(src) && strings.IndexByte(" \t\n", src[s.Stop]) >= 0 {
				src[s.Stop] = ' '
				n.Segment = s.WithStop(s.Stop + 1)
				n.SetSoftLineBreak(false)
			}
		case *ast.ListItem:
			for c := n.FirstChild(); c != nil && c.NextSibling() != nil; c = c.NextSibling() {
				if k := c.NextSibling().Kind(); k == ast.KindParagraph || k == ast.KindTextBlock {
					breakAfter(c)
				}
			}
			if list, ok := n.Parent().(*ast.List); ok && !list.IsTight && n.NextSibling() != nil {
				last := n.LastChild() // the item's last text, in nested lists too
				for last != nil && (last.Kind() == ast.KindList || last.Kind() == ast.KindListItem) {
					last = last.LastChild()
				}
				if last != nil {
					breakAfter(last)
				}
			}
		}
		return ast.WalkContinue, nil
	})
}

// markdownStyle builds the glamour style from the palette: no margins or
// background fills, headings in bold accent, dim rules, muted list markers.
func markdownStyle(width int) ansi.StyleConfig {
	if Colorless() {
		s := styles.NoTTYStyleConfig
		s.Document = ansi.StyleBlock{}
		s.HorizontalRule.Format = "\n" + strings.Repeat("─", min(width, 80)) + "\n"
		return s
	}
	color := func(c lipgloss.CompleteAdaptiveColor) *string { s := colorCode(c); return &s }
	yes, no := ptr(true), ptr(false)
	return ansi.StyleConfig{
		Document:      ansi.StyleBlock{},
		Paragraph:     ansi.StyleBlock{},
		BlockQuote:    ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: color(colMuted), Italic: yes}, Indent: ptr(uint(1)), IndentToken: ptr("│ ")},
		List:          ansi.StyleList{LevelIndent: 2},
		Heading:       ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{BlockSuffix: "\n", Color: color(colAccent), Bold: yes}},
		H1:            ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Underline: yes}},
		H3:            ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "### "}},
		H4:            ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "#### "}},
		H5:            ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "##### "}},
		H6:            ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "###### ", Bold: no}},
		Strikethrough: ansi.StylePrimitive{CrossedOut: yes},
		Emph:          ansi.StylePrimitive{Italic: yes},
		Strong:        ansi.StylePrimitive{Bold: yes},
		HorizontalRule: ansi.StylePrimitive{
			Color:  color(colDim),
			Format: "\n" + strings.Repeat("─", min(width, 80)) + "\n",
		},
		Item:        ansi.StylePrimitive{Prefix: "• ", Color: color(colMuted)},
		Enumeration: ansi.StylePrimitive{BlockPrefix: ". ", Color: color(colMuted)},
		Task:        ansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},
		Link:        ansi.StylePrimitive{Color: color(colLink)},
		LinkText:    ansi.StylePrimitive{Color: color(colLink), Underline: yes},
		Image:       ansi.StylePrimitive{Color: color(colLink)},
		ImageText:   ansi.StylePrimitive{Color: color(colDim), Format: "Image: {{.text}} →"},
		Code:        ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: color(colAccent)}},
		// Fences inside list items; the top-level ones are drawn by codeBlock.
		CodeBlock:             ansi.StyleCodeBlock{StyleBlock: ansi.StyleBlock{Margin: ptr(uint(2))}},
		Table:                 ansi.StyleTable{CenterSeparator: ptr("┼"), ColumnSeparator: ptr("│"), RowSeparator: ptr("─")},
		DefinitionDescription: ansi.StylePrimitive{BlockPrefix: "\n→ "},
	}
}

func ptr[T any](v T) *T { return &v }

// renderMarkdown renders md with m (raw md when m is nil or fails) at width
// and tidies the output: no padding at the end of a line, no blank lines
// around or doubled, text wrapped at spaces under its indent, quote bars or
// list marker. Unindented code fences are drawn by codeBlock, between dim
// fence lines; unindented tables are laid out by glamour at the width.
func renderMarkdown(m *markdown, width int, md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	var parts []string
	add := func(out string) {
		if out != "" {
			parts = append(parts, out)
		}
	}
	flush := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if m == nil {
			add(tidy(text))
			return
		}
		var b strings.Builder
		if err := m.prose.Convert([]byte(text), &b); err != nil {
			add(tidy(text))
			return
		}
		add(rewrap(tidy(b.String()), width))
	}

	lines := strings.SplitAfter(md, "\n")
	start := 0 // first line of the pending markdown
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\n")
		if strings.TrimLeft(line, " \t") != line {
			continue
		}
		if fence := opensFence(line); fence != "" {
			end := i + 1
			for end < len(lines) && !closesFence(strings.TrimLeft(strings.TrimRight(lines[end], "\n"), " \t"), fence) {
				end++
			}
			flush(strings.Join(lines[start:i], ""))
			code := strings.TrimSuffix(strings.Join(lines[i+1:min(end, len(lines))], ""), "\n")
			parts = append(parts, codeBlock(strings.TrimSpace(line[len(fence):]), code, end < len(lines)))
			start, i = end+1, end
			continue
		}
		if m != nil && strings.Contains(line, "|") && i+1 < len(lines) && tableDelimiter.MatchString(strings.TrimRight(lines[i+1], "\n")) {
			end := i + 2
			for end < len(lines) && strings.Contains(lines[end], "|") && strings.TrimSpace(lines[end]) != "" {
				end++
			}
			flush(strings.Join(lines[start:i], ""))
			table := strings.Join(lines[i:end], "")
			if out, err := m.tables.Render(table); err == nil {
				table = out
			}
			add(tidy(table))
			start, i = end, end-1
		}
	}
	if start < len(lines) {
		flush(strings.Join(lines[start:], ""))
	}
	return strings.Join(parts, "\n\n")
}

// tableDelimiter matches the line under a table's header: | --- | :-: |
var tableDelimiter = regexp.MustCompile(`^\|?(?:\s*:?-+:?\s*\|)+\s*(?::?-+:?\s*)?$|^\|\s*:?-+:?\s*$`)

// tidy drops glamour's line padding, the blank lines around its output and
// all but one of consecutive blank lines (a list followed by another).
func tidy(out string) string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = trimPadding(line)
		if line == "" && (len(lines) == 0 || lines[len(lines)-1] == "") {
			continue
		}
		lines = append(lines, line)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// lineLead matches how a rendered line starts: its indent and quote bars,
// then a list item's marker.
var lineLead = regexp.MustCompile(`^((?: |│ |\| )*)(• |\d+\. |\[[✓x ]\] )?`)

// rewrap wraps the lines of unwrapped glamour output at width. A line
// continues under its indent and quote bars, a list item's under its text,
// as do the item's own later lines (a hard break, another paragraph), which
// glamour puts back at the item's indent.
func rewrap(out string, width int) string {
	lines := strings.Split(out, "\n")
	var res []string
	var items []int // the indents of the list items open, innermost last
	hang := ""      // the prefix the innermost item's lines go under
	for i, line := range lines {
		plain := xansi.Strip(line)
		if strings.Trim(plain, " │|") == "" { // blank, in a quote or not
			// glamour starts a quote that starts with a list with an empty row
			if plain != "" && (len(res) == 0 || res[len(res)-1] == "") {
				continue
			}
			// and leaves a blank line where lists nested two deep end
			if i+1 < len(lines) {
				if m := lineLead.FindStringSubmatch(xansi.Strip(lines[i+1])); m[2] != "" {
					deeper := 0
					for _, n := range items {
						if n > xansi.StringWidth(m[1]) {
							deeper++
						}
					}
					if deeper >= 2 {
						continue
					}
				}
			}
			items = nil
			res = append(res, line)
			continue
		}
		m := lineLead.FindStringSubmatch(plain)
		n := xansi.StringWidth(m[0])
		indent := xansi.StringWidth(m[1])
		first := xansi.Truncate(line, n, "")
		next := first
		switch {
		case m[2] != "":
			for len(items) > 0 && items[len(items)-1] >= indent {
				items = items[:len(items)-1]
			}
			items = append(items, indent)
			bars := 0 // the columns up to the last quote bar and its space
			if at := strings.LastIndexAny(m[1], "│|"); at >= 0 {
				bars = xansi.StringWidth(m[1][:at]) + 2
			}
			hang = xansi.Truncate(line, bars, "") + strings.Repeat(" ", n-bars)
			next = hang
		case len(items) > 0 && indent <= items[len(items)-1]:
			first, next = hang, hang
		}
		for k, row := range wrapWords(xansi.TruncateLeft(line, n, ""), width-xansi.StringWidth(next)) {
			if k == 0 {
				res = append(res, first+row)
			} else {
				res = append(res, next+row)
			}
		}
	}
	return strings.Join(res, "\n")
}

// wrapWords breaks the styled line s into rows at most width wide, at
// spaces only: a word that fits on a row is never split (a path, an id, a
// hyphenated word); a longer one is cut at the row's end, starting on the
// row it is on unless that is nearly full. The spaces at a break go, and a
// row ends with a reset when styled; the next one sets its styles again.
func wrapWords(s string, width int) []string {
	plain := xansi.Strip(s)
	if width < 1 || xansi.StringWidth(plain) <= width || strings.TrimSpace(plain) == "" {
		return []string{s}
	}
	type span struct{ from, to int }
	var words []span
	var bounds []int // the columns where graphemes start
	col, state := 0, -1
	for rest := plain; rest != ""; {
		var g string
		var w int
		g, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
		bounds = append(bounds, col)
		if g != " " {
			if len(words) == 0 || words[len(words)-1].to != col {
				words = append(words, span{col, col})
			}
			words[len(words)-1].to = col + w
		}
		col += w
	}

	var rows []string
	emit := func(from, to int) {
		if from >= to {
			return
		}
		row := xansi.Cut(s, from, to)
		if strings.Contains(row, "\x1b[") {
			row += "\x1b[0m"
		}
		rows = append(rows, row)
	}
	from, end := 0, -1 // the row's first column, the end of its last word (-1: none)
	for _, w := range words {
		if end >= 0 && w.to-from > width && (w.to-w.from <= width || from+width-w.from < 8) {
			emit(from, end)
			from, end = w.from, -1
		}
		for w.to-from > width {
			at := bounds[sort.SearchInts(bounds, from+width+1)-1]
			if at <= from { // a grapheme wider than the row
				at = w.to
				if j := sort.SearchInts(bounds, from+1); j < len(bounds) && bounds[j] < w.to {
					at = bounds[j]
				}
			}
			emit(from, at)
			from = at
		}
		end = w.to
	}
	emit(from, end)
	return rows
}

// codeBlock draws a fenced code block like pi: dim fence lines around the
// code, which is indented by two spaces and highlighted when the language
// is known. closed is false while the closing fence has not streamed in.
func codeBlock(lang, code string, closed bool) string {
	var b strings.Builder
	b.WriteString(T.Dim.Render("```" + lang))
	for _, line := range strings.Split(highlight(lang, code), "\n") {
		b.WriteString("\n")
		if line != "" {
			b.WriteString("  " + line)
		}
	}
	if closed {
		b.WriteString("\n" + T.Dim.Render("```"))
	}
	return b.String()
}

// highlight colors code with the palette's syntax tones, by token.
func highlight(lang, code string) string {
	lexer := lexers.Get(lang)
	if lexer == nil || lang == "" || Colorless() {
		return strings.ReplaceAll(code, "\t", "    ")
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return code
	}
	var b strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		b.WriteString(paint(syntaxStyle(tok.Type), strings.ReplaceAll(tok.Value, "\t", "    ")))
	}
	return b.String()
}

// paint styles every line of s on its own: lipgloss would pad the lines of
// a multi-line string to one width.
func paint(style lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = style.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// syntaxStyle maps a token type to its tone (pi's syntax colors).
func syntaxStyle(t chroma.TokenType) lipgloss.Style {
	fg := func(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	switch {
	case t.InCategory(chroma.Comment):
		return fg(colMuted).Italic(true)
	case t == chroma.KeywordType || t == chroma.NameClass:
		return fg(colAccent)
	case t.InCategory(chroma.Keyword):
		return fg(colLink)
	case t.InSubCategory(chroma.LiteralString):
		return fg(colString)
	case t.InSubCategory(chroma.LiteralNumber):
		return fg(colSuccess)
	case t == chroma.NameFunction || t == chroma.NameBuiltin || t == chroma.NameDecorator:
		return fg(colWarning)
	case t == chroma.NameVariable || t == chroma.NameAttribute || t == chroma.NameTag:
		return fg(colVariable)
	case t.InCategory(chroma.Operator) || t == chroma.Punctuation:
		return fg(colMuted)
	}
	return lipgloss.NewStyle()
}

var trailingPadding = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// trimPadding drops the spaces glamour pads every line with to the full
// width (styled ones included), keeping a reset if styling was cut off.
func trimPadding(line string) string {
	line = trailingPadding.ReplaceAllStringFunc(line, func(tail string) string {
		if strings.Contains(tail, "\x1b") {
			return "\x1b[0m"
		}
		return ""
	})
	if lipgloss.Width(line) == 0 {
		return ""
	}
	return line
}
