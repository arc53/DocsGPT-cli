package display

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Theme holds semantic styles for all UI elements.
type Theme struct {
	Text      lipgloss.Style
	Muted     lipgloss.Style
	Accent    lipgloss.Style
	Success   lipgloss.Style
	Warn      lipgloss.Style
	Danger    lipgloss.Style
	Info      lipgloss.Style
	Border    lipgloss.Style
	Selection lipgloss.Style
	Reasoning lipgloss.Style
}

// T is the active theme. It starts dark without asking the terminal, so
// display helpers work before InitTheme runs.
var T = newTheme(true)

// darkBackground records the background InitTheme settled on, for the
// markdown style.
var darkBackground = true

// InitTheme initializes the global theme. Mode can be "auto", "dark", or "light".
func InitTheme(mode string) {
	dark := mode != "light"
	if mode == "" || mode == "auto" {
		dark = detectDarkBackground()
	}
	darkBackground = dark
	// Adaptive colors elsewhere must not query the terminal a second time.
	lipgloss.SetHasDarkBackground(dark)
	T = newTheme(dark)
}

// colorless reports whether output must stay free of colors.
func colorless() bool {
	return termenv.ColorProfile() == termenv.Ascii || os.Getenv("NO_COLOR") != ""
}

// UsePlainTheme swaps the active theme for the unstyled one regardless of
// terminal capabilities. Used when output goes to a log file instead of a
// terminal (host service mode).
func UsePlainTheme() { T = plainTheme() }

// plainTheme returns the unstyled (no ANSI) theme.
func plainTheme() *Theme {
	return &Theme{
		Text:      lipgloss.NewStyle(),
		Muted:     lipgloss.NewStyle(),
		Accent:    lipgloss.NewStyle(),
		Success:   lipgloss.NewStyle(),
		Warn:      lipgloss.NewStyle(),
		Danger:    lipgloss.NewStyle(),
		Info:      lipgloss.NewStyle(),
		Border:    lipgloss.NewStyle(),
		Selection: lipgloss.NewStyle().Bold(true),
		Reasoning: lipgloss.NewStyle(),
	}
}

func newTheme(dark bool) *Theme {
	if colorless() {
		return plainTheme()
	}

	if dark {
		return &Theme{
			Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
			Muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
			Accent:    lipgloss.NewStyle().Foreground(lipgloss.Color("133")),            // dark magenta/purple
			Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("78")),             // muted green
			Warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("214")),            // yellow/orange
			Danger:    lipgloss.NewStyle().Foreground(lipgloss.Color("196")),            // red
			Info:      lipgloss.NewStyle().Foreground(lipgloss.Color("183")),            // light purple/lavender
			Border:    lipgloss.NewStyle().Foreground(lipgloss.Color("238")),            // dark gray
			Selection: lipgloss.NewStyle().Foreground(lipgloss.Color("177")).Bold(true), // bright purple
			Reasoning: lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true),
		}
	}

	// Light theme
	return &Theme{
		Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("235")),
		Muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Accent:    lipgloss.NewStyle().Foreground(lipgloss.Color("90")),  // dark magenta
		Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("28")),  // dark green
		Warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("172")), // dark yellow
		Danger:    lipgloss.NewStyle().Foreground(lipgloss.Color("160")), // dark red
		Info:      lipgloss.NewStyle().Foreground(lipgloss.Color("97")),  // muted purple
		Border:    lipgloss.NewStyle().Foreground(lipgloss.Color("250")), // light gray
		Selection: lipgloss.NewStyle().Foreground(lipgloss.Color("90")).Bold(true),
		Reasoning: lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true),
	}
}
