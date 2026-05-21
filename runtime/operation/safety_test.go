package operationruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
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

func TestSafetyEnvelopeRejectsOverMaxCommandRiskWithoutOverride(t *testing.T) {
	approval := recordingApproval{}
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("should not run")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:    fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Approval:       &approval,
		Sandbox:        allowSandbox{},
		MaxCommandRisk: operation.RiskMedium,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "cmdrisk_denied: critical: could not classify") {
		t.Fatalf("error = %#v, want cmdrisk_denied", result.Error)
	}
	if approval.calls != 0 {
		t.Fatalf("approval calls = %d, want 0", approval.calls)
	}
}

func TestSafetyEnvelopeApprovesOverMaxCommandRiskWithOverride(t *testing.T) {
	approval := recordingApproval{}
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:               fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Approval:                  &approval,
		Sandbox:                   allowSandbox{},
		MaxCommandRisk:            operation.RiskMedium,
		ApproveOverMaxCommandRisk: true,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, "ok")

	if result.Status != operation.StatusOK || result.Output != "ok" {
		t.Fatalf("result = %#v, want ok", result)
	}
	if approval.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", approval.calls)
	}
	if approval.last.Risk.Level != operation.RiskCritical || !approval.last.Risk.RequiresApproval {
		t.Fatalf("approval risk = %#v, want critical approval", approval.last.Risk)
	}
}

func TestSafetyEnvelopeAutoApproverAllowsOverMaxCommandRiskWithOverride(t *testing.T) {
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("ran")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:               fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Approval:                  AutoApprover{},
		Sandbox:                   allowSandbox{},
		MaxCommandRisk:            operation.RiskMedium,
		ApproveOverMaxCommandRisk: true,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusOK || result.Output != "ran" {
		t.Fatalf("result = %#v, want ran", result)
	}
}

func TestSafetyEnvelopeApprovalDenialRejectsOverMaxCommandRiskWithOverride(t *testing.T) {
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("should not run")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:               fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Approval:                  denyApproval{},
		Sandbox:                   allowSandbox{},
		MaxCommandRisk:            operation.RiskMedium,
		ApproveOverMaxCommandRisk: true,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "approval_denied") {
		t.Fatalf("error = %#v, want approval_denied", result.Error)
	}
}

func TestSafetyEnvelopeRiskOverrideDoesNotBypassACL(t *testing.T) {
	approval := recordingApproval{}
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("should not run")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		ACL:                       denyACL{},
		CommandRisk:               fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Approval:                  &approval,
		Sandbox:                   allowSandbox{},
		MaxCommandRisk:            operation.RiskMedium,
		ApproveOverMaxCommandRisk: true,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "acl_denied") {
		t.Fatalf("error = %#v, want acl_denied", result.Error)
	}
	if approval.calls != 0 {
		t.Fatalf("approval calls = %d, want 0", approval.calls)
	}
}

func TestSafetyEnvelopeRiskOverrideDoesNotBypassSecretGuard(t *testing.T) {
	approval := recordingApproval{}
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess, operation.EffectSensitiveData},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("should not run")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:               fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Secrets:                   denySecrets{},
		Approval:                  &approval,
		Sandbox:                   allowSandbox{},
		MaxCommandRisk:            operation.RiskMedium,
		ApproveOverMaxCommandRisk: true,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "secret_guard_denied") {
		t.Fatalf("error = %#v, want secret_guard_denied", result.Error)
	}
	if approval.calls != 0 {
		t.Fatalf("approval calls = %d, want 0", approval.calls)
	}
}

func TestSafetyEnvelopeRiskOverrideDoesNotBypassSandbox(t *testing.T) {
	approval := recordingApproval{}
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("should not run")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:               fixedCommandRisk{risk: CommandRisk{Level: operation.RiskCritical, Reason: "could not classify"}},
		Approval:                  &approval,
		Sandbox:                   denySandbox{},
		MaxCommandRisk:            operation.RiskMedium,
		ApproveOverMaxCommandRisk: true,
	}))

	result := executor.Execute(operation.NewContext(context.Background(), nil), op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Message, "sandbox_denied") {
		t.Fatalf("error = %#v, want sandbox_denied", result.Error)
	}
	if approval.calls != 1 {
		t.Fatalf("approval calls = %d, want 1 before sandbox check", approval.calls)
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

func TestSafetyEnvelopeApprovalRequiredEmitsDenialWithoutGate(t *testing.T) {
	var events []event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	}))
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

	result := executor.Execute(ctx, op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if !hasEvent[ApprovalRequested](events) || !hasEvent[ApprovalDenied](events) {
		t.Fatalf("events = %#v, want approval requested and denied", events)
	}
}

func TestSafetyEnvelopeApprovalRequiredGrantRequiresApprovalGrantAuthority(t *testing.T) {
	approval := recordingApproval{}
	var events []event.Event
	ctx := approvalPolicyContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceDatasource, Name: "local_docs"}},
		Actions:   []policy.Action{policy.ActionDatasourceIndex},
	}}, event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	}))
	op := operation.New(operation.Spec{
		Ref:       operation.Ref{Name: "datasource_index"},
		Semantics: operation.Semantics{Effects: operation.EffectSet{operation.EffectWriteExternal}, Risk: operation.RiskLow},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK(nil)
	})
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		ACL: approvalRequiredACL{
			resource: policy.ResourceRef{Kind: policy.ResourceDatasource, Name: "local_docs"},
			action:   policy.ActionDatasourceIndex,
		},
		Approval: &approval,
		Sandbox:  allowSandbox{},
	}))

	result := executor.Execute(ctx, op, nil)

	if result.Status != operation.StatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if approval.calls != 0 {
		t.Fatalf("approval calls = %d, want 0 without approval.grant", approval.calls)
	}
	denied, ok := findEvent[ApprovalDenied](events)
	if !ok || !strings.Contains(denied.Error, "approval_unauthorized") {
		t.Fatalf("ApprovalDenied = %#v, want approval_unauthorized", denied)
	}
}

func TestSafetyEnvelopeApprovalRequiredGrantWithApprovalGrantSucceeds(t *testing.T) {
	approval := recordingApproval{}
	var events []event.Event
	ctx := approvalPolicyContext([]policy.Grant{{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{
			Kind: policy.ResourceDatasource,
			Name: "local_docs",
		}},
		Actions: []policy.Action{policy.ActionApprovalGrant},
	}}, event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	}))
	op := operation.New(operation.Spec{
		Ref:       operation.Ref{Name: "datasource_index"},
		Semantics: operation.Semantics{Effects: operation.EffectSet{operation.EffectWriteExternal}, Risk: operation.RiskLow},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("indexed")
	})
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		ACL: approvalRequiredACL{
			resource: policy.ResourceRef{Kind: policy.ResourceDatasource, Name: "local_docs"},
			action:   policy.ActionDatasourceIndex,
		},
		Approval: &approval,
		Sandbox:  allowSandbox{},
	}))

	result := executor.Execute(ctx, op, nil)

	if result.Status != operation.StatusOK || result.Output != "indexed" {
		t.Fatalf("result = %#v, want indexed", result)
	}
	if approval.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", approval.calls)
	}
	if !hasEvent[ApprovalRequested](events) || !hasEvent[ApprovalGranted](events) {
		t.Fatalf("events = %#v, want approval requested and granted", events)
	}
}

func TestSafetyEnvelopeCommandRiskApprovalRequiresOperationApprovalGrant(t *testing.T) {
	approval := recordingApproval{}
	ctx := approvalPolicyContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceOperation, Name: "shell_exec"}},
		Actions:   []policy.Action{policy.ActionApprovalGrant},
	}}, nil)
	op := intentOperation{Operation: operation.New(operation.Spec{
		Ref: operation.Ref{Name: "shell_exec"},
		Semantics: operation.Semantics{
			Effects: operation.EffectSet{operation.EffectProcess},
			Risk:    operation.RiskMedium,
		},
	}, func(_ operation.Context, _ operation.Value) operation.Result {
		return operation.OK("ran")
	})}
	executor := NewExecutor(WithSafetyGate(SafetyEnvelope{
		CommandRisk:    fixedCommandRisk{risk: CommandRisk{Level: operation.RiskHigh, Reason: "needs review", RequiresApproval: true}},
		Approval:       &approval,
		Sandbox:        allowSandbox{},
		MaxCommandRisk: operation.RiskMedium,
	}))

	result := executor.Execute(ctx, op, nil)

	if result.Status != operation.StatusOK || result.Output != "ran" {
		t.Fatalf("result = %#v, want ran", result)
	}
	if approval.calls != 1 {
		t.Fatalf("approval calls = %d, want 1", approval.calls)
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

type denySandbox struct{}

func (denySandbox) Check(operation.Context, operation.Spec, operation.Value) error {
	return errors.New("no")
}

type denyACL struct{}

func (denyACL) Authorize(operation.Context, operation.Operation, operation.Value) error {
	return errors.New("no")
}

type approvalRequiredACL struct {
	resource policy.ResourceRef
	action   policy.Action
}

func (a approvalRequiredACL) Authorize(_ operation.Context, _ operation.Operation, _ operation.Value) error {
	return AuthorizationApprovalRequired{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resource: a.resource,
		Action:   a.action,
		Reason:   "grant requires approval",
	}
}

type denySecrets struct{}

func (denySecrets) Check(operation.Context, operation.Spec, operation.Value) error {
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

func approvalPolicyContext(grants []policy.Grant, sink event.Sink) operation.Context {
	base := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy:   policy.AuthorizationPolicy{Grants: grants},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
	})
	return operation.NewContext(base, sink)
}

func hasEvent[T any](events []event.Event) bool {
	_, ok := findEvent[T](events)
	return ok
}

func findEvent[T any](events []event.Event) (T, bool) {
	var zero T
	for _, evt := range events {
		if typed, ok := evt.(T); ok {
			return typed, true
		}
	}
	return zero, false
}
