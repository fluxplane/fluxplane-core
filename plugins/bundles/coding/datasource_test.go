package coding

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/web"
	system "github.com/fluxplane/fluxplane-core/runtime/workspace"
)

func TestCodingPluginForwardsWebDatasourceProviders(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	providers, err := New(sys).DatasourceProviders(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	for _, provider := range providers {
		for _, entity := range provider.Entities() {
			if entity.Type == web.SearchResultEntity {
				return
			}
		}
	}
	t.Fatalf("web search datasource provider not found in %#v", providers)
}

func zeroPluginContext() pluginhost.Context { return pluginhost.Context{} }
