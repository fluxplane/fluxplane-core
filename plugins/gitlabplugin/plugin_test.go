package gitlabplugin

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
)

func TestPluginContributesGitLabConnectorProvider(t *testing.T) {
	providers, err := New(nil, nil).ConnectorProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("ConnectorProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "gitlab" {
		t.Fatalf("providers = %#v, want gitlab", providers)
	}
}

func TestPluginMaterializesProjectSearch(t *testing.T) {
	plugin := New(nil, []connectorplugin.Instance{{ID: "gitlab-prod", Kind: "gitlab"}})
	bundle, err := plugin.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(bundle.Operations))
	}
	if got := string(bundle.Operations[0].Ref.Name); got != "gitlab_prod_project_search" {
		t.Fatalf("operation name = %q, want gitlab_prod_project_search", got)
	}
}
