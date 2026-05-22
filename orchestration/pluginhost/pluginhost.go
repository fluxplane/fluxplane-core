package pluginhost

import (
	"context"
	"fmt"

	corecontext "github.com/fluxplane/engine/core/context"
	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	corereaction "github.com/fluxplane/engine/core/reaction"
	"github.com/fluxplane/engine/core/resource"
	coresecret "github.com/fluxplane/engine/core/secret"
	"github.com/fluxplane/engine/orchestration/channelruntime"
	"github.com/fluxplane/engine/orchestration/identity"
	runtimediscovery "github.com/fluxplane/engine/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/engine/runtime/endpoint"
	runtimeevidence "github.com/fluxplane/engine/runtime/evidence"
	runtimesecret "github.com/fluxplane/engine/runtime/secret"
)

// Manifest describes one plugin implementation.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// Context is passed to a plugin when resolving contributions.
type Context struct {
	Ref        resource.PluginRef         `json:"ref"`
	Config     any                        `json:"-"`
	EventStore event.Store                `json:"-"`
	DataStore  coredata.Store             `json:"-"`
	Discovery  *runtimediscovery.Registry `json:"-"`
	Discoverer *runtimediscovery.Runner   `json:"-"`
	Endpoints  *runtimeendpoint.Registry  `json:"-"`
	Secrets    runtimesecret.Resolver     `json:"-"`
}

// Plugin contributes resources during app composition.
type Plugin interface {
	Manifest() Manifest
	Contributions(context.Context, Context) (resource.ContributionBundle, error)
}

// InstanceFactory is implemented by plugin registrations that materialize a
// concrete plugin for each named ref before contributions are collected.
type InstanceFactory interface {
	Instantiate(context.Context, Context) (Plugin, error)
}

// OperationContributor is implemented by plugins that provide executable
// operation implementations in addition to pure resource contributions.
type OperationContributor interface {
	Operations(context.Context, Context) ([]operation.Operation, error)
}

// ContextProviderContributor is implemented by plugins that provide executable
// context providers in addition to pure context provider specs.
type ContextProviderContributor interface {
	ContextProviders(context.Context, Context) ([]corecontext.Provider, error)
}

// ObserverContributor is implemented by plugins that provide executable
// environment observers in addition to inert observer specs.
type ObserverContributor interface {
	EnvironmentObservers(context.Context, Context) ([]runtimeevidence.Observer, error)
}

// AssertionDeriverContributor is implemented by plugins that derive normalized
// evidence assertions from observations.
type AssertionDeriverContributor interface {
	AssertionDerivers(context.Context, Context) ([]runtimeevidence.AssertionDeriver, error)
}

// ReactionContributor is implemented by plugins that contribute instance-aware
// default reaction rules.
type ReactionContributor interface {
	Reactions(context.Context, Context) ([]corereaction.Rule, error)
}

// ChannelContributor is implemented by plugins that can provide long-running
// runtime channels for daemon mode.
type ChannelContributor interface {
	Channels(context.Context, Context) ([]ChannelContribution, error)
}

// DatasourceProviderContributor is implemented by plugins that make
// datasource entity accessors available to host-level app composition.
type DatasourceProviderContributor interface {
	DatasourceProviders(context.Context, Context) ([]coredatasource.Provider, error)
}

// DiscoveryProviderContributor is implemented by plugins that make endpoint
// candidate discovery providers available to other plugins.
type DiscoveryProviderContributor interface {
	DiscoveryProviders(context.Context, Context) ([]runtimediscovery.Provider, error)
}

// SecretResolverContributor is implemented by plugins that can resolve
// non-model-visible credential material for trusted operation code.
type SecretResolverContributor interface {
	SecretResolvers(context.Context, Context) ([]runtimesecret.Resolver, error)
}

// AuthMethodContributor is implemented by plugins that declare supported
// authentication methods without carrying credential values.
type AuthMethodContributor interface {
	AuthMethods(context.Context, Context) ([]coresecret.AuthMethodSpec, error)
}

// AuthTestContributor is implemented by plugins that can perform a live
// credential connectivity check for host-level auth test commands.
type AuthTestContributor interface {
	TestConnection(context.Context, Context, AuthTestRequest, chan<- AuthTestReport) error
}

// ExternalIdentityContributor is implemented by plugins that can link a
// resolved canonical user to provider-specific account identities.
type ExternalIdentityContributor interface {
	ExternalIdentityResolvers(context.Context, Context) ([]identity.ExternalResolver, error)
}

// OperationContribution is one executable operation contributed by a plugin.
type OperationContribution struct {
	Source    resource.SourceRef
	Operation operation.Operation
}

// ChannelContribution is one runtime channel contributed by a plugin.
type ChannelContribution struct {
	Source  resource.SourceRef
	Channel channelruntime.Channel
}

// DatasourceProviderContribution is one datasource provider contribution.
type DatasourceProviderContribution struct {
	Source   resource.SourceRef
	Provider coredatasource.Provider
}

// DiscoveryProviderContribution is one endpoint discovery provider.
type DiscoveryProviderContribution struct {
	Source   resource.SourceRef
	Provider runtimediscovery.Provider
}

// SecretResolverContribution is one credential resolver contribution.
type SecretResolverContribution struct {
	Source   resource.SourceRef
	Resolver runtimesecret.Resolver
}

// ContextProviderContribution is one executable context provider contribution.
type ContextProviderContribution struct {
	Source   resource.SourceRef
	Provider corecontext.Provider
}

// EnvironmentObserverContribution is one executable observer contributed by a
// plugin instance.
type EnvironmentObserverContribution struct {
	Source   resource.SourceRef
	Observer runtimeevidence.Observer
}

// AssertionDeriverContribution is one executable assertion deriver contributed by a
// plugin instance.
type AssertionDeriverContribution struct {
	Source  resource.SourceRef
	Deriver runtimeevidence.AssertionDeriver
}

// ReactionContribution is one reaction rule contributed by a plugin instance.
type ReactionContribution struct {
	Source resource.SourceRef
	Rule   corereaction.Rule
}

// AuthMethodContribution is one plugin auth method declaration.
type AuthMethodContribution struct {
	Source resource.SourceRef
	Method coresecret.AuthMethodSpec
}

// AuthTestRequest describes one plugin instance auth test.
type AuthTestRequest struct {
	Ref     resource.PluginRef
	Method  string
	Secrets runtimesecret.Resolver
}

// AuthTestReport describes the outcome of one live credential check.
type AuthTestReport struct {
	Plugin   string            `json:"plugin"`
	Instance string            `json:"instance,omitempty"`
	Method   string            `json:"method,omitempty"`
	Check    string            `json:"check,omitempty"`
	Status   string            `json:"status"`
	Message  string            `json:"message,omitempty"`
	Details  map[string]string `json:"details,omitempty"`
}

// ExternalIdentityContribution is one external identity resolver contributed by
// a plugin instance.
type ExternalIdentityContribution struct {
	Source   resource.SourceRef
	Resolver identity.ExternalResolver
}

// Resolution is the complete contribution set resolved for plugin refs.
type Resolution struct {
	Bundles             []resource.ContributionBundle
	Operations          []OperationContribution
	ContextProviders    []ContextProviderContribution
	Observers           []EnvironmentObserverContribution
	AssertionDerivers   []AssertionDeriverContribution
	Reactions           []ReactionContribution
	Channels            []ChannelContribution
	DatasourceProviders []DatasourceProviderContribution
	DiscoveryProviders  []DiscoveryProviderContribution
	SecretResolvers     []SecretResolverContribution
	AuthMethods         []AuthMethodContribution
	ExternalIdentities  []ExternalIdentityContribution
}

// Host resolves plugin refs through registered plugin implementations.
type Host struct {
	plugins    map[string]Plugin
	eventStore event.Store
	dataStore  coredata.Store
	discovery  *runtimediscovery.Registry
	discoverer *runtimediscovery.Runner
	endpoints  *runtimeendpoint.Registry
	secrets    *runtimesecret.Registry
}

// New returns a plugin host.
func New(plugins ...Plugin) (*Host, error) {
	host := &Host{plugins: map[string]Plugin{}}
	for _, plugin := range plugins {
		if err := host.Register(plugin); err != nil {
			return nil, err
		}
	}
	return host, nil
}

// SetEventStore configures the event store passed to plugin contexts.
func (h *Host) SetEventStore(store event.Store) {
	if h != nil {
		h.eventStore = store
	}
}

// SetDataStore configures the data store passed to plugin contexts.
func (h *Host) SetDataStore(store coredata.Store) {
	if h != nil {
		h.dataStore = store
	}
}

// SetDiscoveryRegistry configures the shared endpoint discovery registry.
func (h *Host) SetDiscoveryRegistry(registry *runtimediscovery.Registry) {
	if h != nil {
		h.discovery = registry
	}
}

// SetDiscoveryRunner configures the shared endpoint discovery runner.
func (h *Host) SetDiscoveryRunner(runner *runtimediscovery.Runner) {
	if h != nil {
		h.discoverer = runner
	}
}

// SetEndpointRegistry configures the shared endpoint registry.
func (h *Host) SetEndpointRegistry(registry *runtimeendpoint.Registry) {
	if h != nil {
		h.endpoints = registry
	}
}

// SetSecretRegistry configures the shared secret resolver registry.
func (h *Host) SetSecretRegistry(registry *runtimesecret.Registry) {
	if h != nil {
		h.secrets = registry
	}
}

// Register adds a plugin implementation.
func (h *Host) Register(plugin Plugin) error {
	if h == nil {
		return fmt.Errorf("pluginhost: host is nil")
	}
	if plugin == nil {
		return fmt.Errorf("pluginhost: plugin is nil")
	}
	manifest := plugin.Manifest()
	if manifest.Name == "" {
		return fmt.Errorf("pluginhost: plugin name is empty")
	}
	if h.plugins == nil {
		h.plugins = map[string]Plugin{}
	}
	if _, exists := h.plugins[manifest.Name]; exists {
		return fmt.Errorf("pluginhost: plugin %q already registered", manifest.Name)
	}
	h.plugins[manifest.Name] = plugin
	return nil
}

// Resolve returns resource bundles and executable implementations contributed
// by refs in order.
func (h *Host) Resolve(ctx context.Context, refs ...resource.PluginRef) (Resolution, error) {
	if h == nil {
		return Resolution{}, fmt.Errorf("pluginhost: host is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.ensureSharedRegistries()
	var out Resolution
	for _, ref := range refs {
		if ref.Name == "" {
			return Resolution{}, fmt.Errorf("pluginhost: plugin ref name is empty")
		}
		plugin, ok := h.plugins[ref.Name]
		if !ok {
			return Resolution{}, fmt.Errorf("pluginhost: plugin %q is not registered", pluginLabel(ref))
		}
		pluginCtx := Context{Ref: ref, EventStore: h.eventStore, DataStore: h.dataStore, Discovery: h.discovery, Discoverer: h.discoverer, Endpoints: h.endpoints, Secrets: h.secrets}
		pluginCtx, err := PrepareContext(ctx, plugin, pluginCtx)
		if err != nil {
			return Resolution{}, fmt.Errorf("pluginhost: %w", err)
		}
		resolvedPlugin := plugin
		if factory, ok := plugin.(InstanceFactory); ok {
			var err error
			resolvedPlugin, err = factory.Instantiate(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q instantiate: %w", pluginLabel(ref), err)
			}
			if resolvedPlugin == nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q instantiate returned nil", pluginLabel(ref))
			}
		}
		bundle, err := resolvedPlugin.Contributions(ctx, pluginCtx)
		if err != nil {
			return Resolution{}, fmt.Errorf("pluginhost: plugin %q contributions: %w", pluginLabel(ref), err)
		}
		source := bundle.Source
		if source.ID == "" {
			source = pluginSource(ref)
			bundle.Source = source
		}
		out.Bundles = append(out.Bundles, bundle)
		if contributor, ok := resolvedPlugin.(OperationContributor); ok {
			ops, err := contributor.Operations(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q operations: %w", pluginLabel(ref), err)
			}
			for _, op := range ops {
				out.Operations = append(out.Operations, OperationContribution{
					Source:    source,
					Operation: op,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(ContextProviderContributor); ok {
			providers, err := contributor.ContextProviders(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q context providers: %w", pluginLabel(ref), err)
			}
			for _, provider := range providers {
				out.ContextProviders = append(out.ContextProviders, ContextProviderContribution{
					Source:   source,
					Provider: provider,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(ObserverContributor); ok {
			observers, err := contributor.EnvironmentObservers(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q environment observers: %w", pluginLabel(ref), err)
			}
			for _, observer := range observers {
				if observer == nil {
					continue
				}
				out.Observers = append(out.Observers, EnvironmentObserverContribution{
					Source:   source,
					Observer: observer,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(AssertionDeriverContributor); ok {
			derivers, err := contributor.AssertionDerivers(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q assertion derivers: %w", pluginLabel(ref), err)
			}
			for _, deriver := range derivers {
				if deriver == nil {
					continue
				}
				out.AssertionDerivers = append(out.AssertionDerivers, AssertionDeriverContribution{
					Source:  source,
					Deriver: deriver,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(ReactionContributor); ok {
			rules, err := contributor.Reactions(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q reactions: %w", pluginLabel(ref), err)
			}
			for _, rule := range rules {
				out.Reactions = append(out.Reactions, ReactionContribution{
					Source: source,
					Rule:   rule,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(ChannelContributor); ok {
			channels, err := contributor.Channels(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q channels: %w", pluginLabel(ref), err)
			}
			for _, ch := range channels {
				if ch.Source.ID == "" {
					ch.Source = source
				}
				out.Channels = append(out.Channels, ch)
			}
		}
		if contributor, ok := resolvedPlugin.(DatasourceProviderContributor); ok {
			providers, err := contributor.DatasourceProviders(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q datasource providers: %w", pluginLabel(ref), err)
			}
			for _, provider := range providers {
				out.DatasourceProviders = append(out.DatasourceProviders, DatasourceProviderContribution{
					Source:   source,
					Provider: provider,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(DiscoveryProviderContributor); ok {
			providers, err := contributor.DiscoveryProviders(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q discovery providers: %w", pluginLabel(ref), err)
			}
			for _, provider := range providers {
				if provider == nil {
					continue
				}
				out.DiscoveryProviders = append(out.DiscoveryProviders, DiscoveryProviderContribution{
					Source:   source,
					Provider: provider,
				})
				if h.discovery != nil {
					if err := h.discovery.Register(provider); err != nil {
						return Resolution{}, fmt.Errorf("pluginhost: plugin %q discovery provider: %w", pluginLabel(ref), err)
					}
				}
			}
		}
		if contributor, ok := resolvedPlugin.(SecretResolverContributor); ok {
			resolvers, err := contributor.SecretResolvers(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q secret resolvers: %w", pluginLabel(ref), err)
			}
			for _, resolver := range resolvers {
				if resolver == nil {
					continue
				}
				out.SecretResolvers = append(out.SecretResolvers, SecretResolverContribution{
					Source:   source,
					Resolver: resolver,
				})
				if h.secrets != nil {
					h.secrets.Register(resolver)
				}
			}
		}
		if contributor, ok := resolvedPlugin.(AuthMethodContributor); ok {
			methods, err := contributor.AuthMethods(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q auth methods: %w", pluginLabel(ref), err)
			}
			for _, method := range methods {
				if err := coresecret.ValidateAuthMethod(method); err != nil {
					return Resolution{}, fmt.Errorf("pluginhost: plugin %q auth method: %w", pluginLabel(ref), err)
				}
				out.AuthMethods = append(out.AuthMethods, AuthMethodContribution{
					Source: source,
					Method: method,
				})
			}
		}
		if contributor, ok := resolvedPlugin.(ExternalIdentityContributor); ok {
			resolvers, err := contributor.ExternalIdentityResolvers(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q external identity resolvers: %w", pluginLabel(ref), err)
			}
			for _, resolver := range resolvers {
				if resolver == nil {
					continue
				}
				out.ExternalIdentities = append(out.ExternalIdentities, ExternalIdentityContribution{
					Source:   source,
					Resolver: resolver,
				})
			}
		}
	}
	return out, nil
}

func (h *Host) ensureSharedRegistries() {
	if h.discovery == nil {
		h.discovery = runtimediscovery.NewRegistry()
	}
	if h.endpoints == nil {
		h.endpoints = runtimeendpoint.NewRegistry(0)
	}
	if h.discoverer == nil {
		h.discoverer = runtimediscovery.NewRunner(h.discovery, h.endpoints)
	}
	if h.secrets == nil {
		h.secrets = runtimesecret.NewRegistry()
	}
}

func pluginSource(ref resource.PluginRef) resource.SourceRef {
	id := "plugin:" + ref.Name
	location := "plugins/" + ref.Name
	if instance := ref.InstanceName(); instance != "" && instance != ref.Name {
		id += "/" + instance
		location += "/" + instance
	}
	return resource.SourceRef{
		ID:        id,
		Ecosystem: "embedded",
		Scope:     resource.ScopeEmbedded,
		Location:  location,
		Ref:       ref.InstanceName(),
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	}
}

func pluginLabel(ref resource.PluginRef) string {
	return ref.Key()
}
