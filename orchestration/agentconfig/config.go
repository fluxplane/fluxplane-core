// Package agentconfig applies composed profile constraints to agent specs.
package agentconfig

import (
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/tool"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	"github.com/fluxplane/fluxplane-operation"
)

// FilterTools returns the projected tools allowed by spec-level tool,
// command, and operation selectors.
func FilterTools(spec agent.Spec, tools []tool.Spec) []tool.Spec {
	if len(spec.Tools) == 0 && spec.Commands == nil && len(spec.Operations) == 0 {
		return tools
	}
	allowedTools := map[string]struct{}{}
	for _, ref := range spec.Tools {
		if ref.Name != "" {
			allowedTools[ref.Name] = struct{}{}
		}
	}
	allowedCommands := map[string]struct{}{}
	for _, ref := range spec.Commands {
		if ref.Name != "" {
			allowedCommands[ref.Name] = struct{}{}
		}
	}
	allowedOperations := make([]operation.Ref, 0, len(spec.Operations))
	for _, ref := range spec.Operations {
		if ref.Name != "" {
			allowedOperations = append(allowedOperations, ref)
		}
	}
	out := make([]tool.Spec, 0, len(tools))
	for _, projected := range tools {
		if toolAllowed(projected, allowedTools, allowedCommands, allowedOperations) {
			out = append(out, projected)
		}
	}
	return out
}

// FilterContextProviders returns the provider implementations selected by the
// agent spec. Auto-context providers are always retained.
func FilterContextProviders(spec agent.Spec, providers []corecontext.Provider) []corecontext.Provider {
	if len(providers) == 0 {
		return nil
	}
	if spec.Context == nil {
		return append([]corecontext.Provider(nil), providers...)
	}
	allowed := map[corecontext.ProviderName]struct{}{}
	for _, ref := range spec.Context {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]corecontext.Provider, 0, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		providerSpec := provider.Spec()
		if providerSpec.Annotations[corecontext.AnnotationAutoContext] == "true" {
			out = append(out, provider)
			continue
		}
		if _, ok := allowed[providerSpec.Name]; ok {
			out = append(out, provider)
		}
	}
	return out
}

// ApplySessionProfile narrows an agent spec by session-level context, command,
// and operation caps.
func ApplySessionProfile(spec agent.Spec, profile coresession.Spec) agent.Spec {
	if profile.Context != nil {
		spec.Context = narrowAgentContext(spec.Context, profile.Context)
	}
	if profile.Commands != nil {
		spec.Commands = narrowAgentCommands(spec.Commands, profile.Commands)
	}
	if profile.Operations != nil {
		spec.Operations = narrowAgentOperations(spec.Operations, profile.Operations)
	}
	return spec
}

func toolAllowed(projected tool.Spec, tools map[string]struct{}, commands map[string]struct{}, operations []operation.Ref) bool {
	if _, ok := tools[string(projected.Name)]; ok {
		return true
	}
	for ref := range commands {
		if refMatches(ref, projected.Annotations["command_id"]) || ref == string(projected.Name) {
			return true
		}
	}
	if operationAllowed(projected.Target.Operation, operations) {
		return true
	}
	if dispatchAllowedByOperations(projected.Dispatch, operations) {
		return true
	}
	for _, ref := range operations {
		if refMatches(string(ref.Name), projected.Annotations["operation_id"]) {
			return true
		}
	}
	return false
}

func dispatchAllowedByOperations(dispatch *tool.Dispatch, operations []operation.Ref) bool {
	if dispatch == nil || len(dispatch.Cases) == 0 || len(operations) == 0 {
		return false
	}
	for _, candidate := range dispatch.Cases {
		if candidate.Target.Kind != invocation.TargetOperation || candidate.Target.Operation.Name == "" {
			return false
		}
		if !operationAllowed(candidate.Target.Operation, operations) {
			return false
		}
	}
	return true
}

func operationAllowed(candidate operation.Ref, selectors []operation.Ref) bool {
	for _, selector := range selectors {
		if selector.Matches(candidate) {
			return true
		}
	}
	return false
}

func narrowAgentContext(base []corecontext.ProviderRef, caps []corecontext.ProviderRef) []corecontext.ProviderRef {
	if base == nil {
		return append([]corecontext.ProviderRef(nil), caps...)
	}
	allowed := map[corecontext.ProviderName]struct{}{}
	for _, ref := range caps {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]corecontext.ProviderRef, 0, len(base))
	for _, ref := range base {
		if _, ok := allowed[ref.Name]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func narrowAgentCommands(base []agent.CommandRef, caps []command.Path) []agent.CommandRef {
	if base == nil {
		out := make([]agent.CommandRef, 0, len(caps))
		for _, path := range caps {
			if ref := commandPathRef(path); ref != "" {
				out = append(out, agent.CommandRef{Name: ref})
			}
		}
		return out
	}
	allowed := map[string]struct{}{}
	for _, path := range caps {
		if ref := commandPathRef(path); ref != "" {
			allowed[ref] = struct{}{}
		}
		if display := path.String(); display != "" {
			allowed[display] = struct{}{}
		}
	}
	out := make([]agent.CommandRef, 0, len(base))
	for _, ref := range base {
		if commandRefAllowed(ref.Name, allowed) {
			out = append(out, ref)
		}
	}
	return out
}

func commandRefAllowed(ref string, allowed map[string]struct{}) bool {
	if _, ok := allowed[ref]; ok {
		return true
	}
	for candidate := range allowed {
		if refMatches(candidate, ref) || refMatches(ref, candidate) {
			return true
		}
	}
	return false
}

func narrowAgentOperations(base []operation.Ref, caps []operation.Ref) []operation.Ref {
	if base == nil {
		return append([]operation.Ref(nil), caps...)
	}
	out := make([]operation.Ref, 0, len(base))
	for _, baseRef := range base {
		for _, capRef := range caps {
			if narrowed, ok := intersectOperationRefs(baseRef, capRef); ok {
				out = append(out, narrowed)
				break
			}
		}
	}
	return out
}

func intersectOperationRefs(base, cap operation.Ref) (operation.Ref, bool) {
	if base.Name == "" || cap.Name == "" {
		return operation.Ref{}, false
	}
	if base.Matches(cap) {
		return cap, true
	}
	if cap.Matches(base) {
		return base, true
	}
	if operation.HasSelectorMeta(base) && operation.HasSelectorMeta(cap) && base.Name == cap.Name && base.Version == cap.Version {
		return base, true
	}
	return operation.Ref{}, false
}

func commandPathRef(path command.Path) string {
	if len(path) == 0 {
		return ""
	}
	parts := make([]string, 0, len(path))
	for _, part := range path {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ":") + ":" + parts[len(parts)-1]
}

func refMatches(ref, address string) bool {
	ref = strings.TrimSpace(ref)
	address = strings.TrimSpace(address)
	if ref == "" || address == "" {
		return false
	}
	return ref == address || strings.HasSuffix(address, ":"+ref)
}
