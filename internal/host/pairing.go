package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// PairRequest is the JSON body sent to /api/devices/pairings/redeem.
type PairRequest struct {
	UserCode      string `json:"user_code"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	CLIVersion    string `json:"cli_version"`
	MachinePubkey string `json:"machine_pubkey"`
}

// PairResponse mirrors the redeem response on success.
type PairResponse struct {
	DeviceID     string `json:"device_id"`
	SessionToken string `json:"session_token"`
	Name         string `json:"name"`
}

// Pair performs a single redeem call against the server, persisting the
// result to host.yml on success. Returns the redeemed pairing response.
func Pair(baseURL, userCode, cliVersion string) (*PairResponse, error) {
	key, err := LoadOrCreateKey()
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	body, err := json.Marshal(PairRequest{
		UserCode:      strings.TrimSpace(userCode),
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		CLIVersion:    cliVersion,
		MachinePubkey: key.PublicKeyB64(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal pair body: %w", err)
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/api/devices/pairings/redeem"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pair request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pair failed: HTTP %d %s", resp.StatusCode, string(respBody))
	}
	var pr PairResponse
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("decode pair response: %w", err)
	}

	cfg, _ := LoadHostConfig()
	cfg.DeviceID = pr.DeviceID
	cfg.SessionToken = pr.SessionToken
	cfg.BaseURL = strings.TrimRight(baseURL, "/")
	if cfg.PollInterval == "" {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.ApprovalMode == "" {
		cfg.ApprovalMode = defaultApprovalMode
	}
	if err := cfg.Save(); err != nil {
		return &pr, fmt.Errorf("write host.yml: %w", err)
	}
	return &pr, nil
}
