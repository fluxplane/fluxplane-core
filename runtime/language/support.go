package language

import (
	"strings"

	corelanguage "github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/operation"
	coreproject "github.com/fluxplane/agentruntime/core/project"
)

// Support describes the reusable activation surface for one language family.
// Concrete plugins own execution; this runtime shape only connects project
// signals, operation sets, and optional toolchain probes.
type Support interface {
	SupportSpec() SupportSpec
}

// SupportSpec is the inert runtime description of language support.
type SupportSpec struct {
	Provider               corelanguage.ProviderSpec
	Signals                []SignalMatcher
	OperationSets          []operation.Set
	ToolchainOperationSets []operation.Set
	Toolchains             []corelanguage.ToolchainSpec
}

// SignalMatcher matches project inventory signals that activate a support.
type SignalMatcher struct {
	Kind      string
	Path      string
	Language  corelanguage.LanguageID
	Toolchain string
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

// SignalActivatedOperationSets returns operation sets activated only by project
// signals. These sets should not require external toolchains.
func SignalActivatedOperationSets(supports []Support, signals []coreproject.Signal) []operation.Set {
	var out []operation.Set
	for _, support := range supports {
		if support == nil {
			continue
		}
		spec := support.SupportSpec()
		if MatchesAnySignal(spec.Signals, signals) {
			out = append(out, spec.OperationSets...)
		}
	}
	return out
}

// ToolchainActivatedOperationSets returns operation sets activated by both
// project signals and observed available toolchains.
func ToolchainActivatedOperationSets(supports []Support, signals []coreproject.Signal, statuses []corelanguage.ToolchainStatus) []operation.Set {
	availableToolchains := map[string]bool{}
	for _, status := range statuses {
		if status.Available && strings.TrimSpace(status.ID) != "" {
			availableToolchains[status.ID] = true
		}
	}
	var out []operation.Set
	for _, support := range supports {
		if support == nil {
			continue
		}
		spec := support.SupportSpec()
		if !MatchesAnySignal(spec.Signals, signals) {
			continue
		}
		if hasAvailableToolchain(spec.Toolchains, availableToolchains) {
			out = append(out, spec.ToolchainOperationSets...)
		}
	}
	return out
}

// ActivatedOperationSets returns all support operation sets activated by
// project signals and available toolchains.
func ActivatedOperationSets(supports []Support, signals []coreproject.Signal, statuses []corelanguage.ToolchainStatus) []operation.Set {
	out := SignalActivatedOperationSets(supports, signals)
	out = append(out, ToolchainActivatedOperationSets(supports, signals, statuses)...)
	return out
}

// MatchesAnySignal reports whether any matcher accepts any observed project
// signal. Empty matchers do not match; callers should opt in explicitly.
func MatchesAnySignal(matchers []SignalMatcher, signals []coreproject.Signal) bool {
	for _, signal := range signals {
		for _, matcher := range matchers {
			if matcher.Matches(signal) {
				return true
			}
		}
	}
	return false
}

// Matches reports whether matcher accepts the signal.
func (m SignalMatcher) Matches(signal coreproject.Signal) bool {
	if m.Kind != "" && m.Kind != signal.Kind {
		return false
	}
	if m.Path != "" && m.Path != signal.Path {
		return false
	}
	if m.Language != "" && m.Language != signal.Language {
		return false
	}
	if m.Toolchain != "" && m.Toolchain != signal.Toolchain {
		return false
	}
	return m.Kind != "" || m.Path != "" || m.Language != "" || m.Toolchain != ""
}

func hasAvailableToolchain(toolchains []corelanguage.ToolchainSpec, available map[string]bool) bool {
	for _, toolchain := range toolchains {
		if available[toolchain.ID] {
			return true
		}
	}
	return false
}
