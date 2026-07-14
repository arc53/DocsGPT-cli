package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestIsReleaseVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v1.1.2", true},
		{"1.1.2", true},
		{"v10.0.0", true},
		{"dev", false},
		{"", false},
		{"v1.1.2-3-gabc123", false},
		{"v1.1.2-dirty", false},
		{"v1.1.2-3-gabc123-dirty", false},
	}
	for _, tt := range tests {
		if got := IsReleaseVersion(tt.version); got != tt.want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.0", "v1.1.2", true},
		{"v1.1.2", "v1.1.2", false},
		{"v1.1.1", "v1.1.2", false},
		{"v2.0.0", "v1.9.9", true},
		{"1.2.0", "v1.1.2", true},
		{"v1.10.0", "v1.9.0", true},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.latest, tt.current); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	name := AssetName()
	if !strings.HasPrefix(name, "docsgpt-cli_") {
		t.Errorf("AssetName() = %q, want docsgpt-cli_ prefix", name)
	}
	if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".zip") {
		t.Errorf("AssetName() = %q, want .tar.gz or .zip suffix", name)
	}
}

func TestFindChecksum(t *testing.T) {
	checksums := "abc123  docsgpt-cli_darwin_arm64.tar.gz\nDEF456  docsgpt-cli_windows_amd64.zip\n"

	got, err := findChecksum(checksums, "docsgpt-cli_darwin_arm64.tar.gz")
	if err != nil || got != "abc123" {
		t.Errorf("findChecksum() = %q, %v, want abc123, nil", got, err)
	}

	got, err = findChecksum(checksums, "docsgpt-cli_windows_amd64.zip")
	if err != nil || got != "def456" {
		t.Errorf("findChecksum() = %q, %v, want def456 (lowercased), nil", got, err)
	}

	if _, err = findChecksum(checksums, "missing.tar.gz"); err == nil {
		t.Error("findChecksum() for missing entry should error")
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	content := []byte("fake binary contents")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0644, Size: 5, Typeflag: tar.TypeReg})
	tw.Write([]byte("hello"))
	tw.WriteHeader(&tar.Header{Name: "docsgpt-cli", Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write(content)
	tw.Close()
	gz.Close()

	got, err := extractBinary(buf.Bytes(), "docsgpt-cli_linux_arm64.tar.gz")
	if err != nil {
		t.Fatalf("extractBinary() error: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extractBinary() = %q, want %q", got, content)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	content := []byte("fake windows binary")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("docsgpt-cli.exe")
	w.Write(content)
	zw.Close()

	got, err := extractBinary(buf.Bytes(), "docsgpt-cli_windows_amd64.zip")
	if err != nil {
		t.Fatalf("extractBinary() error: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extractBinary() = %q, want %q", got, content)
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "other-file", Mode: 0755, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("data"))
	tw.Close()
	gz.Close()

	if _, err := extractBinary(buf.Bytes(), "docsgpt-cli_linux_arm64.tar.gz"); err == nil {
		t.Error("extractBinary() without the binary should error")
	}
}
