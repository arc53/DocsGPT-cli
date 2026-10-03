package tools

import (
	"testing"
	"time"
)

func TestTimeoutText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Second: "1s", 45 * time.Second: "45s", 2 * time.Minute: "2m", 90 * time.Second: "1m30s",
		time.Hour: "1h", 61 * time.Minute: "1h1m", 3601 * time.Second: "1h0m1s",
	} {
		if got := timeoutText(d); got != want {
			t.Errorf("timeoutText(%v) = %q, want %q", d, got, want)
		}
	}
}
