package manage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// writeTree creates files (slash-separated relative paths) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func names(files []UploadFile) string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return strings.Join(out, "|")
}

func TestCollectFilesFolder(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"README.md":               "readme",
		"guide/intro.md":          "intro",
		"guide/deep/setup.MD":     "setup",
		"документы/введение.md":   "вступление",
		"文档/指南.md":                "指南",
		"guide/logo.svg":          "<svg/>",
		"guide/empty.md":          "",
		"guide/.draft.md":         "hidden",
		".git/config":             "hidden dir",
		"node_modules/pkg/a.md":   "dep",
		"drafts/wip.md":           "wip",
		"guide/drafts/later.md":   "later",
		"notes/keep.txt":          "keep",
		"notes/data.pdf":          "%PDF",
		"it's \"quoted\" & co.md": "q",
	})

	got, err := CollectFiles([]string{dir}, CollectOptions{Exclude: []string{"node_modules", "drafts", "*.pdf"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "README.md|guide/deep/setup.MD|guide/intro.md|it's \"quoted\" & co.md|notes/keep.txt|документы/введение.md|文档/指南.md"
	if names(got.Files) != want {
		t.Errorf("files:\n got %s\nwant %s", names(got.Files), want)
	}
	if !got.Folders || got.Archives || got.Duplicate != "" {
		t.Errorf("flags = %+v", got)
	}
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		rel, _ := filepath.Rel(dir, s.Path)
		reasons[filepath.ToSlash(rel)] = s.Reason
	}
	wantReasons := map[string]string{
		"guide/logo.svg": "file type not supported",
		"guide/empty.md": "empty",
		"notes/data.pdf": "excluded",
	}
	for rel, reason := range wantReasons {
		if reasons[rel] != reason {
			t.Errorf("skipped[%s] = %q, want %q (all: %v)", rel, reasons[rel], reason, reasons)
		}
	}
	// Hidden entries and excluded folders are not walked, so not listed.
	for _, rel := range []string{"guide/.draft.md", ".git/config", "node_modules/pkg/a.md", "drafts/wip.md", "guide/drafts/later.md"} {
		if _, listed := reasons[rel]; listed {
			t.Errorf("%s should not be listed as skipped", rel)
		}
	}
	for _, f := range got.Files {
		if f.Size == 0 || f.SHA256 != "" {
			t.Errorf("%s: size %d sha %q", f.Name, f.Size, f.SHA256)
		}
	}
}

func TestCollectFilesInclude(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "a", "b.txt": "b", "guide/c.md": "c", "guide/d.txt": "d"})

	got, err := CollectFiles([]string{dir}, CollectOptions{Include: []string{"*.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if names(got.Files) != "a.md|guide/c.md" {
		t.Errorf("files = %s", names(got.Files))
	}
	got, err = CollectFiles([]string{dir}, CollectOptions{Include: []string{"guide/*"}, Exclude: []string{"*.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if names(got.Files) != "guide/c.md" {
		t.Errorf("files = %s", names(got.Files))
	}
	if _, err := CollectFiles([]string{dir}, CollectOptions{Exclude: []string{"[bad"}}); err == nil {
		t.Error("a malformed pattern must be refused")
	}
}

func TestCollectFilesNormalizesNames(t *testing.T) {
	dir := t.TempDir()
	// "é" decomposed, as macOS may hand a name over.
	decomposed := norm.NFD.String("café/résumé.md")
	writeTree(t, dir, map[string]string{decomposed: "cv"})

	got, err := CollectFiles([]string{dir}, CollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := norm.NFC.String("café/résumé.md"); names(got.Files) != want {
		t.Errorf("name = %q, want the composed %q", names(got.Files), want)
	}
}

func TestCollectFilesPlainFiles(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a/index.md": "a", "b/index.md": "b", "docs.zip": "PK", "c.svg": "x"})
	a, b := filepath.Join(dir, "a", "index.md"), filepath.Join(dir, "b", "index.md")

	// Named files are sent as given: no type filter, their base names, their order.
	got, err := CollectFiles([]string{filepath.Join(dir, "c.svg"), a}, CollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if names(got.Files) != "c.svg|index.md" || got.Folders || len(got.Skipped) != 0 {
		t.Errorf("got %+v", got)
	}
	got, err = CollectFiles([]string{a, b}, CollectOptions{})
	if err != nil || got.Duplicate != "index.md" || len(got.Files) != 2 {
		t.Errorf("two files with one name: %+v, %v", got, err)
	}
	got, err = CollectFiles([]string{filepath.Join(dir, "docs.zip")}, CollectOptions{})
	if err != nil || !got.Archives {
		t.Errorf("archive: %+v, %v", got, err)
	}
	if _, err := CollectFiles([]string{filepath.Join(dir, "missing.md")}, CollectOptions{}); err == nil {
		t.Error("a missing file must be an error")
	}
}

func TestCollectFilesNameClashes(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	writeTree(t, one, map[string]string{"guide/a.md": "1", "only-one.md": "1"})
	writeTree(t, two, map[string]string{"guide/a.md": "2"})

	_, err := CollectFiles([]string{one, two}, CollectOptions{})
	if err == nil || !strings.Contains(err.Error(), `"guide/a.md"`) {
		t.Errorf("two folders with the same path: err = %v", err)
	}
	_, err = CollectFiles([]string{one, filepath.Join(one, "only-one.md")}, CollectOptions{})
	if err == nil || !strings.Contains(err.Error(), `"only-one.md"`) {
		t.Errorf("a folder and a file with the same name: err = %v", err)
	}
}

func TestCollectFilesLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir, outside := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]string{"real.md": "real"})
	writeTree(t, outside, map[string]string{"target.md": "target", "sub/x.md": "x"})
	for link, target := range map[string]string{
		"file-link.md": filepath.Join(outside, "target.md"),
		"dir-link":     filepath.Join(outside, "sub"),
		"loop":         dir,
		"broken.md":    filepath.Join(outside, "nope.md"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := CollectFiles([]string{dir}, CollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if names(got.Files) != "file-link.md|real.md" {
		t.Errorf("files = %s", names(got.Files))
	}
	reasons := map[string]string{}
	for _, s := range got.Skipped {
		reasons[filepath.Base(s.Path)] = s.Reason
	}
	if reasons["dir-link"] != "linked folder" || reasons["loop"] != "linked folder" || reasons["broken.md"] != "broken link" {
		t.Errorf("skipped = %v", reasons)
	}
}

func TestMatchesAny(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"drafts", "drafts/a.md", true},
		{"drafts", "guide/drafts/a.md", true},
		{"drafts", "guide/drafts.md", false},
		{"*.png", "img/logo.png", true},
		{"guide/*.md", "guide/a.md", true},
		{"guide/*.md", "other/a.md", false},
		{"guide/deep", "guide/deep/x/y.md", true},
		{"./guide/", "guide/a.md", true},
		{"文档", "文档/指南.md", true},
		{"a.md", "b.md", false},
	}
	for _, tt := range tests {
		if got := matchesAny([]string{tt.pattern}, tt.name); got != tt.want {
			t.Errorf("matchesAny(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestHashFilesAndTreeHash(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.md": "alpha", "b/c.md": "gamma"})
	got, err := CollectFiles([]string{dir}, CollectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := HashFiles(got.Files); err != nil {
		t.Fatal(err)
	}
	// sha256("alpha")
	if got.Files[0].SHA256 != "8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8" {
		t.Errorf("sha = %s", got.Files[0].SHA256)
	}
	base := TreeHash(got.Files)
	reversed := []UploadFile{got.Files[1], got.Files[0]}
	if TreeHash(reversed) != base {
		t.Error("the tree hash must not depend on order")
	}
	renamed := []UploadFile{got.Files[0], {Name: "b/d.md", SHA256: got.Files[1].SHA256}}
	edited := []UploadFile{got.Files[0], {Name: "b/c.md", SHA256: got.Files[0].SHA256}}
	if TreeHash(renamed) == base || TreeHash(edited) == base || TreeHash(got.Files[:1]) == base {
		t.Error("a rename, an edit and a removal must each change the tree hash")
	}
	if err := HashFiles([]UploadFile{{Path: filepath.Join(dir, "missing")}}); err == nil {
		t.Error("hashing a missing file must fail")
	}
}

func removeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
