package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"docsgpt-cli/internal/bench/assert"
	"docsgpt-cli/internal/bench/runner"
)

// JUnit writes a single <testsuite> with one <testcase> per case. Failing cases
// carry a <failure> (assertion messages), errored cases an <error>, skipped
// cases a <skipped>. All content is escaped by encoding/xml.
func JUnit(w io.Writer, r *runner.SuiteResult) error {
	ts := junitTestsuite{
		Name:     r.Suite,
		Tests:    len(r.Cases),
		Failures: r.Totals.Fail,
		Errors:   r.Totals.Error,
		Skipped:  r.Totals.Skip,
		Time:     seconds(r.DurationMS),
	}
	for _, c := range r.Cases {
		tc := junitTestcase{
			Classname: r.Suite,
			Name:      c.Name,
			Time:      seconds(c.DurationMS),
		}
		switch c.Status {
		case runner.StatusFail:
			tc.Failure = &junitDetail{
				Message: "assertions failed",
				Content: failingAssertionText(c),
			}
		case runner.StatusError:
			tc.Err = &junitDetail{
				Message: "run errored",
				Content: runErrorText(c),
			}
		case runner.StatusSkip:
			tc.Skipped = &junitSkipped{Message: c.SkipReason}
		}
		ts.Cases = append(ts.Cases, tc)
	}

	out, err := xml.MarshalIndent(ts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal junit: %w", err)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	if _, err := w.Write(out); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

type junitTestsuite struct {
	XMLName  xml.Name        `xml:"testsuite"`
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     float64         `xml:"time,attr"`
	Cases    []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      float64       `xml:"time,attr"`
	Failure   *junitDetail  `xml:"failure,omitempty"`
	Err       *junitDetail  `xml:"error,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitDetail struct {
	Message string `xml:"message,attr"`
	Content string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// failingAssertionText joins the failing assertions across a case's runs.
func failingAssertionText(c *runner.CaseResult) string {
	var lines []string
	multi := len(c.Runs) > 1
	for _, rr := range c.Runs {
		for _, a := range rr.Assertions {
			if a.Status != assert.StatusFail {
				continue
			}
			line := a.Name + ": " + a.Message
			if multi {
				line = fmt.Sprintf("run %d: %s", rr.Index, line)
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// runErrorText joins the run-level errors of an errored case.
func runErrorText(c *runner.CaseResult) string {
	var lines []string
	multi := len(c.Runs) > 1
	for _, rr := range c.Runs {
		if rr.Error == "" {
			continue
		}
		line := rr.Error
		if multi {
			line = fmt.Sprintf("run %d: %s", rr.Index, line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func seconds(ms int64) float64 { return float64(ms) / 1000 }
