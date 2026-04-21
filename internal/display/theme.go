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

// T is the active theme instance. Call InitTheme before using.
var T *Theme

// InitTheme initializes the global theme. Mode can be "auto", "dark", or "light".
func InitTheme(mode string) {
	if mode == "" {
		mode = "auto"
	}

	dark := true
	switch mode {
	case "dark":
		dark = true
	case "light":
		dark = false
	default: // "auto"
		dark = lipgloss.HasDarkBackground()
	}

	T = newTheme(dark)
}

func newTheme(dark bool) *Theme {
	profile := termenv.ColorProfile()

	if profile == termenv.Ascii || os.Getenv("NO_COLOR") != "" {
		// No color support — return unstyled theme
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

	if dark {
		return &Theme{
			Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
			Muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
			Accent:    lipgloss.NewStyle().Foreground(lipgloss.Color("133")), // dark magenta/purple
			Success:   lipgloss.NewStyle().Foreground(lipgloss.Color("78")),  // muted green
			Warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("214")), // yellow/orange
			Danger:    lipgloss.NewStyle().Foreground(lipgloss.Color("196")), // red
			Info:      lipgloss.NewStyle().Foreground(lipgloss.Color("183")), // light purple/lavender
			Border:    lipgloss.NewStyle().Foreground(lipgloss.Color("238")), // dark gray
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
