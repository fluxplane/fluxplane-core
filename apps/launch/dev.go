package launch

import (
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/plugins/native/datasource"
	"github.com/fluxplane/fluxplane-core/plugins/native/sessionhistory"
	usageplugin "github.com/fluxplane/fluxplane-core/plugins/native/usage"
)

func enableDevSessionHistory(bundles []resource.ContributionBundle) ([]resource.ContributionBundle, error) {
	if len(bundles) == 0 {
		bundles = append(bundles, resource.ContributionBundle{})
	}
	if !hasDatasource(bundles, sessionhistory.DatasourceName) {
		bundles[0].Datasources = append(bundles[0].Datasources, sessionhistory.DatasourceSpec())
	}
	for bundleIndex := range bundles {
		for agentIndex := range bundles[bundleIndex].Agents {
			agent := &bundles[bundleIndex].Agents[agentIndex]
			appendOperationRef(&agent.Operations, datasource.SearchOperation)
			appendOperationRef(&agent.Operations, datasource.GetOperation)
			appendOperationRef(&agent.Operations, datasource.BatchGetOperation)
			appendDatasourceRef(&agent.Datasources, sessionhistory.DatasourceName)
			appendContextRef(&agent.Context, datasource.ContextProvider)
		}
	}
	return bundles, nil
}

func ensureDevSessionHistoryPlugin(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	if len(bundles) == 0 {
		bundles = append(bundles, resource.ContributionBundle{})
	}
	ensurePluginRef(bundles, sessionhistory.Name)
	return bundles
}

func enableDevUsageDatasource(bundles []resource.ContributionBundle) ([]resource.ContributionBundle, error) {
	if len(bundles) == 0 {
		bundles = append(bundles, resource.ContributionBundle{})
	}
	if !hasDatasource(bundles, usageplugin.DatasourceName) {
		bundles[0].Datasources = append(bundles[0].Datasources, usageplugin.DatasourceSpec())
	}
	for bundleIndex := range bundles {
		for agentIndex := range bundles[bundleIndex].Agents {
			appendDatasourceRef(&bundles[bundleIndex].Agents[agentIndex].Datasources, usageplugin.DatasourceName)
		}
	}
	return bundles, nil
}

func ensureDevUsagePlugin(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	if len(bundles) == 0 {
		bundles = append(bundles, resource.ContributionBundle{})
	}
	ensurePluginRef(bundles, usageplugin.Name)
	return bundles
}

func appendOperationRef(refs *[]operation.Ref, name string) {
	for _, ref := range *refs {
		if ref.Name == operation.Name(name) {
			return
		}
	}
	*refs = append(*refs, operation.Ref{Name: operation.Name(name)})
}

func appendDatasourceRef(refs *[]coredatasource.Ref, name coredatasource.Name) {
	for _, ref := range *refs {
		if ref.Name == name {
			return
		}
	}
	*refs = append(*refs, coredatasource.Ref{Name: name})
}

func appendContextRef(refs *[]corecontext.ProviderRef, name string) {
	provider := corecontext.ProviderName(name)
	for _, ref := range *refs {
		if ref.Name == provider {
			return
		}
	}
	*refs = append(*refs, corecontext.ProviderRef{Name: provider})
}
