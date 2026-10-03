package update

import (
	"context"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// ReleasesPage lists every release with its notes.
const ReleasesPage = "https://github.com/arc53/DocsGPT-cli/releases"

// Notes are a release's notes.
type Notes struct {
	Version string `json:"version"`
	Body    string `json:"body"` // markdown, as GitHub has it
	URL     string `json:"url,omitempty"`
}

// MarkUpdated records that version was installed, so the next chat shows
// what is new in it.
func MarkUpdated(version string) {
	st := loadState()
	st.WhatsNew = version
	saveState(st)
}

// PendingNotes returns the notes of the update that installed current, when
// they are known and have not been shown yet (see ShownNotes).
func PendingNotes(current string) (Notes, bool) {
	st := loadState()
	if st.WhatsNew == "" || st.Notes == nil || !sameVersion(st.WhatsNew, current) || !sameVersion(st.Notes.Version, current) {
		return Notes{}, false
	}
	return *st.Notes, strings.TrimSpace(st.Notes.Body) != ""
}

// ShownNotes records that the pending notes were shown.
func ShownNotes() {
	st := loadState()
	if st.WhatsNew != "" {
		st.WhatsNew = ""
		saveState(st)
	}
}

// LatestNotes returns the latest release's notes: those of the last check
// when it was within a day, else fetched from GitHub (and recorded), else,
// offline, those of the last check however old.
func LatestNotes(ctx context.Context) (Notes, error) {
	st := loadState()
	if st.Notes != nil && time.Since(st.LastChecked) < checkInterval {
		return *st.Notes, nil
	}
	rel, err := FetchLatestContext(ctx)
	if err != nil {
		if st.Notes != nil {
			return *st.Notes, nil
		}
		return Notes{}, err
	}
	RecordCheck(rel)
	return Notes{Version: rel.TagName, Body: rel.Body, URL: rel.HTMLURL}, nil
}

func sameVersion(a, b string) bool {
	return semver.IsValid(normalize(a)) && semver.Compare(normalize(a), normalize(b)) == 0
}

var (
	commitLine = regexp.MustCompile(`^([*-]) [0-9a-f]{7,40} `)
	mergeLine  = regexp.MustCompile(`^[*-] (?:[0-9a-f]{7,40} )?Merge (?:pull request|branch|remote-tracking branch) `)
)

// TidyNotes readies release notes for the terminal: GoReleaser's
// "## Changelog" heading, merge commits and commit hashes go.
func TidyNotes(body string) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(trimmed, "## Changelog"), mergeLine.MatchString(trimmed):
			continue
		}
		out = append(out, commitLine.ReplaceAllString(line, "$1 "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
