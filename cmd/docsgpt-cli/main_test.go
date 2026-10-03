package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEarlyThemeInitOrder checks that earlytheme initialises before
// bubbletea, whose init would otherwise query the terminal background.
func TestEarlyThemeInitOrder(t *testing.T) {
	c := exec.Command(os.Args[0], "-test.run=^$")
	c.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	trace := string(out)
	early := strings.Index(trace, "init github.com/arc53/DocsGPT-cli/internal/earlytheme ")
	tea := strings.Index(trace, "init github.com/charmbracelet/bubbletea ")
	if early < 0 || tea < 0 || early > tea {
		t.Errorf("earlytheme must initialise before bubbletea (positions %d, %d)", early, tea)
	}
}
