// Package mcp connects the CLI to MCP servers (Model Context Protocol) run
// over stdio, and offers their tools to the agent: the user's servers in
// ~/.docsgpt/mcp.json and the project's in .mcp.json.
package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

// Server is an MCP server the CLI can start: an entry of "mcpServers" in
// the user's file or the project's.
type Server struct {
	Name     string
	Command  string
	Args     []string
	Env      map[string]string // added to the CLI's environment
	Dir      string            // where it runs
	File     string            // the file that defines it
	Project  bool              // the project's: started only once the user approves it
	Disabled bool
	Err      error // why it cannot start: a remote server, a variable not set

	raw     []byte // the entry as the file has it, for approvals
	written entry  // the same, read
}

// entry is a server as the files write it, the shape most MCP clients
// share.
type entry struct {
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Cwd      string            `json:"cwd"`
	URL      string            `json:"url"`
	Disabled bool              `json:"disabled"`
}

// UserFile is the user's own server list.
func UserFile() string { return filepath.Join(config.Dir(), "mcp.json") }

// ProjectFile finds the project's .mcp.json, in cwd or the nearest
// directory above it up to the git root (cwd alone outside a repository),
// or returns "".
func ProjectFile(cwd string) string {
	for d := cwd; ; {
		path := filepath.Join(d, ".mcp.json")
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			return path
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return ""
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// Load reads the user's servers and the project's (which win over the
// user's of the same name), sorted by name. A file that is missing is
// fine; one that cannot be read or parsed is an error.
func Load(cwd string) ([]Server, error) {
	byName := map[string]Server{}
	user, err := read(UserFile(), false, cwd)
	if err != nil {
		return nil, err
	}
	for _, s := range user {
		byName[s.Name] = s
	}
	if path := ProjectFile(cwd); path != "" {
		project, err := read(path, true, cwd)
		if err != nil {
			return nil, err
		}
		for _, s := range project {
			byName[s.Name] = s
		}
	}
	var out []Server
	for _, s := range byName {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Server) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// read parses one file. A project's servers run in the file's directory, the
// user's in cwd, unless an entry says otherwise ("cwd", relative to the file).
func read(path string, project bool, cwd string) ([]Server, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	dir := cwd
	if project {
		dir = filepath.Dir(path)
	}
	var out []Server
	for name, raw := range file.Servers {
		s := Server{Name: name, File: path, Project: project, Dir: dir, raw: raw}
		var e entry
		if err := json.Unmarshal(raw, &e); err != nil {
			s.Err = fmt.Errorf("not a server entry: %w", err)
			out = append(out, s)
			continue
		}
		s.Disabled, s.written = e.Disabled, e
		switch {
		case e.URL != "" || e.Type != "" && e.Type != "stdio":
			s.Err = fmt.Errorf("remote servers (%s) are not supported yet, only commands run over stdio", firstOf(e.Type, "url"))
		case strings.TrimSpace(e.Command) == "":
			s.Err = fmt.Errorf("no command")
		default:
			s.Err = s.expand(e, filepath.Dir(path))
		}
		out = append(out, s)
	}
	return out, nil
}

// expand fills s from e, expanding ${VAR} and ${VAR:-default} in the
// command, its arguments, environment and directory.
func (s *Server) expand(e entry, base string) error {
	var err error
	ex := func(v string) string {
		v, e := expand(v)
		if err == nil {
			err = e
		}
		return v
	}
	s.Command = ex(e.Command)
	for _, a := range e.Args {
		s.Args = append(s.Args, ex(a))
	}
	if len(e.Env) > 0 {
		s.Env = map[string]string{}
		for k, v := range e.Env {
			s.Env[k] = ex(v)
		}
	}
	if e.Cwd != "" {
		dir := ex(e.Cwd)
		if home, herr := os.UserHomeDir(); herr == nil && (dir == "~" || strings.HasPrefix(dir, "~/")) {
			dir = filepath.Join(home, dir[1:])
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		s.Dir = dir
	}
	return err
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expand replaces ${VAR} with the variable's value and ${VAR:-default}
// with it or, when it is unset or empty, the default. A variable that is
// not set and has no default is an error.
func expand(s string) (string, error) {
	var missing []string
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		g := varRe.FindStringSubmatch(m)
		v, ok := os.LookupEnv(g[1])
		switch {
		case g[2] != "" && v == "":
			return g[3]
		case !ok:
			missing = append(missing, g[1])
		}
		return v
	})
	if len(missing) > 0 {
		return out, fmt.Errorf("%s is not set", strings.Join(missing, ", "))
	}
	return out, nil
}

func firstOf(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// CommandLine is the server's command as one line, for the user: as the
// file writes it, so that no secret a variable holds shows.
func (s Server) CommandLine() string {
	command, args := s.written.Command, s.written.Args
	if command == "" {
		command, args = s.Command, s.Args
	}
	parts := []string{quote(command)}
	for _, a := range args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

// Settings are the environment variables and the directory the file sets
// for the server, as it writes them ("NAME=value", "cwd dir").
func (s Server) Settings() []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(s.written.Env)) {
		out = append(out, k+"="+s.written.Env[k])
	}
	if s.written.Cwd != "" {
		out = append(out, "cwd "+s.written.Cwd)
	}
	return out
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$`") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
