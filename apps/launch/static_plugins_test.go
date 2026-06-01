package launch

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/resource"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

func TestStaticPluginViewIncludesConfigSchemaContributionsWhenRequested(t *testing.T) {
	result := StaticPluginView(context.Background(), StaticPluginOptions{
		Bundles: []resource.ContributionBundle{{
			Datasources: []coredatasource.Spec{{Name: "slack-bot", Kind: "slack"}},
		}},
		IncludeConfigSchemaContributions: true,
	})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", result.Diagnostics)
	}
	if !staticPluginViewHasDatasource(result, "datasource") {
		t.Fatalf("static plugin view missing schema-only datasource catalog")
	}
}

func staticPluginViewHasDatasource(result StaticPluginResult, name coredatasource.Name) bool {
	for _, bundle := range result.Bundles {
		for _, spec := range bundle.Datasources {
			if spec.Name == name {
				return true
			}
		}
	}
	return false
}
