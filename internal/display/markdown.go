package display

import (
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// newMarkdown returns a markdown renderer for the active theme that wraps at
// width. Nil when glamour refuses the options.
func newMarkdown(width int) *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle(width)),
		glamour.WithWordWrap(width),
		glamour.WithColorProfile(termenv.ColorProfile()),
	)
	if err != nil {
		return nil
	}
	return r
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

// renderMarkdown renders md with r (raw md when r is nil or fails) at width
// and tidies glamour's output: no padding at the end of a line, no blank
// lines around or doubled, wrapped list items indented under their text.
// Unindented code fences are drawn by codeBlock, between dim fence lines.
func renderMarkdown(r *glamour.TermRenderer, width int, md string) string {
	var parts []string
	flush := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		out := text
		if r != nil {
			if s, err := r.Render(text); err == nil {
				out = s
			}
		}
		if out = hangLists(tidy(out), width); out != "" {
			parts = append(parts, out)
		}
	}

	lines := strings.SplitAfter(md, "\n")
	start := 0 // first line of the pending markdown
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\n")
		fence := opensFence(line)
		if fence == "" || strings.TrimLeft(line, " \t") != line {
			continue
		}
		end := i + 1
		for end < len(lines) && !closesFence(strings.TrimLeft(strings.TrimRight(lines[end], "\n"), " \t"), fence) {
			end++
		}
		flush(strings.Join(lines[start:i], ""))
		code := strings.TrimSuffix(strings.Join(lines[i+1:min(end, len(lines))], ""), "\n")
		parts = append(parts, codeBlock(strings.TrimSpace(line[len(fence):]), code, end < len(lines)))
		start, i = end+1, end
	}
	if start < len(lines) {
		flush(strings.Join(lines[start:], ""))
	}
	return strings.Join(parts, "\n\n")
}

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

// listMarker matches a rendered list item's indent and marker.
var listMarker = regexp.MustCompile(`^( *)(?:• |\d+\. |\[[✓ ]\] )`)

// hangLists rewraps list items whose text glamour wrapped back to the
// item's own indent, so the continuation lines sit under the text instead.
func hangLists(out string, width int) string {
	lines := strings.Split(out, "\n")
	var res []string
	for i := 0; i < len(lines); {
		m := listMarker.FindStringSubmatch(xansi.Strip(lines[i]))
		j := i + 1
		for m != nil && j < len(lines) {
			p := xansi.Strip(lines[j])
			if strings.TrimSpace(p) == "" || listMarker.MatchString(p) || len(p)-len(strings.TrimLeft(p, " ")) > len(m[1]) {
				break
			}
			j++
		}
		if j == i+1 || width <= 0 {
			res = append(res, lines[i])
			i++
			continue
		}
		hang := xansi.StringWidth(m[0])
		body := xansi.TruncateLeft(lines[i], hang, "")
		for _, l := range lines[i+1 : j] {
			p := xansi.Strip(l)
			body += " " + xansi.TruncateLeft(l, len(p)-len(strings.TrimLeft(p, " ")), "")
		}
		for k, l := range strings.Split(xansi.Wrap(body, max(width-hang, 10), ""), "\n") {
			if k == 0 {
				res = append(res, xansi.Truncate(lines[i], hang, "")+l)
			} else {
				res = append(res, strings.Repeat(" ", hang)+l)
			}
		}
		i = j
	}
	return strings.Join(res, "\n")
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
