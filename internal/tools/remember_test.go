package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// TestApprovalsKeptForTheProject: "Always allow" holds in later sessions of
// the same project, not in another one; "Always approve" too.
func TestApprovalsKeptForTheProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".git"), 0o755)
	os.MkdirAll(filepath.Join(proj, "sub"), 0o755)
	t.Chdir(filepath.Join(proj, "sub"))
	cwd, _ := os.Getwd()
	root := ProjectRoot(cwd)
	if real, _ := filepath.EvalSymlinks(proj); root != real {
		t.Fatalf("ProjectRoot = %s, want %s", root, real)
	}

	session := func(project, choice string) (*Session, *recordingUI) {
		u := &recordingUI{choice: choice}
		s := &Session{UI: u, Timeout: time.Minute, Project: project}
		if _, err := s.LoadApprovals(); err != nil {
			t.Fatal(err)
		}
		return s, u
	}
	run := func(s *Session, cmd string) {
		s.Handle(context.Background(), func() {}, docsgpt.ToolCall{Function: docsgpt.FunctionCall{Name: "run_command", Arguments: `{"command":"` + cmd + `"}`}})
	}

	s, u := session(root, "always")
	run(s, "git status")
	if len(u.offered) != 1 || !slices.Contains(u.offered[0], "Always allow git status") {
		t.Fatalf("offered %v", u.offered)
	}
	s, u = session(root, "deny") // a later chat here
	run(s, "git status")
	if len(u.offered) != 0 {
		t.Errorf("asked again in the same project: %v", u.offered)
	}
	other := t.TempDir()
	s, u = session(other, "deny") // another project
	run(s, "git status")
	if len(u.offered) != 1 {
		t.Errorf("another project did not ask")
	}

	s, _ = session(root, "all")
	run(s, "git log")
	if all, _ := (&Session{Project: root}).LoadApprovals(); !all {
		t.Error("Always approve was not kept")
	}
	a, _ := ProjectApprovals(root)
	if want := []string{"every tool call (always approve)", "commands: git status"}; !slices.Equal(a.Describe(), want) {
		t.Errorf("Describe = %q", a.Describe())
	}
	s, _ = session(root, "deny")
	if err := s.Forget("every tool call (always approve)"); err != nil || s.AutoApprove {
		t.Fatalf("Forget: %v, auto %v", err, s.AutoApprove)
	}
	if a, _ := ProjectApprovals(root); a.ApproveAll || len(a.Commands) != 1 {
		t.Errorf("after Forget: %+v", a)
	}
	if fi, err := os.Stat(PermissionsFile()); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions file: %v %v", fi, err)
	}
	if err := s.ForgetApprovals(false); err != nil {
		t.Fatal(err)
	}
	if all, _ := AllApprovals(); len(all) != 0 {
		t.Errorf("after ForgetApprovals: %v", all)
	}
}
