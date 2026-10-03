package cmd

import (
	"slices"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/attach"
	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
)

// attachments are the files a message carries: those attached already
// (markers), then the ones its @paths name, numbered after them.
type attachments struct {
	files []attach.File // as sent, with their hashes
	parts []docsgpt.ContentPart
}

// attachFiles adds the files the @paths of text name to files and reads
// them all into content parts.
func attachFiles(text string, files []attach.File) (attachments, error) {
	id := 0
	for _, f := range files {
		id = max(id, f.ID)
	}
	all := append([]attach.File(nil), files...)
	for _, ref := range attach.Refs(text) {
		if slices.ContainsFunc(all, func(f attach.File) bool { return f.Path == ref.Path }) {
			continue
		}
		f, err := attach.Open(ref.Path)
		if err != nil {
			return attachments{}, err
		}
		id++
		f.ID, f.Ref = id, ref.Token
		all = append(all, f)
	}
	if len(all) == 0 {
		return attachments{}, nil
	}
	parts, sent, err := attach.Parts(all)
	if err != nil {
		return attachments{}, err
	}
	return attachments{sent, parts}, nil
}

// markers lists the files' markers, a space apart.
func markers(files []attach.File) string {
	var m []string
	for _, f := range files {
		m = append(m, f.Marker())
	}
	return strings.Join(m, " ")
}
