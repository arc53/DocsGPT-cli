package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

// Approvals are the "Always allow" and "Always approve" choices kept for a
// project in ~/.docsgpt/permissions.json, never in the project itself: a
// repository cannot approve anything for whoever clones it.
type Approvals struct {
	Commands   []string  `json:"commands,omitempty"` // alwaysKey keys
	MCP        []string  `json:"mcp,omitempty"`      // a server, or server/tool
	Writes     bool      `json:"writes,omitempty"`
	Reads      bool      `json:"reads,omitempty"`
	ApproveAll bool      `json:"approve_all,omitempty"`
	Updated    time.Time `json:"updated"`
}

// Empty reports whether nothing is kept.
func (a Approvals) Empty() bool {
	return len(a.Commands) == 0 && len(a.MCP) == 0 && !a.Writes && !a.Reads && !a.ApproveAll
}

// PermissionsFile is where the approvals of every project are kept.
func PermissionsFile() string { return filepath.Join(config.Dir(), "permissions.json") }

// ProjectRoot is the project dir belongs to: the nearest directory with a
// .git, else dir itself, symlinks resolved.
func ProjectRoot(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

func readPermissions() (map[string]Approvals, error) {
	all := map[string]Approvals{}
	data, err := os.ReadFile(PermissionsFile())
	if os.IsNotExist(err) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", PermissionsFile(), err)
	}
	return all, nil
}

// ProjectApprovals returns what is kept for project.
func ProjectApprovals(project string) (Approvals, error) {
	all, err := readPermissions()
	return all[project], err
}

// AllApprovals returns what is kept for every project.
func AllApprovals() (map[string]Approvals, error) { return readPermissions() }

// UpdateApprovals changes what is kept for project with change, read
// afresh (another chat may have added to it) and written atomically, 0600.
func UpdateApprovals(project string, change func(a *Approvals)) error {
	all, err := readPermissions()
	if err != nil {
		return err
	}
	a := all[project]
	change(&a)
	slices.Sort(a.Commands)
	a.Commands = slices.Compact(a.Commands)
	slices.Sort(a.MCP)
	a.MCP = slices.Compact(a.MCP)
	if a.Empty() {
		delete(all, project)
	} else {
		a.Updated = time.Now().UTC()
		all[project] = a
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	path := PermissionsFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".permissions-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Describe lists what is kept, a line each.
func (a Approvals) Describe() []string {
	var out []string
	if a.ApproveAll {
		out = append(out, "every tool call (always approve)")
	}
	for _, c := range a.Commands {
		out = append(out, "commands: "+c)
	}
	for _, m := range a.MCP {
		if server, tool, ok := strings.Cut(m, "/"); ok {
			out = append(out, "MCP tool: "+tool+" of "+server)
		} else {
			out = append(out, "MCP server: every tool of "+m)
		}
	}
	if a.Writes {
		out = append(out, "writes and edits in the project")
	}
	if a.Reads {
		out = append(out, "reads of any file")
	}
	return out
}

// LoadApprovals takes what is kept for the session's project: what it
// allows runs without asking. It reports whether every call is approved
// that way, for the chat to say so.
func (s *Session) LoadApprovals() (approveAll bool, err error) {
	if s.Project == "" {
		return false, nil
	}
	a, err := ProjectApprovals(s.Project)
	if err != nil {
		return false, err
	}
	if s.allowed == nil {
		s.allowed = map[string]bool{}
	}
	for _, c := range a.Commands {
		s.allowed[c] = true
	}
	if s.allowedMCP == nil {
		s.allowedMCP = map[string]bool{}
	}
	for _, m := range a.MCP {
		s.allowedMCP[m] = true
	}
	s.allowWrites = s.allowWrites || a.Writes
	s.allowReads = s.allowReads || a.Reads
	if a.ApproveAll {
		s.AutoApprove = true
	}
	return a.ApproveAll, nil
}

// keep saves a choice for the session's project; a failure is shown.
func (s *Session) keep(change func(a *Approvals)) {
	if s.Project == "" {
		return
	}
	if err := UpdateApprovals(s.Project, change); err != nil {
		s.ui().Open("approval not saved", "")
		s.ui().Close(false, err.Error())
	}
}

// SetApproveAll turns AutoApprove on or off, and keeps that for the
// project (/approve).
func (s *Session) SetApproveAll(on bool) error {
	s.AutoApprove = on
	if s.Project == "" {
		return nil
	}
	return UpdateApprovals(s.Project, func(a *Approvals) { a.ApproveAll = on })
}

// ForgetApprovals drops what the session allowed and what is kept for its
// project; AutoApprove stays as the flag (--auto-approve) set it, given.
func (s *Session) ForgetApprovals(flag bool) error {
	s.allowed, s.allowedMCP = nil, nil
	s.allowWrites, s.allowReads, s.AutoApprove = false, false, flag
	if s.Project == "" {
		return nil
	}
	return UpdateApprovals(s.Project, func(a *Approvals) { *a = Approvals{} })
}

// Forget drops one kept approval, as Describe names it.
func (s *Session) Forget(line string) error {
	switch {
	case line == "every tool call (always approve)":
		s.AutoApprove = false
	case strings.HasPrefix(line, "commands: "):
		delete(s.allowed, strings.TrimPrefix(line, "commands: "))
	case strings.HasPrefix(line, "MCP tool: "):
		tool, server, _ := strings.Cut(strings.TrimPrefix(line, "MCP tool: "), " of ")
		delete(s.allowedMCP, server+"/"+tool)
	case strings.HasPrefix(line, "MCP server: every tool of "):
		delete(s.allowedMCP, strings.TrimPrefix(line, "MCP server: every tool of "))
	case line == "writes and edits in the project":
		s.allowWrites = false
	case line == "reads of any file":
		s.allowReads = false
	}
	if s.Project == "" {
		return nil
	}
	return UpdateApprovals(s.Project, func(a *Approvals) { a.forget(line) })
}

func (a *Approvals) forget(line string) {
	switch {
	case line == "every tool call (always approve)":
		a.ApproveAll = false
	case strings.HasPrefix(line, "commands: "):
		a.Commands = slices.DeleteFunc(a.Commands, func(c string) bool { return c == strings.TrimPrefix(line, "commands: ") })
	case strings.HasPrefix(line, "MCP tool: "):
		tool, server, _ := strings.Cut(strings.TrimPrefix(line, "MCP tool: "), " of ")
		a.MCP = slices.DeleteFunc(a.MCP, func(m string) bool { return m == server+"/"+tool })
	case strings.HasPrefix(line, "MCP server: every tool of "):
		a.MCP = slices.DeleteFunc(a.MCP, func(m string) bool { return m == strings.TrimPrefix(line, "MCP server: every tool of ") })
	case line == "writes and edits in the project":
		a.Writes = false
	case line == "reads of any file":
		a.Reads = false
	}
}
