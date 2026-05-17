package app

import (
	"context"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/appresources"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/resourcecatalog"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Config describes app composition input.
type Config struct {
	Context           context.Context
	Agent             agent.Agent
	Operations        []operation.Operation
	ContextProviders  []corecontext.Provider
	EventStore        event.Store
	EventTypes        []event.Event
	Plugins           []pluginhost.Plugin
	Bundles           []resource.ContributionBundle
	BundleTransforms  []BundleTransform
	OperationExecutor operationruntime.Executor
	Security          policy.AuthorizationPolicy
	IdentityResolver  identity.Resolver
}

// BundleTransform mutates or augments resource bundles after plugin
// contributions have been resolved and before catalogs are collected.
type BundleTransform func([]resource.ContributionBundle) []resource.ContributionBundle

// Composition is executable runtime configuration assembled from resources and
// provided implementations.
type Composition struct {
	Agent         agent.Agent
	ResourceIndex *resource.ResourceIndex
	Resolver      *resource.Resolver
	resourcecatalog.Catalogs
	ContextProviderImpls []corecontext.Provider
	DatasourceProviders  []coredatasource.Provider
	resourcecatalog.Specs
	appresources.Resources
	OperationExecutor operationruntime.Executor
	Security          policy.AuthorizationPolicy
	IdentityResolver  identity.Resolver
	EventRegistry     *event.Registry
	EventStore        event.Store
	Bundles           []resource.ContributionBundle
	Diagnostics       []resource.Diagnostic
}

// Compose validates and registers resource contributions with supplied and
// plugin-contributed runtime implementations. Resource operation specs are
// declarations; executable operation implementations come from host or plugin
// code.
func Compose(cfg Config) (Composition, error) {
	if cfg.EventStore == nil {
		cfg.EventStore = eventstore.NewMemoryStore()
	}
	bundles, pluginOperations, pluginContextProviders, pluginDatasourceProviders, diagnostics, err := resolvePluginContributions(cfg.Context, cfg.Bundles, cfg.Plugins, cfg.EventStore)
	if err != nil {
		return Composition{Diagnostics: diagnostics}, err
	}
	for _, transform := range cfg.BundleTransforms {
		if transform == nil {
			continue
		}
		bundles = transform(bundles)
	}
	for _, bundle := range bundles {
		diagnostics = append(diagnostics, bundle.Diagnostics...)
	}
	eventRegistry, err := eventregistry.New(eventregistry.Config{EventTypes: appendEventTypesFromBundles(cfg.EventTypes, bundles)})
	if err != nil {
		diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
		return Composition{Diagnostics: diagnostics}, err
	}

	index := resource.NewResourceIndex()
	resolver := resource.NewResolver(resource.ResolverConfig{Index: index})

	catalogs, specs, catalogDiagnostic, err := resourcecatalog.Collect(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, catalogDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	pluginOperationResources := make([]appresources.OperationContribution, 0, len(pluginOperations))
	for _, op := range pluginOperations {
		pluginOperationResources = append(pluginOperationResources, appresources.OperationContribution{
			Source:    op.Source,
			Operation: op.Operation,
		})
	}
	appResources, resourceDiagnostic, err := appresources.Collect(appresources.Config{
		Bundles:          bundles,
		Operations:       cfg.Operations,
		PluginOperations: pluginOperationResources,
		AppCatalog:       catalogs.AppCatalog,
		Resolver:         resolver,
		Index:            index,
	})
	if err != nil {
		diagnostics = append(diagnostics, resourceDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	security := mergeSecurity(cfg.Security, bundles)
	identityResolver := cfg.IdentityResolver
	if identitySpec := mergeIdentity(bundles); len(identitySpec.Users) > 0 || len(identitySpec.Groups) > 0 {
		resolver, err := identity.NewDirectoryResolver(identitySpec, identityResolver)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
		identityResolver = resolver
	}

	return Composition{
		Agent:                cfg.Agent,
		ResourceIndex:        index,
		Resolver:             resolver,
		Catalogs:             catalogs,
		ContextProviderImpls: append(append([]corecontext.Provider(nil), cfg.ContextProviders...), pluginContextProviders...),
		DatasourceProviders:  pluginDatasourceProviders,
		Specs:                specs,
		Resources:            appResources,
		OperationExecutor:    cfg.OperationExecutor,
		Security:             security,
		IdentityResolver:     identityResolver,
		EventRegistry:        eventRegistry,
		EventStore:           cfg.EventStore,
		Bundles:              bundles,
		Diagnostics:          diagnostics,
	}, nil
}

func mergeIdentity(bundles []resource.ContributionBundle) coreapp.IdentitySpec {
	var out coreapp.IdentitySpec
	for _, bundle := range bundles {
		for _, appSpec := range bundle.Apps {
			out.Users = append(out.Users, appSpec.Identity.Users...)
			out.Groups = append(out.Groups, appSpec.Identity.Groups...)
		}
	}
	return out
}

func mergeSecurity(base policy.AuthorizationPolicy, bundles []resource.ContributionBundle) policy.AuthorizationPolicy {
	out := policy.AuthorizationPolicy{Grants: append([]policy.Grant(nil), base.Grants...)}
	for _, bundle := range bundles {
		for _, appSpec := range bundle.Apps {
			out.Grants = append(out.Grants, appSpec.Security.Grants...)
		}
	}
	return out
}

func appendEventTypesFromBundles(base []event.Event, bundles []resource.ContributionBundle) []event.Event {
	out := append([]event.Event(nil), base...)
	for _, bundle := range bundles {
		out = append(out, bundle.EventTypes...)
	}
	return out
}

func resolvePluginContributions(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, eventStore event.Store) ([]resource.ContributionBundle, []pluginhost.OperationContribution, []corecontext.Provider, []coredatasource.Provider, []resource.Diagnostic, error) {
	out := append([]resource.ContributionBundle(nil), bundles...)
	var operations []pluginhost.OperationContribution
	var contextProviders []corecontext.Provider
	var datasourceProviders []coredatasource.Provider
	var diagnostics []resource.Diagnostic
	host, err := pluginhost.New(plugins...)
	if err != nil {
		diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
		return out, operations, contextProviders, datasourceProviders, diagnostics, err
	}
	host.SetEventStore(eventStore)
	for _, bundle := range bundles {
		if len(bundle.Plugins) == 0 {
			continue
		}
		contributed, err := host.Resolve(ctx, bundle.Plugins...)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
			return out, operations, contextProviders, datasourceProviders, diagnostics, err
		}
		out = append(out, contributed.Bundles...)
		operations = append(operations, contributed.Operations...)
		for _, provider := range contributed.ContextProviders {
			contextProviders = append(contextProviders, provider.Provider)
		}
		for _, provider := range contributed.DatasourceProviders {
			datasourceProviders = append(datasourceProviders, provider.Provider)
		}
	}
	return out, operations, contextProviders, datasourceProviders, diagnostics, nil
}

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{
		Severity: resource.SeverityError,
		Source:   source,
		Message:  err.Error(),
	}
}
