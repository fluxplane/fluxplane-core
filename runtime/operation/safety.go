package operationruntime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

// SafetyGate rejects operation execution before the handler runs.
type SafetyGate interface {
	Check(operation.Context, operation.Operation, operation.Value) error
}

// SafetyGateFunc adapts a function to SafetyGate.
type SafetyGateFunc func(operation.Context, operation.Operation, operation.Value) error

func (f SafetyGateFunc) Check(ctx operation.Context, op operation.Operation, input operation.Value) error {
	if f == nil {
		return nil
	}
	return f(ctx, op, input)
}

// Sandbox models the sandboxing decision for one operation execution. Concrete
// process/container/browser sandboxes belong in adapters or plugins.
type Sandbox interface {
	Check(operation.Context, operation.Spec, operation.Value) error
}

// AccessController models ACL/scope enforcement for one operation execution.
type AccessController interface {
	Authorize(operation.Context, operation.Operation, operation.Value) error
}

// CommandRiskClassifier models shell/code execution risk classification, for
// example via codewandler/cmdrisk or a successor.
type CommandRiskClassifier interface {
	Classify(operation.Context, operation.Spec, operation.IntentSet) (CommandRisk, error)
}

// SecretGuard models secret detection/redaction policy before execution.
type SecretGuard interface {
	Check(operation.Context, operation.Spec, operation.Value) error
}

// ApprovalGate models approval enforcement before execution.
type ApprovalGate interface {
	Approve(operation.Context, ApprovalRequest) error
}

// AutoApprover approves every approval request. It is intended for explicit
// local CLI opt-ins, not as a default runtime policy.
type AutoApprover struct{}

// Approve implements ApprovalGate.
func (AutoApprover) Approve(operation.Context, ApprovalRequest) error {
	return nil
}

// CommandRisk is the runtime command-risk classification result.
type CommandRisk struct {
	Level            operation.RiskLevel `json:"level,omitempty"`
	Reason           string              `json:"reason,omitempty"`
	RequiresApproval bool                `json:"requires_approval,omitempty"`
}

// ApprovalRequest describes one operation execution that needs user approval.
type ApprovalRequest struct {
	Subjects []policy.SubjectRef `json:"subjects,omitempty"`
	Resource policy.ResourceRef  `json:"resource,omitempty"`
	Action   policy.Action       `json:"action,omitempty"`
	Spec     operation.Spec      `json:"spec"`
	Input    operation.Value     `json:"input,omitempty"`
	Risk     CommandRisk         `json:"risk,omitempty"`
	Reason   string              `json:"reason,omitempty"`
}

// SafetyEnvelope is the default runtime safety gate shape for real
// side-effecting operations.
type SafetyEnvelope struct {
	Sandbox                   Sandbox
	ACL                       AccessController
	CommandRisk               CommandRiskClassifier
	Secrets                   SecretGuard
	Approval                  ApprovalGate
	AllowPure                 bool
	MaxCommandRisk            operation.RiskLevel
	ApproveOverMaxCommandRisk bool
}

// Check enforces the configured safety envelope.
func (e SafetyEnvelope) Check(ctx operation.Context, op operation.Operation, input operation.Value) error {
	spec := op.Spec()
	if e.ACL != nil {
		if err := e.ACL.Authorize(ctx, op, input); err != nil {
			var approvalRequired AuthorizationApprovalRequired
			if errors.As(err, &approvalRequired) {
				if err := e.approveAuthorization(ctx, spec, input, approvalRequired); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("acl_denied: %w", err)
			}
		}
	}
	if e.Secrets != nil {
		if err := e.Secrets.Check(ctx, spec, input); err != nil {
			return fmt.Errorf("secret_guard_denied: %w", err)
		}
	} else if spec.Semantics.Effects.Has(operation.EffectSensitiveData) {
		return fmt.Errorf("secret_guard_required")
	}
	approved := false
	if e.CommandRisk != nil {
		intents, hasIntent, err := operation.IntentFor(ctx, op, input)
		if err != nil {
			return fmt.Errorf("intent_failed: %w", err)
		}
		var risk CommandRisk
		if hasIntent && !intents.Empty() {
			risk, err = e.CommandRisk.Classify(ctx, spec, intents)
			if err != nil {
				return fmt.Errorf("cmdrisk_failed: %w", err)
			}
		} else if explicitIntentRequired(spec.Semantics) {
			return fmt.Errorf("intent_required")
		} else {
			risk = CommandRisk{Level: spec.Semantics.Risk, Reason: "declared operation risk"}
		}
		if risk.RequiresApproval {
			if err := e.approve(ctx, spec, input, risk); err != nil {
				return err
			}
			approved = true
		} else if e.MaxCommandRisk != "" && !riskAllowed(risk.Level, e.MaxCommandRisk) {
			if e.ApproveOverMaxCommandRisk {
				approvalRisk := risk
				approvalRisk.RequiresApproval = true
				if err := e.approve(ctx, spec, input, approvalRisk); err != nil {
					return err
				}
				approved = true
			} else {
				if risk.Reason != "" {
					return fmt.Errorf("cmdrisk_denied: %s: %s", risk.Level, risk.Reason)
				}
				return fmt.Errorf("cmdrisk_denied: %s", risk.Level)
			}
		}
	} else if spec.Semantics.Effects.Has(operation.EffectProcess) {
		return fmt.Errorf("cmdrisk_required")
	}
	if requiresApproval(spec.Semantics) && !approved {
		if err := e.approve(ctx, spec, input, CommandRisk{Level: spec.Semantics.Risk, Reason: "operation semantics require approval", RequiresApproval: true}); err != nil {
			return err
		}
	}
	if e.Sandbox != nil {
		if err := e.Sandbox.Check(ctx, spec, input); err != nil {
			return fmt.Errorf("sandbox_denied: %w", err)
		}
		return nil
	}
	if e.AllowPure && spec.Semantics.Pure() {
		return nil
	}
	if !spec.Semantics.Effects.Empty() {
		return fmt.Errorf("sandbox_required")
	}
	return nil
}

func (e SafetyEnvelope) approveAuthorization(ctx operation.Context, spec operation.Spec, input operation.Value, approval AuthorizationApprovalRequired) error {
	if e.Approval == nil {
		return approvalRequiredError(CommandRisk{Reason: approval.Error(), RequiresApproval: true})
	}
	req := ApprovalRequest{
		Subjects: approval.Subjects,
		Resource: approval.Resource,
		Action:   approval.Action,
		Spec:     spec,
		Input:    input,
		Reason:   approval.Reason,
		Risk:     CommandRisk{Reason: approval.Error(), RequiresApproval: true},
	}
	if err := e.Approval.Approve(ctx, req); err != nil {
		return fmt.Errorf("approval_denied: %w", err)
	}
	return nil
}

func (e SafetyEnvelope) approve(ctx operation.Context, spec operation.Spec, input operation.Value, risk CommandRisk) error {
	if e.Approval == nil {
		return approvalRequiredError(risk)
	}
	req := ApprovalRequest{Spec: spec, Input: input, Risk: risk}
	if auth, ok := policy.AuthorizationFromContext(ctx); ok {
		req.Subjects = auth.Subjects
	}
	if err := e.Approval.Approve(ctx, req); err != nil {
		return fmt.Errorf("approval_denied: %w", err)
	}
	return nil
}

func approvalRequiredError(risk CommandRisk) error {
	var parts []string
	if risk.Level != "" {
		parts = append(parts, string(risk.Level))
	}
	if strings.TrimSpace(risk.Reason) != "" {
		parts = append(parts, risk.Reason)
	}
	if len(parts) == 0 {
		return fmt.Errorf("approval_required")
	}
	return fmt.Errorf("approval_required: %s", strings.Join(parts, ": "))
}

func requiresApproval(semantics operation.Semantics) bool {
	return semantics.Risk == operation.RiskHigh ||
		semantics.Risk == operation.RiskCritical ||
		semantics.Effects.Has(operation.EffectDestructive) ||
		semantics.Effects.Has(operation.EffectIrreversible) ||
		semantics.Effects.Has(operation.EffectDelete)
}

func explicitIntentRequired(semantics operation.Semantics) bool {
	return semantics.Effects.Has(operation.EffectProcess)
}

func riskAllowed(actual, max operation.RiskLevel) bool {
	return riskRank(actual) <= riskRank(max)
}

func riskRank(risk operation.RiskLevel) int {
	switch risk {
	case operation.RiskCritical:
		return 4
	case operation.RiskHigh:
		return 3
	case operation.RiskMedium:
		return 2
	case operation.RiskLow:
		return 1
	default:
		return 0
	}
}
