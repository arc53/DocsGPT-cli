package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"docsgpt-cli/internal/config"
)

const checkInterval = 24 * time.Hour

type checkState struct {
	LastChecked   time.Time `json:"last_checked"`
	LatestVersion string    `json:"latest_version"`
	SkipVersion   string    `json:"skip_version,omitempty"`
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

func saveState(st checkState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return
	}
	os.WriteFile(statePath(), data, 0600)
}

// RecordCheck persists the result of a release lookup so later runs can skip
// re-querying GitHub for a day.
func RecordCheck(rel *Release) {
	st := loadState()
	st.LastChecked = time.Now()
	st.LatestVersion = rel.TagName
	saveState(st)
}

// SkipVersion returns the version auto-update must not install (set by a
// rollback), or "".
func SkipVersion() string {
	return loadState().SkipVersion
}

// SetSkipVersion records the version auto-update must not install; ""
// clears the skip.
func SetSkipVersion(version string) {
	st := loadState()
	st.SkipVersion = version
	saveState(st)
}

// CachedNotice returns the newer release recorded by a previous background
// check, or "". Reads only local state, never the network.
func CachedNotice(current string) string {
	if !IsReleaseVersion(current) {
		return ""
	}
	st := loadState()
	if st.LatestVersion == "" || !IsNewer(st.LatestVersion, current) || st.LatestVersion == st.SkipVersion {
		return ""
	}
	return st.LatestVersion
}
