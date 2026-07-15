package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func staticLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestExpandEnv(t *testing.T) {
	lookup := staticLookup(map[string]string{"KEY": "abc-123", "EMPTY": ""})
	cases := []struct {
		in, want, wantErr string
	}{
		{"agent: ${KEY}", "agent: abc-123", ""},
		{"no refs here", "no refs here", ""},
		{"price is $5 and $HOME stays", "price is $5 and $HOME stays", ""}, // bare $ untouched
		{"literal $${KEY}", "literal ${KEY}", ""},                          // $$ escape
		{"x: ${EMPTY}!", "x: !", ""},                                       // set-but-empty is set
		{"trailing $", "trailing $", ""},
		{"${KEY}${KEY}", "abc-123abc-123", ""},
		{"a ${MISSING} b", "", "environment variable MISSING is not set"},
		{"bad ${unclosed", "", "unterminated"},
		{"bad ${}", "", "empty ${}"},
	}
	for _, tc := range cases {
		got, err := expandEnv(tc.in, lookup)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want substring %q", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q (%v), want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestParseDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte(`
# comment
export API_KEY=abc-123
QUOTED="hello world"
SINGLE='sq'
SPACED =  padded value
NOEQUALS
=novalue
`), 0o600)

	vars := parseDotEnvFile(path)
	want := map[string]string{
		"API_KEY": "abc-123",
		"QUOTED":  "hello world",
		"SINGLE":  "sq",
		"SPACED":  "padded value",
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
	if len(vars) != len(want) {
		t.Errorf("vars = %v, want exactly %v", vars, want)
	}
	if parseDotEnvFile(filepath.Join(dir, "missing")) != nil {
		t.Error("missing file should yield nil")
	}
}

func TestLoadInterpolatesEnv(t *testing.T) {
	root := t.TempDir()
	// Suite .env provides the key; the OS environment must win over it.
	os.WriteFile(filepath.Join(root, ".env"), []byte("BENCH_KEY=from-dotenv\nWH=https://x/hook/tok\n"), 0o600)
	os.WriteFile(filepath.Join(root, SuiteFileName),
		[]byte("agent: ${BENCH_KEY}\nwebhook_url: ${WH}\n"), 0o644)
	writeCase(t, root, "01-a", minimalCase)

	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Config.Agent != "from-dotenv" || s.Config.WebhookURL != "https://x/hook/tok" {
		t.Errorf("dotenv interpolation: %+v", s.Config)
	}

	t.Setenv("BENCH_KEY", "from-os")
	s, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Config.Agent != "from-os" {
		t.Errorf("OS env should win over .env, got %q", s.Config.Agent)
	}
}

func TestLoadFailsOnUnsetEnvVar(t *testing.T) {
	root := t.TempDir()
	writeCase(t, root, "01-a", "question: hi\nagent: ${DEFINITELY_NOT_SET_ABC}\nexpect:\n  answer: {contains: hi}\n")
	_, err := Load(root)
	if err == nil ||
		!strings.Contains(err.Error(), "DEFINITELY_NOT_SET_ABC is not set") ||
		!strings.Contains(err.Error(), CaseFileName) {
		t.Errorf("want unset-var error naming the variable and file, got %v", err)
	}
}

func TestExpandEnvSkipsCommentLines(t *testing.T) {
	lookup := staticLookup(map[string]string{"KEY": "abc"})
	in := "# example: agent: ${UNSET_IN_COMMENT}\nagent: ${KEY}\n  # indented ${ALSO_UNSET}\n"
	got, err := expandEnv(in, lookup)
	if err != nil {
		t.Fatalf("comment lines must not be expanded: %v", err)
	}
	want := "# example: agent: ${UNSET_IN_COMMENT}\nagent: abc\n  # indented ${ALSO_UNSET}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvErrorNamesLine(t *testing.T) {
	_, err := expandEnv("ok: 1\nagent: ${NOPE_UNSET}\n", staticLookup(nil))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("want line number in error, got %v", err)
	}
}
