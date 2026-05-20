package terminal

import (
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/testrun"
)

func TestRenderTestRunEvent(t *testing.T) {
	tests := []struct {
		name  string
		event testrun.Event
		want  []string
	}{
		{
			name:  "passed",
			event: testrun.Event{Target: "./runtime/thread", Status: testrun.StatusPassed, Summary: testrun.Summary{TestsPassed: 6, PackagesPassed: 1}, DurationMS: 420},
			want:  []string{"✅ tests passed  ./runtime/thread", "6 passed", "420ms"},
		},
		{
			name:  "build failed",
			event: testrun.Event{Target: "./adapters/storage/event/sqlite", Status: testrun.StatusFailed, Summary: testrun.Summary{PackagesFailed: 1}, Failures: []testrun.Failure{{Kind: testrun.FailureBuild, File: "runtime/thread/store.go", Line: 171, Column: 15, Message: "undefined: threadIdempotencyKey"}}, DurationMS: 310},
			want:  []string{"🛠️ tests failed to build  ./adapters/storage/event/sqlite", "store.go:171 undefined: threadIdempotencyKey", "1 package failed"},
		},
		{
			name:  "assertion failed",
			event: testrun.Event{Target: "./runtime/thread", Status: testrun.StatusFailed, Summary: testrun.Summary{TestsPassed: 5, TestsFailed: 1}, Failures: []testrun.Failure{{Kind: testrun.FailureAssertion, Test: "TestRetry", File: "runtime/thread/store_test.go", Line: 262, Message: "Append returned error"}}, DurationMS: 440},
			want:  []string{"❌ tests failed  ./runtime/thread", "TestRetry", "store_test.go:262 Append returned error", "5 passed · 1 failed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderTestRunEvent(tt.event)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("RenderTestRunEvent() = %q, want substring %q", got, want)
				}
			}
		})
	}
}
