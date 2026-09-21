package manage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentDoc(name string) string {
	return "apiVersion: docsgpt.arc53.com/v1\nkind: Agent\nmetadata:\n  slug: " + strings.ToLower(name) +
		"\nspec:\n  name: " + name + "\n"
}

func TestSplitYAMLDocuments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "a: 1\n", []string{"a: 1\n"}},
		{"leading marker", "---\na: 1\n", []string{"a: 1\n"}},
		{"two docs", "a: 1\n---\nb: 2\n", []string{"a: 1\n", "b: 2\n"}},
		{"trailing marker and blank docs", "---\na: 1\n---\n\n---\nb: 2\n---\n", []string{"a: 1\n", "b: 2\n"}},
		{"end marker", "a: 1\n...\n---\nb: 2\n", []string{"a: 1\n", "b: 2\n"}},
		{"crlf", "a: 1\r\n---\r\nb: 2\r\n", []string{"a: 1\r\n", "b: 2\r\n"}},
		{"marker with comment", "--- # first\na: 1\n", []string{"a: 1\n"}},
		{"content on the marker line", "--- {a: 1}\n", []string{"{a: 1}\n"}},
		{"directive", "%YAML 1.2\n---\na: 1\n", []string{"a: 1\n"}},
		{
			"dashes inside a block scalar are content",
			"prompt: |\n  intro\n  ---\n  outro\n---\nb: 2\n",
			[]string{"prompt: |\n  intro\n  ---\n  outro\n", "b: 2\n"},
		},
		{"longer dash runs are not markers", "a: 1\n----\n", []string{"a: 1\n----\n"}},
		{"bom", string(rune(0xFEFF)) + "a: 1\n", []string{"a: 1\n"}},
		{"empty", "\n\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitYAMLDocuments(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d docs %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("doc %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseDocuments(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantNames []string
		wantErr   string
	}{
		{"one agent", agentDoc("Support"), []string{"Support"}, ""},
		{"multi-doc with a comment-only doc", agentDoc("A") + "---\n# nothing here\n---\n" + agentDoc("B"), []string{"A", "B"}, ""},
		{"wrong kind names the document", agentDoc("A") + "---\napiVersion: docsgpt.arc53.com/v1\nkind: Tool\nspec: {name: x}\n", nil, `f.yaml#2: unsupported kind "Tool"`},
		{"missing kind", "apiVersion: docsgpt.arc53.com/v1\nspec: {name: x}\n", nil, `unsupported kind ""`},
		{"wrong apiVersion", "apiVersion: v1\nkind: Agent\nspec: {name: x}\n", nil, "unsupported apiVersion"},
		{"missing name", "apiVersion: docsgpt.arc53.com/v1\nkind: Agent\nspec: {}\n", nil, "spec.name is required"},
		{"not a mapping", "- a\n- b\n", nil, "must be a mapping"},
		{"invalid yaml", "kind: [unclosed\n", nil, "invalid YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := ParseDocuments("f.yaml", []byte(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, d := range docs {
				names = append(names, d.Name)
			}
			if strings.Join(names, ",") != strings.Join(tt.wantNames, ",") {
				t.Errorf("names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestParseDocumentsKeepsTextVerbatim(t *testing.T) {
	first := "# keep me\n" + agentDoc("A")
	docs, err := ParseDocuments("all.yaml", []byte(first+"---\n"+agentDoc("B")))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Text != first || docs[1].Text != agentDoc("B") {
		t.Fatalf("docs = %+v", docs)
	}
	if docs[0].Source != "all.yaml#1" || docs[1].Source != "all.yaml#2" || docs[1].Slug != "b" {
		t.Errorf("labels = %q, %q slug %q", docs[0].Source, docs[1].Source, docs[1].Slug)
	}
	single, _ := ParseDocuments("one.yaml", []byte(agentDoc("A")))
	if single[0].Source != "one.yaml" {
		t.Errorf("single-document label = %q", single[0].Source)
	}
}

func TestLoadDocuments(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("agents/b.yml", agentDoc("B"))
	write("agents/a.yaml", agentDoc("A1")+"---\n"+agentDoc("A2"))
	write("agents/notes.txt", "not yaml")
	write("agents/nested/skip.yaml", agentDoc("Nested"))
	single := write("single.yaml", agentDoc("Single"))
	bad := write("bad/tool.yaml", "apiVersion: docsgpt.arc53.com/v1\nkind: Prompt\nspec: {name: p}\n")
	empty := write("empty/readme.md", "x")

	names := func(docs []Document) string {
		var out []string
		for _, d := range docs {
			out = append(out, d.Name)
		}
		return strings.Join(out, ",")
	}

	docs, err := LoadDocuments([]string{single, filepath.Join(dir, "agents"), "-"}, strings.NewReader(agentDoc("Stdin")))
	if err != nil {
		t.Fatal(err)
	}
	// Directory entries are sorted by name and not recursive; -f order is kept.
	if got := names(docs); got != "Single,A1,A2,B,Stdin" {
		t.Errorf("order = %s", got)
	}
	if docs[len(docs)-1].Source != "<stdin>" {
		t.Errorf("stdin label = %q", docs[len(docs)-1].Source)
	}

	errTests := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "no input"},
		{"missing file", []string{filepath.Join(dir, "nope.yaml")}, "no such file"},
		{"non-agent kind in a directory rejects everything", []string{single, filepath.Dir(bad)}, `unsupported kind "Prompt"`},
		{"directory without yaml", []string{filepath.Dir(empty)}, "no *.yaml or *.yml files"},
		{"stdin twice", []string{"-", "-"}, "only be given once"},
		{"empty stdin", []string{"-"}, "no YAML documents"},
	}
	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadDocuments(tt.args, strings.NewReader(""))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
