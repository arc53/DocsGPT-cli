package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/attach"

	tea "github.com/charmbracelet/bubbletea"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func pasteKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
}

func testFiles(t *testing.T) (shot, spec string) {
	dir := t.TempDir()
	shot, spec = filepath.Join(dir, "My Shot.png"), filepath.Join(dir, "spec.pdf")
	os.WriteFile(shot, pngBytes, 0o644)
	os.WriteFile(spec, []byte("%PDF-1.4 x"), 0o644)
	return shot, spec
}

func escaped(p string) string { return strings.ReplaceAll(p, " ", `\ `) }

// TestEditorDropFiles: a paste of dropped paths attaches the files as
// markers, which are one unit like a paste's, and undo takes them back.
func TestEditorDropFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-escaped paths")
	}
	shot, spec := testFiles(t)
	m := testEditor()
	press(m, runes("why"), pasteKey(escaped(shot)+" "+escaped(spec)+" "), runes("fails?"))
	want := "why [image #1 · My Shot.png · 16 B] [file #2 · spec.pdf · 10 B] fails?"
	if m.text() != want {
		t.Fatalf("text = %q", m.text())
	}
	// The cursor steps over a marker and backspace deletes all of it.
	press(m, key(tea.KeyHome), key(tea.KeyRight), key(tea.KeyRight), key(tea.KeyRight), key(tea.KeyRight), key(tea.KeyRight))
	if m.col != len([]rune("why [image #1 · My Shot.png · 16 B]")) {
		t.Fatalf("cursor at %d, want after the marker", m.col)
	}
	press(m, key(tea.KeyBackspace))
	if m.text() != "why  [file #2 · spec.pdf · 10 B] fails?" || len(m.files) != 1 {
		t.Fatalf("after backspace: %q, %d files", m.text(), len(m.files))
	}
	undo(m)
	if m.text() != want || len(m.files) != 2 {
		t.Fatalf("undo: %q, %d files", m.text(), len(m.files))
	}
	// A marker typed by hand is text.
	press(m, key(tea.KeyEnd), runes(" [file #9 · x.md · 1 B]"))
	text, shown, files := m.take()
	if text != shown || len(files) != 2 || files[0].Path != shot || files[1].Path != spec || files[0].ID != 1 {
		t.Fatalf("take: %q, %q, %+v", text, shown, files)
	}
	// History keeps the marker text, not the files.
	press(m, key(tea.KeyUp))
	if m.text() != text || len(m.files) != 0 {
		t.Fatalf("recalled: %q, %d files", m.text(), len(m.files))
	}

	// In a shell command, and a paste that is not only paths, the paths
	// stay text.
	m = testEditor()
	press(m, runes("!ls "), pasteKey(escaped(shot)))
	if m.text() != "!ls "+escaped(shot) {
		t.Fatalf("shell: %q", m.text())
	}
	m = testEditor()
	press(m, pasteKey("see "+escaped(shot)))
	if m.text() != "see "+escaped(shot) || len(m.files) != 0 {
		t.Fatalf("mixed paste: %q", m.text())
	}
	// A file that cannot be attached is pasted as text, and said why.
	bin := filepath.Join(filepath.Dir(shot), "app.bin")
	os.WriteFile(bin, []byte{0, 1, 2}, 0o644)
	m = testEditor()
	press(m, pasteKey(bin))
	if m.text() != bin || !strings.Contains(m.notice, "Not attached: app.bin") {
		t.Fatalf("unsupported: %q, notice %q", m.text(), m.notice)
	}
}

type fakeClip struct {
	clip attach.Clip
	err  error
}

func (f fakeClip) Read(path string) (attach.Clip, error) {
	if f.clip.Image {
		os.WriteFile(path, pngBytes, 0o600)
	}
	return f.clip, f.err
}

// ctrlV presses Ctrl+V and delivers what the clipboard read.
func ctrlV(m *editorModel) {
	cmd, _ := m.update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m.update(cmd())
}

// TestEditorPasteImage: Ctrl+V attaches the clipboard's image, else its
// files, else pastes its text.
func TestEditorPasteImage(t *testing.T) {
	m := testEditor()
	m.clip = fakeClip{clip: attach.Clip{Image: true}}
	press(m, runes("what is"))
	ctrlV(m)
	if m.text() != "what is [image #1 · 16 B] " || len(m.files) != 1 || !m.files[1].Clipboard {
		t.Fatalf("image: %q, %+v", m.text(), m.files)
	}
	defer os.Remove(m.files[1].Path)
	ctrlV(m)
	if m.text() != "what is [image #1 · 16 B] [image #2 · 16 B] " {
		t.Fatalf("second image: %q", m.text())
	}
	defer os.Remove(m.files[2].Path)
	undo(m)
	if m.text() != "what is [image #1 · 16 B] " || len(m.files) != 1 {
		t.Fatalf("undo: %q", m.text())
	}

	_, spec := testFiles(t)
	m = testEditor()
	m.clip = fakeClip{clip: attach.Clip{Files: []string{spec}}}
	ctrlV(m)
	if m.text() != "[file #1 · spec.pdf · 10 B] " {
		t.Fatalf("files: %q", m.text())
	}
	m.clip = fakeClip{clip: attach.Clip{Text: "plain words"}}
	ctrlV(m)
	if m.text() != "[file #1 · spec.pdf · 10 B] plain words" {
		t.Fatalf("text: %q", m.text())
	}
	m.clip = fakeClip{}
	ctrlV(m)
	if !strings.Contains(m.notice, "holds no image") {
		t.Fatalf("empty: notice %q", m.notice)
	}
}

// TestScreenQueueFiles: messages queued with files keep them when Alt+↑
// takes them back, renumbered, and hand them on when sent.
func TestScreenQueueFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-escaped paths")
	}
	shot, spec := testFiles(t)
	m := testScreen(60, 16)
	m.cancel = func() {}
	m.Update(pasteKey(escaped(spec)))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(pasteKey(escaped(spec) + " " + escaped(shot)))
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.queue) != 2 || len(m.queue[1].Files) != 2 || m.queue[1].Files[1].Path != shot {
		t.Fatalf("queue: %+v", m.queue)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	want := "[file #1 · spec.pdf · 10 B] \n\n[file #2 · spec.pdf · 10 B] [image #3 · My Shot.png · 16 B] "
	if m.ed.text() != want || len(m.ed.files) != 3 {
		t.Fatalf("alt+up: %q, %d files", m.ed.text(), len(m.ed.files))
	}
	waiter := make(chan Message, 1)
	m.waiter = waiter
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-waiter; len(got.Files) != 3 || got.Files[2].Path != shot || got.Files[2].ID != 3 {
		t.Fatalf("sent: %+v", got.Files)
	}
}

// The notice of a file not attached shows in the status row.
func TestScreenEditorNotice(t *testing.T) {
	m := testScreen(60, 12)
	m.ed.clip = fakeClip{}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	for _, msg := range flatten(cmd) {
		m.Update(msg)
	}
	if r := rows(m); !strings.Contains(strings.Join(r, "\n"), "The clipboard holds no image, file or text") {
		t.Fatalf("rows:\n%s", strings.Join(r, "\n"))
	}
}

// flatten runs a command, a batch's one by one, and returns the messages
// that are not ticks.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, flatten(c)...)
		}
		return out
	case clipMsg:
		return []tea.Msg{msg}
	}
	return nil
}
