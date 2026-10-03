package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/ui"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

func TestApplyEdits(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		edits      []edit
		want       string
		at         []int
		err        string
	}{
		{name: "one", text: "a\nb\nc\n", edits: []edit{{"b", "B"}}, want: "a\nB\nc\n", at: []int{2}},
		{name: "several, matched against the original", text: "x = 1\ny = 2\nz = 3\n",
			edits: []edit{{"z = 3", "z = 30"}, {"x = 1", "x = 1\nw = 0"}}, want: "x = 1\nw = 0\ny = 2\nz = 30\n", at: []int{1, 4}},
		{name: "delete", text: "a\nb\nc\n", edits: []edit{{"b\n", ""}}, want: "a\nc\n", at: []int{2}},
		{name: "not found", text: "a\n", edits: []edit{{"zz", "y"}}, err: "edits[0].old_text was not found"},
		{name: "ambiguous", text: "x\nx\n", edits: []edit{{"x", "y"}}, err: "matches 2 places"},
		{name: "overlapping occurrences are ambiguous", text: "aaa", edits: []edit{{"aa", "b"}}, err: "matches 2 places"},
		{name: "overlap", text: "abcdef", edits: []edit{{"abcd", "1"}, {"cdef", "2"}}, err: "edits[0] and edits[1] overlap"},
		{name: "empty old_text", text: "a", edits: []edit{{"", "b"}}, err: "edits[0].old_text is empty"},
		{name: "no change", text: "a\n", edits: []edit{{"a", "a"}}, err: "change nothing"},
		{name: "no edits", text: "a\n", err: "no edits"},

		// What models get wrong, matched leniently; the file keeps its own
		// bytes around the match.
		{name: "trailing spaces in the file", text: "if x {  \n\treturn\n}\n", edits: []edit{{"if x {\n\treturn", "if y {\n\treturn"}},
			want: "if y {\n\treturn\n}\n", at: []int{1}},
		{name: "trailing spaces kept after the match", text: "a  \nb\n", edits: []edit{{"a", "A"}}, want: "A  \nb\n", at: []int{1}},
		{name: "typographic quotes and dashes", text: "say(“hi”) — ok\n", edits: []edit{{`say("hi") - ok`, `say("bye")`}},
			want: "say(\"bye\")\n", at: []int{1}},
		{name: "non-breaking space", text: "a\u00a0b\n", edits: []edit{{"a b", "c"}}, want: "c\n", at: []int{1}},
		{name: "CRLF file", text: "one\r\ntwo\r\nthree\r\n", edits: []edit{{"one\ntwo\n", "1\n2\n"}},
			want: "1\r\n2\r\nthree\r\n", at: []int{1}},
		{name: "BOM kept", text: "\ufeffa\nb\n", edits: []edit{{"a", "A"}}, want: "\ufeffA\nb\n", at: []int{1}},
		{name: "an exact match wins", text: "a \na\n", edits: []edit{{"a\n", "b\n"}}, want: "a \nb\n", at: []int{2}},
		{name: "fuzzy ambiguous", text: "a \nx\na\t\nx\n", edits: []edit{{"a\nx", "c"}}, err: "matches 2 places"},
		{name: "multibyte at the end of a fuzzy match", text: "ä“x”\n", edits: []edit{{`ä"x"`, "y"}}, want: "y\n", at: []int{1}},
	} {
		got, at, err := applyEdits(tc.text, tc.edits)
		switch {
		case tc.err != "":
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err = %v, want %q", tc.name, err, tc.err)
			}
		case err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case got != tc.want || !slices.Equal(at, tc.at):
			t.Errorf("%s: got %q at %v, want %q at %v", tc.name, got, at, tc.want, tc.at)
		}
	}
}

func TestCallArgs(t *testing.T) {
	for in, want := range map[string]callArgs{
		`{"timeout":"120"}`:          {Timeout: 120},
		`{"timeout":90.0}`:           {Timeout: 90},
		`{"offset":null}`:            {},
		`{"limit":" 5 "}`:            {Limit: 5},
		`{"timeout":1e12}`:           {Timeout: 1<<31 - 1},
		`{"old_text":"a"}`:           {OldText: "a"},
		`{"path":"x","content":"y"}`: {Path: "x", Content: "y"},
	} {
		var got callArgs
		if err := json.Unmarshal([]byte(in), &got); err != nil || !equalArgs(got, want) {
			t.Errorf("%s: %+v (%v), want %+v", in, got, err, want)
		}
	}
	if err := json.Unmarshal([]byte(`{"timeout":"soon"}`), &callArgs{}); err == nil {
		t.Error(`timeout "soon": want an error`)
	}

	want := editList{{"a", "b"}}
	for _, in := range []string{
		`[{"old_text":"a","new_text":"b"}]`,
		`{"old_text":"a","new_text":"b"}`,
		`"[{\"old_text\":\"a\",\"new_text\":\"b\"}]"`,
		`[{"oldText":"a","newText":"b"}]`,
		`[{"old_string":"a","new_string":"b"}]`,
	} {
		var got editList
		if err := json.Unmarshal([]byte(in), &got); err != nil || !slices.Equal(got, want) {
			t.Errorf("edits %s: %+v (%v)", in, got, err)
		}
	}
	if err := json.Unmarshal([]byte(`42`), &editList{}); err == nil {
		t.Error("edits 42: want an error")
	}
}

func equalArgs(a, b callArgs) bool {
	return a.Timeout == b.Timeout && a.Offset == b.Offset && a.Limit == b.Limit && a.Path == b.Path &&
		a.Content == b.Content && a.OldText == b.OldText && slices.Equal(a.Edits, b.Edits)
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	proj := t.TempDir()
	t.Chdir(proj)
	os.WriteFile("main.go", nil, 0o644)
	os.WriteFile("@odd", nil, 0o644)
	for in, want := range map[string]string{
		"~/x":       filepath.Join(home, "x"),
		"~":         home,
		" main.go ": "main.go",
		"@main.go":  "main.go",
		"@odd":      "@odd",    // a file of that name
		"@new.go":   "@new.go", // neither exists: as given
		"a/~/b":     "a/~/b",   // only a leading ~
		"@~/x":      "@~/x",    // ~/x does not exist
	} {
		if got := expandPath(in); got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// recordingUI approves with choice and records what was offered.
type recordingUI struct {
	choice  string
	offered [][]string // the labels of each approval
	titles  []string
	status  []string
}

func (u *recordingUI) Open(title, note string) {
	u.titles = append(u.titles, strings.TrimSpace(title+" "+note))
}
func (*recordingUI) Lines([]string)           {}
func (*recordingUI) Output() io.Writer        { return io.Discard }
func (u *recordingUI) Close(_ bool, s string) { u.status = append(u.status, s) }
func (u *recordingUI) Choose(items []ui.Item) (string, error) {
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	u.offered = append(u.offered, labels)
	return u.choice, nil
}
func (*recordingUI) Edit(_, v string) (string, error) { return v, nil }

func TestSessionEditAndWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	proj := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(filepath.Join(proj, ".git"), 0o755)
	t.Chdir(proj)
	os.WriteFile("main.go", []byte("package main\n\nfunc a() {}\n"), 0o600)

	u := &recordingUI{choice: "always"}
	s := &Session{UI: u, Timeout: time.Minute}
	call := func(name, args string) string {
		return s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: name, Arguments: args}}).Content
	}

	got := call("edit_file", `{"path":"main.go","edits":[{"old_text":"func a() {}","new_text":"func b() {}"}]}`)
	if got != "Edited main.go: replaced 1 block, at line 3." {
		t.Errorf("edit: %q", got)
	}
	if b, _ := os.ReadFile("main.go"); string(b) != "package main\n\nfunc b() {}\n" {
		t.Errorf("edited file: %q", b)
	}
	if fi, _ := os.Stat("main.go"); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("edit changed the mode to %v", fi.Mode().Perm())
	}
	if len(u.offered) != 1 || !slices.Contains(u.offered[0], "Always allow writes") {
		t.Fatalf("edit approval offered %v", u.offered)
	}

	// Allowed now: a write in the working directory goes unasked, but
	// not one outside it, into a .git directory or of a secret.
	if got := call("write_file", `{"path":"new.txt","content":"x"}`); !strings.HasPrefix(got, "Wrote 1 bytes") || len(u.offered) != 1 {
		t.Errorf("allowed write: %q, asked %d times", got, len(u.offered))
	}
	for _, path := range []string{"~/.zshrc", ".git/hooks/pre-commit", ".env", "../elsewhere.txt"} {
		n := len(u.offered)
		call("write_file", `{"path":"`+path+`","content":"x"}`)
		if len(u.offered) != n+1 {
			t.Errorf("write %s after Always allow writes: not asked", path)
		} else if slices.Contains(u.offered[n], "Always allow writes") {
			t.Errorf("write %s: offered Always allow writes, which would not cover it", path)
		}
	}
	// ~ is the home directory, not a directory named ~.
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err != nil {
		t.Errorf("write ~/.zshrc: %v", err)
	}
	if _, err := os.Stat("~"); err == nil {
		t.Error("write ~/.zshrc created ./~")
	}

	// Failures the model can act on.
	for args, want := range map[string]string{
		`{"path":"main.go","edits":[{"old_text":"nope","new_text":"x"}]}`: "was not found",
		`{"path":"missing.go","edits":[{"old_text":"a","new_text":"b"}]}`: "does not exist; create a new file with write_file",
		`{"path":"","edits":[]}`:                                     "path is required",
		`{"path":"main.go","old_text":"func b","new_text":"func c"}`: "replaced 1 block",
	} {
		if got := call("edit_file", args); !strings.Contains(got, want) {
			t.Errorf("edit %s: %q, want %q", args, got, want)
		}
	}
	if got := call("run_command", `{"command":"  "}`); got != "Error: the command is empty." {
		t.Errorf("empty command: %q", got)
	}
}

func TestAlwaysAllowStaysInItsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh commands")
	}
	outside := t.TempDir()
	t.Chdir(t.TempDir())
	u := &recordingUI{choice: "always"}
	s := &Session{UI: u, Timeout: time.Minute}
	run := func(args string) string {
		return s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: "run_command", Arguments: args}}).Content
	}
	run(`{"command":"ls"}`)
	run(`{"command":"ls"}`)
	if len(u.offered) != 1 {
		t.Fatalf("ls asked %d times, want once", len(u.offered))
	}
	run(`{"command":"ls","working_directory":"` + outside + `"}`)
	if len(u.offered) != 2 {
		t.Fatal("ls in another directory ran unasked after Always allow ls")
	}
	if slices.ContainsFunc(u.offered[1], func(l string) bool { return strings.HasPrefix(l, "Always allow ") }) {
		t.Errorf("ls in another directory offered %v", u.offered[1])
	}
	run(`{"command":"ls","working_directory":"."}`)
	if len(u.offered) != 2 {
		t.Error("ls in the working directory, named: asked again")
	}
}

func TestCommandTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh commands")
	}
	u := &recordingUI{choice: "approve"}
	s := &Session{UI: u, Timeout: time.Minute}
	got := s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{
		Name: "run_command", Arguments: `{"command":"sleep 5","timeout":1}`}}).Content
	if !strings.Contains(got, "timed out after 1s") {
		t.Errorf("result %q", got)
	}
	if !strings.Contains(u.titles[0], "timeout 1s") {
		t.Errorf("title %q does not show the timeout", u.titles[0])
	}
}

func TestReadImage(t *testing.T) {
	t.Chdir(t.TempDir())
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 3)))
	os.WriteFile("shot.png", b.Bytes(), 0o644)
	os.WriteFile("empty.txt", nil, 0o644)

	s := &Session{UI: &recordingUI{}, Timeout: time.Minute}
	read := func(path string) docsgpt.ToolResult {
		return s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: "read_file", Arguments: `{"path":"` + path + `"}`}})
	}
	got := read("shot.png")
	if !strings.Contains(got.Content, "PNG · 2×3") || len(got.Parts) != 1 || got.Parts[0].Type != "image_url" ||
		!strings.HasPrefix(got.Parts[0].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("image read: %q, parts %+v", got.Content, got.Parts)
	}
	if got := read("empty.txt"); got.Content != "(empty file)" || got.Parts != nil {
		t.Errorf("empty file: %+v", got)
	}
}
