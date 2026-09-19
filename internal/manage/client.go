// Package manage is a small client for the DocsGPT account-level ("management")
// API: identity, agents (list/export/plan/apply/delete), sources
// (list/upload/task status/delete), prompts and tools. Every request carries a
// personal access token as `Authorization: Bearer dgpt_pat_…`.
//
// Failures reported by the server are returned as *APIError, which surfaces the
// server `message`, the machine-readable `error` code and, for
// insufficient_scope, the scope the token is missing.
package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Timeouts applied when the caller's context carries no earlier deadline.
const (
	DefaultTimeout       = 60 * time.Second // JSON requests
	DefaultUploadTimeout = 15 * time.Minute // multipart uploads
)

// DefaultUserAgent is used when the caller does not stamp a version.
const DefaultUserAgent = "docsgpt-cli"

// Server error codes (the JSON `error` field).
const (
	CodeInvalidToken       = "invalid_token"
	CodeInsufficientScope  = "insufficient_scope"
	CodeResourceNotAllowed = "resource_not_allowed"
	CodeNotForTokens       = "not_available_to_tokens" // endpoint has no token scope at all
)

// maxErrorBody caps how much of a response is read for diagnostics.
const maxErrorBody = 64 << 10

// Client talks to one DocsGPT deployment with one personal access token.
type Client struct {
	BaseURL   string
	Token     string
	UserAgent string // "docsgpt-cli/<version>"

	Timeout       time.Duration // per JSON request; 0 = DefaultTimeout
	UploadTimeout time.Duration // per upload; 0 = DefaultUploadTimeout

	// HTTP is the underlying client. It deliberately has no Timeout of its
	// own: every request is bounded by its context instead.
	HTTP *http.Client
}

// New returns a client for baseURL authenticating with token.
func New(baseURL, token, userAgent string) *Client {
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		UserAgent: userAgent,
		HTTP:      &http.Client{},
	}
}

// APIError is a failure reported by the server (any non-2xx response, or a
// 2xx body with success:false).
type APIError struct {
	Method        string
	Path          string
	Status        int
	Code          string // server `error` field, e.g. insufficient_scope
	Message       string // server `message` field
	RequiredScope string // server `required_scope` (insufficient_scope only)
	Body          string // truncated raw body, for diagnostics
}

func (e *APIError) Error() string {
	where := fmt.Sprintf("%s %s", e.Method, e.Path)
	switch {
	case e.Code == CodeInsufficientScope:
		msg := "the token lacks a required scope"
		if e.RequiredScope != "" {
			msg = fmt.Sprintf("the token lacks the required scope %q", e.RequiredScope)
		}
		if e.Message != "" {
			msg += " (" + e.Message + ")"
		}
		return fmt.Sprintf("%s: %s — create a token with that scope and run 'docsgpt-cli login'", where, msg)
	case e.Code == CodeResourceNotAllowed:
		msg := "the token's resource restrictions do not allow this resource"
		if e.Message != "" {
			msg += " (" + e.Message + ")"
		}
		return fmt.Sprintf("%s: %s", where, msg)
	case e.Code == CodeNotForTokens:
		return fmt.Sprintf("%s: this endpoint cannot be called with a personal access token", where)
	case e.Code == CodeInvalidToken || e.Status == http.StatusUnauthorized:
		msg := "authentication failed: the token is invalid, expired or revoked"
		if e.Message != "" && e.Code != CodeInvalidToken {
			msg = "authentication failed: " + e.Message
		}
		return fmt.Sprintf("%s: %s — run 'docsgpt-cli login' or set DOCSGPT_TOKEN", where, msg)
	}
	detail := e.Message
	if detail == "" {
		detail = e.Body
	}
	if detail == "" {
		detail = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s returned %d: %s", where, e.Status, detail)
}

// IsInsufficientScope reports whether err is a missing-scope rejection.
func IsInsufficientScope(err error) bool { return hasCode(err, CodeInsufficientScope) }

// IsResourceNotAllowed reports whether err is a resource-restriction rejection.
func IsResourceNotAllowed(err error) bool { return hasCode(err, CodeResourceNotAllowed) }

// IsUnauthorized reports whether err is a 401 / invalid_token rejection.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Code == CodeInvalidToken)
}

// IsNotFound reports whether err is a 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func hasCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

// endpoint builds the absolute URL for path + query.
func (c *Client) endpoint(path string, query url.Values) string {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// withTimeout bounds ctx by d unless it already has an earlier deadline.
func withTimeout(ctx context.Context, d, fallback time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = fallback
	}
	return context.WithTimeout(ctx, d)
}

// setHeaders stamps auth and identification headers.
func (c *Client) setHeaders(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	ua := c.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
}

// send performs the request and returns the body of a successful response.
// Non-2xx statuses, and 2xx JSON bodies carrying success:false, become
// *APIError. Transport failures are returned with the token-free URL path.
func (c *Client) send(req *http.Request, path string) ([]byte, http.Header, error) {
	c.setHeaders(req)
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Err != nil {
			err = ue.Err
		}
		return nil, nil, fmt.Errorf("%s %s%s: %w", req.Method, c.BaseURL, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, resp.Header, newAPIError(req.Method, path, resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.Header, fmt.Errorf("%s %s: read response: %w", req.Method, path, err)
	}
	if isJSON(resp.Header) && reportsFailure(body) {
		return nil, resp.Header, newAPIError(req.Method, path, resp.StatusCode, body)
	}
	return body, resp.Header, nil
}

func isJSON(h http.Header) bool {
	return strings.Contains(strings.ToLower(h.Get("Content-Type")), "json")
}

// reportsFailure detects {"success": false, ...} on a 2xx response.
func reportsFailure(body []byte) bool {
	var probe struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(body, &probe) != nil {
		return false // arrays and non-objects are plain payloads
	}
	return probe.Success != nil && !*probe.Success
}

// newAPIError decodes the server's error envelope. DocsGPT uses
// {success:false, message}, {error, message, required_scope} and, on a few
// older routes, {status: "not found"}.
func newAPIError(method, path string, status int, body []byte) *APIError {
	ae := &APIError{Method: method, Path: path, Status: status, Body: truncate(body, 300)}
	var env struct {
		Message       any    `json:"message"`
		Error         any    `json:"error"`
		RequiredScope string `json:"required_scope"`
		Status        any    `json:"status"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ae
	}
	if s, ok := env.Message.(string); ok {
		ae.Message = s
	}
	if s, ok := env.Error.(string); ok {
		ae.Code = s
	}
	ae.RequiredScope = env.RequiredScope
	if ae.Message == "" {
		if s, ok := env.Status.(string); ok && s != "" && s != "error" {
			ae.Message = s
		}
	}
	return ae
}

func truncate(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// getRaw performs a GET and returns the raw body.
func (c *Client) getRaw(ctx context.Context, path string, query url.Values, accept string) ([]byte, error) {
	ctx, cancel := withTimeout(ctx, c.Timeout, DefaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path, query), nil)
	if err != nil {
		return nil, fmt.Errorf("build GET %s: %w", path, err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	body, _, err := c.send(req, path)
	return body, err
}

// getJSON performs a GET, decodes the body into out and returns the raw body
// (so --json output can pass the server document through untouched).
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) (json.RawMessage, error) {
	body, err := c.getRaw(ctx, path, query, "")
	if err != nil {
		return nil, err
	}
	if err := decode(http.MethodGet, path, body, out); err != nil {
		return nil, err
	}
	return body, nil
}

// doJSON sends method with an optional JSON payload and decodes the reply.
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, payload, out any) (json.RawMessage, error) {
	ctx, cancel := withTimeout(ctx, c.Timeout, DefaultTimeout)
	defer cancel()
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s body: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query), rdr)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	body, _, err := c.send(req, path)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := decode(method, path, body, out); err != nil {
			return nil, err
		}
	}
	return body, nil
}

func decode(method, path string, body []byte, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s %s: unexpected response (%v): %s", method, path, err, truncate(body, 300))
	}
	return nil
}
