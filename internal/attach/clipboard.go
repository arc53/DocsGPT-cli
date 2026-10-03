package attach

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // clipboard images to convert
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

// Clip is what the system clipboard holds for Ctrl+V: files copied (in
// Finder or Explorer), else an image (saved as a PNG where Read was told),
// else text.
type Clip struct {
	Files []string
	Image bool
	Text  string
}

// Clipboard reads the system clipboard, an image into a PNG at imagePath.
type Clipboard interface {
	Read(imagePath string) (Clip, error)
}

// System is the system clipboard: osascript on macOS, wl-paste or xclip
// on Linux, PowerShell on Windows; its text through the usual tools.
var System Clipboard = system{}

// Paste reads cb for Ctrl+V: the files it holds or its image (saved in the
// temporary directory) as attachments, else its text.
func Paste(cb Clipboard) (files []File, text string, err error) {
	dir := filepath.Join(os.TempDir(), "docsgpt-clipboard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	id := make([]byte, 3)
	rand.Read(id)
	path := filepath.Join(dir, "image-"+time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(id)+".png")
	clip, err := cb.Read(path)
	if err != nil {
		return nil, "", err
	}
	switch {
	case len(clip.Files) > 0:
		for _, p := range clip.Files {
			f, err := Open(p)
			if err != nil {
				return nil, "", err
			}
			files = append(files, f)
		}
		return files, "", nil
	case clip.Image:
		f, err := Open(path)
		f.Clipboard = true
		if err == nil && !f.Image {
			err = errors.New("the clipboard's image could not be read")
		}
		if err != nil {
			os.Remove(path)
			return nil, "", err
		}
		return []File{f}, "", nil
	}
	return nil, clip.Text, nil
}

// clipTimeout bounds a clipboard tool.
const clipTimeout = 5 * time.Second

// run runs a clipboard tool and returns its output (a variable for tests).
var run = func(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), clipTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%s: %s", name, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// readText is the clipboard's text (a variable for tests).
var readText = clipboard.ReadAll

type system struct{}

func (system) Read(imagePath string) (Clip, error) {
	var clip Clip
	var err error
	switch runtime.GOOS {
	case "darwin":
		clip, err = readMac(imagePath)
	case "windows":
		clip, err = readWindows(imagePath)
	default:
		clip, err = readUnix(imagePath)
	}
	if err != nil || len(clip.Files) > 0 || clip.Image {
		return clip, err
	}
	clip.Text, err = readText()
	if err != nil {
		return clip, fmt.Errorf("no image on the clipboard, and its text could not be read: %w", err)
	}
	return clip, nil
}

// macScript prints "files" and the paths of the files copied, else saves
// the image (PNG, or anything AppKit reads, converted) at argv[0] and
// prints "image", else prints "none".
const macScript = `ObjC.import('AppKit');
function run(argv) {
  var pb = $.NSPasteboard.generalPasteboard;
  var items = pb.pasteboardItems, files = [];
  for (var i = 0; items && !items.isNil() && i < items.count; i++) {
    var u = items.objectAtIndex(i).stringForType('public.file-url');
    if (!u.isNil()) files.push($.NSURL.URLWithString(u).path.js);
  }
  if (files.length) return 'files\n' + files.join('\n');
  var data = pb.dataForType('public.png');
  if (data.isNil()) {
    var img = $.NSImage.alloc.initWithPasteboard(pb);
    if (img.isNil()) return 'none';
    var rep = $.NSBitmapImageRep.imageRepWithData(img.TIFFRepresentation);
    if (rep.isNil()) return 'none';
    data = rep.representationUsingTypeProperties($.NSBitmapImageFileTypePNG, $({}));
  }
  if (data.isNil() || !data.writeToFileAtomically(argv[0], true)) return 'none';
  return 'image';
}`

func readMac(imagePath string) (Clip, error) {
	out, err := run("osascript", "-l", "JavaScript", "-e", macScript, imagePath)
	if err != nil {
		return Clip{}, err
	}
	return parseReport(out), nil
}

// windowsScript is macScript for PowerShell; $path is set before it.
const windowsScript = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing
$files = [System.Windows.Forms.Clipboard]::GetFileDropList()
if ($files -and $files.Count -gt 0) { 'files'; $files; exit }
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); 'image'; exit }
'none'`

func readWindows(imagePath string) (Clip, error) {
	script := "$path = '" + strings.ReplaceAll(imagePath, "'", "''") + "'\n" + windowsScript
	out, err := run("powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	if err != nil {
		return Clip{}, err
	}
	return parseReport(out), nil
}

// parseReport reads what macScript and windowsScript print.
func parseReport(out []byte) Clip {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n"), "\n")
	switch lines[0] {
	case "files":
		var c Clip
		for _, l := range lines[1:] {
			if l = strings.TrimSpace(l); l != "" {
				c.Files = append(c.Files, l)
			}
		}
		return c
	case "image":
		return Clip{Image: true}
	}
	return Clip{}
}

// imageTypes are the clipboard's image types taken, the first one held
// winning; the others are converted to PNG.
var imageTypes = []string{"image/png", "image/jpeg", "image/jpg"}

// readUnix reads the Wayland clipboard (wl-paste) or the X one (xclip).
func readUnix(imagePath string) (Clip, error) {
	list, get := []string{"wl-paste", "--list-types"}, func(t string) []string { return []string{"wl-paste", "--no-newline", "--type", t} }
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		list = []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"}
		get = func(t string) []string { return []string{"xclip", "-selection", "clipboard", "-t", t, "-o"} }
	}
	if _, err := exec.LookPath(list[0]); err != nil {
		return Clip{}, nil // the text, as copying does
	}
	out, err := run(list[0], list[1:]...)
	if err != nil {
		return Clip{}, nil // an empty clipboard, for xclip
	}
	types := strings.Fields(string(out))
	if slices.Contains(types, "text/uri-list") {
		if b, err := run(get("text/uri-list")[0], get("text/uri-list")[1:]...); err == nil {
			if files := fileURIs(string(b)); len(files) > 0 {
				return Clip{Files: files}, nil
			}
		}
	}
	for _, t := range imageTypes {
		if !slices.Contains(types, t) {
			continue
		}
		args := get(t)
		data, err := run(args[0], args[1:]...)
		if err != nil {
			return Clip{}, err
		}
		if t != "image/png" {
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return Clip{}, fmt.Errorf("the clipboard's image: %w", err)
			}
			var b bytes.Buffer
			png.Encode(&b, img)
			data = b.Bytes()
		}
		if err := os.WriteFile(imagePath, data, 0o600); err != nil {
			return Clip{}, err
		}
		return Clip{Image: true}, nil
	}
	return Clip{}, nil
}

// fileURIs returns the paths of the file:// URIs of a text/uri-list.
func fileURIs(list string) []string {
	var paths []string
	for _, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		u, err := url.Parse(l)
		if err != nil || u.Scheme != "file" {
			return nil
		}
		paths = append(paths, u.Path)
	}
	return paths
}
