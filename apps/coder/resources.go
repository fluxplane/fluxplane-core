package coder

import (
	"context"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/core/resource"
)

type startupResources struct {
	Root        string
	Bundles     []resource.ContributionBundle
	Diagnostics []resource.Diagnostic
}

func loadStartupResources(ctx context.Context) startupResources {
	base := []resource.ContributionBundle{Bundle()}
	loaded, err := distlocal.LoadRequestedResources(ctx, ".", base)
	if err != nil {
		return startupResources{
			Bundles:     base,
			Diagnostics: []resource.Diagnostic{{Severity: resource.SeverityError, Message: err.Error()}},
		}
	}
	return startupResources{
		Root:        loaded.Root,
		Bundles:     loaded.Bundles,
		Diagnostics: loaded.Diagnostics,
	}
}

func cloneContributionBundles(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	if len(bundles) == 0 {
		return nil
	}
	out := make([]resource.ContributionBundle, len(bundles))
	copy(out, bundles)
	return out
}
