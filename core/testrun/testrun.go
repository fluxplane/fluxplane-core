package testrun

import "time"

// EventKind identifies the lifecycle point represented by a test-run event.
type EventKind string

const (
	// EventStarted reports that a test run has started.
	EventStarted EventKind = "test.run.started"
	// EventProgress reports incremental progress for a test run.
	EventProgress EventKind = "test.run.progress"
	// EventFinished reports that a test run has finished.
	EventFinished EventKind = "test.run.finished"
)

// Status describes the normalized status of a test run.
type Status string

const (
	// StatusRunning means the run is currently executing.
	StatusRunning Status = "running"
	// StatusPassed means all selected tests passed.
	StatusPassed Status = "passed"
	// StatusFailed means one or more tests or packages failed.
	StatusFailed Status = "failed"
	// StatusSkipped means the run selected no executable tests or all selected tests were skipped.
	StatusSkipped Status = "skipped"
	// StatusError means the runner itself could not complete normally.
	StatusError Status = "error"
)

// FailureKind classifies a test-run failure without binding the event to a specific toolchain.
type FailureKind string

const (
	// FailureAssertion is a test assertion or explicit test failure.
	FailureAssertion FailureKind = "assertion"
	// FailureBuild is a build or compile failure before tests could run.
	FailureBuild FailureKind = "build"
	// FailurePanic is a panic, crash, or unhandled exception during a test.
	FailurePanic FailureKind = "panic"
	// FailureTimeout is a test or package timeout.
	FailureTimeout FailureKind = "timeout"
	// FailureSetup is a package, suite, environment, or fixture setup failure.
	FailureSetup FailureKind = "setup"
	// FailureUnknown is a failure that could not be classified more specifically.
	FailureUnknown FailureKind = "unknown"
)

// Event is a normalized, toolchain-neutral test-run lifecycle event.
type Event struct {
	Kind      EventKind `json:"kind"`
	RunID     string    `json:"run_id,omitempty"`
	Toolchain string    `json:"toolchain"`
	Command   string    `json:"command,omitempty"`
	Target    string    `json:"target,omitempty"`

	Status Status `json:"status"`

	Summary Summary `json:"summary"`

	Failures []Failure `json:"failures,omitempty"`

	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`

	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Summary contains normalized package and test counters for a test run.
type Summary struct {
	PackagesTotal   int `json:"packages_total,omitempty"`
	PackagesPassed  int `json:"packages_passed,omitempty"`
	PackagesFailed  int `json:"packages_failed,omitempty"`
	PackagesSkipped int `json:"packages_skipped,omitempty"`

	TestsTotal   int `json:"tests_total,omitempty"`
	TestsPassed  int `json:"tests_passed,omitempty"`
	TestsFailed  int `json:"tests_failed,omitempty"`
	TestsSkipped int `json:"tests_skipped,omitempty"`
}

// Failure contains compact, actionable failure context for one failed test or package.
type Failure struct {
	Kind    FailureKind `json:"kind"`
	Package string      `json:"package,omitempty"`
	Test    string      `json:"test,omitempty"`

	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`

	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

// HasFailureKind reports whether event contains at least one failure of kind.
func HasFailureKind(event Event, kind FailureKind) bool {
	for _, failure := range event.Failures {
		if failure.Kind == kind {
			return true
		}
	}
	return false
}
