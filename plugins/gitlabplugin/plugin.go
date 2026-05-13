package gitlabplugin

import (
	"context"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const Name = "gitlab"

type Plugin struct {
	executor  connectorplugin.Executor
	instances []connectorplugin.Instance
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ConnectorProviderContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

func New(executor connectorplugin.Executor, instances []connectorplugin.Instance) Plugin {
	return Plugin{executor: executor, instances: append([]connectorplugin.Instance(nil), instances...)}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "GitLab connector operations."}
}

func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs, err := connectorplugin.Specs(p.instances, gitlabActions())
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	return resource.ContributionBundle{Operations: specs}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return connectorplugin.Operations(p.executor, p.instances, gitlabActions())
}

func (Plugin) ConnectorProviders(context.Context, pluginhost.Context) ([]pluginhost.ConnectorProvider, error) {
	return []pluginhost.ConnectorProvider{{Name: Name}}, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	projectEntity := runtimedatasource.EntityOf[Project](ProjectEntity, "GitLab project.")
	projectEntity.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "gitlab_project_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/([^/\s<>"']+/[^/\s<>"'#?]+)(?:[/?#][^\s<>"']*)?`,
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.8,
		},
	}
	return []coredatasource.Provider{connectorplugin.NewDatasourceProvider(p.executor, []connectorplugin.DatasourceAction{{
		Kind:        Name,
		Entity:      projectEntity,
		SearchOp:    "gitlab.project.search",
		GetOp:       "gitlab.project.get",
		QueryParam:  "query",
		LimitParam:  "per_page",
		IDParam:     "id",
		IDFields:    []string{"id", "path_with_namespace"},
		TitleFields: []string{"path_with_namespace", "name"},
		TextFields:  []string{"description"},
		URLFields:   []string{"web_url"},
		MetadataFields: map[string][]string{
			"visibility":     {"visibility"},
			"default_branch": {"default_branch"},
			"namespace":      {"namespace.full_path", "namespace.name"},
		},
	}})}, nil
}

const ProjectEntity coredatasource.EntityType = "gitlab.project"

type Project struct {
	ID                int    `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab project id."`
	Name              string `json:"name" datasource:"searchable" jsonschema:"description=Project name."`
	PathWithNamespace string `json:"path_with_namespace" datasource:"searchable,filterable" jsonschema:"description=Full project path with namespace."`
	Description       string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Project description."`
	WebURL            string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Project web URL."`
}

func gitlabActions() []connectorplugin.Action {
	return []connectorplugin.Action{{
		Kind:        Name,
		Operation:   "gitlab.project.search",
		Suffix:      "project_search",
		Description: "Search GitLab projects by name.",
		Spec: func(name string) operation.Spec {
			return operationruntime.WithTypedContract[projectSearchInput, connectorplugin.Output](operation.Spec{
				Ref:         operation.Ref{Name: operation.Name(name)},
				Description: "Search GitLab projects by name.",
				Semantics: operation.Semantics{
					Determinism: operation.DeterminismNonDeterministic,
					Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
					Idempotency: operation.IdempotencyIdempotent,
					Risk:        operation.RiskLow,
				},
			})
		},
	}}
}

type projectSearchInput struct {
	Query   string `json:"query" jsonschema:"description=Project search query.,required"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"description=Maximum projects per page. Defaults to 20."`
	Page    int    `json:"page,omitempty" jsonschema:"description=Result page number. Defaults to 1."`
}
