package terminal

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/testrun"
)

// RenderTestRunEvent returns a concise terminal-oriented status message for a generic test-run event.
func RenderTestRunEvent(event testrun.Event) string {
	target := strings.TrimSpace(event.Target)
	if target == "" {
		target = strings.TrimSpace(event.Command)
	}
	header := strings.TrimSpace(testRunIcon(event) + " " + testRunTitle(event) + "  " + target)
	lines := []string{header}
	if len(event.Failures) > 0 {
		failure := event.Failures[0]
		if failure.Test != "" {
			lines = append(lines, failure.Test)
		}
		lines = append(lines, renderTestRunFailureLine(failure))
	}
	if summary := renderTestRunSummary(event); summary != "" {
		lines = append(lines, summary)
	}
	return strings.Join(lines, "\n")
}

func testRunIcon(event testrun.Event) string {
	switch {
	case event.Status == testrun.StatusPassed:
		return "✅"
	case testrun.HasFailureKind(event, testrun.FailureBuild):
		return "🛠️"
	case testrun.HasFailureKind(event, testrun.FailurePanic):
		return "💥"
	case event.Status == testrun.StatusFailed:
		return "❌"
	case event.Status == testrun.StatusRunning:
		return "🧪"
	case event.Status == testrun.StatusSkipped:
		return "⚪"
	default:
		return "🧪"
	}
}

func testRunTitle(event testrun.Event) string {
	switch {
	case event.Status == testrun.StatusPassed:
		return "tests passed"
	case testrun.HasFailureKind(event, testrun.FailureBuild):
		return "tests failed to build"
	case testrun.HasFailureKind(event, testrun.FailurePanic):
		return "test panic"
	case event.Status == testrun.StatusFailed:
		return "tests failed"
	case event.Status == testrun.StatusRunning:
		return "running tests"
	case event.Status == testrun.StatusSkipped:
		return "no tests"
	default:
		return "tests"
	}
}

func renderTestRunFailureLine(failure testrun.Failure) string {
	location := filepath.Base(failure.File)
	if location == "." {
		location = ""
	}
	if failure.Line > 0 {
		location += fmt.Sprintf(":%d", failure.Line)
	}
	if location != "" && failure.Message != "" {
		return location + " " + failure.Message
	}
	if failure.Message != "" {
		return failure.Message
	}
	return strings.TrimSpace(failure.Package)
}

func renderTestRunSummary(event testrun.Event) string {
	summary := event.Summary
	var parts []string
	if summary.TestsPassed > 0 {
		parts = append(parts, fmt.Sprintf("%d passed", summary.TestsPassed))
	}
	if summary.TestsFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", summary.TestsFailed))
	}
	if len(parts) == 0 && summary.TestsTotal > 0 {
		parts = append(parts, fmt.Sprintf("%d tests", summary.TestsTotal))
	}
	if len(parts) == 0 && summary.PackagesFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d package failed", summary.PackagesFailed))
	}
	if event.DurationMS > 0 {
		parts = append(parts, (time.Duration(event.DurationMS) * time.Millisecond).String())
	}
	return strings.Join(parts, " · ")
}
