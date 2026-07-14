package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"docsgpt-cli/internal/config"

	"github.com/minio/selfupdate"
)

const downloadTimeout = 5 * time.Minute

// AssetName returns the release archive filename for the current platform,
// matching the name_template in .goreleaser.yaml.
func AssetName() string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("docsgpt-cli_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
}

func (r *Release) asset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// Apply downloads, verifies, and installs rel's binary at targetPath,
// keeping the replaced binary (running as currentVersion) as the rollback
// backup. On failure the old binary is left in place.
func Apply(rel *Release, targetPath, currentVersion string) error {
	binary, err := fetchVerifiedBinary(rel)
	if err != nil {
		return err
	}
	return swapBinary(binary, targetPath, currentVersion)
}

// CheckAndApply is the host daemon's update pass: check for a release,
// download, verify, and swap the running binary in place. Returns the
// installed version, or "" when already current. The caller restarts into
// the new binary.
func CheckAndApply(currentVersion string) (string, error) {
	if !IsReleaseVersion(currentVersion) {
		return "", nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if IsHomebrewPath(realPath) {
		return "", nil
	}

	rel, err := FetchLatest(30 * time.Second)
	if err != nil {
		return "", err
	}
	RecordCheck(rel)
	if !IsNewer(rel.TagName, currentVersion) || rel.TagName == SkipVersion() {
		return "", nil
	}
	if err := Apply(rel, realPath, currentVersion); err != nil {
		return "", err
	}
	return rel.TagName, nil
}

// Rollback swaps the current binary with the backup kept by the last update
// and marks the replaced version as skipped so auto-update won't reinstall
// it. Returns the version rolled back to ("" when the backup predates
// manifests).
func Rollback(currentVersion, targetPath string) (string, error) {
	binary, err := os.ReadFile(backupBinaryPath())
	if err != nil {
		return "", fmt.Errorf("no rollback backup found at %s", backupBinaryPath())
	}
	var m binaryManifest
	if data, err := os.ReadFile(backupManifestPath()); err == nil {
		json.Unmarshal(data, &m)
	}
	if m.SHA256 != "" {
		sum := sha256.Sum256(binary)
		if hex.EncodeToString(sum[:]) != m.SHA256 {
			return "", fmt.Errorf("backup binary does not match its manifest, refusing to roll back")
		}
	}
	if m.GOOS != "" && (m.GOOS != runtime.GOOS || m.GOARCH != runtime.GOARCH) {
		return "", fmt.Errorf("backup binary is for %s/%s, not this machine", m.GOOS, m.GOARCH)
	}
	if err := swapBinary(binary, targetPath, currentVersion); err != nil {
		return "", err
	}
	SetSkipVersion(currentVersion)
	return m.Version, nil
}

func backupDir() string {
	return filepath.Join(config.Dir(), "backup")
}

func backupBinaryPath() string {
	return filepath.Join(backupDir(), binaryBaseName())
}

func backupManifestPath() string {
	return filepath.Join(backupDir(), "manifest.json")
}

// swapBinary installs binary at targetPath via an atomic replace, saving
// the previous executable (recorded as oldVersion) for Rollback. Any staged
// update is cleared since it no longer matches what is installed.
func swapBinary(binary []byte, targetPath, oldVersion string) error {
	if err := os.MkdirAll(backupDir(), 0700); err != nil {
		return err
	}
	err := selfupdate.Apply(bytes.NewReader(binary), selfupdate.Options{
		TargetPath:  targetPath,
		OldSavePath: backupBinaryPath(),
	})
	if err != nil {
		if rbErr := selfupdate.RollbackError(err); rbErr != nil {
			return fmt.Errorf("update failed and the old binary could not be restored (%v), reinstall manually: %w", rbErr, err)
		}
		return err
	}
	writeBackupManifest(oldVersion)
	ClearStaging()
	return nil
}

func writeBackupManifest(version string) {
	old, err := os.ReadFile(backupBinaryPath())
	if err != nil {
		return
	}
	sum := sha256.Sum256(old)
	m := binaryManifest{
		Version: version,
		SHA256:  hex.EncodeToString(sum[:]),
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	os.WriteFile(backupManifestPath(), data, 0600)
}

// fetchVerifiedBinary downloads the platform archive for rel, verifies it
// against the release checksums, and returns the extracted binary.
func fetchVerifiedBinary(rel *Release) ([]byte, error) {
	name := AssetName()
	asset := rel.asset(name)
	if asset == nil {
		return nil, fmt.Errorf("release %s has no asset for %s/%s (%s)", rel.TagName, runtime.GOOS, runtime.GOARCH, name)
	}

	archive, err := download(asset.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	if err := verifyChecksum(rel, name, archive); err != nil {
		return nil, err
	}
	return extractBinary(archive, name)
}

func download(url string) ([]byte, error) {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func verifyChecksum(rel *Release, name string, archive []byte) error {
	asset := rel.asset("checksums.txt")
	if asset == nil {
		return fmt.Errorf("release %s has no checksums.txt, refusing to update", rel.TagName)
	}
	data, err := download(asset.DownloadURL)
	if err != nil {
		return fmt.Errorf("could not download checksums: %w", err)
	}
	want, err := findChecksum(string(data), name)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, want, got)
	}
	return nil
}

func findChecksum(checksums, name string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", name)
}

func extractBinary(archive []byte, name string) ([]byte, error) {
	binaryName := "docsgpt-cli"
	if strings.HasSuffix(name, ".zip") {
		binaryName += ".exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == binaryName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in %s", binaryName, name)
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == binaryName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in %s", binaryName, name)
}
