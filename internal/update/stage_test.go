package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func setupTestHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
}

// serveRelease builds a valid release archive for the current platform and
// serves it (plus checksums.txt) from a local test server.
func serveRelease(t *testing.T, version string, binary []byte) *Release {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "docsgpt-cli", Mode: 0755, Size: int64(len(binary)), Typeflag: tar.TypeReg})
	tw.Write(binary)
	tw.Close()
	gz.Close()
	archive := buf.Bytes()

	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%x  %s\n", sum, AssetName())

	mux := http.NewServeMux()
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(checksums)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &Release{
		TagName: version,
		Assets: []Asset{
			{Name: AssetName(), DownloadURL: srv.URL + "/archive"},
			{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums"},
		},
	}
}

func TestStageApplyRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test archive is tar.gz")
	}
	setupTestHome(t)

	newBinary := []byte("new binary v1.3.0")
	rel := serveRelease(t, "v1.3.0", newBinary)

	if err := Stage(rel); err != nil {
		t.Fatalf("Stage() error: %v", err)
	}
	if got := StagedVersion(); got != "v1.3.0" {
		t.Fatalf("StagedVersion() = %q, want v1.3.0", got)
	}

	oldBinary := []byte("old binary v1.2.0")
	target := filepath.Join(t.TempDir(), "docsgpt-cli")
	if err := os.WriteFile(target, oldBinary, 0755); err != nil {
		t.Fatal(err)
	}

	v, err := ApplyStaged("v1.2.0", target)
	if err != nil || v != "v1.3.0" {
		t.Fatalf("ApplyStaged() = %q, %v, want v1.3.0, nil", v, err)
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, newBinary) {
		t.Fatalf("target = %q, want new binary", got)
	}
	if StagedVersion() != "" {
		t.Error("staging area not cleared after apply")
	}
	if got, _ := os.ReadFile(backupBinaryPath()); !bytes.Equal(got, oldBinary) {
		t.Fatalf("backup = %q, want old binary", got)
	}

	rolledTo, err := Rollback("v1.3.0", target)
	if err != nil || rolledTo != "v1.2.0" {
		t.Fatalf("Rollback() = %q, %v, want v1.2.0, nil", rolledTo, err)
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, oldBinary) {
		t.Fatalf("target after rollback = %q, want old binary", got)
	}
	if got, _ := os.ReadFile(backupBinaryPath()); !bytes.Equal(got, newBinary) {
		t.Fatalf("backup after rollback = %q, want new binary", got)
	}
	if got := SkipVersion(); got != "v1.3.0" {
		t.Errorf("SkipVersion() = %q, want v1.3.0", got)
	}

	// The skipped version must not be auto-applied again.
	if err := Stage(rel); err != nil {
		t.Fatal(err)
	}
	v, err = ApplyStaged("v1.2.0", target)
	if err != nil || v != "" {
		t.Fatalf("ApplyStaged() after rollback = %q, %v, want skip", v, err)
	}
}

func TestApplyStagedRejectsInvalid(t *testing.T) {
	writeManifest := func(t *testing.T, m binaryManifest, binary []byte) {
		if err := os.MkdirAll(stagingDir(), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stagedBinaryPath(), binary, 0755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(m)
		if err := os.WriteFile(stagedManifestPath(), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	binary := []byte("staged binary")
	sum := sha256.Sum256(binary)
	valid := binaryManifest{Version: "v9.0.0", SHA256: fmt.Sprintf("%x", sum), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}

	cases := []struct {
		name     string
		manifest binaryManifest
		content  []byte
	}{
		{"wrong platform", binaryManifest{Version: "v9.0.0", SHA256: valid.SHA256, GOOS: "plan9", GOARCH: "mips"}, binary},
		{"not newer", binaryManifest{Version: "v0.0.1", SHA256: valid.SHA256, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, binary},
		{"corrupt binary", valid, []byte("tampered")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupTestHome(t)
			writeManifest(t, tc.manifest, tc.content)
			target := filepath.Join(t.TempDir(), "docsgpt-cli")
			os.WriteFile(target, []byte("current"), 0755)

			v, err := ApplyStaged("v1.0.0", target)
			if v != "" || err != nil {
				t.Fatalf("ApplyStaged() = %q, %v, want rejection", v, err)
			}
			if got, _ := os.ReadFile(target); string(got) != "current" {
				t.Error("target was modified")
			}
			if StagedVersion() != "" {
				t.Error("invalid staging area not cleared")
			}
		})
	}
}

func TestCachedNoticeSkip(t *testing.T) {
	setupTestHome(t)
	RecordCheck(&Release{TagName: "v9.9.9"})
	if got := CachedNotice("v1.0.0"); got != "v9.9.9" {
		t.Fatalf("CachedNotice() = %q, want v9.9.9", got)
	}
	SetSkipVersion("v9.9.9")
	if got := CachedNotice("v1.0.0"); got != "" {
		t.Fatalf("CachedNotice() with skip = %q, want empty", got)
	}
	SetSkipVersion("")
	if got := CachedNotice("v1.0.0"); got != "v9.9.9" {
		t.Fatalf("CachedNotice() after clearing skip = %q, want v9.9.9", got)
	}
	if got := CachedNotice("v9.9.9"); got != "" {
		t.Fatalf("CachedNotice() when current = %q, want empty", got)
	}
}

func TestShouldSpawnWorker(t *testing.T) {
	setupTestHome(t)
	// No state yet: the daily check is due.
	if !ShouldSpawnWorker("v1.0.0", ModeNotify) {
		t.Error("want spawn when check is due")
	}
	if ShouldSpawnWorker("dev", ModeOn) {
		t.Error("dev builds must never spawn")
	}

	RecordCheck(&Release{TagName: "v2.0.0"})
	if !ShouldSpawnWorker("v1.0.0", ModeOn) {
		t.Error("want spawn to stage a known newer release")
	}
	if ShouldSpawnWorker("v1.0.0", ModeNotify) {
		t.Error("notify mode has nothing to stage after a fresh check")
	}
	if ShouldSpawnWorker("v2.0.0", ModeOn) {
		t.Error("up-to-date binary must not spawn")
	}
	SetSkipVersion("v2.0.0")
	if ShouldSpawnWorker("v1.0.0", ModeOn) {
		t.Error("skipped version must not spawn")
	}
}
