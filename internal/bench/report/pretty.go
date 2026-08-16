package report

import (
	"fmt"
	"io"
	"strings"

	"docsgpt-cli/internal/bench/assert"
	"docsgpt-cli/internal/bench/runner"
	"docsgpt-cli/internal/display"
)

// verboseAnswerLimit caps how much of each run's answer is echoed in verbose
// mode.
const verboseAnswerLimit = 2000

// Pretty writes a colored, aligned terminal report. verbose additionally prints
// each run's answer and judge reasoning. It relies on the active display theme
// (display.InitTheme / UsePlainTheme), degrading to plain text when color is
// unavailable.
func Pretty(w io.Writer, r *runner.SuiteResult, verbose bool) {
	printHeader(w, r)

	nameWidth := caseNameWidth(r.Cases)
	for _, c := range r.Cases {
		printCaseLine(w, c, nameWidth)
		switch c.Status {
		case runner.StatusFail:
			printFailingAssertions(w, c)
		case runner.StatusError:
			printCaseErrors(w, c)
		}
		if verbose {
			printVerbose(w, c)
		}
	}

	printSummary(w, r)
}

func printHeader(w io.Writer, r *runner.SuiteResult) {
	title := display.Accent("docsgpt bench") + " " + display.Muted("·") + " " + r.Suite
	meta := []string{}
	if r.AgentLabel != "" {
		meta = append(meta, display.Muted("agent: ")+r.AgentLabel)
	}
	if r.Target != "" {
		meta = append(meta, display.Muted("target: ")+r.Target)
	}
	if r.Model != "" {
		meta = append(meta, display.Muted("model: ")+r.Model)
	}
	if r.RunTag != "" {
		meta = append(meta, display.Muted("tag: ")+r.RunTag)
	}
	line := title
	if len(meta) > 0 {
		line += "   " + strings.Join(meta, "   ")
	}
	fmt.Fprintln(w, line)
	fmt.Fprintln(w)
}

// printCaseLine renders the single status line for a case.
func printCaseLine(w io.Writer, c *runner.CaseResult, nameWidth int) {
	name := truncate(c.Name, nameWidth) // rune-safe: never slice mid-UTF-8
	extras := caseExtras(c)
	dur := ""
	if c.Status != runner.StatusSkip {
		dur = formatDuration(c.DurationMS)
	}
	fmt.Fprintf(w, " %s  %-*s  %8s%s\n", statusTag(c.Status), nameWidth, name, dur, extras)
}

// caseExtras builds the trailing "runs P/R", token count, or skip reason.
func caseExtras(c *runner.CaseResult) string {
	var parts []string
	if c.Status == runner.StatusSkip {
		if c.SkipReason != "" {
			return "  " + display.Muted("("+c.SkipReason+")")
		}
		return ""
	}
	if c.Repeat > 1 {
		parts = append(parts, fmt.Sprintf("runs %d/%d", c.PassedRuns, c.Repeat))
	}
	if tok := caseTokens(c); tok > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", tok))
	}
	if cost, ok := caseCost(c); ok {
		parts = append(parts, formatUSD(cost))
	}
	if ttft := caseFirstOutputMS(c); ttft > 0 {
		parts = append(parts, fmt.Sprintf("ttft %.2fs", float64(ttft)/1000))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + display.Muted(strings.Join(parts, "  "))
}

// caseFirstOutputMS is the mean time-to-first-token across runs that observed
// it, or 0.
func caseFirstOutputMS(c *runner.CaseResult) int64 {
	var sum, n int64
	for _, rr := range c.Runs {
		if rr.FirstOutputMS > 0 {
			sum += rr.FirstOutputMS
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// printFailingAssertions lists each failing assertion (name, then message)
// under a failed case, de-duplicated across runs.
func printFailingAssertions(w io.Writer, c *runner.CaseResult) {
	seen := make(map[string]bool)
	for _, rr := range c.Runs {
		for _, a := range rr.Assertions {
			if a.Status != assert.StatusFail {
				continue
			}
			key := a.Name + "\x00" + a.Message
			if seen[key] {
				continue
			}
			seen[key] = true
			fmt.Fprintf(w, "        %s\n", display.Danger(a.Name))
			for _, line := range strings.Split(a.Message, "\n") {
				fmt.Fprintf(w, "          %s\n", display.Muted(line))
			}
		}
	}
}

// printCaseErrors lists the run-level errors under an errored case.
func printCaseErrors(w io.Writer, c *runner.CaseResult) {
	seen := make(map[string]bool)
	for _, rr := range c.Runs {
		if rr.Error == "" || seen[rr.Error] {
			continue
		}
		seen[rr.Error] = true
		fmt.Fprintf(w, "        %s\n", display.Danger(rr.Error))
	}
}

// printVerbose echoes each run's answer (every turn for multi-turn cases),
// the asserted server error for negative cases, and any judge reasoning.
func printVerbose(w io.Writer, c *runner.CaseResult) {
	for _, rr := range c.Runs {
		for _, tr := range rr.Turns {
			fmt.Fprintf(w, "      %s %s\n", display.Muted(fmt.Sprintf("run %d turn %d:", rr.Index, tr.Index)), truncate(strings.ReplaceAll(tr.Question, "\n", " "), 120))
			for _, line := range strings.Split(truncate(tr.Answer, verboseAnswerLimit), "\n") {
				fmt.Fprintf(w, "        %s\n", line)
			}
		}
		if rr.Answer != "" && len(rr.Turns) == 0 {
			fmt.Fprintf(w, "      %s\n", display.Muted(fmt.Sprintf("run %d answer:", rr.Index)))
			for _, line := range strings.Split(truncate(rr.Answer, verboseAnswerLimit), "\n") {
				fmt.Fprintf(w, "        %s\n", line)
			}
		}
		if er := rr.ErrorResponse; er != nil {
			label := "server error:"
			if er.Status > 0 {
				label = fmt.Sprintf("server error (HTTP %d):", er.Status)
			}
			fmt.Fprintf(w, "      %s %s\n", display.Muted(fmt.Sprintf("run %d %s", rr.Index, label)), truncate(er.Message, verboseAnswerLimit))
		}
		for _, a := range rr.Assertions {
			if strings.HasSuffix(a.Name, "judge") && a.Message != "" {
				fmt.Fprintf(w, "      %s %s\n", display.Muted(a.Name+":"), a.Message)
			}
		}
	}
}

func printSummary(w io.Writer, r *runner.SuiteResult) {
	fmt.Fprintln(w)
	t := r.Totals
	parts := []string{
		display.Success(fmt.Sprintf("%d passed", t.Pass)),
		countStyled(t.Fail, "failed", display.Danger),
		countStyled(t.Skip, "skipped", display.Warn),
		countStyled(t.Error, "errored", display.Danger),
	}
	summary := "Summary: " + strings.Join(parts, ", ")
	summary += "  " + display.Muted("·") + "  " + formatDuration(r.DurationMS)
	if tok := suiteTokens(r); tok > 0 {
		summary += "  " + display.Muted("·") + fmt.Sprintf("  %d tokens", tok)
	}
	if cost, ok := suiteCost(r); ok {
		summary += "  " + display.Muted("·") + "  " + formatUSD(cost)
	}
	fmt.Fprintln(w, summary)

	// Latency distribution across executed runs, TTFT where observed, and the
	// mean judge score when any case was judged.
	var stats []string
	if lat := runLatencies(r); len(lat) > 0 {
		stats = append(stats, fmt.Sprintf("latency p50 %.2fs p95 %.2fs", percentile(lat, 50)/1000, percentile(lat, 95)/1000))
	}
	if ttft := runFirstOutputs(r); len(ttft) > 0 {
		stats = append(stats, fmt.Sprintf("ttft p50 %.2fs p95 %.2fs", percentile(ttft, 50)/1000, percentile(ttft, 95)/1000))
	}
	if js := judgeScores(r); len(js) > 0 {
		stats = append(stats, fmt.Sprintf("judge mean %.2f (n=%d)", mean(js), len(js)))
	}
	if len(stats) > 0 {
		fmt.Fprintln(w, display.Muted("         "+strings.Join(stats, "  ·  ")))
	}
}

// countStyled colors a non-zero count and leaves a zero count muted.
func countStyled(n int, label string, style func(string) string) string {
	s := fmt.Sprintf("%d %s", n, label)
	if n == 0 {
		return display.Muted(s)
	}
	return style(s)
}

// statusTag returns a fixed-width (5 visible chars) colored status label.
func statusTag(s runner.Status) string {
	switch s {
	case runner.StatusPass:
		return display.Success("PASS ")
	case runner.StatusFail:
		return display.Danger("FAIL ")
	case runner.StatusSkip:
		return display.Warn("SKIP ")
	case runner.StatusError:
		return display.Danger("ERROR")
	default:
		return display.Muted(string(s))
	}
}

// caseNameWidth is the padding width for the case-name column, clamped so long
// names don't blow out the layout.
func caseNameWidth(cases []*runner.CaseResult) int {
	const minW, maxW = 12, 48
	w := minW
	for _, c := range cases {
		if len(c.Name) > w {
			w = len(c.Name)
		}
	}
	if w > maxW {
		w = maxW
	}
	return w
}
