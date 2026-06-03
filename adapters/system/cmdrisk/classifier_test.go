package cmdrisk

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
)

func TestClassifierRejectsDestructiveProcessIntent(t *testing.T) {
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "process"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}

	risk, err := classifier.Classify(operation.NewContext(context.Background(), event.Discard()), spec, operation.IntentSet{
		Operations: []operation.IntentOperation{processIntent("rm", "-rf", "/")},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if risk.Level != operation.RiskHigh && risk.Level != operation.RiskCritical {
		t.Fatalf("risk = %#v, want high or critical", risk)
	}
}

func TestClassifierDoesNotRequireApprovalForGitStatusProcessIntent(t *testing.T) {
	root := t.TempDir()
	classifier := New(Config{
		WorkingDirectory:      root,
		WorkspacePathPrefixes: []string{root},
		SensitivePathPrefixes: []string{root + "/.git"},
	})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "status"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
			Risk:    operation.RiskLow,
		},
	}

	risk, err := classifier.Classify(operation.NewContext(context.Background(), event.Discard()), spec, operation.IntentSet{
		Operations: []operation.IntentOperation{processIntent("git", "status", "--short", "--branch")},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if risk.RequiresApproval || risk.Level == operation.RiskHigh || risk.Level == operation.RiskCritical {
		t.Fatalf("risk = %#v, want non-approval low/medium risk", risk)
	}
}

func TestClassifierEmitsAssessmentEventForURLIntent(t *testing.T) {
	var emitted event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "fetch"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Risk:    operation.RiskLow,
		},
	}

	if _, err := classifier.Classify(ctx, spec, operation.IntentSet{
		Operations: []operation.IntentOperation{{
			Behavior:  operation.IntentNetworkFetch,
			Target:    operation.URLTarget{URL: operation.URL("https://example.com")},
			Role:      operation.IntentRoleNetworkTarget,
			Certainty: operation.IntentCertain,
		}},
	}); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assessed, ok := emitted.(Assessed)
	if !ok {
		t.Fatalf("emitted = %#v, want Assessed", emitted)
	}
	if assessed.Operation.Name != "fetch" || assessed.Action == "" || assessed.Action == "declared_risk" {
		t.Fatalf("assessment = %#v", assessed)
	}
}

func TestClassifierFallsBackToDeclaredRiskForEmptyIntent(t *testing.T) {
	var emitted event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: t.TempDir()})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "lifecycle"},
		Semantics: operation.Semantics{
			Risk: operation.RiskLow,
		},
	}

	risk, err := classifier.Classify(ctx, spec, operation.IntentSet{})
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

func TestClassifierIncludesPathIntentTargets(t *testing.T) {
	var emitted event.Event
	root := t.TempDir()
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		emitted = evt
	}))
	classifier := New(Config{WorkingDirectory: root})
	spec := operation.Spec{
		Ref: operation.Ref{Name: "stage"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectFilesystem, operation.EffectWriteExternal},
			Risk:    operation.RiskMedium,
		},
	}

	if _, err := classifier.Classify(ctx, spec, operation.IntentSet{
		Operations: []operation.IntentOperation{
			{
				Behavior:  operation.IntentFilesystemRead,
				Target:    operation.PathTarget{Path: operation.Path("README.md")},
				Role:      operation.IntentRoleReadTarget,
				Certainty: operation.IntentCertain,
			},
			{
				Behavior:  operation.IntentPersistenceModify,
				Target:    operation.PathTarget{Path: operation.Path(".git/index")},
				Role:      operation.IntentRoleWriteTarget,
				Certainty: operation.IntentCertain,
			},
		},
	}); err != nil {
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

func TestClassifierHasNoOperationNameSpecialCases(t *testing.T) {
	data, err := os.ReadFile("classifier.go")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	source := string(data)
	for _, forbidden := range []string{"git_", "shell_exec", "web_request"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("classifier.go contains operation-specific token %q", forbidden)
		}
	}
}

func processIntent(command string, args ...string) operation.IntentOperation {
	arguments := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		arguments = append(arguments, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command(command), Args: arguments},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}
}
