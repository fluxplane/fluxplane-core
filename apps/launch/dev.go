package launch

import (
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/sessionhistoryplugin"
)

func enableDevSessionHistory(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	if len(bundles) == 0 {
		bundles = append(bundles, resource.ContributionBundle{})
	}
	if !hasDatasource(bundles, sessionhistoryplugin.DatasourceName) {
		bundles[0].Datasources = append(bundles[0].Datasources, sessionhistoryplugin.DatasourceSpec())
	}
	for bundleIndex := range bundles {
		for agentIndex := range bundles[bundleIndex].Agents {
			agent := &bundles[bundleIndex].Agents[agentIndex]
			appendOperationRef(&agent.Operations, datasourceplugin.SearchOperation)
			appendOperationRef(&agent.Operations, datasourceplugin.GetOperation)
			appendOperationRef(&agent.Operations, datasourceplugin.BatchGetOperation)
			appendDatasourceRef(&agent.Datasources, sessionhistoryplugin.DatasourceName)
			appendContextRef(&agent.Context, datasourceplugin.ContextProvider)
		}
	}
	return bundles
}

func ensureDevSessionHistoryPlugin(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	if len(bundles) == 0 {
		bundles = append(bundles, resource.ContributionBundle{})
	}
	ensurePluginRef(bundles, sessionhistoryplugin.Name)
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
