package jiraplugin

import (
	"context"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
)

func TestPluginContributesJiraConnectorProvider(t *testing.T) {
	providers, err := New(nil, nil).ConnectorProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("ConnectorProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "jira" {
		t.Fatalf("providers = %#v, want jira", providers)
	}
}

func TestPluginContributesJiraDatasourceEntities(t *testing.T) {
	providers, err := New(nil, nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
	got := map[coredatasource.EntityType]bool{}
	for _, entity := range providers[0].Entities() {
		got[entity.Type] = true
	}
	for _, want := range []coredatasource.EntityType{IssueEntity, ProjectEntity} {
		if !got[want] {
			t.Fatalf("entities = %#v, missing %s", got, want)
		}
	}
}

func TestPluginContributesJiraIssueDetectors(t *testing.T) {
	providers, err := New(nil, nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	var issue coredatasource.EntitySpec
	for _, entity := range providers[0].Entities() {
		if entity.Type == IssueEntity {
			issue = entity
		}
	}
	if len(issue.Detectors) != 2 {
		t.Fatalf("detectors = %#v, want key and url detectors", issue.Detectors)
	}
	if issue.Detectors[0].Kind != coredatasource.DetectorRegex || issue.Detectors[0].IDTemplate == "" {
		t.Fatalf("detector = %#v, want generic regex detector with id template", issue.Detectors[0])
	}
}

func TestPluginMaterializesIssueSearch(t *testing.T) {
	plugin := New(nil, []connectorplugin.Instance{{ID: "jira-prod", Kind: "jira"}})
	bundle, err := plugin.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(bundle.Operations))
	}
	if got := string(bundle.Operations[0].Ref.Name); got != "jira_prod_issue_search" {
		t.Fatalf("operation name = %q, want jira_prod_issue_search", got)
	}
}

func TestJiraDatasourceJQLBuildsUsefulDefaultQueries(t *testing.T) {
	tests := map[string]string{
		"DEV-381":             `issuekey = DEV-381 OR text ~ "DEV-381"`,
		"lyse":                `text ~ "lyse"`,
		`project = DEV`:       `project = DEV`,
		`summary ~ "billing"`: `summary ~ "billing"`,
		`lyse "quoted" value`: `text ~ "lyse \"quoted\" value"`,
	}
	for input, want := range tests {
		if got := jiraDatasourceJQL(input); got != want {
			t.Fatalf("jiraDatasourceJQL(%q) = %q, want %q", input, got, want)
		}
	}
}
