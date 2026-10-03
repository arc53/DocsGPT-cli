package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestWhatsNewAfterStagedUpdate: the worker's check records the notes, the
// staged update applies, and the new version finds them once.
func TestWhatsNewAfterStagedUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test archive is tar.gz")
	}
	setupTestHome(t)
	rel := serveRelease(t, "v1.3.0", []byte("new"))
	rel.Body = "## Changelog\n* abc1234 feat: a thing"
	RecordCheck(rel)
	if err := Stage(rel); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "docsgpt-cli")
	os.WriteFile(target, []byte("old"), 0o755)

	if _, ok := PendingNotes("v1.2.0"); ok {
		t.Fatal("notes pending before the update")
	}
	if v, err := ApplyStaged("v1.2.0", target); err != nil || v != "v1.3.0" {
		t.Fatalf("ApplyStaged() = %q, %v", v, err)
	}
	if _, ok := PendingNotes("v1.2.0"); ok {
		t.Fatal("the old binary, still running, took the notes")
	}
	// GoReleaser stamps the version without the v.
	notes, ok := PendingNotes("1.3.0")
	if !ok || notes.Body != rel.Body {
		t.Fatalf("PendingNotes() = %+v, %v", notes, ok)
	}
	ShownNotes()
	if _, ok := PendingNotes("1.3.0"); ok {
		t.Fatal("notes shown twice")
	}
}

func TestWhatsNewNeedsTheNotesOfThatVersion(t *testing.T) {
	setupTestHome(t)
	RecordCheck(&Release{TagName: "v1.4.0", Body: "newer"})
	MarkUpdated("v1.3.0")
	if _, ok := PendingNotes("v1.3.0"); ok {
		t.Fatal("showed the notes of another release")
	}
	RecordCheck(&Release{TagName: "v1.3.0", Body: "  "})
	if _, ok := PendingNotes("v1.3.0"); ok {
		t.Fatal("showed empty notes")
	}
}

func TestLatestNotes(t *testing.T) {
	setupTestHome(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(Release{TagName: "v2.0.0", Body: "fresh", HTMLURL: "https://x/v2"})
	}))
	defer srv.Close()
	old := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = old })

	ctx := context.Background()
	if n, err := LatestNotes(ctx); err != nil || n.Body != "fresh" || calls != 1 {
		t.Fatalf("LatestNotes() = %+v, %v after %d calls", n, err, calls)
	}
	if n, _ := LatestNotes(ctx); n.Body != "fresh" || calls != 1 {
		t.Fatalf("a recent check was not reused: %d calls", calls)
	}

	// Offline, an old check still answers.
	st := loadState()
	st.LastChecked = time.Now().Add(-48 * time.Hour)
	saveState(st)
	srv.Close()
	if n, err := LatestNotes(ctx); err != nil || n.Version != "v2.0.0" {
		t.Fatalf("offline: %+v, %v", n, err)
	}
	setupTestHome(t)
	if _, err := LatestNotes(ctx); err == nil {
		t.Fatal("no error offline with nothing recorded")
	}
}

func TestTidyNotes(t *testing.T) {
	body := "## Changelog\r\n" +
		"* 756d6bff899eaf16652c090b5acb018373e6b35d Merge pull request #6 from arc53/feat/x\n" +
		"* 7f299b4ff560d644161d9b6888404290fff98e4e feat(agents): trigger a webhook\n" +
		"* fccea01 fix: something\n" +
		"- plain item\n"
	want := "* feat(agents): trigger a webhook\n* fix: something\n- plain item"
	if got := TidyNotes(body); got != want {
		t.Errorf("TidyNotes() = %q, want %q", got, want)
	}
}
