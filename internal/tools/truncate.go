package tools

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// What a tool sends back to the model at most (pi's limits).
const (
	maxOutputLines = 2000
	maxOutputBytes = 50 * 1024
)

// truncateTail keeps the end of a command's output within maxOutputLines
// and maxOutputBytes, cut at a line start (or, for one huge line, a rune
// start), and says what was dropped. dropped counts lines the capture
// already let go.
func truncateTail(output string, dropped int) string {
	lines := strings.SplitAfter(output, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := dropped + len(lines)
	keep, size := 0, 0
	for keep < len(lines) && keep < maxOutputLines {
		l := lines[len(lines)-1-keep]
		if size+len(l) > maxOutputBytes {
			break
		}
		size += len(l)
		keep++
	}
	if keep == len(lines) && dropped == 0 {
		return output
	}
	if keep == 0 && len(lines) > 0 { // the last line alone is too long: keep its end
		last := lines[len(lines)-1]
		cut := len(last) - maxOutputBytes
		for cut < len(last) && !utf8.RuneStart(last[cut]) {
			cut++
		}
		return fmt.Sprintf("[Output truncated: showing the last %d bytes of line %d.]\n%s", len(last)-cut, total, last[cut:])
	}
	return fmt.Sprintf("[Output truncated: showing the last %d of %d lines.]\n%s",
		keep, total, strings.Join(lines[len(lines)-keep:], ""))
}

// tailBuffer captures a command's output, letting go of the front once it
// is far past what truncateTail would keep.
type tailBuffer struct {
	buf     []byte
	dropped int // complete lines let go
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > 4*maxOutputBytes {
		cut := len(b.buf) - 2*maxOutputBytes
		if i := bytes.IndexByte(b.buf[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		b.dropped += bytes.Count(b.buf[:cut], []byte{'\n'})
		b.buf = append([]byte(nil), b.buf[cut:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return truncateTail(string(b.buf), b.dropped) }
