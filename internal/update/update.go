package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const latestReleaseURL = "https://api.github.com/repos/arc53/DocsGPT-cli/releases/latest"

// Auto-update modes; config.Settings.AutoUpdateMode resolves to one of these.
const (
	ModeOn     = "on"     // stage and apply updates automatically
	ModeNotify = "notify" // only print a notice when a release is available
	ModeOff    = "off"    // no checks at all
)

// IsHomebrewPath reports whether a resolved executable path is managed by
// Homebrew and must be updated via brew instead. Cellar covers formulae and
// Caskroom covers casks; on Apple Silicon both sit under /opt/homebrew, but on
// an Intel Mac the prefix is /usr/local, so the Caskroom check is what catches
// a cask install there.
func IsHomebrewPath(path string) bool {
	return strings.Contains(path, "/Cellar/") ||
		strings.Contains(path, "/Caskroom/") ||
		strings.Contains(path, "/homebrew/")
}

type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

func normalize(version string) string {
	if version == "" || strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

// IsReleaseVersion reports whether version comes from an exact release tag,
// as opposed to "dev" or a between-tags/dirty git describe string.
func IsReleaseVersion(version string) bool {
	v := normalize(version)
	return semver.IsValid(v) && semver.Prerelease(v) == "" && semver.Build(v) == ""
}

// IsNewer reports whether latest is a higher version worth updating to.
//
// Prereleases are never offered, even though semver orders v1.6.0-rc1 above
// v1.5.1: installing one stamps the binary with a prerelease version, which
// IsReleaseVersion rejects, so that install would stop checking for updates
// altogether. release.prerelease in .goreleaser.yaml already keeps an -rc out
// of releases/latest; this is the second lock on the same door.
func IsNewer(latest, current string) bool {
	v := normalize(latest)
	if semver.Prerelease(v) != "" {
		return false
	}
	return semver.Compare(v, normalize(current)) > 0
}

// FetchLatest queries GitHub for the most recent release.
func FetchLatest(timeout time.Duration) (*Release, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "docsgpt-cli")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("release response is missing a tag name")
	}
	return &rel, nil
}
