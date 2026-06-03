package echo

import (
	"context"

	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
)

const Name = "echo"

// Plugin contributes a pure echo operation and command.
type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns the echo plugin.
func New() Plugin { return Plugin{} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{
		Name:        Name,
		Description: "Pure echo operation and command for smoke tests and examples.",
	}
}

// Contributions returns command and operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Operations: []operation.Spec{echoSpec()},
		Commands: []command.Spec{{
			Path:        command.Path{"echo"},
			Description: "Return the provided input.",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "echo"},
			},
			Policy: policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			},
		}},
	}, nil
}

// Operations returns executable operation implementations.
func (Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(echoSpec(), func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(input)
		}),
	}, nil
}

func echoSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: "echo"},
		Description: "Return the provided input.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}
