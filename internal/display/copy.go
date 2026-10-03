package display

import (
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/ui"

	"github.com/charmbracelet/x/ansi"
)

// How the transcript's lines copy (ui.Plain). A block knows its text before
// it was wrapped: a message's own, or for markdown the same markdown
// rendered wide enough that nothing wraps. joins lines the wrapped
// rows up against it, so a copied paragraph is one line again and a code
// line cut for the width is whole.

// joins tells which rows go on from the row above: the ones whose text
// follows that row's in logical (the rows' text unwrapped) with nothing but
// spaces between, which are what the wrap took out. Any other row starts a
// line, with no indent (the caller sets those). A row's leading spaces and
// quote bars are not part of its text.
func joins(rows []string, logical string) []ui.Plain {
	out := make([]ui.Plain, len(rows))
	pos, prev := 0, false
	for i, r := range rows {
		row := strings.TrimRight(ansi.Strip(r), " ")
		if strings.TrimSpace(row) == "" {
			prev = false
			continue
		}
		matched, resync := false, -1
		for _, text := range []string{strings.TrimLeft(row, " "), strings.TrimLeft(row, " │|")} {
			at := strings.Index(logical[pos:], text)
			if at < 0 || text == "" {
				continue
			}
			gap := logical[pos : pos+at]
			if strings.TrimSpace(gap) != "" {
				if resync < 0 {
					resync = pos + at + len(text)
				}
				continue
			}
			if prev && !strings.Contains(gap, "\n") {
				out[i] = ui.Plain{Indent: ansi.StringWidth(row[:len(row)-len(text)]), Wrap: true, Sep: gap}
			}
			pos, matched = pos+at+len(text), true
			break
		}
		if !matched && resync >= 0 {
			pos, matched = resync, true
		}
		prev = matched
	}
	return out
}

// indent leaves out up to n leading spaces of the rows that start a line.
func indent(plain []ui.Plain, rows []string, n int) []ui.Plain {
	for i, r := range rows {
		if !plain[i].Wrap {
			s := ansi.Strip(r)
			plain[i].Indent = min(n, len(s)-len(strings.TrimLeft(s, " ")))
		}
	}
	return plain
}

// markdownPlain tells how the rows of rendered markdown copy, given the
// markdown rendered unwrapped: code lines without the two columns
// codeBlock indents them by, quotes without their bars.
func markdownPlain(rows []string, unwrapped string) []ui.Plain {
	plain := joins(rows, unwrapped)
	code := false
	for i, r := range rows {
		row := ansi.Strip(r)
		if strings.HasPrefix(row, "```") {
			code = !code
			continue
		}
		if plain[i].Wrap {
			continue
		}
		lead := row[:len(row)-len(strings.TrimLeft(row, " "))]
		switch {
		case code:
			plain[i].Indent = min(2, len(lead))
		case strings.HasPrefix(row[len(lead):], "│ ") || strings.HasPrefix(row[len(lead):], "| "):
			plain[i].Indent = ansi.StringWidth(row[:len(row)-len(strings.TrimLeft(row, " │|"))])
		}
	}
	return plain
}

// unwrapped renders md as the transcript does, but wide enough that no line
// of it wraps: as wide as its longest paragraph, and some.
func unwrapped(md string) string {
	w := 0
	for _, p := range strings.Split(md, "\n\n") {
		w = max(w, len(p))
	}
	w = min(w+64, 1<<14)
	return ansi.Strip(renderMarkdown(newMarkdown(w), w, md))
}
