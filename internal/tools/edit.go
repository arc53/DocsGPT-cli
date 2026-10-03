package tools

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// span is a byte range [start, end) of a file's text.
type span struct{ start, end int }

// applyEdits applies edits to text, each old_text matched against text as
// it is (not as earlier edits leave it): exactly, else ignoring what
// models often get wrong (trailing spaces, \r\n, typographic quotes,
// dashes and spaces; see fuzzyText), in which case the file keeps its own
// bytes around the match. Every old_text must match once, and no two may
// overlap. A UTF-8 byte order mark stays, and in a \r\n file new_text's
// line breaks become \r\n. It returns the new text and the first line of
// each change in it, in file order.
func applyEdits(text string, edits []edit) (string, []int, error) {
	if len(edits) == 0 {
		return "", nil, fmt.Errorf("no edits given")
	}
	bom := ""
	if rest, ok := strings.CutPrefix(text, "\ufeff"); ok {
		bom, text = "\ufeff", rest
	}
	crlf := strings.Count(text, "\r\n") > strings.Count(text, "\n")/2

	type change struct {
		span
		i    int
		with string
	}
	var changes []change
	var fuzzy *fuzzyText
	for i, e := range edits {
		if e.OldText == "" {
			return "", nil, fmt.Errorf("edits[%d].old_text is empty", i)
		}
		with := e.NewText
		if crlf {
			with = strings.ReplaceAll(strings.ReplaceAll(with, "\r\n", "\n"), "\n", "\r\n")
		}
		at, n := find(text, e.OldText)
		if n == 0 {
			if fuzzy == nil {
				fuzzy = newFuzzyText(text)
			}
			at, n = fuzzy.find(e.OldText)
		}
		switch {
		case n == 0:
			return "", nil, fmt.Errorf("edits[%d].old_text was not found. It must match the file exactly, including whitespace and line breaks; read the file again to see what it holds now", i)
		case n > 1:
			return "", nil, fmt.Errorf("edits[%d].old_text matches %d places; add surrounding lines to make it unique", i, n)
		}
		changes = append(changes, change{at, i, with})
	}
	slices.SortFunc(changes, func(a, b change) int { return a.start - b.start })
	for k := 1; k < len(changes); k++ {
		if a, b := changes[k-1], changes[k]; b.start < a.end {
			return "", nil, fmt.Errorf("edits[%d] and edits[%d] overlap; merge them into one edit", min(a.i, b.i), max(a.i, b.i))
		}
	}

	var b strings.Builder
	var at []int
	last := 0
	for _, c := range changes {
		b.WriteString(text[last:c.start])
		at = append(at, strings.Count(b.String(), "\n")+1)
		b.WriteString(c.with)
		last = c.end
	}
	b.WriteString(text[last:])
	out := b.String()
	if out == text {
		return "", nil, fmt.Errorf("the edits change nothing: every new_text equals its old_text")
	}
	return bom + out, at, nil
}

// find returns where old occurs in text and how many times (counting
// overlapping occurrences, which are just as ambiguous).
func find(text, old string) (span, int) {
	first, n := -1, 0
	for i := 0; i <= len(text)-len(old); {
		j := strings.Index(text[i:], old)
		if j < 0 {
			break
		}
		if first < 0 {
			first = i + j
		}
		n++
		_, size := utf8.DecodeRuneInString(text[i+j:])
		i += j + size
	}
	return span{first, first + len(old)}, n
}

// fuzzyText is a text normalized for matching (normalize), with the byte
// offset in the original text of each of its bytes.
type fuzzyText struct {
	src, norm string
	orig      []int // orig[i]: where the rune of norm[i] starts in src
}

func newFuzzyText(text string) *fuzzyText {
	norm, orig := normalize(text)
	return &fuzzyText{text, norm, orig}
}

// find matches old, normalized the same way, and maps the match back to
// the original text: from the start of its first rune to the end of its
// last, so trailing spaces after it stay as they are.
func (f *fuzzyText) find(old string) (span, int) {
	o, _ := normalize(old)
	if strings.TrimSpace(o) == "" {
		return span{}, 0
	}
	at, n := find(f.norm, o)
	if n == 0 {
		return span{}, 0
	}
	last := f.orig[at.end-1]
	_, size := utf8.DecodeRuneInString(f.src[last:])
	return span{f.orig[at.start], last + size}, n
}

// normalize folds what models often get wrong: \r\n is \n, spaces and tabs
// at the end of a line go, typographic quotes are ' and ", dashes are -,
// and other spaces are a space. It returns the result and, for each of its
// bytes, the offset of the original rune it came from.
func normalize(s string) (string, []int) {
	var b strings.Builder
	orig := make([]int, 0, len(s))
	put := func(r rune, from int) {
		n := utf8.RuneLen(r)
		if n < 0 {
			n, r = 1, utf8.RuneError
		}
		b.WriteRune(r)
		for k := 0; k < n; k++ {
			orig = append(orig, from)
		}
	}
	for i := 0; i < len(s); {
		// The line's end without its trailing spaces.
		eol := strings.IndexByte(s[i:], '\n')
		if eol < 0 {
			eol = len(s)
		} else {
			eol += i
		}
		end := eol
		for end > i && strings.ContainsRune(" \t\r", rune(s[end-1])) {
			end--
		}
		for i < end {
			r, size := utf8.DecodeRuneInString(s[i:])
			put(fold(r), i)
			i += size
		}
		if eol < len(s) {
			put('\n', eol)
		}
		i = eol + 1
	}
	return b.String(), orig
}

// fold maps typographic quotes, dashes and spaces to their ASCII forms.
func fold(r rune) rune {
	switch r {
	case '‘', '’', '‚', '‛', '′':
		return '\''
	case '“', '”', '„', '‟', '″':
		return '"'
	case '‐', '‑', '‒', '–', '—', '―', '−':
		return '-'
	case '\u00a0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200a', '\u202f', '\u205f', '\u3000':
		return ' '
	}
	return r
}
