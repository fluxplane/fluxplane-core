package language

import (
	corelanguage "github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/operation"
)

// Support describes the reusable activation surface for one language family.
// Concrete plugins own execution; this runtime shape only connects project
// assertions, operation sets, and optional toolchain probes.
type Support interface {
	SupportSpec() SupportSpec
}

// SupportSpec is the inert runtime description of language support.
type SupportSpec struct {
	Provider               corelanguage.ProviderSpec
	OperationSets          []operation.Set
	ToolchainOperationSets []operation.Set
	Toolchains             []corelanguage.ToolchainSpec
}

// StaticSupport is a Support backed by a fixed spec.
type StaticSupport struct {
	Spec SupportSpec
}

// SupportSpec returns the fixed support spec.
func (s StaticSupport) SupportSpec() SupportSpec { return s.Spec }

// OperationSets returns all operation sets contributed by supports.
func OperationSets(supports []Support) []operation.Set {
	var out []operation.Set
	for _, support := range supports {
		if support == nil {
			continue
		}
		spec := support.SupportSpec()
		out = append(out, spec.OperationSets...)
		out = append(out, spec.ToolchainOperationSets...)
	}
	return out
}

// Toolchains returns all toolchain specs contributed by supports.
func Toolchains(supports []Support) []corelanguage.ToolchainSpec {
	var out []corelanguage.ToolchainSpec
	for _, support := range supports {
		if support == nil {
			continue
		}
		out = append(out, support.SupportSpec().Toolchains...)
	}
	return out
}
