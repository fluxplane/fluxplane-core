package openai

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
)

func TestPluginContributesOpenAIConnectorProvider(t *testing.T) {
	providers, err := New().ConnectorProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("ConnectorProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "openai" {
		t.Fatalf("providers = %#v, want openai", providers)
	}
}
