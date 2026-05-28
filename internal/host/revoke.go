package host

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RevokeFromServer hits DELETE /api/devices/{id} using the stored token.
func RevokeFromServer(cfg HostConfig) error {
	if cfg.DeviceID == "" {
		return nil
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/devices/" + cfg.DeviceID
	req, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.SessionToken)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
