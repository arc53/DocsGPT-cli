package host

import (
	"fmt"
	"strings"
	"time"
)

// bannerFetchTimeout bounds the cosmetic approval_mode lookup so a slow or
// unreachable server never delays daemon startup.
const bannerFetchTimeout = 3 * time.Second

// ShowStartupBanner prints the connection banner on daemon start. The
// approval_mode line is fetched live (server is the source of truth, and
// the user can change it in the UI at any time); on any failure the banner
// degrades to a no-value line rather than printing a stale local value.
func ShowStartupBanner(cfg HostConfig) {
	fmt.Println(strings.Repeat("-", 64))
	fmt.Printf("docsgpt-cli host  (device_id=%s)\n", cfg.DeviceID)
	fmt.Printf("base_url:       %s\n", cfg.BaseURL)
	fmt.Printf("approval_mode:  %s\n", liveApprovalMode(cfg))
	fmt.Printf("poll_interval:  %s\n", cfg.PollInterval)
	fmt.Println(strings.Repeat("-", 64))
}

// liveApprovalMode does a single best-effort fetch of the server-side
// approval mode. Errors are swallowed: this is banner cosmetics, and the
// daemon's job is to keep retrying regardless.
func liveApprovalMode(cfg HostConfig) string {
	d, _, err := FetchDeviceMe(cfg, bannerFetchTimeout)
	if err != nil || d == nil || d.ApprovalMode == "" {
		return "managed in the DocsGPT UI"
	}
	return d.ApprovalMode + " (managed in the DocsGPT UI)"
}

// LogStamp returns the ``[hh:mm:ss]`` prefix used for daemon log lines.
func LogStamp(t time.Time) string {
	return t.Format("[15:04:05]")
}
