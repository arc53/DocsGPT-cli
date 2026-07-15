package report

import (
	"fmt"
	"io"
	"strings"

	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/display"
)

// Compare writes an A/B (or A/B/C…) table: one row per case, one column per
// run, each cell showing that run's case status, latency, and total tokens.
// Cases absent from a run render as "-". A footer summarizes pass counts.
func Compare(w io.Writer, results []*runner.SuiteResult) {
	if len(results) == 0 {
		return
	}

	names := orderedCaseNames(results)
	maps := make([]map[string]*runner.CaseResult, len(results))
	for i, r := range results {
		maps[i] = indexCases(r)
	}

	// Column 0 is the case name; the rest are one per result.
	nameW := len("Case")
	for _, n := range names {
		if len(n) > nameW {
			nameW = len(n)
		}
	}
	if nameW > 48 {
		nameW = 48
	}

	colW := make([]int, len(results))
	for i, r := range results {
		colW[i] = len(columnLabel(r, i))
		for _, n := range names {
			if cell := plainCell(maps[i][n]); len(cell) > colW[i] {
				colW[i] = len(cell)
			}
		}
	}

	// Header.
	var b strings.Builder
	b.WriteString(pad("Case", nameW))
	for i, r := range results {
		b.WriteString("  ")
		b.WriteString(pad(columnLabel(r, i), colW[i]))
	}
	fmt.Fprintln(w, display.Muted(b.String()))

	// Rows.
	for _, n := range names {
		var row strings.Builder
		row.WriteString(pad(truncate(n, nameW), nameW))
		for i := range results {
			row.WriteString("  ")
			row.WriteString(styledCell(maps[i][n], colW[i]))
		}
		fmt.Fprintln(w, row.String())
	}

	// Footer: pass counts and pass rate per column.
	var footer strings.Builder
	footer.WriteString(pad("TOTAL", nameW))
	for i, r := range results {
		footer.WriteString("  ")
		footer.WriteString(pad(passRate(r), colW[i]))
	}
	fmt.Fprintln(w, display.Muted(footer.String()))
}

// columnLabel is the header for a result's column: its agent label, falling
// back to a positional name.
func columnLabel(r *runner.SuiteResult, i int) string {
	if r.AgentLabel != "" {
		return r.AgentLabel
	}
	return fmt.Sprintf("run %d", i+1)
}

// plainCell renders a cell's uncolored text (for width measurement).
func plainCell(c *runner.CaseResult) string {
	if c == nil {
		return "-"
	}
	cell := strings.ToUpper(string(c.Status)) + " " + formatDuration(c.DurationMS)
	if tok := caseTokens(c); tok > 0 {
		cell += fmt.Sprintf(" %dt", tok)
	}
	return cell
}

// styledCell pads the plain cell then tints the whole cell by status.
func styledCell(c *runner.CaseResult, width int) string {
	padded := pad(plainCell(c), width)
	if c == nil {
		return display.Muted(padded)
	}
	switch c.Status {
	case runner.StatusPass:
		return display.Success(padded)
	case runner.StatusFail, runner.StatusError:
		return display.Danger(padded)
	case runner.StatusSkip:
		return display.Warn(padded)
	default:
		return padded
	}
}

func passRate(r *runner.SuiteResult) string {
	total := len(r.Cases)
	if total == 0 {
		return "0/0"
	}
	pct := 100 * float64(r.Totals.Pass) / float64(total)
	return fmt.Sprintf("%d/%d (%.0f%%)", r.Totals.Pass, total, pct)
}

// orderedCaseNames returns the union of case names, preserving first-seen order
// across results.
func orderedCaseNames(results []*runner.SuiteResult) []string {
	var names []string
	seen := make(map[string]bool)
	for _, r := range results {
		for _, c := range r.Cases {
			if !seen[c.Name] {
				seen[c.Name] = true
				names = append(names, c.Name)
			}
		}
	}
	return names
}

func indexCases(r *runner.SuiteResult) map[string]*runner.CaseResult {
	m := make(map[string]*runner.CaseResult, len(r.Cases))
	for _, c := range r.Cases {
		m[c.Name] = c
	}
	return m
}

// pad right-pads s with spaces to width (no truncation).
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
