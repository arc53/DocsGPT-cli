// Package assert evaluates a case's expect sections against a target result.
//
// The Evaluate entry point and the answer/sources/tools/limits/golden checks
// live in evaluate.go; the expect.json path matchers and the ParseAnswerJSON
// preprocessor live in json.go.
package assert

// Status of a single assertion.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip" // assertion not applicable (e.g. token limit off-v1)
)

// Result is the outcome of one assertion.
type Result struct {
	Name    string `json:"name"`              // e.g. `answer contains "45 minutes"` or `json overall_status`
	Status  Status `json:"status"`            //
	Message string `json:"message,omitempty"` // failure/skip detail with expected vs got
}

// Result constructors keep the section evaluators terse.
func pass(name string) Result      { return Result{Name: name, Status: StatusPass} }
func fail(name, msg string) Result { return Result{Name: name, Status: StatusFail, Message: msg} }
func skip(name, msg string) Result { return Result{Name: name, Status: StatusSkip, Message: msg} }
