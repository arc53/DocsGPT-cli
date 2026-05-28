package host

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DeviceMe is the shape of GET /api/devices/me — the live, server-side
// view of this device. The server is the source of truth for approval_mode.
type DeviceMe struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Status       string `json:"status"`
	ApprovalMode string `json:"approval_mode"`
	Description  string `json:"description"`
	PairedAt     string `json:"paired_at"`
	LastSeenAt   string `json:"last_seen_at"`
}

// FetchDeviceMe queries the live server-side view of this device. A nil
// device with unauthorized=false means the server was unreachable;
// unauthorized=true means the device has been revoked. ``timeout`` bounds
// the request — callers that must not block (e.g. the startup banner) pass
// a short one.
func FetchDeviceMe(cfg HostConfig, timeout time.Duration) (d *DeviceMe, unauthorized bool, err error) {
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/devices/me"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.SessionToken)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out DeviceMe
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false, fmt.Errorf("decode: %w", err)
	}
	return &out, false, nil
}
