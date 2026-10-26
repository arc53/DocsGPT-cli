package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestShellQuotesOnWindows: cmd.exe gets the command line as written, so
// quotes reach it as quotes.
func TestShellQuotesOnWindows(t *testing.T) {
	where := `"` + os.Getenv("SystemRoot") + `\System32\where.exe"`
	for cmd, want := range map[string]string{
		`echo "a b"`:                       `"a b"`,
		where + ` cmd`:                     `cmd.exe`,
		where + ` "cmd"`:                   `cmd.exe`,
		`echo one && echo "two & three"`:   `"two & three"`,
		`set "X=1" && if defined X echo y`: "y",
	} {
		var out strings.Builder
		if err := runCommand(context.Background(), cmd, "", time.Minute, &out); err != nil || !strings.Contains(out.String(), want) {
			t.Errorf("%s: %q, %v; want %q", cmd, out.String(), err, want)
		}
	}
}
