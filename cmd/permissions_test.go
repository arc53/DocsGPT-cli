package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/tools"
)

// stdoutOf returns what f prints on stdout.
func stdoutOf(t *testing.T, f func() error) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := f()
	w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPermissionsListAndReset(t *testing.T) {
	isolateConfig(t)
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".git"), 0o755)
	t.Chdir(proj)
	t.Cleanup(func() { permissionsAll = false })

	if got := stdoutOf(t, func() error { return permissionsListCmd.RunE(permissionsListCmd, nil) }); !strings.Contains(got, "Nothing runs without asking in this project") {
		t.Errorf("empty list: %q", got)
	}
	tools.UpdateApprovals(currentProject(), func(a *tools.Approvals) {
		a.Commands, a.MCP, a.Writes = []string{"npm test"}, []string{"laravel-boost/search-docs"}, true
	})
	tools.UpdateApprovals("/elsewhere", func(a *tools.Approvals) { a.Reads = true })
	got := stdoutOf(t, func() error { return permissionsListCmd.RunE(permissionsListCmd, nil) })
	for _, want := range []string{"commands: npm test", "MCP tool: search-docs of laravel-boost", "writes and edits in the project"} {
		if !strings.Contains(got, want) {
			t.Errorf("list lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "elsewhere") {
		t.Errorf("another project listed:\n%s", got)
	}
	if got := stdoutOf(t, func() error { return permissionsResetCmd.RunE(permissionsResetCmd, nil) }); !strings.Contains(got, "Forgot the approvals of 1 project") {
		t.Errorf("reset: %q", got)
	}
	if all, _ := tools.AllApprovals(); len(all) != 1 || all["/elsewhere"].Reads != true {
		t.Errorf("after reset: %+v", all)
	}
}
