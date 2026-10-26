package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/config"
)

// A project's .mcp.json comes with the repository, and its servers are
// commands: each one starts only once the user has approved it as the file
// has it now. The answers live in ~/.docsgpt/mcp-approvals.json, by file
// and server name.

// Decision is what the user said about a project's server.
type Decision int

const (
	Undecided Decision = iota // never asked, or the entry changed since
	Approved
	Refused
)

type approval struct {
	Hash  string `json:"hash"` // of the entry approved or refused
	Allow bool   `json:"allow"`
}

func approvalsFile() string { return filepath.Join(config.Dir(), "mcp-approvals.json") }

func (s Server) key() string { return s.File + "#" + s.Name }

// hash identifies the entry as the file has it, whatever its spacing.
func (s Server) hash() string {
	var b bytes.Buffer
	if json.Compact(&b, s.raw) != nil {
		b.Write(s.raw)
	}
	sum := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(sum[:])
}

func loadApprovals() map[string]approval {
	m := map[string]approval{}
	if data, err := os.ReadFile(approvalsFile()); err == nil {
		json.Unmarshal(data, &m)
	}
	return m
}

// Decided returns what the user said about the project's server s as it is
// now. The user's own servers are always approved.
func Decided(s Server) Decision {
	if !s.Project {
		return Approved
	}
	a, ok := loadApprovals()[s.key()]
	switch {
	case !ok || a.Hash != s.hash():
		return Undecided
	case a.Allow:
		return Approved
	}
	return Refused
}

// Remember records the user's answer about the project's server s.
func Remember(s Server, allow bool) error {
	m := loadApprovals()
	m[s.key()] = approval{Hash: s.hash(), Allow: allow}
	return saveApprovals(m)
}

// Forget drops the answers about the servers of the project file, so that
// they are asked about again. It returns how many there were.
func Forget(file string) (int, error) {
	m := loadApprovals()
	n := 0
	for k := range m {
		if strings.HasPrefix(k, file+"#") {
			delete(m, k)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, saveApprovals(m)
}

// saveApprovals writes the file privately, through a temp file renamed
// into place.
func saveApprovals(m map[string]approval) error {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mcp-approvals-*.json") // 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), approvalsFile())
}
