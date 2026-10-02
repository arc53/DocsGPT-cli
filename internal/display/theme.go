package display

import (
	"os"

	"github.com/arc53/DocsGPT-cli/internal/ui"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// tone is one palette color: truecolor hex with xterm-256 and 16-color
// fallbacks, for dark and light backgrounds. An empty 16-color value keeps
// the terminal's default foreground.
func tone(darkHex, dark256, lightHex, light256, ansi string) lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Dark:  lipgloss.CompleteColor{TrueColor: darkHex, ANSI256: dark256, ANSI: ansi},
		Light: lipgloss.CompleteColor{TrueColor: lightHex, ANSI256: light256, ANSI: ansi},
	}
}

// The palette: pi's OKHSL tones, so every hue sits at the same perceived
// lightness (67% on dark backgrounds, 46% on light ones).
var (
	colText     = tone("#dee0e1", "254", "#3b3f41", "237", "")
	colAccent   = tone("#a798d7", "140", "#7459b4", "61", "5")
	colMuted    = tone("#9da5a9", "248", "#677176", "242", "8")
	colDim      = tone("#7e888e", "102", "#879095", "245", "8")
	colSuccess  = tone("#68b78d", "72", "#337e58", "29", "2")
	colWarning  = tone("#cd9a22", "172", "#8f6802", "94", "3")
	colError    = tone("#ea7f81", "174", "#c8253d", "160", "1")
	colLink     = tone("#69add0", "74", "#2f7899", "31", "4")
	colThinking = tone("#96a0a4", "247", "#7c868c", "102", "8")
	colString   = tone("#de8d5a", "173", "#a45417", "130", "3")
	colVariable = tone("#5db3ba", "73", "#287a81", "30", "6")
	colUserBg   = lipgloss.CompleteAdaptiveColor{
		Dark:  lipgloss.CompleteColor{TrueColor: "#213b49", ANSI256: "236", ANSI: "8"},
		Light: lipgloss.CompleteColor{TrueColor: "#dfe7ec", ANSI256: "254", ANSI: "7"},
	}
	// Tool block backgrounds (running, succeeded, failed): pi's, a step
	// fainter (OKHSL lightness 20% dark, 94% light) so they sit below the
	// user's message. No 16-color value: those blocks have no background.
	colToolBg     = tone("#2b2f30", "236", "#edeeef", "255", "")
	colToolOkBg   = tone("#243229", "22", "#e9f0eb", "194", "")
	colToolFailBg = tone("#3e2727", "52", "#f4eceb", "224", "")
)

// Theme holds the semantic styles every UI element draws with.
type Theme struct {
	Text       lipgloss.Style
	Accent     lipgloss.Style // prompts, headings, the selected item
	Muted      lipgloss.Style // secondary text, tool output
	Dim        lipgloss.Style // chrome, hints, metadata
	Success    lipgloss.Style
	Warning    lipgloss.Style
	Error      lipgloss.Style
	Link       lipgloss.Style
	Thinking   lipgloss.Style // reasoning tokens
	ToolTitle  lipgloss.Style // "$ command", "read path"
	ToolOutput lipgloss.Style
	ToolBg     bool // tool blocks sit on colToolBg and friends (on a terminal)
}

// T is the active theme. It starts dark without asking the terminal, so
// display helpers work before InitTheme runs.
var T = func() *Theme {
	lipgloss.SetHasDarkBackground(true)
	return newTheme()
}()

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
	T = newTheme()
}

// DarkBackground reports the background the theme was set up for.
func DarkBackground() bool { return darkBackground }

// Colorless reports whether output must stay free of colors.
func Colorless() bool {
	return termenv.ColorProfile() == termenv.Ascii || os.Getenv("NO_COLOR") != ""
}

// UsePlainTheme swaps the active theme for the unstyled one regardless of
// terminal capabilities. Used when output goes to a log file instead of a
// terminal (host service mode).
func UsePlainTheme() { T = plainTheme() }

// plainTheme returns the unstyled (no ANSI) theme.
func plainTheme() *Theme {
	none := lipgloss.NoColor{}
	ui.Colors = ui.Palette{Accent: none, Muted: none, Dim: none, Success: none, Error: none, Border: none}
	s := lipgloss.NewStyle()
	return &Theme{
		Text: s, Accent: s, Muted: s, Dim: s, Success: s, Warning: s, Error: s, Link: s,
		Thinking: s, ToolTitle: s.Bold(true), ToolOutput: s,
	}
}

func newTheme() *Theme {
	if Colorless() {
		return plainTheme()
	}
	ui.Colors = ui.Palette{
		Accent: colAccent, Muted: colMuted, Dim: colDim,
		Success: colSuccess, Error: colError, Border: colDim,
	}
	fg := func(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	return &Theme{
		Text:       fg(colText),
		Accent:     fg(colAccent),
		Muted:      fg(colMuted),
		Dim:        fg(colDim),
		Success:    fg(colSuccess),
		Warning:    fg(colWarning),
		Error:      fg(colError),
		Link:       fg(colLink),
		Thinking:   fg(colThinking).Italic(true),
		ToolTitle:  lipgloss.NewStyle().Bold(true),
		ToolOutput: fg(colMuted),
		ToolBg:     true,
	}
}

// colorCode returns c as glamour takes it: the value for the active
// background and color profile ("" for none).
func colorCode(c lipgloss.CompleteAdaptiveColor) string {
	cc := c.Light
	if darkBackground {
		cc = c.Dark
	}
	switch termenv.ColorProfile() {
	case termenv.TrueColor:
		return cc.TrueColor
	case termenv.ANSI256:
		return cc.ANSI256
	case termenv.ANSI:
		return cc.ANSI
	}
	return ""
}
