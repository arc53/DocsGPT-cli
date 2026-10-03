package tools

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // image.DecodeConfig reads their sizes
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/attach"
)

// maxImageBytes is the largest image read_file sends; the server scales
// images down to what the model takes.
const maxImageBytes = 20 << 20

// imageFile is an image read_file sends to the model.
type imageFile struct {
	mimeType string
	data     []byte
	status   string // "PNG · 1280×800 · 240 KB"
}

// imageTypes are the images read_file sends, by the type their first bytes
// tell (http.DetectContentType).
var imageTypes = map[string]string{
	"image/png": "PNG", "image/jpeg": "JPEG", "image/gif": "GIF", "image/webp": "WebP", "image/bmp": "BMP",
}

// readImage returns the image at path, nil when the file is not one of
// imageTypes, or an error for an image too large to send.
func readImage(path string) (*imageFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	mimeType := http.DetectContentType(head[:n])
	kind, ok := imageTypes[mimeType]
	if !ok {
		return nil, nil
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxImageBytes {
		return nil, fmt.Errorf("the image is %s, more than read_file sends (%s)", attach.Size(fi.Size()), attach.Size(maxImageBytes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	status := []string{kind}
	if c, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		status = append(status, fmt.Sprintf("%d×%d", c.Width, c.Height))
	}
	status = append(status, attach.Size(int64(len(data))))
	return &imageFile{mimeType, data, strings.Join(status, " · ")}, nil
}

// readText returns the text file at path, refusing a binary file or one
// over limit bytes.
func readText(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	switch {
	case err != nil:
		return "", err
	case int64(len(data)) > limit:
		return "", fmt.Errorf("%s is larger than %s; change it with write_file or run_command", path, attach.Size(limit))
	case bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0:
		return "", fmt.Errorf("%s is a binary file", path)
	}
	return string(data), nil
}
