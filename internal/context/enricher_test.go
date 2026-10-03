package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

func TestBuild(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	for _, d := range []string{filepath.Join(root, ".git"), filepath.Join(sub, "dir")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < maxEntries+3; i++ {
		os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%02d", i)), nil, 0o644)
	}
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root rules"), 0o644)
	os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("sub rules"), 0o644)
	t.Chdir(sub)
	t.Setenv("SHELL", "/bin/unknown")

	s := config.DefaultConfig().Settings
	s.SendLastCommands = true
	got := Build(s, false)
	for _, want := range []string{
		"<context>\n", "Working directory: " + sub, "Files in it: CLAUDE.md, dir/, f00", "(+5 more)",
		"from ../AGENTS.md:\n<instructions>\nroot rules\n</instructions>\nProject instructions from CLAUDE.md:\n<instructions>\nsub rules",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Build() lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "shell") {
		t.Errorf("Build() mentions shell history it could not read:\n%s", got)
	}
	if strings.Contains(got, "run_command") || strings.Contains(got, "System:") {
		t.Errorf("Build() without tools describes them:\n%s", got)
	}
	if got := Build(config.Settings{}, false); got != "" {
		t.Errorf("Build() with nothing enabled = %q", got)
	}
	// With tools, the system, the shell and the guides go too, even with
	// nothing else enabled.
	got = Build(config.Settings{}, true)
	for _, want := range []string{"System: ", "run_command runs commands with ", "<tools_guide>", "edit_file", "<coding_guide>"} {
		if !strings.Contains(got, want) {
			t.Errorf("Build() with tools lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Working directory") {
		t.Errorf("Build() with tools sends what the settings keep back:\n%s", got)
	}
	if got := Prepend("", "q"); got != "q" {
		t.Errorf("Prepend without context = %q", got)
	}
}
