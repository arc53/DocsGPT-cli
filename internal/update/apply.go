package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"

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

// Apply downloads the release archive for this platform, verifies it against
// the release checksums, and swaps the binary at targetPath. On failure the
// old binary is left in place.
func Apply(rel *Release, targetPath string) error {
	name := AssetName()
	asset := rel.asset(name)
	if asset == nil {
		return fmt.Errorf("release %s has no asset for %s/%s (%s)", rel.TagName, runtime.GOOS, runtime.GOARCH, name)
	}

	archive, err := download(asset.DownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if err := verifyChecksum(rel, name, archive); err != nil {
		return err
	}

	binary, err := extractBinary(archive, name)
	if err != nil {
		return err
	}

	err = selfupdate.Apply(bytes.NewReader(binary), selfupdate.Options{TargetPath: targetPath})
	if err != nil {
		if rbErr := selfupdate.RollbackError(err); rbErr != nil {
			return fmt.Errorf("update failed and the old binary could not be restored (%v), reinstall manually: %w", rbErr, err)
		}
		return err
	}
	return nil
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
