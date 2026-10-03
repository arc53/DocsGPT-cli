package attach

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func write(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	shot := write(t, dir, "shot.png", pngBytes)
	f, err := Open(shot)
	if err != nil || !f.Image || f.Name != "shot.png" || f.Size != int64(len(pngBytes)) || f.Path != shot {
		t.Fatalf("image: %+v, %v", f, err)
	}
	for _, name := range []string{"notes.md", "main.go", "no-extension"} {
		if f, err := Open(write(t, dir, name, []byte("plain text\n"))); err != nil || f.Image {
			t.Errorf("%s: %+v, %v", name, f, err)
		}
	}
	// A PDF is binary, and taken by its extension.
	if _, err := Open(write(t, dir, "spec.pdf", []byte("%PDF-1.4\x00\x01\x02"))); err != nil {
		t.Errorf("pdf: %v", err)
	}
	for name, want := range map[string]string{
		"app.bin":   "not a type the server reads",
		"empty.txt": "is empty",
		"missing":   "no such file",
		"sub":       "is a directory",
	} {
		var data []byte
		switch name {
		case "app.bin":
			data = []byte{0, 1, 2, 3}
		case "sub":
			os.Mkdir(filepath.Join(dir, "sub"), 0o755)
		}
		if name == "app.bin" || name == "empty.txt" {
			write(t, dir, name, data)
		}
		if _, err := Open(filepath.Join(dir, name)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	if _, err := Open(filepath.Join(dir, "app.bin")); !errors.Is(err, ErrType) {
		t.Errorf("app.bin: %v, want ErrType", err)
	}
}

func TestMarker(t *testing.T) {
	for _, tc := range []struct {
		f    File
		want string
	}{
		{File{ID: 1, Image: true, Clipboard: true, Size: 240 << 10}, "[image #1 · 240 KB]"},
		{File{ID: 2, Image: true, Name: "shot.png", Size: 900}, "[image #2 · shot.png · 900 B]"},
		{File{ID: 3, Name: "spec [v2].pdf", Size: 1258291}, "[file #3 · spec _v2_.pdf · 1.2 MB]"},
		{File{ID: 4, Name: strings.Repeat("a", 50) + ".md", Size: 30 << 20}, "[file #4 · aaaaaaaaaaaaaaaaaaa…aaaaaaaaaaaaaaaaa.md · 30 MB]"},
	} {
		if got := tc.f.Marker(); got != tc.want {
			t.Errorf("Marker() = %q, want %q", got, tc.want)
		}
	}
}

func TestParts(t *testing.T) {
	dir := t.TempDir()
	var g bytes.Buffer
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.White, color.Black})
	gif.Encode(&g, img, nil)
	files := []File{
		{Path: write(t, dir, "a.png", pngBytes), Name: "a.png", Image: true},
		{Path: write(t, dir, "b.md", []byte("# B")), Name: "b.md"},
		{Path: write(t, dir, "c.gif", g.Bytes()), Name: "c.gif", Image: true},
	}
	parts, sent, err := Parts(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || parts[0].Type != "image_url" || parts[1].Type != "file" || parts[1].File.Filename != "b.md" {
		t.Fatalf("parts: %+v", parts)
	}
	if !strings.HasPrefix(parts[2].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("the GIF did not go as a PNG: %.40s", parts[2].ImageURL.URL)
	}
	if sent[1].SHA256 != sha("# B") || sent[1].Size != 3 {
		t.Errorf("sent: %+v", sent[1])
	}
	os.Remove(files[1].Path)
	if _, _, err := Parts(files); err == nil || !strings.Contains(err.Error(), "b.md") {
		t.Errorf("a file gone: %v", err)
	}
	if _, _, err := Parts(make([]File, MaxFiles+1)); err == nil {
		t.Error("too many files: no error")
	}
}

func TestPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell escapes")
	}
	dir := t.TempDir()
	a := write(t, dir, "My Shot.png", pngBytes)
	b := write(t, dir, "spec.pdf", []byte("%PDF"))
	c := write(t, dir, "it's.md", []byte("x"))
	esc := func(p string) string { return strings.NewReplacer(" ", `\ `, "'", `\'`).Replace(p) }
	for _, tc := range []struct {
		paste string
		want  []string
	}{
		{esc(a), []string{a}},
		{esc(a) + " ", []string{a}}, // iTerm2 adds a space
		{esc(a) + " " + esc(b), []string{a, b}},
		{"'" + a + "'\n" + b + "\n", []string{a, b}},
		{`"` + a + `"`, []string{a}},
		{esc(c), []string{c}},
		{"file://" + strings.ReplaceAll(a, " ", "%20"), []string{a}},
		{a, nil},                    // unescaped: two words, neither a file
		{esc(a) + " and more", nil}, // words that are not files
		{"spec.pdf", nil},           // relative
		{dir, nil},                  // a directory
		{"'" + a, nil},              // an open quote
		{"", nil},
	} {
		if got := Paths(tc.paste); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Paths(%q) = %q, want %q", tc.paste, got, tc.want)
		}
	}
}

func TestRefs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/install.md", []byte("x"))
	write(t, dir, "My Shot.png", pngBytes)
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	dir, _ = os.Getwd() // /private/var on macOS
	got := Refs(`see @docs/install.md, and @"My Shot.png"; not @docs, @missing.md, a@docs/install.md or @docs/install.md again`)
	want := []Ref{
		{"@docs/install.md", filepath.Join(dir, "docs/install.md")},
		{`@"My Shot.png"`, filepath.Join(dir, "My Shot.png")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Refs() = %q, want %q", got, want)
	}
	if got := Refs("@docs/install.md"); len(got) != 1 {
		t.Errorf("at the start: %q", got)
	}
}

func TestShow(t *testing.T) {
	files := []File{
		{ID: 1, Name: "install.md", Size: 3 << 10, Ref: "@docs/install.md"},
		{ID: 2, Image: true, Clipboard: true, Size: 10},
	}
	got := Show("read @docs/install.md, not @docs/install.mdx [image #2 · 10 B]", files)
	if want := "read [file #1 · install.md · 3 KB], not @docs/install.mdx [image #2 · 10 B]"; got != want {
		t.Errorf("Show() = %q, want %q", got, want)
	}
}

type fakeClipboard struct {
	clip  Clip
	image []byte
}

func (f fakeClipboard) Read(path string) (Clip, error) {
	if f.clip.Image {
		os.WriteFile(path, f.image, 0o600)
	}
	return f.clip, nil
}

func TestPaste(t *testing.T) {
	files, text, err := Paste(fakeClipboard{clip: Clip{Image: true}, image: pngBytes})
	if err != nil || len(files) != 1 || !files[0].Image || !files[0].Clipboard || text != "" {
		t.Fatalf("image: %+v %q %v", files, text, err)
	}
	if !strings.HasPrefix(files[0].Marker(), "[image #0 · ") || strings.Contains(files[0].Marker(), ".png") {
		t.Errorf("marker: %s", files[0].Marker())
	}
	os.Remove(files[0].Path)
	if _, _, err := Paste(fakeClipboard{clip: Clip{Image: true}, image: []byte("not an image")}); err == nil {
		t.Error("a broken image: no error")
	}
	dir := t.TempDir()
	p := write(t, dir, "a.md", []byte("x"))
	if files, _, err := Paste(fakeClipboard{clip: Clip{Files: []string{p}}}); err != nil || len(files) != 1 || files[0].Path != p {
		t.Errorf("files: %+v %v", files, err)
	}
	if files, text, err := Paste(fakeClipboard{clip: Clip{Text: "hello"}}); err != nil || files != nil || text != "hello" {
		t.Errorf("text: %+v %q %v", files, text, err)
	}
}

func TestParseReport(t *testing.T) {
	if c := parseReport([]byte("files\r\n/a b.png\r\n/c.md\r\n")); !reflect.DeepEqual(c.Files, []string{"/a b.png", "/c.md"}) {
		t.Errorf("files: %+v", c)
	}
	if c := parseReport([]byte("image\n")); !c.Image {
		t.Errorf("image: %+v", c)
	}
	if c := parseReport([]byte("none\n")); c.Image || c.Files != nil {
		t.Errorf("none: %+v", c)
	}
	if got := fileURIs("# comment\nfile:///home/a/My%20Shot.png\r\nfile:///b.md\n"); !reflect.DeepEqual(got, []string{"/home/a/My Shot.png", "/b.md"}) {
		t.Errorf("fileURIs = %q", got)
	}
}

func TestReadUnix(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("PATH", t.TempDir()) // wl-paste is looked up
	os.WriteFile(filepath.Join(os.Getenv("PATH"), "wl-paste"), []byte("#!/bin/sh\n"), 0o755)
	var calls [][]string
	held := map[string]string{"text/plain": "hi", "image/png": string(pngBytes)}
	defer func(r func(string, ...string) ([]byte, error)) { run = r }(run)
	run = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if args[0] == "--list-types" {
			var types []string
			for t := range held {
				types = append(types, t)
			}
			return []byte(strings.Join(types, "\n")), nil
		}
		return []byte(held[args[len(args)-1]]), nil
	}
	path := filepath.Join(t.TempDir(), "x.png")
	c, err := readUnix(path)
	if err != nil || !c.Image {
		t.Fatalf("image: %+v %v", c, err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, pngBytes) {
		t.Errorf("saved %q", b)
	}
	held = map[string]string{"text/plain": "hi"}
	if c, err := readUnix(path); err != nil || c.Image || c.Files != nil {
		t.Errorf("text only: %+v %v", c, err)
	}
	held = map[string]string{"text/uri-list": "file:///tmp/a.md\n", "image/png": "x"}
	if c, _ := readUnix(path); !reflect.DeepEqual(c.Files, []string{"/tmp/a.md"}) {
		t.Errorf("files: %+v", c)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
