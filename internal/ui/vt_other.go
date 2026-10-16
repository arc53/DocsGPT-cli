//go:build !windows

package ui

// EnableVT is a no-op outside Windows, whose terminals always process
// escape sequences.
func EnableVT() {}
