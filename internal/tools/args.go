package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/attach"
)

// callArgs are the arguments of every tool, read leniently: models send
// numbers as strings ("120") or floats (120.0), and edits in other shapes
// (see editList).
type callArgs struct {
	Command          string   `json:"command"`
	WorkingDirectory string   `json:"working_directory"`
	Timeout          number   `json:"timeout"`
	Path             string   `json:"path"`
	Content          string   `json:"content"`
	Offset           number   `json:"offset"`
	Limit            number   `json:"limit"`
	Edits            editList `json:"edits"`
	// One edit given beside path instead of in edits.
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// number is an integer argument given as a number or a numeric string.
type number int

func (n *number) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%s is not a number", b)
	}
	*n = number(max(min(f, math.MaxInt32), math.MinInt32))
	return nil
}

// edit is one replacement of edit_file.
type edit struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// UnmarshalJSON takes old_text/new_text, or the camelCase names models
// carry over from other tools (oldText, old_string).
func (e *edit) UnmarshalJSON(b []byte) error {
	var m map[string]*string
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	pick := func(names ...string) string {
		for _, n := range names {
			if v := m[n]; v != nil {
				return *v
			}
		}
		return ""
	}
	e.OldText = pick("old_text", "oldText", "old_string", "old")
	e.NewText = pick("new_text", "newText", "new_string", "new")
	return nil
}

// editList is edit_file's edits: an array, one edit object, or either of
// those encoded as a JSON string, as some models send it.
type editList []edit

func (l *editList) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		b = []byte(s)
	}
	var list []edit
	if err := json.Unmarshal(b, &list); err == nil {
		*l = list
		return nil
	}
	var one edit
	if err := json.Unmarshal(b, &one); err != nil {
		return fmt.Errorf("edits must be an array of {old_text, new_text}")
	}
	*l = editList{one}
	return nil
}

// expandPath turns a path as a model gives it into one the system takes:
// spaces around it trimmed, a leading ~ expanded, and a leading @ (copied
// from the user's @file mention) dropped when the path without it exists.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "@"); ok && rest != "" {
		if _, err := os.Lstat(p); err != nil {
			if _, err := os.Lstat(attach.Expand(rest)); err == nil {
				p = rest
			}
		}
	}
	return attach.Expand(p)
}
