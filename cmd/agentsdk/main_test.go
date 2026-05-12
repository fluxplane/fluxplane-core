package main

import (
	"bytes"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/apps/coder"
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
	if !strings.Contains(help, "--openai-store") {
		t.Fatalf("help = %q, want inherited openai-store flag", help)
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
	line := terminalui.UsageLine(usage.Recorded{
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
	if !strings.Contains(line, "usage:") || !strings.Contains(line, "subject=gpt-test") || !strings.Contains(line, "llm.input_tokens=12") {
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
