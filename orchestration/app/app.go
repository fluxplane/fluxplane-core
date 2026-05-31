package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/appresources"
	"github.com/fluxplane/fluxplane-core/orchestration/eventregistry"
	"github.com/fluxplane/fluxplane-core/orchestration/identity"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/resourcecatalog"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-policy"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
)

// Config describes app composition input.
type Config struct {
	Context              context.Context
	Agent                agent.Agent
	Operations           []operation.Operation
	ContextProviders     []corecontext.Provider
	EventStore           event.Store
	DataStore            coredata.Store
	EventTypes           []event.Event
	Plugins              []pluginhost.Plugin
	Bundles              []resource.ContributionBundle
	BundleTransforms     []BundleTransform
	EnvironmentObservers []runtimeevidence.Observer
	AssertionDerivers    []runtimeevidence.AssertionDeriver
	ReactionRules        []corereaction.Rule
	OperationExecutor    operationruntime.Executor
	Security             policy.AuthorizationPolicy
	IdentityResolver     identity.Resolver
	ExternalIdentity     identity.ExternalResolver
	Discovery            *fpendpoint.DiscoveryRegistry
	Discoverer           *fpendpoint.Runner
	Endpoints            *fpendpoint.Registry
}

// BundleTransform mutates or augments resource bundles after plugin
// contributions have been resolved and before catalogs are collected.
type BundleTransform func([]resource.ContributionBundle) ([]resource.ContributionBundle, error)

// Composition is executable runtime configuration assembled from resources and
// provided implementations.
type Composition struct {
	Agent         agent.Agent
	ResourceIndex *resource.ResourceIndex
	Resolver      *resource.Resolver
	resourcecatalog.Catalogs
	ContextProviderImpls []corecontext.Provider
	SessionCommands      session.SessionCommandCatalog
	DatasourceProviders  []coredatasource.Provider
	EnvironmentObservers []runtimeevidence.Observer
	AssertionDerivers    []runtimeevidence.AssertionDeriver
	ReactionRules        []corereaction.Rule
	resourcecatalog.Specs
	appresources.Resources
	OperationExecutor operationruntime.Executor
	Security          policy.AuthorizationPolicy
	IdentityResolver  identity.Resolver
	ExternalIdentity  identity.ExternalResolver
	EventRegistry     *event.Registry
	EventStore        event.Store
	DataStore         coredata.Store
	Discovery         *fpendpoint.DiscoveryRegistry
	Discoverer        *fpendpoint.Runner
	Endpoints         *fpendpoint.Registry
	Secrets           *sharedsecret.Registry
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
	bundles, pluginOperations, pluginContextProviders, pluginSessionCommands, pluginDatasourceProviders, pluginObservers, pluginAssertionDerivers, pluginReactions, pluginExternalIdentities, discoveryRegistry, discoverer, endpointRegistry, secretRegistry, diagnostics, err := resolvePluginContributions(cfg.Context, cfg.Bundles, cfg.Plugins, cfg.EventStore, cfg.DataStore, cfg.Discovery, cfg.Discoverer, cfg.Endpoints)
	if err != nil {
		return Composition{Diagnostics: diagnostics}, err
	}
	for _, transform := range cfg.BundleTransforms {
		if transform == nil {
			continue
		}
		bundles, err = transform(bundles)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}
	for _, bundle := range bundles {
		diagnostics = append(diagnostics, bundle.Diagnostics...)
	}
	bundleReactions, reactionDiagnostic, err := collectBundleReactions(bundles)
	if err != nil {
		diagnostics = append(diagnostics, reactionDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	reactions := append(append([]reactionRuleBinding(nil), bundleReactions...), pluginReactions...)
	for _, rule := range cfg.ReactionRules {
		reactions = append(reactions, reactionRuleBinding{Rule: rule})
	}
	baselineObservers := []runtimeevidence.Observer{runtimeevidence.BaselineObserver()}
	environmentObservers := append([]runtimeevidence.Observer(nil), baselineObservers...)
	environmentObservers = append(environmentObservers, cfg.EnvironmentObservers...)
	environmentObservers = append(environmentObservers, pluginObservers...)
	environmentObservers = applyObserverOverrides(environmentObservers, observerOverrideSpecs(cfg.Bundles))
	configuredAssertionDerivers := runtimeevidence.TemplateAssertionDerivers(templateAssertionDeriverSpecs(bundles, pluginAssertionDerivers))
	assertionDerivers := append(configuredAssertionDerivers, pluginAssertionDerivers...)
	assertionDerivers = append(assertionDerivers, cfg.AssertionDerivers...)
	environmentDiagnostics := validateEnvironmentContributions(bundles, environmentObservers, assertionDerivers)
	diagnostics = append(diagnostics, environmentDiagnostics...)
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
	if deriver := newSkillTriggerAssertionDeriver(specs.SkillSpecs); deriver != nil {
		assertionDerivers = append(assertionDerivers, deriver)
	}
	reactions = append(reactions, skillTriggerReactionBindings(specs.SkillSpecs)...)
	reactionDiagnostics := validateReactionTargets(reactions, specs, appResources)
	diagnostics = append(diagnostics, reactionDiagnostics...)
	security := mergeSecurity(cfg.Security, bundles)
	identityResolver := cfg.IdentityResolver
	if identitySpec := mergeIdentity(bundles); len(identitySpec.Users) > 0 || len(identitySpec.Groups) > 0 || len(identitySpec.Rules) > 0 {
		resolver, err := identity.NewDirectoryResolver(identitySpec, identityResolver)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
		identityResolver = resolver
	}
	externalIdentity := cfg.ExternalIdentity
	if len(pluginExternalIdentities) > 0 {
		resolvers := make([]identity.ExternalResolver, 0, len(pluginExternalIdentities)+1)
		if externalIdentity != nil {
			resolvers = append(resolvers, externalIdentity)
		}
		resolvers = append(resolvers, pluginExternalIdentities...)
		externalIdentity = identity.ChainExternalResolver{Resolvers: resolvers}
	}

	return Composition{
		Agent:                cfg.Agent,
		ResourceIndex:        index,
		Resolver:             resolver,
		Catalogs:             catalogs,
		ContextProviderImpls: append(append([]corecontext.Provider(nil), cfg.ContextProviders...), pluginContextProviders...),
		SessionCommands:      pluginSessionCommands,
		DatasourceProviders:  pluginDatasourceProviders,
		EnvironmentObservers: environmentObservers,
		AssertionDerivers:    assertionDerivers,
		ReactionRules:        reactionRules(reactions),
		Specs:                specs,
		Resources:            appResources,
		OperationExecutor:    cfg.OperationExecutor,
		Security:             security,
		IdentityResolver:     identityResolver,
		ExternalIdentity:     externalIdentity,
		EventRegistry:        eventRegistry,
		EventStore:           cfg.EventStore,
		DataStore:            cfg.DataStore,
		Discovery:            discoveryRegistry,
		Discoverer:           discoverer,
		Endpoints:            endpointRegistry,
		Secrets:              secretRegistry,
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
			out.Rules = append(out.Rules, appSpec.Identity.Rules...)
		}
	}
	return out
}

func templateAssertionDeriverSpecs(bundles []resource.ContributionBundle, executable []runtimeevidence.AssertionDeriver) []coreevidence.AssertionDeriverSpec {
	executableNames := assertionDeriverNames(executable)
	var out []coreevidence.AssertionDeriverSpec
	for _, bundle := range bundles {
		for _, spec := range bundle.AssertionDerivers {
			if len(spec.Assertions) == 0 {
				continue
			}
			if executableNames[strings.TrimSpace(spec.Name)] {
				continue
			}
			out = append(out, spec)
		}
	}
	return out
}

type observerSpecOverride struct {
	observer runtimeevidence.Observer
	spec     coreevidence.ObserverSpec
}

func (o observerSpecOverride) Spec() coreevidence.ObserverSpec {
	return o.spec
}

func (o observerSpecOverride) Observe(ctx context.Context, req runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	return o.observer.Observe(ctx, req)
}

func observerOverrideSpecs(bundles []resource.ContributionBundle) map[string]coreevidence.ObserverSpec {
	out := map[string]coreevidence.ObserverSpec{}
	for _, bundle := range bundles {
		for _, spec := range bundle.Observers {
			name := strings.TrimSpace(spec.Name)
			if name == "" {
				continue
			}
			spec.Name = name
			out[name] = spec
		}
	}
	return out
}

func applyObserverOverrides(observers []runtimeevidence.Observer, overrides map[string]coreevidence.ObserverSpec) []runtimeevidence.Observer {
	if len(overrides) == 0 {
		return observers
	}
	out := make([]runtimeevidence.Observer, 0, len(observers))
	for _, observer := range observers {
		if observer == nil {
			out = append(out, observer)
			continue
		}
		base := observer.Spec()
		override, ok := overrides[strings.TrimSpace(base.Name)]
		if !ok {
			out = append(out, observer)
			continue
		}
		if override.Disabled {
			continue
		}
		out = append(out, observerSpecOverride{
			observer: observer,
			spec:     mergeObserverSpec(base, override),
		})
	}
	return out
}

func mergeObserverSpec(base, override coreevidence.ObserverSpec) coreevidence.ObserverSpec {
	out := base
	if name := strings.TrimSpace(override.Name); name != "" {
		out.Name = name
	}
	if override.Description != "" {
		out.Description = override.Description
	}
	if override.Environment.Name != "" {
		out.Environment = override.Environment
	}
	if override.Phase != "" {
		out.Phase = override.Phase
	}
	if override.ObservableKinds != nil {
		out.ObservableKinds = append([]string(nil), override.ObservableKinds...)
	}
	if override.Dynamic {
		out.Dynamic = true
	}
	if len(override.Annotations) > 0 {
		out.Annotations = mergeStringMap(out.Annotations, override.Annotations)
	}
	return out
}

func mergeStringMap(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func validateEnvironmentContributions(bundles []resource.ContributionBundle, observers []runtimeevidence.Observer, derivers []runtimeevidence.AssertionDeriver) []resource.Diagnostic {
	observerNames := map[string]bool{}
	for _, observer := range observers {
		if observer == nil {
			continue
		}
		if name := strings.TrimSpace(observer.Spec().Name); name != "" {
			observerNames[name] = true
		}
	}
	deriverNames := assertionDeriverNames(derivers)
	var diagnostics []resource.Diagnostic
	for _, bundle := range bundles {
		for _, spec := range bundle.Observers {
			name := strings.TrimSpace(spec.Name)
			if name == "" || spec.Disabled || observerNames[name] {
				continue
			}
			diagnostics = append(diagnostics, warningDiagnostic(bundle.Source, fmt.Sprintf("observer %q is configured but no enabled runtime or plugin provides an executable observer", name)))
		}
		for _, spec := range bundle.AssertionDerivers {
			name := strings.TrimSpace(spec.Name)
			if name == "" || deriverNames[name] {
				continue
			}
			diagnostics = append(diagnostics, warningDiagnostic(bundle.Source, fmt.Sprintf("assertion deriver %q is configured but no enabled runtime or plugin provides an executable assertion deriver", name)))
		}
	}
	return diagnostics
}

func assertionDeriverNames(derivers []runtimeevidence.AssertionDeriver) map[string]bool {
	names := map[string]bool{}
	for _, deriver := range derivers {
		if deriver == nil {
			continue
		}
		if name := strings.TrimSpace(deriver.Spec().Name); name != "" {
			names[name] = true
		}
	}
	return names
}

type reactionRuleBinding struct {
	Source resource.SourceRef
	Rule   corereaction.Rule
}

func collectBundleReactions(bundles []resource.ContributionBundle) ([]reactionRuleBinding, resource.Diagnostic, error) {
	var out []reactionRuleBinding
	for _, bundle := range bundles {
		for _, rule := range bundle.Reactions {
			if err := rule.Validate(); err != nil {
				return nil, diagnostic(bundle.Source, err), err
			}
			out = append(out, reactionRuleBinding{Source: bundle.Source, Rule: rule})
		}
	}
	return out, resource.Diagnostic{}, nil
}

func validateReactionTargets(bindings []reactionRuleBinding, specs resourcecatalog.Specs, resources appresources.Resources) []resource.Diagnostic {
	skills := map[string]bool{}
	references := map[string]map[string]bool{}
	for _, spec := range specs.SkillSpecs {
		name := strings.TrimSpace(string(spec.Name))
		if name == "" {
			continue
		}
		skills[name] = true
		if references[name] == nil {
			references[name] = map[string]bool{}
		}
		for _, ref := range spec.References {
			if path := strings.TrimSpace(ref.Path); path != "" {
				references[name][path] = true
			}
		}
	}
	operationSets := map[string]bool{}
	for _, spec := range specs.OperationSets {
		operationSets[strings.TrimSpace(spec.Name)] = true
	}
	activationSets := map[string]bool{}
	for _, spec := range specs.ActivationSets {
		name := strings.TrimSpace(spec.Name)
		if name != "" {
			activationSets[name] = true
		}
		for _, alias := range spec.Aliases {
			if alias = strings.TrimSpace(alias); alias != "" {
				activationSets[alias] = true
			}
		}
	}
	datasources := map[string]bool{}
	for _, spec := range specs.DatasourceSpecs {
		datasources[strings.TrimSpace(string(spec.Name))] = true
	}
	contextProviders := map[string]bool{}
	for _, spec := range specs.ContextSpecs {
		contextProviders[strings.TrimSpace(string(spec.Name))] = true
	}
	workflows := map[string]bool{}
	for _, spec := range specs.WorkflowSpecs {
		workflows[strings.TrimSpace(string(spec.Name))] = true
	}

	var diagnostics []resource.Diagnostic
	for _, binding := range bindings {
		for _, action := range binding.Rule.Actions {
			switch action.Kind {
			case corereaction.ActionActivateSkill:
				name := strings.TrimSpace(string(action.Skill.Name))
				if !skills[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q activates unknown skill %q", binding.Rule.Name, name)))
				}
			case corereaction.ActionActivateReference:
				name := strings.TrimSpace(string(action.Reference.Skill.Name))
				path := strings.TrimSpace(action.Reference.Path)
				if !skills[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q activates reference for unknown skill %q", binding.Rule.Name, name)))
					continue
				}
				if len(references[name]) > 0 && !references[name][path] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q activates unknown reference %q for skill %q", binding.Rule.Name, path, name)))
				}
			case corereaction.ActionEnableOperationSet:
				name := strings.TrimSpace(action.OperationSet)
				if !operationSets[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q enables unknown operation set %q", binding.Rule.Name, name)))
				}
			case corereaction.ActionEnableActivationSet:
				name := strings.TrimSpace(action.ActivationSet)
				if !activationSets[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q enables unknown activation set %q", binding.Rule.Name, name)))
				}
			case corereaction.ActionEnableDatasource:
				name := strings.TrimSpace(string(action.Datasource.Name))
				if !datasources[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q enables unknown datasource %q", binding.Rule.Name, name)))
				}
			case corereaction.ActionEnableContext:
				name := strings.TrimSpace(string(action.ContextProvider.Name))
				if !contextProviders[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q enables unknown context provider %q", binding.Rule.Name, name)))
				}
			case corereaction.ActionRunWorkflow:
				name := strings.TrimSpace(string(action.Workflow.Name))
				if !workflows[name] {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q runs unknown workflow %q", binding.Rule.Name, name)))
				}
			case corereaction.ActionRunOperation:
				if _, ok := resources.Operations.Resolve(action.Operation.Operation); !ok {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q runs unknown operation %q", binding.Rule.Name, action.Operation.Operation.Name)))
				}
			case corereaction.ActionRunCommand:
				if _, ok := resources.Commands.Resolve(action.Command.Path); !ok {
					diagnostics = append(diagnostics, reactionTargetDiagnostic(binding, fmt.Sprintf("reaction rule %q runs unknown command %q", binding.Rule.Name, action.Command.Path.String())))
				}
			}
		}
	}
	return diagnostics
}

func reactionTargetDiagnostic(binding reactionRuleBinding, message string) resource.Diagnostic {
	return warningDiagnostic(binding.Source, message)
}

func reactionRules(bindings []reactionRuleBinding) []corereaction.Rule {
	out := make([]corereaction.Rule, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, binding.Rule)
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

func resolvePluginContributions(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, eventStore event.Store, dataStore coredata.Store, discoveryRegistry *fpendpoint.DiscoveryRegistry, discoverer *fpendpoint.Runner, endpointRegistry *fpendpoint.Registry) ([]resource.ContributionBundle, []pluginhost.OperationContribution, []corecontext.Provider, session.SessionCommandCatalog, []coredatasource.Provider, []runtimeevidence.Observer, []runtimeevidence.AssertionDeriver, []reactionRuleBinding, []identity.ExternalResolver, *fpendpoint.DiscoveryRegistry, *fpendpoint.Runner, *fpendpoint.Registry, *sharedsecret.Registry, []resource.Diagnostic, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out := append([]resource.ContributionBundle(nil), bundles...)
	var operations []pluginhost.OperationContribution
	var contextProviders []corecontext.Provider
	sessionCommands := session.SessionCommandCatalog{}
	var datasourceProviders []coredatasource.Provider
	var observers []runtimeevidence.Observer
	var assertionDerivers []runtimeevidence.AssertionDeriver
	var reactions []reactionRuleBinding
	var externalIdentities []identity.ExternalResolver
	var diagnostics []resource.Diagnostic
	host, err := pluginhost.New(plugins...)
	if err != nil {
		diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
		return out, operations, contextProviders, sessionCommands, datasourceProviders, observers, assertionDerivers, reactions, externalIdentities, nil, nil, nil, nil, diagnostics, err
	}
	if discoveryRegistry == nil {
		discoveryRegistry = fpendpoint.NewDiscoveryRegistry()
	}
	if endpointRegistry == nil {
		endpointRegistry = fpendpoint.NewRegistry(0)
	}
	if discoverer == nil {
		discoverer = fpendpoint.NewRunner(discoveryRegistry, endpointRegistry)
	}
	secretRegistry := sharedsecret.NewRegistry()
	host.SetEventStore(eventStore)
	host.SetDataStore(dataStore)
	host.SetDiscoveryRegistry(discoveryRegistry)
	host.SetEndpointRegistry(endpointRegistry)
	host.SetDiscoveryRunner(discoverer)
	host.SetSecretRegistry(secretRegistry)

	type pluginResolution struct {
		bundle      resource.ContributionBundle
		contributed pluginhost.Resolution
		err         error
	}
	results := make([]pluginResolution, len(bundles))
	resolveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i, bundle := range bundles {
		results[i].bundle = bundle
		if len(bundle.Plugins) == 0 {
			continue
		}
		wg.Add(1)
		go func(i int, bundle resource.ContributionBundle) {
			defer wg.Done()
			contributed, err := host.Resolve(resolveCtx, bundle.Plugins...)
			if err != nil {
				cancel()
			}
			results[i].contributed = contributed
			results[i].err = err
		}(i, bundle)
	}
	wg.Wait()

	for _, result := range results {
		if len(result.bundle.Plugins) == 0 {
			continue
		}
		if result.err != nil {
			diagnostics = append(diagnostics, diagnostic(result.bundle.Source, result.err))
			return out, operations, contextProviders, sessionCommands, datasourceProviders, observers, assertionDerivers, reactions, externalIdentities, discoveryRegistry, discoverer, endpointRegistry, secretRegistry, diagnostics, result.err
		}
		contributed := result.contributed
		out = append(out, contributed.Bundles...)
		operations = append(operations, contributed.Operations...)
		for _, provider := range contributed.ContextProviders {
			contextProviders = append(contextProviders, provider.Provider)
		}
		for _, command := range contributed.SessionCommands {
			key := command.Binding.Spec.Path.String()
			if key == "" {
				continue
			}
			if _, exists := sessionCommands[key]; exists {
				err := fmt.Errorf("duplicate session command handler %q", key)
				diagnostics = append(diagnostics, diagnostic(command.Source, err))
				return out, operations, contextProviders, sessionCommands, datasourceProviders, observers, assertionDerivers, reactions, externalIdentities, discoveryRegistry, discoverer, endpointRegistry, secretRegistry, diagnostics, err
			}
			sessionCommands[key] = command.Binding
		}
		for _, provider := range contributed.DatasourceProviders {
			datasourceProviders = append(datasourceProviders, provider.Provider)
		}
		for _, provider := range contributed.DiscoveryProviders {
			if provider.Provider == nil {
				continue
			}
			if err := discoveryRegistry.Register(provider.Provider); err != nil {
				diagnostics = append(diagnostics, diagnostic(provider.Source, err))
				return out, operations, contextProviders, sessionCommands, datasourceProviders, observers, assertionDerivers, reactions, externalIdentities, discoveryRegistry, discoverer, endpointRegistry, secretRegistry, diagnostics, err
			}
		}
		for _, observer := range contributed.Observers {
			observers = append(observers, observer.Observer)
		}
		for _, deriver := range contributed.AssertionDerivers {
			assertionDerivers = append(assertionDerivers, deriver.Deriver)
		}
		for _, reaction := range contributed.Reactions {
			reactions = append(reactions, reactionRuleBinding{Source: reaction.Source, Rule: reaction.Rule})
		}
		for _, resolver := range contributed.ExternalIdentities {
			externalIdentities = append(externalIdentities, resolver.Resolver)
		}
	}
	return out, operations, contextProviders, sessionCommands, datasourceProviders, observers, assertionDerivers, reactions, externalIdentities, discoveryRegistry, discoverer, endpointRegistry, secretRegistry, diagnostics, nil
}

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{
		Severity: resource.SeverityError,
		Source:   source,
		Message:  err.Error(),
	}
}

func warningDiagnostic(source resource.SourceRef, message string) resource.Diagnostic {
	return resource.Diagnostic{
		Severity: resource.SeverityWarning,
		Source:   source,
		Message:  message,
	}
}
