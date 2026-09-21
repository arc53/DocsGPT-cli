package cmd

import "testing"

// A `go install` build carries no ldflags, so Version would stay "dev" and
// IsReleaseVersion would reject it, permanently disabling update checks.
// resolveVersion recovers it from the build info instead.
func TestResolveVersionIgnoresNonReleases(t *testing.T) {
	// Under `go test` the main module reports "" or "(devel)", neither of
	// which is a release, so resolveVersion must decline to override.
	if got := resolveVersion(); got != "" {
		t.Errorf("resolveVersion() = %q during tests, want \"\"", got)
	}
	// The ldflags-stamped path must win regardless.
	if Version == "" {
		t.Error("Version is empty; it should always carry at least \"dev\"")
	}
}
