package jiraplugin

import (
	"context"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const Name = "jira"

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
	return pluginhost.Manifest{Name: Name, Description: "Jira connector operations."}
}

func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs, err := connectorplugin.Specs(p.instances, jiraActions())
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	return resource.ContributionBundle{Operations: specs}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return connectorplugin.Operations(p.executor, p.instances, jiraActions())
}

func (Plugin) ConnectorProviders(context.Context, pluginhost.Context) ([]pluginhost.ConnectorProvider, error) {
	return []pluginhost.ConnectorProvider{{Name: Name}}, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	issueEntity := runtimedatasource.EntityOf[Issue](IssueEntity, "Jira issue.")
	issueEntity.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "jira_issue_key",
			Kind:          coredatasource.DetectorRegex,
			Pattern:       `\b([A-Z][A-Z0-9]+-\d+)\b`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			Confidence:    0.95,
		},
		{
			Name:          "jira_issue_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/browse/([A-Z][A-Z0-9]+-\d+)`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.95,
		},
	}
	projectEntity := runtimedatasource.EntityOf[Project](ProjectEntity, "Jira project.")
	return []coredatasource.Provider{connectorplugin.NewDatasourceProvider(p.executor, []connectorplugin.DatasourceAction{
		{
			Kind:        Name,
			Entity:      issueEntity,
			SearchOp:    "jira.issue.search",
			GetOp:       "jira.issue.get",
			QueryParam:  "jql",
			LimitParam:  "max_results",
			IDParam:     "key",
			QueryValue:  jiraDatasourceJQL,
			IDFields:    []string{"key", "id"},
			TitleFields: []string{"summary", "fields.summary", "key"},
			TextFields:  []string{"description", "fields.description"},
			URLFields:   []string{"self", "url", "web_url"},
			MetadataFields: map[string][]string{
				"status":       {"fields.status.name", "status"},
				"assignee":     {"fields.assignee.displayName", "assignee.displayName", "assignee"},
				"project":      {"fields.project.key", "project.key", "project"},
				"project_type": {"fields.project.projectTypeKey", "project.projectTypeKey"},
			},
		},
		{
			Kind:        Name,
			Entity:      projectEntity,
			SearchOp:    "jira.project.list",
			GetOp:       "jira.project.get",
			QueryParam:  "-",
			LimitParam:  "max_results",
			IDParam:     "key",
			LocalFilter: true,
			IDFields:    []string{"key", "id"},
			TitleFields: []string{"name", "key"},
			TextFields:  []string{"description", "projectTypeKey"},
			URLFields:   []string{"self"},
			MetadataFields: map[string][]string{
				"project_type": {"projectTypeKey", "project_type_key"},
				"lead":         {"lead.displayName", "lead.name", "lead"},
			},
		},
	})}, nil
}

func jiraDatasourceJQL(query string) string {
	query = strings.TrimSpace(query)
	if query == "" || looksLikeJQL(query) {
		return query
	}
	if looksLikeIssueKey(query) {
		return "issuekey = " + query + " OR text ~ " + quoteJQL(query)
	}
	return "text ~ " + quoteJQL(query)
}

func looksLikeJQL(query string) bool {
	upper := strings.ToUpper(query)
	return strings.Contains(query, "=") || strings.Contains(query, "~") || strings.Contains(upper, " ORDER BY ") || strings.Contains(upper, " AND ") || strings.Contains(upper, " OR ")
}

func looksLikeIssueKey(query string) bool {
	if query == "" {
		return false
	}
	dash := strings.IndexByte(query, '-')
	if dash <= 0 || dash == len(query)-1 {
		return false
	}
	for _, r := range query[:dash] {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	for _, r := range query[dash+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func quoteJQL(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

const IssueEntity coredatasource.EntityType = "jira.issue"
const ProjectEntity coredatasource.EntityType = "jira.project"

type Issue struct {
	ID          string `json:"id,omitempty" datasource:"filterable" jsonschema:"description=Jira issue id."`
	Key         string `json:"key" datasource:"id,searchable,filterable" jsonschema:"description=Jira issue key.,required"`
	Summary     string `json:"summary,omitempty" datasource:"searchable" jsonschema:"description=Issue summary."`
	Description string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Issue description."`
	Status      string `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Issue status."`
	Self        string `json:"self,omitempty" datasource:"url" jsonschema:"description=Jira API URL."`
}

type Project struct {
	ID             string `json:"id,omitempty" datasource:"filterable" jsonschema:"description=Jira project id."`
	Key            string `json:"key" datasource:"id,searchable,filterable" jsonschema:"description=Jira project key.,required"`
	Name           string `json:"name,omitempty" datasource:"searchable" jsonschema:"description=Project name."`
	Description    string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Project description."`
	ProjectTypeKey string `json:"projectTypeKey,omitempty" datasource:"filterable" jsonschema:"description=Jira project type."`
	Self           string `json:"self,omitempty" datasource:"url" jsonschema:"description=Jira API URL."`
}

func jiraActions() []connectorplugin.Action {
	return []connectorplugin.Action{{
		Kind:        Name,
		Operation:   "jira.issue.search",
		Suffix:      "issue_search",
		Description: "Search Jira issues using JQL.",
		Spec: func(name string) operation.Spec {
			return operationruntime.WithTypedContract[issueSearchInput, connectorplugin.Output](operation.Spec{
				Ref:         operation.Ref{Name: operation.Name(name)},
				Description: "Search Jira issues using JQL.",
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

type issueSearchInput struct {
	JQL        string `json:"jql" jsonschema:"description=Jira Query Language expression.,required"`
	StartAt    int    `json:"start_at,omitempty" jsonschema:"description=Zero-based result offset. Defaults to 0."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum issues to return. Defaults to 50."`
}
