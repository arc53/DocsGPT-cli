package display

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"

	xansi "github.com/charmbracelet/x/ansi"
)

const tableMD = "| Name | Role | Count |\n|:-----|:----:|------:|\n| **Ada** | `admin` | 3 |\n| Grace Hopper | user | 12 |\n"

// TestTableBox: a table that fits is drawn like pi's, each column as wide
// as its widest cell, the header and every row ruled off, and the cells
// aligned as the delimiter row says.
func TestTableBox(t *testing.T) {
	restoreColors(t)
	withColors(t, true)
	want := strings.Join([]string{
		"┌──────────────┬───────┬───────┐",
		"│ Name         │ Role  │ Count │",
		"├──────────────┼───────┼───────┤",
		"│ Ada          │ admin │     3 │",
		"├──────────────┼───────┼───────┤",
		"│ Grace Hopper │ user  │    12 │",
		"└──────────────┴───────┴───────┘",
	}, "\n")
	if got := rendered("Before.\n\n"+tableMD+"\nAfter.", 80); got != "Before.\n\n"+want+"\n\nAfter." {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	out := renderMarkdown(newMarkdown(80), 80, tableMD, nil)
	rows := strings.Split(out, "\n")
	if !strings.Contains(rows[1], "\x1b[1mName") {
		t.Errorf("header not bold: %q", rows[1])
	}
	if !strings.Contains(rows[3], "\x1b[1mAda") || !strings.Contains(rows[3], "admin") || strings.Contains(rows[3], "`") {
		t.Errorf("inline styles lost: %q", rows[3])
	}
	sgr := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, row := range rows { // no style left open into the padding or bars
		for _, cell := range strings.Split(row, T.Dim.Render("│")) {
			if seqs := sgr.FindAllString(cell, -1); len(seqs) > 0 && seqs[len(seqs)-1] != "\x1b[0m" {
				t.Errorf("a style left open before a bar: %q", row)
			}
		}
	}
}

// TestTableNarrow: a table wider than the window shrinks its columns
// towards their longest words and wraps the cells at spaces; every row is
// as wide as the box and no wider than the window, and no word is lost.
func TestTableNarrow(t *testing.T) {
	md := "| Column | Description | Wide 中文 |\n|---|---|---|\n" +
		"| alpha | A long description of the first thing that needs wrapping | 中文字符测试 🎉 |\n" +
		"| beta | short | https://example.com/a/rather/long/path/that/is/one/word |\n"
	eachStyle(t, func(colors bool) {
		for w := 16; w <= 100; w += 7 {
			out := rendered(md, w)
			rows := strings.Split(out, "\n")
			box := xansi.StringWidth(rows[0])
			if box > w {
				t.Errorf("colors %v, width %d: a box of %d columns:\n%s", colors, w, box, out)
			}
			for _, r := range rows {
				if xansi.StringWidth(r) != box {
					t.Errorf("colors %v, width %d: a row of %d columns in a box of %d:\n%s", colors, w, xansi.StringWidth(r), box, out)
					break
				}
			}
			// Each cell's text, its rows run together, holds its words.
			var cells []string
			var cell []string
			for _, r := range rows[1:] {
				if !strings.HasPrefix(r, "│") {
					cells, cell = append(cells, cell...), nil
					continue
				}
				parts := strings.Split(strings.Trim(r, "│"), "│")
				cell = append(cell[:0:0], cell...)
				for len(cell) < len(parts) {
					cell = append(cell, "")
				}
				for i, p := range parts {
					cell[i] += strings.ReplaceAll(p, " ", "")
				}
			}
			all := strings.Join(cells, "\n")
			for _, word := range []string{"Description", "description", "wrapping", "中文字符测试", "🎉", "https://example.com/a/rather/long/path"} {
				if !strings.Contains(all, word) {
					t.Errorf("colors %v, width %d: %q lost:\n%s", colors, w, word, out)
				}
			}
		}
	})
}

// TestTableWidths: the columns share the width as pi's do.
func TestTableWidths(t *testing.T) {
	row := func(cells ...string) []tableCell {
		var r []tableCell
		for _, c := range cells {
			r = append(r, tableCell{text: c})
		}
		return r
	}
	for _, c := range []struct {
		rows  [][]tableCell
		avail int
		want  []int
	}{
		{[][]tableCell{row("a", "bb"), row("ccc", "d")}, 20, []int{3, 2}},         // all fit
		{[][]tableCell{row("one two three", "four")}, 10, []int{6, 4}},            // shrink to words, share the rest
		{[][]tableCell{row(strings.Repeat("x", 40), "y")}, 20, []int{19, 1}},      // a long word is capped
		{[][]tableCell{row("aaaaaaaaaa", "bbbbbbbbbb", "cc")}, 9, []int{4, 4, 1}}, // words too wide: shared by weight
		{[][]tableCell{row("中文字符 x", "🎉")}, 6, []int{4, 2}},
		{[][]tableCell{row("中文字符", "🎉")}, 3, nil},                                                              // wide graphemes count twice
		{[][]tableCell{row("first line\nsecond, longer line", "x")}, 40, []int{len("second, longer line"), 1}}, // a <br> splits the width
	} {
		if got := columnWidths(c.rows, c.avail); !slices.Equal(got, c.want) {
			t.Errorf("%v in %d: got %v, want %v", c.rows, c.avail, got, c.want)
		}
	}
}

// TestTableFallback: a window too narrow for one column apiece shows the
// table's markdown, wrapped; markdown that is not a table stays prose.
func TestTableFallback(t *testing.T) {
	eachStyle(t, func(colors bool) {
		out := rendered(tableMD, 12) // 3 columns need 3*3+1+3 = 13
		if strings.Contains(out, "┌") || !strings.Contains(out, "| Name |") {
			t.Errorf("colors %v, not the raw table:\n%s", colors, out)
		}
		checkRows(t, out, 12)
		if out := rendered(tableMD, 13); !strings.Contains(out, "┌") {
			t.Errorf("colors %v, no box at 13:\n%s", colors, out)
		}
		// Wide characters need two columns each: 2*3+1+2*2 = 11.
		if out := rendered("| 中 | 文 |\n|---|---|\n| a | b |\n", 10); strings.Contains(out, "┌") {
			t.Errorf("colors %v, wide characters squeezed:\n%s", colors, out)
		}
		if out := rendered("| 中 | 文 |\n|---|---|\n| a | b |\n", 11); !strings.Contains(out, "│ 中 │ 文 │") {
			t.Errorf("colors %v, no box at 11:\n%s", colors, out)
		}
	})
}

// TestTableBreaks: a <br> in a cell starts a line of it; missing cells are
// empty and extra ones dropped.
func TestTableBreaks(t *testing.T) {
	md := "| A | B |\n|---|---|\n| one<br>two<BR/>three | x |\n| only |\n| p | q | r |\n"
	want := strings.Join([]string{
		"┌───────┬───┐",
		"│ A     │ B │",
		"├───────┼───┤",
		"│ one   │ x │",
		"│ two   │   │",
		"│ three │   │",
		"├───────┼───┤",
		"│ only  │   │",
		"├───────┼───┤",
		"│ p     │ q │",
		"└───────┴───┘",
	}, "\n")
	eachStyle(t, func(colors bool) {
		if got := rendered(md, 40); got != want {
			t.Errorf("colors %v, got:\n%s\nwant:\n%s", colors, got, want)
		}
	})
}

// TestTableLinks: a link in a cell is a hyperlink on each row it wraps
// onto, and else its URL follows it, dim; the box keeps its width.
func TestTableLinks(t *testing.T) {
	md := "| Where | Link |\n|---|---|\n| docs | see [the documentation pages](https://example.com/docs) now |\n| raw | <https://example.com/x> |\n"
	restoreColors(t)
	withColors(t, true)
	for _, on := range []bool{true, false} {
		withHyperlinks(t, on)
		out := renderMarkdown(newMarkdown(30), 30, md, nil)
		rows := strings.Split(out, "\n")
		for _, r := range rows {
			if xansi.StringWidth(r) != xansi.StringWidth(rows[0]) {
				t.Errorf("hyperlinks %v: a row of another width: %q", on, r)
			}
		}
		if !on {
			if wide := xansi.Strip(renderMarkdown(newMarkdown(100), 100, md, nil)); !strings.Contains(wide, "the documentation pages (https://example.com/docs) now") {
				t.Errorf("URL not shown:\n%s", wide)
			}
			continue
		}
		covered := linkRows(t, rows)
		if got := covered["https://example.com/docs"]; got != "the documentation pages" {
			t.Errorf("the link covers %q:\n%s", got, out)
		}
		if got := strings.ReplaceAll(covered["https://example.com/x"], " ", ""); got != "https://example.com/x" { // cut to the column
			t.Errorf("the autolink covers %q", got)
		}
	}
}

// TestTableStream: a table streamed into an answer is drawn once it is
// whole, and copies as shown: borders and padding kept, one row a line.
func TestTableStream(t *testing.T) {
	eachStyle(t, func(colors bool) {
		a := NewAnswer(false)
		a.Delta(docsgpt.Delta{Content: "Here:\n\n" + tableMD})
		a.Finish()
		rows := a.Lines(40)
		if !strings.Contains(xansi.Strip(strings.Join(rows, "\n")), "│ Grace Hopper │ user  │    12 │") {
			t.Fatalf("colors %v:\n%s", colors, strings.Join(rows, "\n"))
		}
		got, ada := copied(a, 40), "│ Ada          │ admin │     3 │"
		if !colors { // glamour's plain style
			ada = "│ **Ada**      │ admin │     3 │"
		}
		if !strings.Contains(got, "\n┌──") || !strings.Contains(got, "\n"+ada+"\n") {
			t.Errorf("colors %v, copied:\n%s", colors, got)
		}
	})
}
