package attach

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
)

// Paths returns the files a paste names when it is nothing but paths of
// existing files, as a terminal pastes the files dropped on it: absolute,
// shell-escaped (/Users/a/My\ Shot.png), quoted ('/a b.png', "C:\a b.png")
// or file:// URLs, separated by spaces or lines. Anything else gives nil.
func Paths(paste string) []string {
	words, ok := split(paste, runtime.GOOS == "windows")
	if !ok || len(words) == 0 {
		return nil
	}
	var paths []string
	for _, w := range words {
		if strings.HasPrefix(w, "file://") {
			u, err := url.Parse(w)
			if err != nil || u.Host != "" && u.Host != "localhost" {
				return nil
			}
			w = filepath.FromSlash(u.Path)
			if runtime.GOOS == "windows" {
				w = strings.TrimPrefix(w, `\`) // file:///C:/x
			}
		}
		w = Expand(w)
		if !filepath.IsAbs(w) {
			return nil
		}
		if fi, err := os.Stat(w); err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		paths = append(paths, w)
	}
	return paths
}

// split breaks s into words as a shell would: on unquoted white space,
// with quotes and (but on Windows, where it separates paths) the
// backslash escaping. ok is false for an unclosed quote.
func split(s string, windows bool) (words []string, ok bool) {
	var b strings.Builder
	in := false // a word has begun (it may be empty: '')
	var quote rune
	rs := []rune(strings.TrimSpace(s))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && !windows && i+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[i+1]):
				i++
				b.WriteRune(rs[i])
			default:
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == '\\' && !windows && i+1 < len(rs):
			i++
			b.WriteRune(rs[i])
			in = true
		case unicode.IsSpace(r):
			if in {
				words = append(words, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if in {
		words = append(words, b.String())
	}
	return words, true
}

// Ref is an @path in a message that names an existing file.
type Ref struct {
	Token string // as written: @docs/a.md, @"My Shot.png"
	Path  string // absolute
}

var refPattern = regexp.MustCompile(`(?:^|\s)(@(?:"[^"\n]+"|'[^'\n]+'|\S+))`)

// Refs returns the @paths of text that name existing files, relative to
// the working directory (~ for the home directory), once each, in order.
// A name in quotes may hold spaces; punctuation right after one
// (@notes.md, …) is not part of it. An @word naming no file is left alone,
// as is a directory.
func Refs(text string) []Ref {
	var refs []Ref
	seen := map[string]bool{}
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		token := m[1]
		name := token[1:]
		if len(name) >= 2 && (name[0] == '"' || name[0] == '\'') {
			name = name[1 : len(name)-1]
			if p, ok := file(name); ok && !seen[p] {
				seen[p] = true
				refs = append(refs, Ref{token, p})
			}
			continue
		}
		for name != "" {
			if p, ok := file(name); ok {
				if !seen[p] {
					seen[p] = true
					refs = append(refs, Ref{"@" + name, p})
				}
				break
			}
			trimmed := strings.TrimRightFunc(name, func(r rune) bool { return strings.ContainsRune(".,;:!?)]}'\"", r) })
			if trimmed == name {
				break
			}
			name = trimmed
		}
	}
	return refs
}

// file returns the absolute path of name when it is an existing regular
// file.
func file(name string) (string, bool) {
	p, err := filepath.Abs(Expand(name))
	if err != nil {
		return "", false
	}
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	return p, true
}

// Show replaces the @path of each file attached by one (its Ref) with the
// file's marker: a message as shown.
func Show(text string, files []File) string {
	for _, f := range files {
		if f.Ref == "" {
			continue
		}
		for _, loc := range regexp.MustCompile(`(?:^|\s)(`+regexp.QuoteMeta(f.Ref)+`)`).FindAllStringSubmatchIndex(text, -1) {
			end := loc[3]
			if end < len(text) && !strings.ContainsRune(" \t\n.,;:!?)]}'\"", rune(text[end])) {
				continue // a longer @path
			}
			text = text[:loc[2]] + f.Marker() + text[end:]
			break
		}
	}
	return text
}
