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

func TestTokens(t *testing.T) {
	for n, want := range map[int]string{0: "0", 950: "950", 1000: "1k", 1234: "1.2k", 12_345: "12k", 999_499: "999k", 999_999: "1M", 1_500_000: "1.5M", 25_000_000: "25M"} {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", n, got, want)
		}
	}
	if got := TokenUsage(0, 0, 0, 0); got != "" {
		t.Errorf("no usage: %q", got)
	}
	if got := TokenUsage(2100, 45, 2100, 45); got != "↑2.1k ↓45" {
		t.Errorf("one exchange: %q", got)
	}
	if got := TokenUsage(2100, 45, 12_000, 3400); got != "↑2.1k ↓45 (Σ ↑12k ↓3.4k)" {
		t.Errorf("more: %q", got)
	}
}

func TestWindowTitle(t *testing.T) {
	for _, c := range []struct{ title, cwd, want string }{
		{"", "/home/me/repo", "docsgpt · repo"},
		{"How do I\n deploy?", "/home/me/repo", "docsgpt · How do I deploy? · repo"},
		{"\x1b]0;evil\x07ok", "/", "docsgpt · ok"},
		{"a very long first question that goes on and on and on", "/r", "docsgpt · a very long first question that goes on… · r"},
	} {
		if got := WindowTitle(c.title, c.cwd); got != c.want {
			t.Errorf("WindowTitle(%q, %q) = %q, want %q", c.title, c.cwd, got, c.want)
		}
	}
}
