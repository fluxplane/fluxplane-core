package gitlab

import (
	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-policy"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

const (
	mergeRequestOp = "mr"
	repoFileOp     = "repo_file"
	branchOp       = "branch"
	tagOp          = "tag"
	commitOp       = "commit"
	ciVariableOp   = "ci_variable"
	pipelineOp     = "pipeline"
	snippetOp      = "snippet"

	requiredGitLabScopeAPI = "api"
)

func (p Plugin) operationName(suffix string) string {
	return Name + "_" + normalize(suffix)
}

func (p Plugin) operationSpecs() []operation.Spec {
	return []operation.Spec{
		p.mrOperationSpec(),
		p.repoFileOperationSpec(),
		p.branchOperationSpec(),
		p.tagOperationSpec(),
		p.commitOperationSpec(),
		p.ciVariableOperationSpec(),
		p.pipelineOperationSpec(),
		p.snippetOperationSpec(),
	}
}

func (p Plugin) operations() []operation.Operation {
	return []operation.Operation{
		p.namedOperation(p.mrOperation()),
		p.namedOperation(p.repoFileOperation()),
		p.namedOperation(p.branchOperation()),
		p.namedOperation(p.tagOperation()),
		p.namedOperation(p.commitOperation()),
		p.namedOperation(p.ciVariableOperation()),
		p.namedOperation(p.pipelineOperation()),
		p.namedOperation(p.snippetOperation()),
	}
}

func (p Plugin) namedOperation(op operation.Operation) operation.Operation {
	return operationruntime.NewNamedInstance(Name, p.ref.InstanceName(), op)
}

func gitlabWriteSpec(name, description string, risk operation.RiskLevel, input, output operation.Type) operation.Spec {
	return gitlabWriteSpecWithEffects(name, description, risk, operation.EffectSet{
		operation.EffectNetwork,
		operation.EffectWriteExternal,
		operation.EffectCreate,
		operation.EffectUpdate,
	}, input, output)
}

func gitlabWriteSpecWithEffects(name, description string, risk operation.RiskLevel, effects operation.EffectSet, input, output operation.Type) operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Input:       input,
		Output:      output,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     effects,
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        risk,
		},
		Annotations: map[string]string{
			operationruntime.AnnotationNamedPluginKind:   Name,
			operationruntime.AnnotationRequiredAuthScope: requiredGitLabScopeAPI,
		},
	}
}

func (p Plugin) gitlabNetworkWriteAccess(operation.Context, any) ([]operationruntime.AccessDescriptor, error) {
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(p.config().baseURL(), policy.ActionNetworkFetch)}, nil
}

func boolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	return gitlab.Ptr(*value)
}
