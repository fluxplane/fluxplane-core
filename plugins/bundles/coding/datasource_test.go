package coding

import (
	"context"
	"testing"

	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/plugins/integrations/web"
	"github.com/fluxplane/engine/runtime/system"
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
