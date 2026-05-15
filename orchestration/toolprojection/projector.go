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
	"github.com/fluxplane/agentruntime/core/resourceaddr"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

var invalidToolName = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// Config describes one tool projection pass.
type Config struct {
	Operations              session.OperationCatalog
	Commands                session.CommandCatalog
	ToolSets                session.ToolSetCatalog
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
	coveredOperations := map[string]struct{}{}

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

	toolSetKeys := sortedToolSetKeys(cfg.ToolSets)
	for _, key := range toolSetKeys {
		binding := cfg.ToolSets[key]
		projected, covered, ok, reason := projectToolSet(cfg, binding)
		if !ok {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{Resource: binding.ID, Reason: reason})
			continue
		}
		projected.Name = uniqueName(projected.Name, usedNames)
		out.Tools = append(out.Tools, projected)
		for _, id := range covered {
			if !id.IsZero() {
				coveredOperations[id.Address()] = struct{}{}
			}
		}
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
		if _, ok := coveredOperations[binding.ID.Address()]; ok {
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

// ProjectForAgent projects tools with the default policy context used by
// model-facing agent factories.
func ProjectForAgent(cfg Config) Result {
	if cfg.Caller.Kind == "" {
		cfg.Caller = policy.Caller{Kind: policy.CallerAgent}
	}
	if cfg.Trust.Kind == "" {
		cfg.Trust.Kind = policy.TrustInvocation
	}
	if cfg.Trust.Level == "" {
		cfg.Trust.Level = policy.TrustVerified
	}
	return Project(cfg)
}

func projectToolSet(cfg Config, binding session.ToolSetBinding) (tool.Spec, []resource.ResourceID, bool, string) {
	spec, ok := toolSetSpec(binding.Spec)
	if !ok {
		return tool.Spec{}, nil, false, "tool_set_spec_unsupported"
	}
	if spec.Action == nil {
		return tool.Spec{}, nil, false, "tool_set_has_no_action_projection"
	}
	if err := spec.Action.Validate(); err != nil {
		return tool.Spec{}, nil, false, err.Error()
	}
	semantics := operation.Semantics{}
	var covered []resource.ResourceID
	dispatch := &tool.Dispatch{
		ActionField: spec.Action.ActionField,
		Cases:       make([]tool.DispatchCase, 0, len(spec.Action.Cases)),
	}
	for _, actionCase := range spec.Action.Cases {
		target := actionCase.Target
		if target.Kind != invocation.TargetOperation {
			return tool.Spec{}, nil, false, "tool_set_action_target_not_operation"
		}
		if target.Operation.Name == "" {
			return tool.Spec{}, nil, false, "tool_set_action_operation_empty"
		}
		operationBinding, err := cfg.Operations.Resolve(target.Operation.String(), binding.ID)
		if err != nil {
			return tool.Spec{}, nil, false, "operation_not_bound"
		}
		opSemantics := operationBinding.Operation.Spec().Semantics
		if ok, reason := safeToProject(cfg, opSemantics); !ok {
			return tool.Spec{}, nil, false, reason
		}
		semantics = mergeSemantics(semantics, opSemantics)
		covered = append(covered, operationBinding.ID)
		dispatch.Cases = append(dispatch.Cases, tool.DispatchCase{
			Action: actionCase.Action,
			Target: target,
		})
	}
	description := spec.Action.Description
	if description == "" {
		description = spec.Description
	}
	return tool.Spec{
		Name:        spec.Action.Tool,
		Description: description,
		TargetID:    resourceaddr.Address(binding.ID.Address()),
		Input:       spec.Action.Input,
		Output:      spec.Action.Output,
		Semantics:   semantics,
		Dispatch:    dispatch,
		Annotations: map[string]string{
			"projection":  "tool_set_action",
			"tool_set_id": binding.ID.Address(),
		},
	}, covered, true, ""
}

func toolSetSpec(value any) (tool.Set, bool) {
	switch spec := value.(type) {
	case tool.Set:
		return spec, true
	case *tool.Set:
		if spec == nil {
			return tool.Set{}, false
		}
		return *spec, true
	default:
		return tool.Set{}, false
	}
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
		TargetID:    resourceaddr.Address(firstResourceID(binding.TargetID, binding.OperationID).Address()),
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
		projected.Annotations["target_id"] = projected.TargetID.String()
	}
	if !binding.OperationID.IsZero() {
		projected.Annotations["operation_id"] = binding.OperationID.Address()
	}
	if projected.TargetID.IsZero() {
		projected.TargetID = resourceaddr.Address(binding.ID.Address())
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
		TargetID:  resourceaddr.Address(binding.ID.Address()),
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

func mergeSemantics(a, b operation.Semantics) operation.Semantics {
	return operation.Semantics{
		Determinism: mergeDeterminism(a.Determinism, b.Determinism),
		Effects:     mergeEffects(a.Effects, b.Effects),
		Idempotency: mergeIdempotency(a.Idempotency, b.Idempotency),
		Risk:        maxRisk(a.Risk, b.Risk),
	}
}

func mergeDeterminism(a, b operation.Determinism) operation.Determinism {
	if a == "" {
		return b
	}
	if b == "" || a == b {
		return a
	}
	return operation.DeterminismNonDeterministic
}

func mergeIdempotency(a, b operation.Idempotency) operation.Idempotency {
	if a == "" {
		return b
	}
	if b == "" || a == b {
		return a
	}
	return operation.IdempotencyUnknown
}

func mergeEffects(a, b operation.EffectSet) operation.EffectSet {
	seen := map[operation.Effect]struct{}{}
	var out operation.EffectSet
	for _, effect := range append(append(operation.EffectSet(nil), a...), b...) {
		if _, ok := seen[effect]; ok {
			continue
		}
		seen[effect] = struct{}{}
		out = append(out, effect)
	}
	return out
}

func maxRisk(a, b operation.RiskLevel) operation.RiskLevel {
	if riskRank(a) >= riskRank(b) {
		return a
	}
	return b
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

func sortedToolSetKeys(catalog session.ToolSetCatalog) []string {
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
		if projected.TargetID == resourceaddr.Address(id.Address()) {
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
