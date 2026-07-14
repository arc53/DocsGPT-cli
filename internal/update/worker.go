package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"docsgpt-cli/internal/config"
)

// The background worker is a short-lived detached copy of the CLI
// (`update --worker`) so the check and download survive fast commands
// that exit before an in-process goroutine could finish.

// ShouldSpawnWorker reports whether a background pass would do useful work:
// the daily check is due, or a known newer release still needs staging.
func ShouldSpawnWorker(current, mode string) bool {
	if !IsReleaseVersion(current) {
		return false
	}
	st := loadState()
	if time.Since(st.LastChecked) >= checkInterval {
		return true
	}
	if mode != ModeOn {
		return false
	}
	latest := st.LatestVersion
	return latest != "" && IsNewer(latest, current) &&
		latest != st.SkipVersion && StagedVersion() != latest
}

// SpawnWorker relaunches this executable as a detached background process.
// Fire-and-forget: failures are silent, the next launch tries again.
func SpawnWorker() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "update", "--worker")
	detachProcess(cmd)
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
}

// RunWorker is the worker process body: refresh the release cache and, in
// auto-update mode, stage the new binary. Never prints; state files are the
// only output.
func RunWorker(current string) {
	if os.Getenv("DOCSGPT_NO_UPDATE_CHECK") != "" || !IsReleaseVersion(current) {
		return
	}
	mode := ModeOff
	if cfg, err := config.Load(); err == nil {
		mode = cfg.Settings.AutoUpdateMode()
	}
	if mode == ModeOff {
		return
	}

	release, ok := acquireStageLock()
	if !ok {
		return
	}
	defer release()

	rel, err := FetchLatest(30 * time.Second)
	if err != nil {
		return
	}
	RecordCheck(rel)

	if mode != ModeOn || !IsNewer(rel.TagName, current) ||
		rel.TagName == SkipVersion() || StagedVersion() == rel.TagName {
		return
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil && IsHomebrewPath(real) {
			return
		}
	}
	Stage(rel)
}
