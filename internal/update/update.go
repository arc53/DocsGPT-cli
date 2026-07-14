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

// IsNewer reports whether latest is a higher version than current.
func IsNewer(latest, current string) bool {
	return semver.Compare(normalize(latest), normalize(current)) > 0
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
