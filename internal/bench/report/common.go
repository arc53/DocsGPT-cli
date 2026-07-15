package report

import (
	"fmt"

	"docsgpt-cli/internal/bench/runner"
)

// formatDuration renders a millisecond duration as seconds with two decimals.
func formatDuration(ms int64) string {
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// caseTokens sums the total tokens reported across a case's runs (usage is only
// populated by the v1 target).
func caseTokens(c *runner.CaseResult) int {
	total := 0
	for _, rr := range c.Runs {
		if rr.Usage != nil {
			total += rr.Usage.TotalTokens
		}
	}
	return total
}

// suiteTokens sums total tokens across every case in a run.
func suiteTokens(r *runner.SuiteResult) int {
	total := 0
	for _, c := range r.Cases {
		total += caseTokens(c)
	}
	return total
}

// avgCaseLatencyMS is the mean per-case wall duration in milliseconds.
func avgCaseLatencyMS(r *runner.SuiteResult) float64 {
	if len(r.Cases) == 0 {
		return 0
	}
	var sum int64
	for _, c := range r.Cases {
		sum += c.DurationMS
	}
	return float64(sum) / float64(len(r.Cases))
}

// truncate shortens s to max runes, appending an ellipsis when it overflows.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
