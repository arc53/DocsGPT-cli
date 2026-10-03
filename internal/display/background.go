package display

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

// bgQueryTimeout bounds the wait for a terminal that ignores the query.
const bgQueryTimeout = 150 * time.Millisecond

// detectDarkBackground reports whether stdout's terminal has a dark
// background: from COLORFGBG when the terminal sets it, else by asking the
// terminal. Dark when it cannot tell, and off a terminal.
func detectDarkBackground() bool {
	if dark, ok := colorFgBgDark(os.Getenv("COLORFGBG")); ok {
		return dark
	}
	if !isatty.IsTerminal(os.Stdout.Fd()) || os.Getenv("TERM") == "dumb" {
		return true
	}
	if dark, ok := parseBackground(queryBackground(bgQueryTimeout)); ok {
		return dark
	}
	return true
}

// colorFgBgDark classifies COLORFGBG ("fg;bg" or "fg;xpm;bg") by the
// background's palette index, like Vim: 0-6 and 8 are dark, 7 and 9-15 light.
func colorFgBgDark(v string) (dark, ok bool) {
	fields := strings.Split(v, ";")
	n, err := strconv.Atoi(strings.TrimSpace(fields[len(fields)-1]))
	if err != nil || n < 0 || n > 15 {
		return false, false
	}
	return n <= 6 || n == 8, true
}

var (
	daReply = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)
	bgReply = regexp.MustCompile(`\x1b\]11;rgba?:([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})`)
)

// parseBackground reads an OSC 11 reply ("rgb:RRRR/GGGG/BBBB") and reports
// whether the color is dark.
func parseBackground(reply string) (dark, ok bool) {
	m := bgReply.FindStringSubmatch(reply)
	if m == nil {
		return false, false
	}
	var luma float64
	for i, weight := range []float64{0.299, 0.587, 0.114} {
		v, _ := strconv.ParseUint(m[i+1], 16, 16)
		luma += weight * float64(v) / float64(uint64(1)<<(4*len(m[i+1]))-1)
	}
	return luma < 0.5, true
}
