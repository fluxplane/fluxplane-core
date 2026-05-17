package terminalui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/plugins/codeplugin"
)

func TestRendererRendersCodeExecuteSuccess(t *testing.T) {
	got := renderCodeExecuteForTest(t, operation.Result{Status: operation.StatusOK, Output: operation.Rendered{
		Model: "code_execute completed",
		Data: codeplugin.ExecuteResult{
			Preset:     "python",
			Image:      "python:3.12-alpine",
			Stdout:     "hello\n",
			ExitCode:   0,
			DurationMS: 200,
		},
	}})
	for _, want := range []string{"🐍 Python code executed successfully", "preset: python", "image: python:3.12-alpine", "duration: 200ms", "exit: 0", "stdout", "hello"} {
		if !strings.Contains(got, want) {
			t.Fatalf("code_execute output = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "=== STDOUT ===") {
		t.Fatalf("code_execute output = %q, did not want raw plugin stdout banner", got)
	}
}

func TestRendererRendersCodeExecuteFailureWithCross(t *testing.T) {
	got := renderCodeExecuteForTest(t, operation.Result{
		Status: operation.StatusFailed,
		Error:  &operation.Error{Code: "code_execute_failed", Message: "exit status 1"},
		Output: operation.Rendered{Data: codeplugin.ExecuteResult{
			Preset:     "node",
			Image:      "node:24-alpine",
			Stdout:     "starting job...\n",
			Stderr:     "Error: boom\n",
			ExitCode:   1,
			DurationMS: 400,
		}},
	})
	for _, want := range []string{"❌ 🟩 Node.js code failed", "exit: 1", "stdout", "starting job...", "stderr", "Error: boom"} {
		if !strings.Contains(got, want) {
			t.Fatalf("code_execute output = %q, missing %q", got, want)
		}
	}
}

func TestRendererRendersCodeExecuteTimeoutWithCross(t *testing.T) {
	got := renderCodeExecuteForTest(t, operation.Result{
		Status: operation.StatusFailed,
		Error:  &operation.Error{Code: "code_execute_failed", Message: context.DeadlineExceeded.Error()},
		Output: operation.Rendered{Data: codeplugin.ExecuteResult{
			Preset:     "go",
			Image:      "golang:1.26-alpine",
			ExitCode:   -1,
			TimedOut:   true,
			DurationMS: 30000,
			TimeoutMS:  30000,
		}},
	})
	for _, want := range []string{"❌ 🐹 Go code timed out", "duration: 30.0s", "timeout: 30.0s", "(no stdout or stderr)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("code_execute output = %q, missing %q", got, want)
		}
	}
}

func renderCodeExecuteForTest(t *testing.T, result operation.Result) string {
	t.Helper()
	var out, err bytes.Buffer
	renderer := NewRenderer(&out, &err, false)
	renderer.Render(clientapi.Event{
		Kind: clientapi.EventOperationCompleted,
		Operation: &clientapi.OperationEvent{
			CallID:    "call_1",
			Operation: operation.Ref{Name: "code_execute"},
			Result:    &result,
		},
	})
	return err.String()
}
