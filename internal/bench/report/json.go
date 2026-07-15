// Package report renders a runner.SuiteResult in several forms — a colored
// terminal summary, machine-readable JSON, JUnit XML for CI, an A/B comparison
// table, and a diff against a saved baseline — plus the run-history store under
// ~/.docsgpt/bench.
package report

import (
	"encoding/json"
	"io"

	"docsgpt-cli/internal/bench/runner"
)

// JSON writes the indented SuiteResult document (schema_version 1) to w.
func JSON(w io.Writer, r *runner.SuiteResult) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = runner.SchemaVersion
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
