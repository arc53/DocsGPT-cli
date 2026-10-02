// Package earlytheme fixes lipgloss's background before bubbletea's init
// asks the terminal for it (an OSC 11 query that stalls every start for 5s
// in a terminal that never answers). Go initialises packages in import-path
// order where dependencies allow, and this one only needs lipgloss, so it
// runs before github.com/charmbracelet/bubbletea. display.InitTheme sets the
// real value later.
package earlytheme

import "github.com/charmbracelet/lipgloss"

func init() { lipgloss.SetHasDarkBackground(true) }
