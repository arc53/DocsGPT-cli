package ui

import (
	"maps"
	"slices"
	"strconv"

	"github.com/arc53/DocsGPT-cli/internal/attach"
)

// killMax is how many kills the ring keeps (pi keeps every one).
const killMax = 100

// kill is text a kill took out of the editor, with the pastes and files
// of the markers in it, so that a yank brings them back whole.
type kill struct {
	text   string
	pastes map[int]string
	files  map[int]attach.File
}

// killKeys are the keys that kill: Ctrl+K, Ctrl+U, Ctrl+W, Alt+Backspace
// and Alt+D (Alt+Delete). A run of them, nothing in between, builds one
// entry of the ring, as in pi and Emacs.
var killKeys = map[string]bool{
	"ctrl+k": true, "ctrl+u": true, "ctrl+w": true, "alt+backspace": true, "alt+d": true, "alt+delete": true,
}

// kill deletes runes [start, end) of the current line, widened to the
// markers it reaches into, and puts them on the kill ring.
func (m *editorModel) kill(start, end int, backward bool) {
	m.killed(m.cut(start, end), backward)
}

// killed puts text a kill took out on the ring: added to the latest entry
// when the edit before was a kill too (before it for a backward kill, after
// it for a forward one), else as an entry of its own. The pastes and files
// of its markers go with it; the editor drops them only after the key.
func (m *editorModel) killed(text string, backward bool) {
	if text == "" {
		return
	}
	k := kill{text: text, pastes: map[int]string{}, files: map[int]attach.File{}}
	for _, sub := range PasteMarker.FindAllStringSubmatch(text, -1) {
		if id, _ := strconv.Atoi(sub[1]); m.pastes[id] != "" {
			k.pastes[id] = m.pastes[id]
		}
	}
	for _, sub := range FileMarker.FindAllStringSubmatch(text, -1) {
		if id, _ := strconv.Atoi(sub[1]); m.files[id].Marker() == sub[0] {
			k.files[id] = m.files[id]
		}
	}
	if n := len(m.ring); n > 0 && killKeys[m.last] {
		last := m.ring[n-1]
		maps.Copy(k.pastes, last.pastes)
		maps.Copy(k.files, last.files)
		if backward {
			k.text += last.text
		} else {
			k.text = last.text + k.text
		}
		m.ring[n-1] = k
		return
	}
	m.ring = append(m.ring, k)
	if len(m.ring) > killMax {
		m.ring = slices.Delete(m.ring, 0, len(m.ring)-killMax)
	}
}

// yank (Ctrl+Y) inserts the latest kill at the cursor.
func (m *editorModel) yank() {
	if len(m.ring) == 0 {
		return
	}
	m.yankFrom = m.save()
	m.insertKill(m.ring[len(m.ring)-1])
}

// yankPop (Alt+Y), right after a yank or another yankPop, puts the kill
// before the one yanked in its place, going round the ring.
func (m *editorModel) yankPop() {
	if m.last != "ctrl+y" && m.last != "alt+y" || len(m.ring) < 2 {
		return
	}
	s := m.yankFrom
	lines := make([][]rune, len(s.lines))
	for i, l := range s.lines {
		lines[i] = slices.Clone(l)
	}
	m.lines, m.row, m.col, m.pastes, m.files = lines, s.row, s.col, maps.Clone(s.pastes), maps.Clone(s.files)
	last := m.ring[len(m.ring)-1]
	m.ring = append([]kill{last}, m.ring[:len(m.ring)-1]...)
	m.insertKill(m.ring[len(m.ring)-1])
}

// insertKill inserts a kill's text at the cursor, its markers numbered
// after the pastes and files the editor holds (a marker of the same
// number may have come since).
func (m *editorModel) insertKill(k kill) {
	text := PasteMarker.ReplaceAllStringFunc(k.text, func(marker string) string {
		sub := PasteMarker.FindStringSubmatch(marker)
		id, _ := strconv.Atoi(sub[1])
		p, ok := k.pastes[id]
		if !ok {
			return marker
		}
		n := len(m.pastes) + 1
		for m.pastes[n] != "" {
			n++
		}
		m.pastes[n] = p
		return "[paste #" + strconv.Itoa(n) + marker[len("[paste #"+sub[1]):]
	})
	text = FileMarker.ReplaceAllStringFunc(text, func(marker string) string {
		id, _ := strconv.Atoi(FileMarker.FindStringSubmatch(marker)[1])
		f, ok := k.files[id]
		if !ok || f.Marker() != marker {
			return marker
		}
		f.ID = 0
		for n := range m.files {
			f.ID = max(f.ID, n)
		}
		f.ID++
		m.files[f.ID] = f
		return f.Marker()
	})
	m.insert(text)
}
