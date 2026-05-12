package operationruntime

import (
	"fmt"

	"github.com/fluxplane/agentruntime/core/operation"
)

// SafetyGate rejects operation execution before the handler runs.
type SafetyGate interface {
	Check(operation.Context, operation.Spec, operation.Value) error
}

// SafetyGateFunc adapts a function to SafetyGate.
type SafetyGateFunc func(operation.Context, operation.Spec, operation.Value) error

func (f SafetyGateFunc) Check(ctx operation.Context, spec operation.Spec, input operation.Value) error {
	if f == nil {
		return nil
	}
	return f(ctx, spec, input)
}

// Sandbox models the sandboxing decision for one operation execution. Concrete
// process/container/browser sandboxes belong in adapters or plugins.
type Sandbox interface {
	Check(operation.Context, operation.Spec, operation.Value) error
}

// AccessController models ACL/scope enforcement for one operation execution.
type AccessController interface {
	Authorize(operation.Context, operation.Spec, operation.Value) error
}

// CommandRiskClassifier models shell/code execution risk classification, for
// example via codewandler/cmdrisk or a successor.
type CommandRiskClassifier interface {
	Classify(operation.Context, operation.Spec, operation.Value) (CommandRisk, error)
}

// SecretGuard models secret detection/redaction policy before execution.
type SecretGuard interface {
	Check(operation.Context, operation.Spec, operation.Value) error
}

// ApprovalGate models approval enforcement before execution.
type ApprovalGate interface {
	Check(operation.Context, operation.Spec, operation.Value) error
}

// CommandRisk is the runtime command-risk classification result.
type CommandRisk struct {
	Level  operation.RiskLevel `json:"level,omitempty"`
	Reason string              `json:"reason,omitempty"`
}

// SafetyEnvelope is the default runtime safety gate shape for real
// side-effecting operations.
type SafetyEnvelope struct {
	Sandbox        Sandbox
	ACL            AccessController
	CommandRisk    CommandRiskClassifier
	Secrets        SecretGuard
	Approval       ApprovalGate
	AllowPure      bool
	MaxCommandRisk operation.RiskLevel
}

// Check enforces the configured safety envelope.
func (e SafetyEnvelope) Check(ctx operation.Context, spec operation.Spec, input operation.Value) error {
	if e.ACL != nil {
		if err := e.ACL.Authorize(ctx, spec, input); err != nil {
			return fmt.Errorf("acl_denied: %w", err)
		}
	}
	if e.Secrets != nil {
		if err := e.Secrets.Check(ctx, spec, input); err != nil {
			return fmt.Errorf("secret_guard_denied: %w", err)
		}
	} else if spec.Semantics.Effects.Has(operation.EffectSensitiveData) {
		return fmt.Errorf("secret_guard_required")
	}
	if e.CommandRisk != nil {
		risk, err := e.CommandRisk.Classify(ctx, spec, input)
		if err != nil {
			return fmt.Errorf("cmdrisk_failed: %w", err)
		}
		if e.MaxCommandRisk != "" && !riskAllowed(risk.Level, e.MaxCommandRisk) {
			if risk.Reason != "" {
				return fmt.Errorf("cmdrisk_denied: %s: %s", risk.Level, risk.Reason)
			}
			return fmt.Errorf("cmdrisk_denied: %s", risk.Level)
		}
	} else if spec.Semantics.Effects.Has(operation.EffectProcess) {
		return fmt.Errorf("cmdrisk_required")
	}
	if e.Approval != nil {
		if err := e.Approval.Check(ctx, spec, input); err != nil {
			return fmt.Errorf("approval_denied: %w", err)
		}
	} else if requiresApproval(spec.Semantics) {
		return fmt.Errorf("approval_required")
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

func requiresApproval(semantics operation.Semantics) bool {
	return semantics.Risk == operation.RiskHigh ||
		semantics.Risk == operation.RiskCritical ||
		semantics.Effects.Has(operation.EffectDestructive) ||
		semantics.Effects.Has(operation.EffectIrreversible) ||
		semantics.Effects.Has(operation.EffectDelete)
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
