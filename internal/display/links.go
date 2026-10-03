package display

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rivo/uniseg"
)

// Links in answers are clickable (OSC 8) where the terminal supports it:
// the text shows, the URL does not. Elsewhere the URL follows the text,
// dim, as "text (url)". The sequences are ours only: the model's text has
// lost its own (StripControls) before it is rendered, and a link is made
// only from a parsed link destination that linkURL accepts.

// SetHyperlinks sets the hyperlinks setting: "on", "off" or "auto" (or
// ""), which the DOCSGPT_HYPERLINKS environment variable overrides.
func SetHyperlinks(mode string) {
	links.mu.Lock()
	defer links.mu.Unlock()
	links.setting, links.known = strings.ToLower(strings.TrimSpace(mode)), false
}

var links struct {
	mu      sync.Mutex
	setting string
	known   bool
	on      bool
}

// hyperlinkMode is the setting in effect: "on", "off" or "auto".
func hyperlinkMode() string {
	links.mu.Lock()
	setting := links.setting
	links.mu.Unlock()
	for _, v := range []string{os.Getenv("DOCSGPT_HYPERLINKS"), setting} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "on", "true", "yes":
			return "on"
		case "0", "off", "false", "no":
			return "off"
		}
	}
	return "auto"
}

// hyperlinks reports whether links are made clickable: per the setting, or
// on "auto" when the terminal is one known to support them (pi's list).
func hyperlinks() bool {
	switch hyperlinkMode() {
	case "on":
		return true
	case "off":
		return false
	}
	links.mu.Lock()
	defer links.mu.Unlock()
	if !links.known {
		links.on, links.known = detectHyperlinks(), true
	}
	return links.on
}

func detectHyperlinks() bool {
	env := func(k string) string { return strings.ToLower(os.Getenv(k)) }
	term, program := env("TERM"), env("TERM_PROGRAM")
	switch {
	case term == "dumb":
		return false
	case os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux"):
		return tmuxHyperlinks()
	case strings.HasPrefix(term, "screen"):
		return false
	case os.Getenv("KITTY_WINDOW_ID") != "" || program == "kitty" || term == "xterm-kitty",
		program == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "",
		os.Getenv("WEZTERM_PANE") != "" || program == "wezterm",
		program == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "",
		os.Getenv("ITERM_SESSION_ID") != "" || program == "iterm.app" || env("LC_TERMINAL") == "iterm2",
		os.Getenv("WT_SESSION") != "",
		program == "alacritty" || program == "vscode" || program == "zed":
		return true
	}
	return false // Terminal.app, JetBrains, unknown: the URL would vanish
}

// tmuxHyperlinks asks tmux whether it passes hyperlinks on to the terminal
// it runs in (its terminal-features).
func tmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}").Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), ",") {
		if strings.TrimSpace(f) == "hyperlinks" {
			return true
		}
	}
	return false
}

// linkURL returns u as it can go into an OSC 8 sequence: an http(s) or
// mailto URL of printable ASCII only (non-ASCII escaped), so it can neither
// end the sequence early nor be a javascript: or file: link. ok is false
// for any other.
func linkURL(u string) (string, bool) {
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "mailto:") {
		return "", false
	}
	if !printableASCII(u) {
		p, err := url.Parse(u)
		if err != nil {
			return "", false
		}
		if u = p.String(); !printableASCII(u) { // a host of other scripts
			return "", false
		}
	}
	return u, true
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] >= 0x7f {
			return false
		}
	}
	return s != ""
}

// openLink and closeLink are OSC 8 sequences, ended by ST.
func openLink(u string) string { return "\x1b]8;;" + u + "\x1b\\" }

const closeLink = "\x1b]8;;\x1b\\"

// relink makes the hyperlinks of a row of text its own: each one opened
// right before its first character and closed after its last, the row
// ending with none open. The row may hold sequences from the rest of its
// line (ansi.Cut keeps them all), and lose others to a wrap; what decides
// is which link each character shows under.
func relink(row string) string {
	if !strings.Contains(row, "\x1b]8;") {
		return row
	}
	var b strings.Builder
	active, shown := "", ""
	for i := 0; i < len(row); {
		if n := escLen(row[i:]); n > 0 {
			seq := row[i : i+n]
			if u, ok := linkSeq(seq); ok {
				active = u
			} else {
				b.WriteString(seq)
			}
			i += n
			continue
		}
		g, _, _, _ := uniseg.FirstGraphemeClusterInString(row[i:], -1)
		if active != shown {
			if shown != "" {
				b.WriteString(closeLink)
			}
			if active != "" {
				b.WriteString(openLink(active))
			}
			shown = active
		}
		b.WriteString(g)
		i += len(g)
	}
	if shown != "" {
		b.WriteString(closeLink)
	}
	return b.String()
}

// escLen is the length of the escape sequence s starts with, 0 for none:
// CSI to its final byte, OSC to BEL or ST, others two bytes.
func escLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 0
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	return 2
}

// linkSeq reports whether seq is an OSC 8 sequence, and its URL ("" ends
// a link).
func linkSeq(seq string) (string, bool) {
	rest, ok := strings.CutPrefix(seq, "\x1b]8;")
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "\x07"), "\x1b\\")
	_, u, _ := strings.Cut(rest, ";")
	return u, true
}
