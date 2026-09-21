package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// On macOS, bash reads only the FIRST of .bash_profile, .bash_login and
// .profile that exists, so creating .bash_profile beside someone's .profile
// would stop that file being read at all.
func TestBashProfilePrefersAnExistingFile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		// bashProfile only consults the filesystem on darwin; elsewhere the
		// answer is always .bashrc.
		if got := bashProfile(t.TempDir()); got != ".bashrc" {
			t.Errorf("bashProfile() = %q, want .bashrc", got)
		}
		return
	}

	for _, existing := range []string{".bash_profile", ".bash_login", ".profile"} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, existing), []byte("# mine\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := bashProfile(home); got != existing {
			t.Errorf("with %s present, bashProfile() = %q, want %q", existing, got, existing)
		}
	}

	// Nothing to shadow: creating .bash_profile is correct.
	if got := bashProfile(t.TempDir()); got != ".bash_profile" {
		t.Errorf("bashProfile() on an empty home = %q, want .bash_profile", got)
	}
}
