//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package ui

// HoldInput is a no-op where the echo cannot be turned off.
func HoldInput() (restore func()) { return func() {} }

// DiscardInput is a no-op here.
func DiscardInput() {}
