package ui

import (
	"testing"
	"time"
)

func TestSpinnerStartStop(t *testing.T) {
	var s Spinner
	s.Stop() // stopping an idle spinner is a no-op
	s.Start("Thinking…")
	s.Start("Uploading…") // swaps the message
	time.Sleep(2 * spinnerInterval)
	s.Stop()
	s.Stop()
	s.Start("again") // restartable
	s.Stop()
	if s.stop != nil || s.done != nil {
		t.Fatal("spinner still running after Stop")
	}
}
