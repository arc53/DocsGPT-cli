package display

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitBranch(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "a", "b")
	os.MkdirAll(sub, 0o755)
	os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "wt"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/feat/x\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".git", "worktrees", "wt", "HEAD"), []byte("0123456789abcdef\n"), 0o644)
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n"), 0o644)

	for dir, want := range map[string]string{sub: "feat/x", wt: "0123456", t.TempDir(): ""} {
		if got := gitBranch(dir); got != want {
			t.Errorf("gitBranch(%s) = %q, want %q", dir, got, want)
		}
	}
}
