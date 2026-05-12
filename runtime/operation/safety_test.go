package operationruntime

import (
	"context"
	"errors"
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

type allowSandbox struct{}

func (allowSandbox) Check(operation.Context, operation.Spec, operation.Value) error { return nil }

type denyACL struct{}

func (denyACL) Authorize(operation.Context, operation.Spec, operation.Value) error {
	return errors.New("no")
}
