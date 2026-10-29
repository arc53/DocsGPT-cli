// Package sse reads Server-Sent Events streams line by line.
//
// bufio.Scanner stops at a fixed token size, so one event larger than its
// buffer ends the whole stream with "token too long". LineReader reads lines
// of any length up to a high safety limit instead, and skips a line past that
// limit (keeping its start, so the caller can tell what it was) rather than
// failing the stream.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"regexp"
)

// DefaultMaxLine is the longest line read whole: well past any event a
// DocsGPT server sends, but a bound on what one line can make the client hold.
const DefaultMaxLine = 64 << 20

// PrefixBytes is how much of an oversized line is kept, enough to read the
// event's type.
const PrefixBytes = 4096

// Line is one line of the stream.
type Line struct {
	// Text is the line without its line ending; for an oversized line, only
	// its first PrefixBytes.
	Text []byte
	// Oversized marks a line longer than the reader's limit, which was skipped.
	Oversized bool
	// Size is the line's full length in bytes, line ending excluded.
	Size int
}

// LineReader reads newline-terminated lines with no fixed buffer cap.
type LineReader struct {
	r   *bufio.Reader
	max int
}

// NewLineReader reads lines from r, reading whole those up to max bytes.
func NewLineReader(r io.Reader, max int) *LineReader {
	if max <= 0 {
		max = DefaultMaxLine
	}
	return &LineReader{r: bufio.NewReaderSize(r, 64<<10), max: max}
}

// Next returns the next line. At the end of the stream it returns io.EOF,
// after any final line that had no line ending.
func (l *LineReader) Next() (Line, error) {
	var (
		buf       []byte
		size      int
		ending    int
		oversized bool
	)
	for {
		chunk, err := l.r.ReadSlice('\n')
		size += len(chunk)
		ending = len(chunk) - len(bytes.TrimRight(chunk, "\r\n"))
		if !oversized {
			buf = append(buf, chunk...) // ReadSlice's slice is only valid until the next read
			if size-ending > l.max {
				oversized = true
				keep := min(PrefixBytes, len(buf))
				buf = buf[:keep:keep]
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && size > 0 {
				break // a last line without a line ending
			}
			return Line{}, err
		}
		break
	}
	if oversized {
		return Line{Text: buf, Oversized: true, Size: size - ending}, nil
	}
	text := bytes.TrimRight(buf, "\r\n")
	return Line{Text: text, Size: len(text)}, nil
}

var frameTypeRE = regexp.MustCompile(`"type"\s*:\s*"([^"\\]+)"`)

// FrameType is the event type named in a “data:“ line (or the start of
// one), or "" when it can't be told.
func FrameType(line []byte) string {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return ""
	}
	match := frameTypeRE.FindSubmatch(line)
	if match == nil {
		return ""
	}
	return string(match[1])
}
