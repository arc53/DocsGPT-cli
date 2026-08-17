package tools

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestReadLine(t *testing.T) {
	cases := []struct {
		in    string
		want  []string
		final error
	}{
		{"1\n2\n", []string{"1", "2"}, io.EOF},
		{"1\r2\r", []string{"1", "2"}, io.EOF},     // raw-mode Enter
		{"1\r\n2\r\n", []string{"1", "2"}, io.EOF}, // CRLF is one ending
		{"approve", []string{"approve"}, io.EOF},   // EOF after input still yields it
		{"", nil, io.EOF},
		{"a\n\nb\n", []string{"a", "", "b"}, io.EOF},
	}
	for _, tc := range cases {
		r := bufio.NewReader(strings.NewReader(tc.in))
		var got []string
		var err error
		for {
			var line string
			line, err = readLine(r)
			if err != nil {
				break
			}
			got = append(got, line)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || err != tc.final {
			t.Errorf("readLine(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.final)
		}
	}
}
