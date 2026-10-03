package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// chatError is an error of a chat request, told in a line that says what
// to do about it. DOCSGPT_DEBUG=1 adds the error as it came.
type chatError struct {
	msg string
	err error
}

func (e *chatError) Error() string {
	if os.Getenv("DOCSGPT_DEBUG") != "" {
		detail := e.err.Error()
		var apiErr *docsgpt.APIError
		if errors.As(e.err, &apiErr) && apiErr.Body != "" {
			detail = fmt.Sprintf("HTTP %d: %s", apiErr.StatusCode, strings.TrimSpace(apiErr.Body))
		}
		if len(detail) > 4000 {
			detail = detail[:4000] + "…"
		}
		return e.msg + "\n  " + detail
	}
	return e.msg
}

func (e *chatError) Unwrap() error { return e.err }

// explainChatError rewrites err, from a chat request to baseURL with the
// key named keyName, for a person to act on; other errors pass unchanged.
func explainChatError(err error, baseURL, keyName string) error {
	if err == nil {
		return nil
	}
	if msg := explain(err, baseURL, keyName); msg != "" {
		return &chatError{msg: msg, err: err}
	}
	return err
}

func explain(err error, baseURL, keyName string) string {
	host := hostOf(baseURL)
	var apiErr *docsgpt.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.StatusCode
		status := fmt.Sprintf("%d %s", code, http.StatusText(code))
		if m := apiErr.Message(); m != "" {
			status = fmt.Sprintf("%d: %s", code, m)
		}
		switch {
		case code == http.StatusUnauthorized || code == http.StatusForbidden:
			if keyName == config.EnvAPIKey {
				return fmt.Sprintf("%s rejected the key in %s (%s). Check it, or unset it to use a stored key.", host, config.EnvAPIKey, status)
			}
			return fmt.Sprintf("%s rejected the API key %q (%s). Add a working one with: docsgpt-cli login", host, keyName, status)
		case code == http.StatusNotFound:
			return fmt.Sprintf("%s has no chat API at %s (%s). Is the server URL right? Change it with: docsgpt-cli config set url <url>", host, baseURL, status)
		case code == http.StatusTooManyRequests:
			wait := "a moment"
			if apiErr.RetryAfter > 0 {
				wait = apiErr.RetryAfter.Round(time.Second).String()
			}
			return fmt.Sprintf("Rate limited by %s (%s). Try again in %s.", host, status, wait)
		case code >= 500:
			return fmt.Sprintf("%s had a server error (%s). Try again in a moment.", host, status)
		}
		return fmt.Sprintf("%s refused the request (%s).", host, status)
	}

	var urlErr *url.Error
	if !errors.As(err, &urlErr) || errors.Is(err, context.Canceled) {
		return ""
	}
	var why string
	var dnsErr *net.DNSError
	var recordErr tls.RecordHeaderError
	var certErr *tls.CertificateVerificationError
	var netErr net.Error
	var opErr *net.OpError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return fmt.Sprintf("Can't reach %s: no such host. Is the server URL right? Change it with: docsgpt-cli config set url <url>", host)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("Can't reach %s: connection refused. Is the server running at %s?", host, baseURL)
	case errors.As(err, &recordErr), strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return fmt.Sprintf("Can't reach %s: it does not speak TLS. Is the URL meant to be http://?", host)
	case errors.As(err, &certErr):
		why = "its TLS certificate is not trusted (" + strings.TrimPrefix(certErr.Err.Error(), "x509: ") + ")"
	case errors.As(err, &netErr) && netErr.Timeout():
		why = "it did not answer in time"
	case errors.Is(err, syscall.ECONNRESET):
		why = "the connection was reset"
	case errors.As(err, &opErr):
		why = opErr.Err.Error()
	default:
		why = urlErr.Err.Error()
	}
	return fmt.Sprintf("Can't reach %s: %s.", host, why)
}
