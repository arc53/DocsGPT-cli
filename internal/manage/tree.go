package manage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// sourceExtensions are the file types the server parses into a source
// (its SUPPORTED_SOURCE_EXTENSIONS). A folder upload sends only these: the
// server would store anything else and never read it.
var sourceExtensions = map[string]bool{
	".csv": true, ".doc": true, ".docm": true, ".docx": true, ".epub": true, ".html": true,
	".jpeg": true, ".jpg": true, ".json": true, ".m4a": true, ".md": true, ".mdx": true,
	".mp3": true, ".odp": true, ".ods": true, ".odt": true, ".ogg": true, ".pdf": true,
	".png": true, ".pot": true, ".pps": true, ".ppsm": true, ".ppsx": true, ".ppt": true,
	".pptm": true, ".pptx": true, ".rst": true, ".rtf": true, ".txt": true, ".wav": true,
	".webm": true, ".xhtml": true, ".xls": true, ".xlsb": true, ".xlsm": true, ".xlsx": true,
}

// CollectOptions narrow what a folder contributes. Both lists hold glob
// patterns (path.Match syntax) tried against a file's path inside the folder
// and against each of its path elements, so "drafts" leaves out a folder of
// that name at any depth and "*.pdf" every PDF.
type CollectOptions struct {
	Include []string // when set, only files matching one of them
	Exclude []string // files and folders to leave out
}

// SkippedFile is a file of a folder that is not uploaded, and why.
type SkippedFile struct {
	Path   string
	Reason string
}

// Collected is what the arguments of an upload hold.
type Collected struct {
	// Files to upload. With Folders they are sorted by name and every name is
	// unique; plain file arguments keep their order.
	Files    []UploadFile
	Skipped  []SkippedFile
	Folders  bool // an argument was a directory
	Archives bool // an argument is a .zip archive, which the server unpacks
	// Duplicate is a name two plain file arguments share ("" if none); the
	// server keeps both, the second as name_2.
	Duplicate string
}

// CollectFiles expands the arguments of an upload. A file is sent under its
// base name. A directory contributes every file under it the server parses,
// each under its path inside the directory: hidden files and folders, empty
// files, other file types and symlinked folders are skipped and listed.
// Names are NFC-normalized, so a tree hashes and syncs the same from macOS
// and Linux.
func CollectFiles(args []string, opts CollectOptions) (*Collected, error) {
	for _, pattern := range append(append([]string(nil), opts.Include...), opts.Exclude...) {
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("bad pattern %q: %w", pattern, err)
		}
	}
	out := &Collected{}
	seen := map[string]string{} // name -> path
	fromFolder := map[string]bool{}
	add := func(file UploadFile, folder bool) error {
		if prev, dup := seen[file.Name]; dup {
			if folder || fromFolder[file.Name] {
				return fmt.Errorf("%s and %s would both be stored as %q", prev, file.Path, file.Name)
			}
			if out.Duplicate == "" {
				out.Duplicate = file.Name
			}
		}
		seen[file.Name] = file.Path
		fromFolder[file.Name] = fromFolder[file.Name] || folder
		out.Files = append(out.Files, file)
		return nil
	}
	for _, arg := range args {
		st, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			if !st.Mode().IsRegular() {
				return nil, fmt.Errorf("%s is not a regular file", arg)
			}
			if strings.EqualFold(filepath.Ext(arg), ".zip") {
				out.Archives = true
			}
			if err := add(UploadFile{Path: arg, Name: norm.NFC.String(filepath.Base(arg)), Size: st.Size()}, false); err != nil {
				return nil, err
			}
			continue
		}
		out.Folders = true
		if err := collectFolder(arg, opts, out, add); err != nil {
			return nil, err
		}
	}
	if out.Folders {
		sort.SliceStable(out.Files, func(i, j int) bool { return out.Files[i].Name < out.Files[j].Name })
	}
	sort.SliceStable(out.Skipped, func(i, j int) bool { return out.Skipped[i].Path < out.Skipped[j].Path })
	return out, nil
}

func collectFolder(root string, opts CollectOptions, out *Collected, add func(UploadFile, bool) error) error {
	skip := func(p, reason string) {
		out.Skipped = append(out.Skipped, SkippedFile{Path: p, Reason: reason})
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		name := norm.NFC.String(filepath.ToSlash(rel))
		base := path.Base(name)
		if strings.HasPrefix(base, ".") {
			// Not listed: every project has them, and the server ignores them too.
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if matchesAny(opts.Exclude, name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			skip(p, "excluded")
			return nil
		}
		if d.IsDir() {
			return nil
		}
		st, err := os.Stat(p) // follows a symlink to its file
		if err != nil {
			if d.Type()&fs.ModeSymlink != 0 {
				skip(p, "broken link")
				return nil
			}
			return err
		}
		switch {
		case st.IsDir():
			skip(p, "linked folder")
		case !st.Mode().IsRegular():
			skip(p, "not a regular file")
		case !sourceExtensions[strings.ToLower(path.Ext(base))]:
			skip(p, "file type not supported")
		case len(opts.Include) > 0 && !matchesAny(opts.Include, name):
			skip(p, "not included")
		case st.Size() == 0:
			skip(p, "empty")
		case !validName(name):
			skip(p, "name has a backslash or a control character")
		default:
			return add(UploadFile{Path: p, Name: name, Size: st.Size()}, true)
		}
		return nil
	})
}

// matchesAny reports whether a pattern matches the name, one of its path
// elements, or one of the folders it is in.
func matchesAny(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return false
	}
	elems := strings.Split(name, "/")
	for _, pattern := range patterns {
		pattern = strings.Trim(path.Clean(filepath.ToSlash(pattern)), "/")
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
		for i, elem := range elems {
			if ok, _ := path.Match(pattern, elem); ok {
				return true
			}
			if ok, _ := path.Match(pattern, strings.Join(elems[:i+1], "/")); ok {
				return true
			}
		}
	}
	return false
}

// validName reports whether the server's manifest accepts the name: "/" is
// its only separator and it holds no control characters.
func validName(name string) bool {
	for _, r := range name {
		if r == '\\' || unicode.IsControl(r) {
			return false
		}
	}
	return name != "" && len([]rune(name)) <= 1024
}

// HashFiles fills in the SHA-256 (and the current size) of every file.
func HashFiles(files []UploadFile) error {
	for i := range files {
		sum, size, err := hashFileSize(files[i].Path)
		if err != nil {
			return err
		}
		files[i].SHA256, files[i].Size = sum, size
	}
	return nil
}

func hashFileSize(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// TreeHash identifies a set of hashed files: their names and contents, in
// any order.
func TreeHash(files []UploadFile) string {
	entries := make([]string, len(files))
	for i, f := range files {
		entries[i] = f.name() + "\x00" + f.SHA256
	}
	sort.Strings(entries)
	h := sha256.New()
	io.WriteString(h, "docsgpt-cli/tree/v1")
	for _, e := range entries {
		io.WriteString(h, "\x00")
		io.WriteString(h, e)
	}
	return hex.EncodeToString(h.Sum(nil))
}
