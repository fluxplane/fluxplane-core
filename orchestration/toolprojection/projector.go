package toolprojection

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

var invalidToolName = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// Config describes one tool projection pass.
type Config struct {
	Operations              session.OperationCatalog
	Commands                session.CommandCatalog
	Caller                  policy.Caller
	Trust                   policy.Trust
	AllowSideEffects        bool
	AllowApprovalRequired   bool
	MaxRisk                 operation.RiskLevel
	IncludeBareOperations   bool
	PreferCommandProjection bool
}

// Result is the projected model-facing tool set plus skipped-resource reasons.
type Result struct {
	Tools       []tool.Spec  `json:"tools,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Diagnostic explains why a resource was not projected.
type Diagnostic struct {
	Resource resource.ResourceID `json:"resource,omitempty"`
	Reason   string              `json:"reason"`
}

// Project projects executable commands and operations into safe model-facing
// tool descriptors. Commands are preferred because they carry invocation
// policy; bare operations can be included for host-controlled tests and
// low-level compositions.
func Project(cfg Config) Result {
	if cfg.Trust.Kind == "" {
		cfg.Trust.Kind = policy.TrustInvocation
	}
	if cfg.MaxRisk == "" {
		cfg.MaxRisk = operation.RiskLow
	}
	out := Result{}
	usedNames := map[string]int{}

	commandKeys := sortedCommandKeys(cfg.Commands)
	for _, key := range commandKeys {
		binding := cfg.Commands[key]
		projected, ok, reason := projectCommand(cfg, binding)
		if !ok {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{Resource: binding.ID, Reason: reason})
			continue
		}
		projected.Name = uniqueName(projected.Name, usedNames)
		out.Tools = append(out.Tools, projected)
	}

	if !cfg.IncludeBareOperations {
		return out
	}
	operationKeys := sortedOperationKeys(cfg.Operations)
	for _, key := range operationKeys {
		binding := cfg.Operations[key]
		if cfg.PreferCommandProjection && operationCovered(out.Tools, binding.ID) {
			continue
		}
		projected, ok, reason := projectOperation(cfg, binding)
		if !ok {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{Resource: binding.ID, Reason: reason})
			continue
		}
		projected.Name = uniqueName(projected.Name, usedNames)
		out.Tools = append(out.Tools, projected)
	}
	return out
}

func projectCommand(cfg Config, binding session.CommandBinding) (tool.Spec, bool, string) {
	spec := binding.Spec
	evaluation := policy.EvaluateInvocation(spec.Policy, cfg.Caller, cfg.Trust)
	switch evaluation.Decision {
	case policy.DecisionDeny:
		return tool.Spec{}, false, evaluation.Reason
	case policy.DecisionApprovalRequired:
		if !cfg.AllowApprovalRequired {
			return tool.Spec{}, false, evaluation.Reason
		}
	}
	semantics := operation.Semantics{}
	if spec.Target.Kind == invocation.TargetOperation {
		operationID := binding.OperationID
		if operationID.IsZero() {
			operationID = binding.TargetID
		}
		operationBinding, ok := cfg.Operations[operationID.Address()]
		if !ok {
			return tool.Spec{}, false, "operation_not_bound"
		}
		semantics = operationBinding.Operation.Spec().Semantics
		if ok, reason := safeToProject(cfg, semantics); !ok {
			return tool.Spec{}, false, reason
		}
	}
	projected := tool.Spec{
		Name:        tool.Name(toolName(binding.ID)),
		Description: spec.Description,
		Target:      spec.Target,
		TargetID:    firstResourceID(binding.TargetID, binding.OperationID),
		Input:       spec.Input,
		Output:      spec.Output,
		Semantics:   semantics,
		Policy:      spec.Policy,
		Annotations: map[string]string{
			"projection": "command",
			"command_id": binding.ID.Address(),
		},
	}
	if !projected.TargetID.IsZero() {
		projected.Annotations["target_id"] = projected.TargetID.Address()
	}
	if !binding.OperationID.IsZero() {
		projected.Annotations["operation_id"] = binding.OperationID.Address()
	}
	if projected.TargetID.IsZero() {
		projected.TargetID = binding.ID
	}
	return projected, true, ""
}

func projectOperation(cfg Config, binding session.OperationBinding) (tool.Spec, bool, string) {
	spec := binding.Operation.Spec()
	if ok, reason := safeToProject(cfg, spec.Semantics); !ok {
		return tool.Spec{}, false, reason
	}
	return tool.Spec{
		Name:        tool.Name(spec.Ref.Name),
		Description: spec.Description,
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: spec.Ref,
		},
		TargetID:  binding.ID,
		Input:     spec.Input,
		Output:    spec.Output,
		Semantics: spec.Semantics,
		Annotations: map[string]string{
			"projection":   "operation",
			"operation_id": binding.ID.Address(),
		},
	}, true, ""
}

func safeToProject(cfg Config, semantics operation.Semantics) (bool, string) {
	if !cfg.AllowSideEffects && !semantics.ReadOnly() {
		return false, "side_effecting_operation"
	}
	if !riskAllowed(semantics.Risk, cfg.MaxRisk) {
		return false, "risk_too_high"
	}
	if requiresApproval(semantics) && !cfg.AllowApprovalRequired {
		return false, "approval_required"
	}
	return true, ""
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

func sortedCommandKeys(catalog session.CommandCatalog) []string {
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedOperationKeys(catalog session.OperationCatalog) []string {
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstResourceID(ids ...resource.ResourceID) resource.ResourceID {
	for _, id := range ids {
		if !id.IsZero() {
			return id
		}
	}
	return resource.ResourceID{}
}

func operationCovered(tools []tool.Spec, id resource.ResourceID) bool {
	if id.IsZero() {
		return false
	}
	for _, projected := range tools {
		if projected.TargetID.Equal(id) {
			return true
		}
	}
	return false
}

func toolName(id resource.ResourceID) string {
	name := id.Address()
	if name == "" {
		name = id.Name
	}
	name = strings.Trim(invalidToolName.ReplaceAllString(name, "_"), "_")
	if name == "" {
		return "tool"
	}
	return name
}

func uniqueName(name tool.Name, used map[string]int) tool.Name {
	base := string(name)
	if base == "" {
		base = "tool"
	}
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return tool.Name(base)
	}
	return tool.Name(fmt.Sprintf("%s_%d", base, count+1))
}
