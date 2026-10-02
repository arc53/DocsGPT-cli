package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 80 * time.Millisecond

// Spinner animates "⠋ message" on one stderr line until Stop clears it. It
// is not a bubbletea program, so it leaves stdin alone. The zero value is
// ready; it does nothing when stderr is not a terminal.
type Spinner struct {
	mu   sync.Mutex
	msg  string
	stop chan struct{}
	done chan struct{}
}

// Start shows the spinner with msg, or just swaps the message if it is
// already running.
func (s *Spinner) Start(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msg = msg
	if s.stop != nil || !term.IsTerminal(os.Stderr.Fd()) {
		return
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go s.loop(os.Stderr, s.stop, s.done)
}

// Stop clears the spinner line and returns once it is gone, so output that
// follows lands on a clean line.
func (s *Spinner) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

func (s *Spinner) loop(f *os.File, stop, done chan struct{}) {
	defer close(done)
	r := lipgloss.NewRenderer(f)
	r.SetHasDarkBackground(lipgloss.HasDarkBackground())
	accent, muted := r.NewStyle().Foreground(Colors.Accent), r.NewStyle().Foreground(Colors.Muted)
	t := time.NewTicker(spinnerInterval)
	defer t.Stop()
	io.WriteString(f, ansi.HideCursor)
	for i := 0; ; i++ {
		s.mu.Lock()
		msg := s.msg
		s.mu.Unlock()
		width := 80
		if w, _, err := term.GetSize(f.Fd()); err == nil {
			width = w
		}
		msg = ansi.Truncate(msg, max(0, width-3), "…")
		fmt.Fprintf(f, "\r%s %s%s", accent.Render(spinnerFrames[i%len(spinnerFrames)]), muted.Render(msg), ansi.EraseLineRight)
		select {
		case <-stop:
			io.WriteString(f, "\r"+ansi.EraseEntireLine+ansi.ShowCursor)
			return
		case <-t.C:
		}
	}
}
