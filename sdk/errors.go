package docsgpt

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// APIError is returned when the server answers with a non-200 status. Callers
// that need to tell "the agent is missing" from "the key is wrong" from "the
// server is down" can inspect StatusCode rather than matching on a string.
type APIError struct {
	// StatusCode is the HTTP status the server replied with.
	StatusCode int
	// Body is the raw response body, which usually carries the server's
	// own error message (see Message).
	Body string
	// RetryAfter is the wait the server asked for in a Retry-After header,
	// or 0.
	RetryAfter time.Duration
	// noRetry is set by an "x-should-retry: false" header.
	noRetry bool
}

func (e *APIError) Error() string {
	if msg := e.Message(); msg != "" {
		return fmt.Sprintf("API error %d: %s", e.StatusCode, msg)
	}
	return fmt.Sprintf("API error %d %s", e.StatusCode, http.StatusText(e.StatusCode))
}

// Message is the server's own error message, on one line: from a JSON body
// ({"error":{"message"}}, {"error":"…"}, {"message"} or {"detail"}), else
// the body's first line. An HTML page (a proxy's error page) gives "".
func (e *APIError) Message() string {
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	if json.Unmarshal([]byte(e.Body), &body) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var flat string
		switch {
		case json.Unmarshal(body.Error, &nested) == nil && nested.Message != "":
			return oneLine(nested.Message)
		case json.Unmarshal(body.Error, &flat) == nil && flat != "":
			return oneLine(flat)
		case body.Message != "":
			return oneLine(body.Message)
		case body.Detail != "":
			return oneLine(body.Detail)
		}
		return ""
	}
	text := strings.TrimSpace(e.Body)
	if strings.HasPrefix(text, "<") {
		return ""
	}
	return oneLine(text)
}

// Code is the machine-readable error code of a JSON body (error.code,
// error.type or error_code), or "".
func (e *APIError) Code() string {
	var body struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
		ErrorCode string `json:"error_code"`
	}
	json.Unmarshal([]byte(e.Body), &body)
	switch {
	case body.Error.Code != "":
		return body.Error.Code
	case body.ErrorCode != "":
		return body.ErrorCode
	}
	return body.Error.Type
}

// maxErrorBody bounds how much of an error response is kept.
const maxErrorBody = 64 << 10

// newAPIError reads resp's body into an APIError and closes it.
func newAPIError(resp *http.Response) *APIError {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return &APIError{
		StatusCode: resp.StatusCode,
		Body:       string(b),
		RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now()),
		noRetry:    strings.EqualFold(resp.Header.Get("X-Should-Retry"), "false"),
	}
}

// retryAfter parses a Retry-After value: seconds or an HTTP date.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(max(n, 0)) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now).Round(time.Second)
	}
	return 0
}

var spaces = regexp.MustCompile(`\s+`)

// oneLine collapses s to one line of at most 200 characters.
func oneLine(s string) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:199]) + "…"
	}
	return s
}
