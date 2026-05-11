package operation

// Status classifies the final outcome of an operation execution.
type Status string

const (
	StatusOK       Status = "ok"
	StatusFailed   Status = "failed"
	StatusRejected Status = "rejected"
	StatusCanceled Status = "canceled"
)

// Error is the structured failure shape for operation results.
type Error struct {
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Result is the final operation outcome. It intentionally does not carry live
// events; runtime/orchestration layers own event emission and persistence.
type Result struct {
	Status Status `json:"status"`
	Output Value  `json:"output,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// OK returns a successful result.
func OK(output Value) Result {
	return Result{Status: StatusOK, Output: output}
}

// Failed returns a failed result.
func Failed(code, message string, details map[string]any) Result {
	return Result{
		Status: StatusFailed,
		Error:  &Error{Code: code, Message: message, Details: details},
	}
}

// Rejected returns a policy-rejected result.
func Rejected(code, message string, details map[string]any) Result {
	return Result{
		Status: StatusRejected,
		Error:  &Error{Code: code, Message: message, Details: details},
	}
}

// Canceled returns a canceled result.
func Canceled(message string) Result {
	return Result{
		Status: StatusCanceled,
		Error:  &Error{Code: "canceled", Message: message},
	}
}

// IsError reports whether the result is not successful.
func (r Result) IsError() bool {
	return r.Status != "" && r.Status != StatusOK
}
