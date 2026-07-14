package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrRevoked is returned by RunPolling / RunSSE when the server has
// revoked the device session. cmd/host.go translates this into a
// non-zero exit so the daemon does not poll forever against a dead token.
var ErrRevoked = errors.New("device session revoked")

// ErrAuthRejected is the sentinel PollOnce / RunSSE return on 401.
// Callers (RunPolling) escalate this to ErrRevoked once they have seen
// at least one successful poll, or immediately when the very first
// poll is rejected (which indicates a bad token from the start).
var ErrAuthRejected = errors.New("auth rejected")

// State enumerates the two daemon states.
type State int

const (
	StatePolling State = iota
	StateStreaming
)

func (s State) String() string {
	switch s {
	case StatePolling:
		return "polling"
	case StateStreaming:
		return "streaming"
	default:
		return "unknown"
	}
}

// Baton is the single-flight guard between Polling and Streaming goroutines.
type Baton struct {
	mu           sync.Mutex
	state        State
	sessionID    string
	lastActivity time.Time
}

// State returns the current state under lock.
func (b *Baton) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Transition atomically swaps state if ``from`` matches.
// Returns true on success, false if a different state is currently active.
func (b *Baton) Transition(from, to State) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != from {
		return false
	}
	b.state = to
	return true
}

// SessionID returns the currently-active SSE session id (empty when polling).
func (b *Baton) SessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessionID
}

// SetSessionID stores the session id for the active SSE.
func (b *Baton) SetSessionID(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionID = id
}

// TouchActivity records that the daemon successfully reached the server.
func (b *Baton) TouchActivity() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastActivity = time.Now()
}

// LastActivity returns the zero value before the first successful poll.
func (b *Baton) LastActivity() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastActivity
}

// PollResponse is the JSON body returned on poll work-queued (HTTP 200).
type PollResponse struct {
	SessionTicket string `json:"session_ticket"`
	SessionURL    string `json:"session_url"`
	ExpiresIn     int    `json:"expires_in"`
}

// Invocation envelope received on SSE.
type Invocation struct {
	InvocationID   string                 `json:"invocation_id"`
	Action         string                 `json:"action"`
	Params         map[string]interface{} `json:"params"`
	ApprovalMode   string                 `json:"approval_mode"`
	IssuedAt       string                 `json:"issued_at"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	AgentID        string                 `json:"agent_id,omitempty"`
}

// Transport carries the cross-cutting state needed by both the polling and
// SSE loops: config, identity, baton, an HTTP client, and event handlers.
type Transport struct {
	Cfg     HostConfig
	Key     *HostKey
	Version string
	Baton   *Baton
	Client  *http.Client
	OnInvocation func(inv Invocation)
}

// NewTransport wires the standard collaborators together.
func NewTransport(cfg HostConfig, key *HostKey, version string) *Transport {
	return &Transport{
		Cfg:     cfg,
		Key:     key,
		Version: version,
		Baton:   &Baton{state: StatePolling},
		Client:  &http.Client{Timeout: 0},
	}
}

func (t *Transport) authHeader() string {
	return "Bearer " + t.Cfg.SessionToken
}

// signHeaders signs the request over the canonical payload, which includes a
// hash of ``body`` (the exact bytes sent as the request body; nil for GET).
// The body hash is always included even when the backend has signature
// verification disabled — it ignores the signature then, so this is harmless
// and keeps the default off-path working.
func (t *Transport) signHeaders(req *http.Request, body []byte) {
	if t.Key == nil {
		return
	}
	sig, ts := t.Key.SignRequest(req.Method, req.URL.Path, body)
	req.Header.Set("Authorization", t.authHeader())
	req.Header.Set("X-Device-Machine-Key", t.Key.Fingerprint())
	req.Header.Set("X-Device-Machine-Pubkey", t.Key.PublicKeyB64())
	req.Header.Set("X-Device-Signature", sig)
	req.Header.Set("X-Device-Timestamp", ts)
}

// PollOnce sends a single ``GET /api/devices/poll`` request and returns the
// (ticket, queued?) tuple. ``queued == false`` is the 202-no-work response.
func (t *Transport) PollOnce(ctx context.Context) (*PollResponse, bool, error) {
	endpoint := strings.TrimRight(t.Cfg.BaseURL, "/") + "/api/devices/poll"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	t.signHeaders(req, nil)
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusAccepted:
		return nil, false, nil
	case http.StatusOK:
		var pr PollResponse
		if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
			return nil, false, fmt.Errorf("decode poll response: %w", err)
		}
		return &pr, true, nil
	case http.StatusUnauthorized:
		return nil, false, ErrAuthRejected
	case http.StatusGone:
		return nil, false, fmt.Errorf("session expired")
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("poll HTTP %d: %s", resp.StatusCode, string(body))
	}
}

// pollBackoff implements the GitHub Actions runner-style backoff.
type pollBackoff struct {
	errorCount int
	rng        *rand.Rand
}

func newPollBackoff() *pollBackoff {
	return &pollBackoff{rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func (b *pollBackoff) sleep() time.Duration {
	if b.errorCount == 0 {
		return 0
	}
	if b.errorCount <= 5 {
		return time.Duration(15+b.rng.Intn(16)) * time.Second
	}
	return time.Duration(30+b.rng.Intn(31)) * time.Second
}

func (b *pollBackoff) recordError() { b.errorCount++ }
func (b *pollBackoff) recordOK()    { b.errorCount = 0 }

// authRejectThreshold is the consecutive-401 count at which a previously
// healthy poller concludes the device has been revoked. One stray 401
// from a transient blip would be alarmist; three back-to-back is decisive.
const authRejectThreshold = 3

// RunPolling runs the polling loop until ``ctx`` is cancelled or a session
// ticket arrives. On ticket, swaps the baton to Streaming and returns the
// ticket so the caller can upgrade. Returns ErrRevoked when the server
// rejects the session token with 401 — immediately if no prior poll has
// succeeded (the token was bad from the start), or after
// ``authRejectThreshold`` consecutive 401s after at least one success
// (the server has revoked the device since the daemon started).
func (t *Transport) RunPolling(ctx context.Context, fastUntil time.Time) (*PollResponse, error) {
	bo := newPollBackoff()
	interval := t.Cfg.PollIntervalDuration()
	fastInterval := 5 * time.Second
	sawSuccess := false
	authFailures := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		pr, queued, err := t.PollOnce(ctx)
		if err != nil {
			if errors.Is(err, ErrAuthRejected) {
				authFailures++
				if !sawSuccess || authFailures >= authRejectThreshold {
					return nil, ErrRevoked
				}
				// 401 isn't an infrastructure error worth backing off on
				// — the next call either escalates to revoke or recovers.
				// Use a short cool-down so we don't busy-loop the server.
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Second):
				}
				continue
			}
			bo.recordError()
			sleep := bo.sleep()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(sleep):
			}
			continue
		}
		bo.recordOK()
		sawSuccess = true
		authFailures = 0
		t.Baton.TouchActivity()
		if queued && pr != nil {
			if t.Baton.Transition(StatePolling, StateStreaming) {
				return pr, nil
			}
			// Lost the race; fall through to wait then re-poll.
		}
		wait := interval
		if time.Now().Before(fastUntil) {
			wait = fastInterval
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// RunSSE opens the SSE stream for ``sessionID`` and dispatches events to
// ``t.OnInvocation`` until the server closes or ``ctx`` cancels. Returns
// ErrRevoked on a 401 (device revoked while a session was being
// negotiated) or when an ``event: revoke`` arrives on the open stream.
func (t *Transport) RunSSE(ctx context.Context, sessionID string, lastEventID string) error {
	endpoint := strings.TrimRight(t.Cfg.BaseURL, "/") + "/api/devices/sessions/" + sessionID + "/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	t.signHeaders(req, nil)
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrRevoked
	}
	if resp.StatusCode == http.StatusGone {
		return fmt.Errorf("session expired")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SSE HTTP %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var event, data string
	revoked := false
	flush := func() {
		if event == "" && data == "" {
			return
		}
		switch event {
		case "invocation":
			var inv Invocation
			if json.Unmarshal([]byte(data), &inv) == nil && t.OnInvocation != nil {
				t.Baton.TouchActivity()
				t.OnInvocation(inv)
			}
		case "revoke":
			revoked = true
		case "session_end":
			// caller decides what to do via context cancellation; we just
			// return after the loop exits.
		}
		event, data = "", ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			if revoked {
				return ErrRevoked
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data != "" {
				data += "\n"
			}
			data += strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			continue
		}
	}
	if revoked {
		return ErrRevoked
	}
	return scanner.Err()
}

// PostAck reports the CLI's accept/deny decision for an invocation.
func (t *Transport) PostAck(ctx context.Context, sessionID, invocationID, decision, reason string) error {
	body, _ := json.Marshal(map[string]string{
		"decision": decision,
		"reason":   reason,
	})
	endpoint := strings.TrimRight(t.Cfg.BaseURL, "/") +
		"/api/devices/sessions/" + sessionID +
		"/invocations/" + invocationID + "/ack"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Sign over the exact body bytes so the signature stays valid against
	// the body the server reads back.
	t.signHeaders(req, body)
	resp, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ack HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// PostOutput streams a single body of NDJSON chunks for an invocation.
func (t *Transport) PostOutput(ctx context.Context, sessionID, invocationID string, body []byte) error {
	endpoint := strings.TrimRight(t.Cfg.BaseURL, "/") +
		"/api/devices/sessions/" + sessionID +
		"/invocations/" + invocationID + "/output"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Sign over the exact body bytes (see PostAck).
	t.signHeaders(req, body)
	resp, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("output HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
