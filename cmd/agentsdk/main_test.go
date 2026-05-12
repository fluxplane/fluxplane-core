package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func TestCoderCommandHasREPLAndUsageFlag(t *testing.T) {
	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"coder", "repl", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "interactive session") {
		t.Fatalf("help = %q, want repl help", help)
	}
	if !strings.Contains(help, "--usage") {
		t.Fatalf("help = %q, want inherited usage flag", help)
	}
}

func TestCoderBundleAppliesModelOverride(t *testing.T) {
	bundle := coderBundle("gpt-test")
	if bundle.Apps[0].Model.Model != "gpt-test" {
		t.Fatalf("app model = %q, want gpt-test", bundle.Apps[0].Model.Model)
	}
	if bundle.Agents[0].Inference.Model != "gpt-test" {
		t.Fatalf("agent model = %q, want gpt-test", bundle.Agents[0].Inference.Model)
	}
	if bundle.Agents[0].Name != coder.AgentName {
		t.Fatalf("agent name = %q", bundle.Agents[0].Name)
	}
}

func TestUsageLineFromRecorded(t *testing.T) {
	line := usageLine(usage.Recorded{
		Source: "adapters/openai",
		Subject: usage.Subject{
			Kind:     usage.SubjectLLM,
			Provider: "openai",
			Name:     "gpt-test",
		},
		Measurements: []usage.Measurement{{
			Metric:   usage.MetricLLMInputTokens,
			Quantity: 12,
			Unit:     usage.UnitToken,
		}},
	})
	if !strings.Contains(line, "usage:") || !strings.Contains(line, "model=gpt-test") || !strings.Contains(line, "llm.input_tokens=12") {
		t.Fatalf("line = %q", line)
	}
}

func TestResultErrorReportsFailedInput(t *testing.T) {
	err := resultError(agentruntime.Result{
		Input: &session.InputResult{
			Status: session.InputStatusFailed,
			Error:  &session.CommandError{Code: "model_failed", Message: "boom"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "model_failed: boom") {
		t.Fatalf("err = %v, want model_failed", err)
	}
}

func TestShellOperationRunsWithoutShellInterpreter(t *testing.T) {
	result := shellOperation().Run(operation.NewContext(context.Background(), nil), map[string]any{
		"command": "printf",
		"args":    []any{"hello"},
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	output := result.Output.(map[string]any)
	if output["output"] != "hello" {
		t.Fatalf("output = %#v, want hello", output["output"])
	}
}

func TestShellOperationRejectsBlockedCommand(t *testing.T) {
	result := shellOperation().Run(operation.NewContext(context.Background(), nil), map[string]any{
		"command": "rm",
		"args":    []any{"-rf", "/tmp/nope"},
	})
	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
}

func TestHTTPRequestOperationGetsBoundedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(server.Close)

	result := httpRequestOperation().Run(operation.NewContext(context.Background(), nil), map[string]any{
		"url":       server.URL,
		"max_bytes": float64(4),
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	output := result.Output.(map[string]any)
	if output["body"] != "hell" || output["truncated"] != true {
		t.Fatalf("output = %#v, want truncated hell", output)
	}
}
