package ui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	historyMax      = 500      // entries kept
	historyEntryMax = 16 << 10 // longer entries are not kept
)

// secretLike matches what looks like a credential: tokens, keys (UUIDs
// included, the agent key format) and long unbroken base64 or hex runs.
var secretLike = regexp.MustCompile(`dgpt_pat_|\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b|\b(?:sk|pk|rk|ghp|gho|ghs|github_pat|xox[abprs]|AKIA)[-_A-Za-z0-9]{12,}|[A-Za-z0-9+/_=-]{40,}`)

// History is the editor's prompt history, oldest first. With a path it is
// kept in that file, one JSON string per line, private to the user.
type History struct {
	path    string
	entries []string
}

// LoadHistory reads the history kept in path. A missing or unreadable file
// gives an empty history; "" keeps it in memory only.
func LoadHistory(path string) *History {
	h := &History{path: path}
	if path == "" {
		return h
	}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4*historyEntryMax)
	for sc.Scan() {
		var s string
		if json.Unmarshal(sc.Bytes(), &s) == nil && s != "" {
			h.entries = append(h.entries, s)
		}
	}
	if len(h.entries) > historyMax {
		h.entries = h.entries[len(h.entries)-historyMax:]
		h.rewrite()
	}
	return h
}

// Add records s, unless it is empty, repeats the last entry, is too long
// or looks like it holds a secret.
func (h *History) Add(s string) {
	if strings.TrimSpace(s) == "" || len(s) > historyEntryMax || secretLike.MatchString(s) ||
		len(h.entries) > 0 && h.entries[len(h.entries)-1] == s {
		return
	}
	h.entries = append(h.entries, s)
	if h.path == "" {
		return
	}
	if len(h.entries) > historyMax+historyMax/2 {
		h.entries = h.entries[len(h.entries)-historyMax:]
		h.rewrite()
		return
	}
	if os.MkdirAll(filepath.Dir(h.path), 0o700) != nil {
		return
	}
	if f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		line, _ := json.Marshal(s)
		f.Write(append(line, '\n'))
		f.Close()
	}
}

// rewrite replaces the file with the current entries, through a new file
// renamed over it: another chat reading or appending meanwhile sees the
// old file or the new one, never half of it.
func (h *History) rewrite() {
	dir := filepath.Dir(h.path)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".history-*") // 0600
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, s := range h.entries {
		line, _ := json.Marshal(s)
		w.Write(append(line, '\n'))
	}
	err = w.Flush()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), h.path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
}
