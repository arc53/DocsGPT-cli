// Package attach holds the files a message carries: images and documents
// attached with @path, a pasted or dropped path, or Ctrl+V. They go to the
// server inline, as content parts of the message (see docsgpt.Message), and
// the server stores them as the conversation's attachments.
package attach

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/gif"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// Limits: a file at most as large as the server takes one (its default
// UPLOAD_MAX_FILE_BYTES), and a message at most that much in all, in a
// handful of files.
const (
	MaxFileBytes  = 100 << 20
	MaxTotalBytes = 100 << 20
	MaxFiles      = 20
)

// File is a file attached to a message. Sessions keep it as it is, never
// the bytes.
type File struct {
	ID        int    `json:"id,omitempty"` // the number of its marker
	Path      string `json:"path"`         // absolute
	Name      string `json:"name"`
	Image     bool   `json:"image,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`    // of the bytes sent
	Ref       string `json:"ref,omitempty"`       // the @path that attached it, as typed
	Clipboard bool   `json:"clipboard,omitempty"` // an image pasted from the clipboard
}

// Marker is how the file shows in a message: [image #1 · 240 KB],
// [image #2 · shot.png · 240 KB] or [file #3 · spec.pdf · 1.2 MB].
func (f File) Marker() string {
	kind := "file"
	if f.Image {
		kind = "image"
	}
	parts := []string{kind + " #" + strconv.Itoa(f.ID)}
	if !f.Clipboard {
		parts = append(parts, markerName(f.Name))
	}
	return "[" + strings.Join(append(parts, Size(f.Size)), " · ") + "]"
}

// markerName is a file name fit for a marker: no brackets or control
// characters, at most 40 characters.
func markerName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '[' || r == ']' || !unicode.IsPrint(r) {
			return '_'
		}
		return r
	}, name)
	if r := []rune(name); len(r) > 40 {
		name = string(r[:19]) + "…" + string(r[len(r)-20:])
	}
	return name
}

// Size renders n bytes: 512 B, 240 KB, 1.2 MB.
func Size(n int64) string {
	switch {
	case n < 1<<10:
		return strconv.FormatInt(n, 10) + " B"
	case n < 1<<20:
		return strconv.FormatInt((n+1<<9)>>10, 10) + " KB"
	}
	mb := float64(n) / (1 << 20)
	if mb < 10 {
		return strconv.FormatFloat(mb, 'f', 1, 64) + " MB"
	}
	return strconv.FormatFloat(mb, 'f', 0, 64) + " MB"
}

// documents are the extensions of the binary files the server has a parser
// for (its ATTACHMENT_PARSER_EXTENSIONS, images aside); files of any other
// extension are taken when they read as text, as the server does.
var documents = map[string]bool{}

func init() {
	for _, ext := range strings.Fields(`.pdf .docx .docm .doc .odt .rtf .pptx .pptm .ppsx .ppsm .ppt .pps
		.pot .odp .xlsx .xlsm .xlsb .xls .ods .csv .epub .html .xhtml .md .mdx .rst .json .adoc .asciidoc
		.xml .vtt .tiff .tif .bmp .wav .mp3 .m4a .ogg .webm .zip`) {
		documents[ext] = true
	}
}

// ErrType is the error for a file the server cannot read.
var ErrType = errors.New("not a type the server reads (images: PNG, JPEG, WebP, GIF; documents: PDF, Office, OpenDocument, EPUB, HTML, text)")

// Open checks that path is a file that can be attached and returns it,
// unread. ~ stands for the home directory.
func Open(path string) (File, error) {
	path = Expand(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return File{}, err
	}
	f := File{Path: abs, Name: filepath.Base(abs)}
	fi, err := os.Stat(abs)
	switch {
	case err != nil:
		return f, fmt.Errorf("%s: %w", f.Name, unwrapPath(err))
	case fi.IsDir():
		return f, fmt.Errorf("%s is a directory", f.Name)
	case !fi.Mode().IsRegular():
		return f, fmt.Errorf("%s is not a regular file", f.Name)
	case fi.Size() == 0:
		return f, fmt.Errorf("%s is empty", f.Name)
	case fi.Size() > MaxFileBytes:
		return f, fmt.Errorf("%s is %s, over the %s a file may be", f.Name, Size(fi.Size()), Size(MaxFileBytes))
	}
	f.Size = fi.Size()
	head, err := readHead(abs)
	if err != nil {
		return f, fmt.Errorf("%s: %w", f.Name, unwrapPath(err))
	}
	switch {
	case docsgpt.ImageType(head) != "":
		f.Image = true
	case documents[strings.ToLower(filepath.Ext(abs))], looksText(head):
	default:
		return f, fmt.Errorf("%s: %w", f.Name, ErrType)
	}
	return f, nil
}

// unwrapPath drops the path an *os.PathError repeats.
func unwrapPath(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// Expand replaces a leading ~ with the home directory.
func Expand(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// sniffBytes is how much of a file tells its type.
const sniffBytes = 8192

func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, b)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return b[:n], nil
}

// looksText reports whether a file's first bytes read as text: no NUL and
// few control bytes (the server's test), or a UTF-16 byte order mark.
func looksText(b []byte) bool {
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) || bytes.HasPrefix(b, []byte{0xfe, 0xff}) {
		return true
	}
	odd := 0
	for _, c := range b {
		switch {
		case c == 0:
			return false
		case c < 0x20 && !strings.ContainsRune("\t\n\v\f\r\x1b", rune(c)):
			odd++
		}
	}
	return odd*10 <= len(b)
}

// Parts reads the files into content parts, in order, and returns the
// files with the hash of what was sent. A GIF goes as a PNG of its first
// frame: the server reads no GIF.
func Parts(files []File) ([]docsgpt.ContentPart, []File, error) {
	if len(files) > MaxFiles {
		return nil, nil, fmt.Errorf("%d files attached, at most %d may go with a message", len(files), MaxFiles)
	}
	var total int64
	var parts []docsgpt.ContentPart
	out := make([]File, len(files))
	for i, f := range files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.Name, unwrapPath(err))
		}
		if len(data) > MaxFileBytes {
			return nil, nil, fmt.Errorf("%s is %s, over the %s a file may be", f.Name, Size(int64(len(data))), Size(MaxFileBytes))
		}
		if total += int64(len(data)); total > MaxTotalBytes {
			return nil, nil, fmt.Errorf("the files attached are over the %s a message may carry", Size(MaxTotalBytes))
		}
		sum := sha256.Sum256(data)
		f.Size, f.SHA256 = int64(len(data)), hex.EncodeToString(sum[:])
		name := f.Name
		if docsgpt.ImageType(data) == "image/gif" {
			if data, err = gifToPNG(data); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".png"
		}
		parts = append(parts, docsgpt.AttachmentPart(name, data))
		out[i] = f
	}
	return parts, out, nil
}

func gifToPNG(data []byte) ([]byte, error) {
	img, err := gif.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
