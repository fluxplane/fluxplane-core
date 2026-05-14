package operationruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
)

func TestSafetyEnvelopeRejectsSideEffectingOperationWithoutSandbox(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "write"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectFilesystem, operation.EffectWriteExternal},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("should not run")
	})
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)
	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || result.Error.Code != "operation_safety_denied" {
		t.Fatalf("error = %#v", result.Error)
	}
}

func TestSafetyEnvelopeAllowsPureOperationWhenConfigured(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "pure"},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
		},
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{AllowPure: true}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, "ok")
	if result.Status != operation.StatusOK || result.Output != "ok" {
		t.Fatalf("result = %#v, want ok", result)
	}
}

func TestSafetyEnvelopeRequiresCommandRiskForProcessOperations(t *testing.T) {
	op := operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskLow,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK(nil)
	})
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{Sandbox: allowSandbox{}}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)
	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || result.Error.Message != "cmdrisk_required" {
		t.Fatalf("error = %#v, want cmdrisk_required", result.Error)
	}
}

func TestSafetyEnvelopeAppliesACL(t *testing.T) {
	gate := SafetyEnvelope{
		ACL:       denyACL{},
		Sandbox:   allowSandbox{},
		AllowPure: true,
	}
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "op"}}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK(nil)
	})
	executor := NewExecutor(WithSafetyGate(gate))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)
	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
}

func TestSafetyEnvelopeRoutesCommandRiskApprovalToGate(t *testing.T) {
	approval := recordingApproval{}
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "git_commit"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:    fixedCommandRisk{risk: CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true}},
		Approval:       &approval,
		Sandbox:        allowSandbox{},
		MaxCommandRisk: operation.RiskMedium,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, map[string]any{"message": "docs"})

	if result.Status != operation.StatusOK {
		t.Fatalf("status = %s, want ok: %#v", result.Status, result.Error)
	}
	if approval.calls != 1 || approval.last.Risk.Reason != "needs review" {
		t.Fatalf("approval = %#v, want one approval request", approval)
	}
}

func TestSafetyEnvelopeApprovalRequiredFailsClosedWithoutGate(t *testing.T) {
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "git_commit"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK(nil)
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:    fixedCommandRisk{risk: CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true}},
		Sandbox:        allowSandbox{},
		MaxCommandRisk: operation.RiskMedium,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "approval_required: high: needs review") {
		t.Fatalf("error = %#v, want approval_required", result.Error)
	}
}

func TestSafetyEnvelopeApprovalDenialRejectsOperation(t *testing.T) {
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "git_commit"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK(nil)
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:    fixedCommandRisk{risk: CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true}},
		Approval:       denyApproval{},
		Sandbox:        allowSandbox{},
		MaxCommandRisk: operation.RiskMedium,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "approval_denied") {
		t.Fatalf("error = %#v, want approval_denied", result.Error)
	}
}

func TestAutoApproverApprovesRequests(t *testing.T) {
	if err := (AutoApprover{}).Approve(operation.NewContext(context.Background(), nil), ApprovalRequest{}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
}

type allowSandbox struct{}

func (allowSandbox) Check(operation.Context, operation.Spec, operation.Value) error { return nil }

type denyACL struct{}

func (denyACL) Authorize(operation.Context, operation.Spec, operation.Value) error {
	return errors.New("no")
}

type fixedCommandRisk struct {
	risk CommandRisk
}

func (f fixedCommandRisk) Classify(operation.Context, operation.Spec, operation.IntentSet) (CommandRisk, error) {
	return f.risk, nil
}

type intentOperation struct {
	operation.Operation
}

func (o intentOperation) Intent(operation.Context, operation.Value) (operation.IntentSet, error) {
	return operation.IntentSet{Operations: []operation.IntentOperation{{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command("git")},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}}}, nil
}

type recordingApproval struct {
	calls int
	last  ApprovalRequest
}

func (a *recordingApproval) Approve(_ operation.Context, req ApprovalRequest) error {
	a.calls++
	a.last = req
	return nil
}

type denyApproval struct{}

func (denyApproval) Approve(operation.Context, ApprovalRequest) error {
	return errors.New("no")
}
