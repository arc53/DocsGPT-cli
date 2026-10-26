package host

import (
	"strings"
	"testing"
	"time"
)

// TestShellQuotesOnWindows: cmd.exe gets the command line as written, so
// quotes reach it as quotes.
func TestShellQuotesOnWindows(t *testing.T) {
	var out, errOut strings.Builder
	r := runCommand(`echo "a b" && echo "c & d"`, "", time.Minute, time.Second, nil, nil, &out, &errOut, func(int) {})
	if r.err != nil || r.exitCode != 0 || !strings.Contains(out.String(), `"a b"`) || !strings.Contains(out.String(), `"c & d"`) {
		t.Fatalf("%+v: %q %q", r, out.String(), errOut.String())
	}
}
