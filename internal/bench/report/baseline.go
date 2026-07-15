package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"
)

// benchHome returns the run-history root (~/.docsgpt/bench). It is a var so
// tests can redirect history writes to a temp directory.
var benchHome = func() string {
	return filepath.Join(config.Dir(), "bench")
}

const latestName = "latest.json"

// SaveRun persists r under ~/.docsgpt/bench/<suite>/ as a timestamped file and
// (over)writes latest.json. It returns the timestamped file path.
func SaveRun(r *runner.SuiteResult) (string, error) {
	dir := filepath.Join(benchHome(), sanitizeSuite(r.Suite))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create history dir: %w", err)
	}

	ts := r.StartedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	base := ts.Format("20060102-150405")
	path := filepath.Join(dir, base+".json")
	for i := 1; fileExists(path); i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.json", base, i))
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal run: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write run history: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, latestName), data, 0o644); err != nil {
		return "", fmt.Errorf("write latest.json: %w", err)
	}
	return path, nil
}

// LoadBaseline loads a saved run. ref "last" (or "") reads the suite's
// latest.json; any other ref is treated as a file path, with a fallback lookup
// inside the suite's history directory for convenience.
func LoadBaseline(suiteName, ref string) (*runner.SuiteResult, error) {
	var path string
	if ref == "" || ref == "last" {
		path = filepath.Join(benchHome(), sanitizeSuite(suiteName), latestName)
	} else {
		path = ref
		if !fileExists(path) {
			alt := filepath.Join(benchHome(), sanitizeSuite(suiteName), ref)
			if fileExists(alt) {
				path = alt
			}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load baseline %s: %w", path, err)
	}
	var r runner.SuiteResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return &r, nil
}

// Diff writes a human-readable comparison of cur against base and returns the
// number of regressions (a case that passed in base and now fails or errors).
func Diff(w io.Writer, base, cur *runner.SuiteResult) (regressions int) {
	baseMap := indexCases(base)
	curMap := indexCases(cur)

	when := base.StartedAt.Format("2006-01-02 15:04:05")
	fmt.Fprintln(w, display.Accent("Diff vs baseline")+" "+display.Muted("("+when+")"))

	changes := 0
	for _, c := range cur.Cases {
		b, ok := baseMap[c.Name]
		if !ok {
			changes++
			fmt.Fprintf(w, "  %s %s %s\n", display.Info("+"), c.Name, display.Muted("(new, "+string(c.Status)+")"))
			continue
		}
		if b.Status == c.Status {
			continue
		}
		changes++
		switch {
		case b.Status == runner.StatusPass && (c.Status == runner.StatusFail || c.Status == runner.StatusError):
			regressions++
			fmt.Fprintf(w, "  %s %s %s\n", display.Danger("REGRESSED"), c.Name,
				display.Muted(fmt.Sprintf("(%s → %s)", b.Status, c.Status)))
		case c.Status == runner.StatusPass && (b.Status == runner.StatusFail || b.Status == runner.StatusError):
			fmt.Fprintf(w, "  %s %s %s\n", display.Success("FIXED"), c.Name,
				display.Muted(fmt.Sprintf("(%s → %s)", b.Status, c.Status)))
		default:
			fmt.Fprintf(w, "  %s %s %s\n", display.Warn("CHANGED"), c.Name,
				display.Muted(fmt.Sprintf("(%s → %s)", b.Status, c.Status)))
		}
	}
	for _, c := range base.Cases {
		if _, ok := curMap[c.Name]; !ok {
			changes++
			fmt.Fprintf(w, "  %s %s %s\n", display.Muted("-"), c.Name, display.Muted("(removed)"))
		}
	}
	if changes == 0 {
		fmt.Fprintln(w, "  "+display.Muted("no case status changes"))
	}

	// Performance deltas.
	baseLat, curLat := avgCaseLatencyMS(base), avgCaseLatencyMS(cur)
	fmt.Fprintf(w, "  %s %.2fs → %.2fs (%s)\n", display.Muted("avg latency:"),
		baseLat/1000, curLat/1000, signedSeconds(curLat-baseLat))

	baseTok, curTok := suiteTokens(base), suiteTokens(cur)
	fmt.Fprintf(w, "  %s %d → %d (%s)\n", display.Muted("total tokens:"),
		baseTok, curTok, signedInt(curTok-baseTok))

	if regressions > 0 {
		fmt.Fprintln(w, "  "+display.Danger(fmt.Sprintf("%d regression(s)", regressions)))
	}
	return regressions
}

func sanitizeSuite(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "suite"
	}
	name = strings.ReplaceAll(name, string(os.PathSeparator), "-")
	name = strings.ReplaceAll(name, "/", "-")
	return name
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func signedSeconds(ms float64) string {
	sign := "+"
	if ms < 0 {
		sign = "-"
		ms = -ms
	}
	return fmt.Sprintf("%s%.2fs", sign, ms/1000)
}

func signedInt(n int) string {
	if n >= 0 {
		return fmt.Sprintf("+%d", n)
	}
	return fmt.Sprintf("%d", n)
}
