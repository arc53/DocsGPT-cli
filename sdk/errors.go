package docsgpt

import "fmt"

// APIError is returned when the server answers with a non-200 status. Callers
// that need to tell "the agent is missing" from "the key is wrong" from "the
// server is down" can inspect StatusCode rather than matching on a string.
type APIError struct {
	// StatusCode is the HTTP status the server replied with.
	StatusCode int
	// Body is the raw response body, which usually carries the server's
	// own error message.
	Body string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}
