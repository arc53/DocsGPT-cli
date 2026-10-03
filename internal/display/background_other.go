//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package display

import "time"

// queryBackground is not supported here; see background_unix.go.
func queryBackground(time.Duration) string { return "" }
