package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"docsgpt-cli/internal/config"
)

const (
	checkInterval = 24 * time.Hour
	notifyTimeout = 3 * time.Second
)

type checkState struct {
	LastChecked   time.Time `json:"last_checked"`
	LatestVersion string    `json:"latest_version"`
}

func statePath() string {
	return filepath.Join(config.Dir(), "update_check.json")
}

func loadState() checkState {
	var st checkState
	data, err := os.ReadFile(statePath())
	if err != nil {
		return st
	}
	json.Unmarshal(data, &st)
	return st
}

// RecordCheck persists the result of a release lookup so later runs can skip
// re-querying GitHub for a day.
func RecordCheck(rel *Release) {
	st := checkState{LastChecked: time.Now(), LatestVersion: rel.TagName}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return
	}
	os.WriteFile(statePath(), data, 0600)
}

// BackgroundCheck looks up the latest release version without blocking.
// The returned channel yields the newer version tag, or "" when the current
// build is up to date. All failures are silent.
func BackgroundCheck(current string) <-chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		ch <- newerVersion(current)
	}()
	return ch
}

func newerVersion(current string) string {
	if !IsReleaseVersion(current) {
		return ""
	}
	st := loadState()
	if time.Since(st.LastChecked) >= checkInterval {
		rel, err := FetchLatest(notifyTimeout)
		if err != nil {
			return ""
		}
		RecordCheck(rel)
		st.LatestVersion = rel.TagName
	}
	if st.LatestVersion != "" && IsNewer(st.LatestVersion, current) {
		return st.LatestVersion
	}
	return ""
}
