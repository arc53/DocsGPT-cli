package tools

import (
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// maxSleep is the longest sleep run_command runs as a command of its own.
const maxSleep = 10 * time.Second

// detaches says how command would leave something running after it
// returns, out of reach of the process group that is stopped with the chat
// (a trailing &, nohup, setsid, disown, cmd's start, Start-Process), or ""
// when it would not. "cmd & wait" waits for it, so it does not.
func detaches(command string) string {
	if runtime.GOOS == "windows" {
		if strings.Contains(strings.ToLower(command), "start-process") {
			return "Start-Process"
		}
		for _, seg := range segments(command, true) {
			if len(seg.words) > 0 && strings.EqualFold(seg.words[0], "start") {
				return "start"
			}
		}
		return ""
	}
	segs := segments(command, false)
	for _, seg := range segs {
		if len(seg.words) > 0 && slices.Contains([]string{"nohup", "setsid", "disown"}, seg.words[0]) {
			return seg.words[0]
		}
	}
	for _, seg := range segs {
		if seg.background && !slices.ContainsFunc(segs, func(s segment) bool { return len(s.words) > 0 && s.words[0] == "wait" }) {
			return "&"
		}
	}
	return ""
}

// longSleep returns how long command sleeps when it starts with a sleep of
// its own (sleep 60; sleep 60 && curl …; Windows: timeout /t 60,
// Start-Sleep 60) longer than maxSleep, else 0. A sleep inside a loop that
// waits for something is fine.
func longSleep(command string) time.Duration {
	segs := segments(command, runtime.GOOS == "windows")
	if len(segs) == 0 || len(segs[0].words) < 2 {
		return 0
	}
	w := segs[0].words
	var d time.Duration
	switch name := strings.ToLower(strings.TrimSuffix(w[0], ".exe")); {
	case name == "sleep" && runtime.GOOS != "windows":
		for _, a := range w[1:] {
			d += sleepArg(a)
		}
	case name == "timeout" && runtime.GOOS == "windows":
		for _, a := range w[1:] {
			if n, err := strconv.Atoi(a); err == nil {
				d = time.Duration(n) * time.Second
			}
		}
	case name == "start-sleep", name == "powershell", name == "pwsh":
		// Start-Sleep [-Seconds|-Milliseconds] n, alone or as powershell's
		// command, which one quoted word may hold.
		w = strings.Fields(strings.Join(w, " "))
		at := slices.IndexFunc(w, func(s string) bool { return strings.EqualFold(strings.Trim(s, `"'`), "start-sleep") })
		unit := time.Second
		for _, a := range w[max(at, 0)+1:] {
			a = strings.ToLower(strings.Trim(a, `"'`))
			if strings.HasPrefix(a, "-m") {
				unit = time.Millisecond
			}
			if n, err := strconv.ParseFloat(a, 64); err == nil && at >= 0 {
				d = time.Duration(n * float64(unit))
				break
			}
		}
	}
	if d > maxSleep {
		return d
	}
	return 0
}

var sleepArgRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)([smhd]?)$`)

// sleepArg is one argument of sleep (5, 1.5m, 2h) as a duration.
func sleepArg(a string) time.Duration {
	m := sleepArgRe.FindStringSubmatch(a)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	unit := map[string]time.Duration{"": time.Second, "s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	return time.Duration(n * float64(unit))
}

// segment is one simple command of a command line: its words, leading
// VAR=value assignments left out, and whether a lone & ends it (it runs
// in the background).
type segment struct {
	words      []string
	background bool
}

// segments splits a command line into its simple commands at ; & && || |
// ( ) and new lines (cmd.exe: & && || |), outside quotes. It is a rough
// reading, enough to find the first word of each command.
func segments(command string, windows bool) []segment {
	var (
		segs  []segment
		cur   segment
		word  strings.Builder
		in    bool
		quote rune
	)
	endWord := func() {
		if !in {
			return
		}
		w := word.String()
		word.Reset()
		in = false
		if len(cur.words) == 0 && !windows && strings.Contains(w, "=") && !strings.HasPrefix(w, "=") {
			return // VAR=value before the program
		}
		cur.words = append(cur.words, w)
	}
	endSeg := func(background bool) {
		endWord()
		cur.background = background
		if len(cur.words) > 0 || background {
			segs = append(segs, cur)
		}
		cur = segment{}
	}
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && !windows && next != 0 {
				word.WriteRune(next)
				i++
			} else {
				word.WriteRune(r)
			}
		case r == '\'' && !windows, r == '"':
			quote, in = r, true
		case r == '\\' && !windows && next != 0:
			word.WriteRune(next)
			in = true
			i++
		case r == ' ' || r == '\t':
			endWord()
		case r == '\n' || r == ';' && !windows || r == '(' && !windows || r == ')' && !windows:
			endSeg(false)
		case r == '|':
			if next == '|' {
				i++
			}
			endSeg(false)
		case r == '&':
			prev := rune(0)
			if ws := []rune(word.String()); len(ws) > 0 {
				prev = ws[len(ws)-1]
			}
			switch {
			case next == '&':
				i++
				endSeg(false)
			case windows:
				endSeg(false)
			case next == '>' || prev == '>' || prev == '<': // &>file, 2>&1, <&3
				word.WriteRune(r)
				in = true
			default:
				endSeg(true)
			}
		default:
			word.WriteRune(r)
			in = true
		}
	}
	endSeg(false)
	return segs
}
