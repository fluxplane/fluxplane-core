package launch

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/resourceview"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
)

func TestBundlesWithStaticPluginContributionsUsesNativeSlackAndDatasourcePlugin(t *testing.T) {
	result := StaticPluginView(context.Background(), StaticPluginOptions{
		Bundles: []resource.ContributionBundle{{
			Source: resource.SourceRef{ID: "app", Scope: resource.ScopeProject, Location: "agentsdk.app.yaml"},
			Datasources: []coredatasource.Spec{{
				Name: "slack-bot",
				Kind: "slack",
			}},
			Plugins: []resource.PluginRef{{Name: slackplugin.Name}},
		}},
		Launch: distribution.LaunchConfig{},
	})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", result.Diagnostics)
	}
	if !result.ImplicitPlugins["datasource"] {
		t.Fatalf("implicit plugins = %#v, want datasource", result.ImplicitPlugins)
	}
	var out bytes.Buffer
	if err := resourceview.RenderTreeWithOptions(&out, result.Bundles, nil, resourceview.TreeOptions{ImplicitPlugins: result.ImplicitPlugins}); err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"plugins",
		"slack",
		"datasource (implicit)",
		"Plugin contributions:",
		"operations",
		"channel_send",
		"context_providers",
		"datasource.catalog",
		"datasource.detected",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "contributes:") {
		t.Fatalf("tree output contains nested contribution summary:\n%s", text)
	}
}
