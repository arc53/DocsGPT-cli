package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"docsgpt-cli/internal/config"
)

// A staged update is a fully downloaded and checksum-verified binary waiting
// in ~/.docsgpt/staging/, applied near-instantly on a later launch so the
// download never blocks a user command.

// binaryManifest describes a binary parked on disk (staged update or
// rollback backup) so it can be validated before ever being installed.
type binaryManifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
}

func binaryBaseName() string {
	if runtime.GOOS == "windows" {
		return "docsgpt-cli.exe"
	}
	return "docsgpt-cli"
}

func stagingDir() string {
	return filepath.Join(config.Dir(), "staging")
}

func stagedBinaryPath() string {
	return filepath.Join(stagingDir(), binaryBaseName())
}

func stagedManifestPath() string {
	return filepath.Join(stagingDir(), "manifest.json")
}

func loadStagedManifest() (binaryManifest, bool) {
	var m binaryManifest
	data, err := os.ReadFile(stagedManifestPath())
	if err != nil || json.Unmarshal(data, &m) != nil || m.Version == "" {
		return m, false
	}
	return m, true
}

// StagedVersion returns the version waiting in the staging area, or "".
func StagedVersion() string {
	m, ok := loadStagedManifest()
	if !ok {
		return ""
	}
	return m.Version
}

// ClearStaging removes any staged update.
func ClearStaging() {
	os.RemoveAll(stagingDir())
}

// Stage downloads and verifies the release binary for this platform and
// parks it in the staging area for a later ApplyStaged.
func Stage(rel *Release) error {
	binary, err := fetchVerifiedBinary(rel)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(stagingDir(), 0700); err != nil {
		return err
	}
	tmp := stagedBinaryPath() + ".tmp"
	if err := os.WriteFile(tmp, binary, 0755); err != nil {
		return err
	}
	if err := os.Rename(tmp, stagedBinaryPath()); err != nil {
		return err
	}

	sum := sha256.Sum256(binary)
	m := binaryManifest{
		Version: rel.TagName,
		SHA256:  hex.EncodeToString(sum[:]),
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp = stagedManifestPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, stagedManifestPath())
}

// ApplyStaged swaps a valid staged binary into targetPath and returns the
// new version, or "" when nothing applied. The manifest is re-verified
// against the staged file so a corrupt or foreign binary is never installed;
// any invalid staging area is discarded.
func ApplyStaged(current, targetPath string) (string, error) {
	m, ok := loadStagedManifest()
	if !ok {
		return "", nil
	}
	if m.GOOS != runtime.GOOS || m.GOARCH != runtime.GOARCH ||
		!IsNewer(m.Version, current) || m.Version == SkipVersion() {
		ClearStaging()
		return "", nil
	}

	binary, err := os.ReadFile(stagedBinaryPath())
	if err != nil {
		ClearStaging()
		return "", nil
	}
	sum := sha256.Sum256(binary)
	if hex.EncodeToString(sum[:]) != m.SHA256 {
		ClearStaging()
		return "", nil
	}

	if err := swapBinary(binary, targetPath, current); err != nil {
		ClearStaging()
		return "", err
	}
	return m.Version, nil
}

// Staging runs in a separate worker process; the lock keeps concurrent CLI
// invocations from downloading the same release twice.
func acquireStageLock() (release func(), ok bool) {
	lockPath := filepath.Join(config.Dir(), "staging.lock")
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return nil, false
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lockPath) }, true
		}
		info, statErr := os.Stat(lockPath)
		if statErr != nil || time.Since(info.ModTime()) < 15*time.Minute {
			return nil, false
		}
		os.Remove(lockPath)
	}
	return nil, false
}
