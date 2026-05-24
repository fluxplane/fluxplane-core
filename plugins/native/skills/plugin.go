// Package skillplugin exposes skill activation, context, and datasource support.
package skills

import (
	"context"

	"github.com/fluxplane/engine/core/activation"
	corecontext "github.com/fluxplane/engine/core/context"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	runtimeskill "github.com/fluxplane/engine/runtime/skill"
)

const (
	Name               = "skills"
	SkillOperation     = "skill"
	DatasourceName     = "skills"
	SkillEntity        = coredatasource.EntityType("skill")
	ReferenceEntity    = coredatasource.EntityType("skill.reference")
	defaultSearchLimit = 10
)

// Plugin contributes skill operation, context, and datasource resources.
type Plugin struct {
	repo *runtimeskill.Repository
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

// New returns the skill plugin.
func New() Plugin { return Plugin{} }

// NewWithRepository returns a skill plugin with a fallback repository for
// datasource calls outside a session context.
func NewWithRepository(repo *runtimeskill.Repository) Plugin {
	return Plugin{repo: repo}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Skill activation, context, and datasource access."}
}

func (Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	name := ctx.Ref.InstanceName()
	if name == "" {
		name = Name
	}
	return resource.ContributionBundle{
		ActivationSets: []activation.Set{{
			Name:        name,
			Aliases:     []string{name + ".default"},
			Description: "Skill activation, context, and datasource access.",
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: SkillOperation},
			}, {
				Kind:            activation.TargetContextProvider,
				ContextProvider: corecontext.ProviderRef{Name: runtimeskill.ContextProviderName},
			}, {
				Kind:       activation.TargetDatasource,
				Datasource: coredatasource.Ref{Name: DatasourceName},
			}},
		}},
		ContextProviders: []corecontext.ProviderSpec{contextSpec()},
		Operations:       []operation.Spec{operationSpec()},
		Datasources:      []coredatasource.Spec{DatasourceSpec()},
	}, nil
}

func (Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operationruntime.NewTypedResult[actionInput, operation.Rendered](operationSpec(), runSkillOperation),
	}, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{datasourceProvider(p)}, nil
}

func contextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:        runtimeskill.ContextProviderName,
		Description: "Lists available skills and active skill references.",
		Kinds:       []corecontext.BlockKind{corecontext.BlockText, corecontext.BlockData},
	}
}

// DatasourceSpec is the configured datasource exposed by the skills plugin.
func DatasourceSpec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        DatasourceName,
		Description: "Composed agent skills and skill references.",
		Entities:    []coredatasource.EntityType{SkillEntity, ReferenceEntity},
		Kind:        DatasourceName,
	}
}

func operationSpec() operation.Spec {
	return operationruntime.WithTypedContract[actionInput, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: SkillOperation},
		Description: "Activate skills and exact skill references for the current session.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}
