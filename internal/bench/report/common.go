package report

import (
	"fmt"
	"sort"

	"docsgpt-cli/internal/bench/runner"
)

// formatDuration renders a millisecond duration as seconds with two decimals.
func formatDuration(ms int64) string {
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

// formatUSD renders a cost with enough precision for sub-cent values.
func formatUSD(usd float64) string {
	switch {
	case usd == 0:
		return "$0"
	case usd < 0.01:
		return fmt.Sprintf("$%.4f", usd)
	case usd < 1:
		return fmt.Sprintf("$%.3f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
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

// caseCost sums the estimated cost across a case's runs; ok is false when no
// run carried a cost.
func caseCost(c *runner.CaseResult) (float64, bool) {
	total, ok := 0.0, false
	for _, rr := range c.Runs {
		if rr.CostUSD != nil {
			total += *rr.CostUSD
			ok = true
		}
	}
	return total, ok
}

// suiteCost sums the estimated cost across every case; ok is false when no run
// in the suite carried a cost.
func suiteCost(r *runner.SuiteResult) (float64, bool) {
	total, ok := 0.0, false
	for _, c := range r.Cases {
		if v, has := caseCost(c); has {
			total += v
			ok = true
		}
	}
	return total, ok
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

// runLatencies collects every executed run's latency (ms) across the suite,
// skipping synthetic error runs (latency 0 with an error and no answer).
func runLatencies(r *runner.SuiteResult) []float64 {
	var out []float64
	for _, c := range r.Cases {
		for _, rr := range c.Runs {
			if rr.LatencyMS <= 0 {
				continue
			}
			out = append(out, float64(rr.LatencyMS))
		}
	}
	return out
}

// runFirstOutputs collects every run's time-to-first-token (ms) where observed.
func runFirstOutputs(r *runner.SuiteResult) []float64 {
	var out []float64
	for _, c := range r.Cases {
		for _, rr := range c.Runs {
			if rr.FirstOutputMS > 0 {
				out = append(out, float64(rr.FirstOutputMS))
			}
		}
	}
	return out
}

// judgeScores collects every recorded judge score across the suite.
func judgeScores(r *runner.SuiteResult) []float64 {
	var out []float64
	for _, c := range r.Cases {
		for _, rr := range c.Runs {
			if rr.Judge != nil {
				out = append(out, rr.Judge.Score)
			}
		}
	}
	return out
}

// percentile returns the p-th percentile (0..100) of vals using nearest-rank
// on a sorted copy; 0 for an empty input.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[len(s)-1]
	}
	rank := int(p/100*float64(len(s))+0.5) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// truncate shortens s to max runes, appending an ellipsis when it overflows.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
