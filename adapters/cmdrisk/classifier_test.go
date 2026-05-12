package cmdrisk

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
)

func TestClassifierRejectsDestructiveCommand(t *testing.T) {
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}

	risk, err := classifier.Classify(operation.NewContext(context.Background(), event.Discard()), spec, map[string]any{
		"command": "rm",
		"args":    []any{"-rf", "/"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if risk.Level != operation.RiskHigh && risk.Level != operation.RiskCritical {
		t.Fatalf("risk = %#v, want high or critical", risk)
	}
}

func TestClassifierEmitsAssessmentEvent(t *testing.T) {
	var emitted event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "web_request"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Risk:    operation.RiskLow,
		},
	}

	if _, err := classifier.Classify(ctx, spec, map[string]any{"url": "https://example.com"}); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	if assessed.Operation.Name != "web_request" || assessed.Action == "" || assessed.Action == "declared_risk" {
		t.Fatalf("assessment = %#v", assessed)
	}
}

func TestClassifierFallsBackToDeclaredRiskForProcessLifecycleOperation(t *testing.T) {
	var emitted event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "process_list"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskLow,
		},
	}

	risk, err := classifier.Classify(ctx, spec, map[string]any{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if risk.Level != operation.RiskLow || risk.Reason != "declared operation risk" {
		t.Fatalf("risk = %#v, want declared low risk", risk)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	if assessed.Action != "declared_risk" || assessed.Level != operation.RiskLow {
		t.Fatalf("assessment = %#v, want declared_risk low", assessed)
	}
}

func TestClassifierFallsBackToDeclaredRiskForBrowserSessionOperation(t *testing.T) {
	var emitted event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "browser_read"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Risk:    operation.RiskMedium,
		},
	}

	risk, err := classifier.Classify(ctx, spec, map[string]any{"session_id": "session-1"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if risk.Level != operation.RiskMedium || risk.Reason != "declared operation risk" {
		t.Fatalf("risk = %#v, want declared medium risk", risk)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	if assessed.Action != "declared_risk" || assessed.Level != operation.RiskMedium {
		t.Fatalf("assessment = %#v, want declared_risk medium", assessed)
	}
}

func TestClassifierShellExecEmptyCommandIsHighRisk(t *testing.T) {
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}

	risk, err := classifier.Classify(operation.NewContext(context.Background(), event.Discard()), spec, map[string]any{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if risk.Level != operation.RiskHigh || risk.Reason != "empty command" {
		t.Fatalf("risk = %#v, want high empty command", risk)
	}
}
