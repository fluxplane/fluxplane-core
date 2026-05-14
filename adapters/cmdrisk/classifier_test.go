package cmdrisk

import (
	"context"
	"fmt"
	"strings"
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

func TestClassifierAssessesNativeGitWriteOperations(t *testing.T) {
	for _, name := range []string{"git_add", "git_commit"} {
		t.Run(name, func(t *testing.T) {
			var emitted event.Event
			ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
				emitted = evt
			}))
			classifier := New(Config{WorkingDirectory: t.TempDir()})
			spec := operation.Spec{
				Ref: operation.Ref{Name: operation.Name(name)},
				Semantics: operation.Semantics{
					Effects: operation.EffectSet{operation.EffectProcess},
					Risk:    operation.RiskMedium,
				},
			}

			if _, err := classifier.Classify(ctx, spec, map[string]any{}); err != nil {
				t.Fatalf("Classify: %v", err)
			}
			assessed, ok := emitted.(Assessed)
			if !ok {
				t.Fatalf("emitted = %#v, want Assessed", emitted)
			}
			if string(assessed.Operation.Name) != name || assessed.Action == "" || assessed.Action == "declared_risk" {
				t.Fatalf("assessment = %#v, want command assessment", assessed)
			}
		})
	}
}

func TestClassifierNativeGitCommitUsesStructuredIntent(t *testing.T) {
	var emitted event.Event
	root := t.TempDir()
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: root})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "git_commit"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}

	if _, err := classifier.Classify(ctx, spec, map[string]any{"message": "docs: add logo"}); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	if assessed.Action == "declared_risk" {
		t.Fatalf("assessment = %#v, want structured assessment", assessed)
	}
	for _, behavior := range assessed.Behaviors {
		if behavior == "network_fetch" {
			t.Fatalf("behaviors = %#v, native git commit must not be modeled as network fetch", assessed.Behaviors)
		}
	}
	if strings.Contains(strings.ToLower(assessed.Rationale), "remote") || strings.Contains(strings.ToLower(assessed.Rationale), "fetch") {
		t.Fatalf("rationale = %q, want local git commit rationale", assessed.Rationale)
	}
}

func TestClassifierNativeGitAddIncludesPathIntent(t *testing.T) {
	var emitted event.Event
	root := t.TempDir()
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: root})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "git_add"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}

	if _, err := classifier.Classify(ctx, spec, map[string]any{"paths": []any{"README.md"}}); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	targets := fmt.Sprint(assessed.Targets)
	if !strings.Contains(targets, "README.md") || !strings.Contains(targets, ".git/index") {
		t.Fatalf("targets = %s, want path and index intent", targets)
	}
}

func TestClassifierShellExecGitCommitUsesCommandAssessment(t *testing.T) {
	var emitted event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}

	if _, err := classifier.Classify(ctx, spec, map[string]any{"command": "git", "args": []any{"commit", "-m", "docs"}}); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	if assessed.Action == "declared_risk" {
		t.Fatalf("assessment = %#v, want shell command assessment", assessed)
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
