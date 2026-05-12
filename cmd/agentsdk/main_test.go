package main

import (
	"bytes"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
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
	if !strings.Contains(help, "--provider") {
		t.Fatalf("help = %q, want inherited provider flag", help)
	}
	if strings.Contains(help, "--openai-store") {
		t.Fatalf("help = %q, want openai-store removed", help)
	}
}

func TestCoderBundleAppliesModelOverride(t *testing.T) {
	bundle := coderBundle("codex", "gpt-test")
	if bundle.Apps[0].Model.Model != "gpt-test" {
		t.Fatalf("app model = %q, want gpt-test", bundle.Apps[0].Model.Model)
	}
	if bundle.Apps[0].Model.Provider != "codex" {
		t.Fatalf("app provider = %q, want codex", bundle.Apps[0].Model.Provider)
	}
	if bundle.Agents[0].Inference.Model != "gpt-test" {
		t.Fatalf("agent model = %q, want gpt-test", bundle.Agents[0].Inference.Model)
	}
	if bundle.Agents[0].Name != coder.AgentName {
		t.Fatalf("agent name = %q", bundle.Agents[0].Name)
	}
}

func TestResolveModelSelectionParsesProviderPrefix(t *testing.T) {
	got := resolveModelSelection(coderOptions{provider: "openai", model: "codex/gpt-5.5"})
	if got.Provider != "codex" || got.Model != "gpt-5.5" {
		t.Fatalf("selection = %#v, want codex/gpt-5.5", got)
	}
}

func TestCoderDefaultModel(t *testing.T) {
	if coder.DefaultModel != "gpt-5.5" {
		t.Fatalf("DefaultModel = %q, want gpt-5.5", coder.DefaultModel)
	}
}

func TestUsageFromEventParsesTypedAndMapPayloads(t *testing.T) {
	typed := usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []usage.Measurement{{
			Metric:   usage.MetricLLMInputTokens,
			Quantity: 12,
			Unit:     usage.UnitToken,
		}},
	}
	for _, evt := range []agentruntime.Event{
		{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: typed}},
		{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: map[string]any{
			"subject": map[string]any{"kind": "llm", "provider": "openai", "name": "gpt-test"},
			"measurements": []any{map[string]any{
				"metric":   "llm.input_tokens",
				"quantity": float64(12),
				"unit":     "token",
			}},
		}}},
	} {
		got, ok := usageFromEvent(evt)
		if !ok || got.Subject.Provider != "openai" || len(got.Measurements) != 1 {
			t.Fatalf("usageFromEvent = %#v, %v", got, ok)
		}
	}
	if _, ok := usageFromEvent(agentruntime.Event{Runtime: &clientapi.RuntimeEvent{Name: event.Name("other")}}); ok {
		t.Fatalf("usageFromEvent accepted non-usage event")
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
