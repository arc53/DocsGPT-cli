package ui

import "testing"

// para is a block of rows that copy as given.
type para struct {
	rows  []string
	plain []Plain
}

func (p *para) Lines(int) []string         { return p.rows }
func (p *para) Plain(int) []Plain          { return p.plain }
func wrapped(indent int, sep string) Plain { return Plain{Indent: indent, Wrap: true, Sep: sep} }

// TestUnwrap: wrapped rows join with what the wrap took out, decoration and
// trailing spaces are left out, and wide characters are taken whole.
func TestUnwrap(t *testing.T) {
	rows := []string{"\x1b[1mThe quick brown\x1b[0m   ", "  fox jumps", "  over-", "  the dog.", "", "  code line", "    cut", "中文字符 end"}
	plain := []Plain{{}, wrapped(2, " "), wrapped(2, " "), wrapped(2, ""), {}, {Indent: 2}, wrapped(4, ""), {}}
	for _, c := range []struct {
		a, b     int
		from, to int
		want     string
	}{
		{0, 7, 0, endCol, "The quick brown fox jumps over-the dog.\n\ncode linecut\n中文字符 end"},
		{0, 3, 4, 5, "quick brown fox jumps over-the"},
		{0, 0, 0, 5, "The q"},
		{5, 5, 0, 4, "co"}, // the indent left out
		{7, 7, 3, 5, "文字"}, // a cut at the middle of wide characters takes them whole
		{7, 7, 1, 2, "中"},
		{0, 1, 10, 7, "brown fox j"},
	} {
		got := Unwrap(rows[c.a:c.b+1], plain[c.a:c.b+1], c.from, c.to)
		if got != c.want {
			t.Errorf("rows %d-%d, columns %d-%d: %q, want %q", c.a, c.b, c.from, c.to, got, c.want)
		}
	}
}
