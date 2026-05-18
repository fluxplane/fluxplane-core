package pluginhost

import (
	"context"
	"fmt"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/channelruntime"
)

// Manifest describes one plugin implementation.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// Context is passed to a plugin when resolving contributions.
type Context struct {
	Ref        resource.PluginRef `json:"ref"`
	Config     any                `json:"-"`
	EventStore event.Store        `json:"-"`
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

// ChannelContributor is implemented by plugins that can provide long-running
// runtime channels for daemon mode.
type ChannelContributor interface {
	Channels(context.Context, Context) ([]ChannelContribution, error)
}

// ConnectorProviderContributor is implemented by plugins that make a
// third-party connection provider available to host-level connect commands.
type ConnectorProviderContributor interface {
	ConnectorProviders(context.Context, Context) ([]ConnectorProvider, error)
}

// DatasourceProviderContributor is implemented by plugins that make
// datasource entity accessors available to host-level app composition.
type DatasourceProviderContributor interface {
	DatasourceProviders(context.Context, Context) ([]coredatasource.Provider, error)
}

// AuthMethodContributor is implemented by plugins that declare supported
// authentication methods without carrying credential values.
type AuthMethodContributor interface {
	AuthMethods(context.Context, Context) ([]coresecret.AuthMethodSpec, error)
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

// ConnectorProvider identifies one connection provider exposed by a plugin.
type ConnectorProvider struct {
	Name string `json:"name"`
}

// ConnectorProviderContribution is one connection provider contribution.
type ConnectorProviderContribution struct {
	Source   resource.SourceRef
	Provider ConnectorProvider
}

// DatasourceProviderContribution is one datasource provider contribution.
type DatasourceProviderContribution struct {
	Source   resource.SourceRef
	Provider coredatasource.Provider
}

// ContextProviderContribution is one executable context provider contribution.
type ContextProviderContribution struct {
	Source   resource.SourceRef
	Provider corecontext.Provider
}

// AuthMethodContribution is one plugin auth method declaration.
type AuthMethodContribution struct {
	Source resource.SourceRef
	Method coresecret.AuthMethodSpec
}

// Resolution is the complete contribution set resolved for plugin refs.
type Resolution struct {
	Bundles             []resource.ContributionBundle
	Operations          []OperationContribution
	ContextProviders    []ContextProviderContribution
	Channels            []ChannelContribution
	ConnectorProviders  []ConnectorProviderContribution
	DatasourceProviders []DatasourceProviderContribution
	AuthMethods         []AuthMethodContribution
}

// Host resolves plugin refs through registered plugin implementations.
type Host struct {
	plugins    map[string]Plugin
	eventStore event.Store
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
	var out Resolution
	for _, ref := range refs {
		if ref.Name == "" {
			return Resolution{}, fmt.Errorf("pluginhost: plugin ref name is empty")
		}
		plugin, ok := h.plugins[ref.Name]
		if !ok {
			return Resolution{}, fmt.Errorf("pluginhost: plugin %q is not registered", pluginLabel(ref))
		}
		pluginCtx := Context{Ref: ref, EventStore: h.eventStore}
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
		if contributor, ok := resolvedPlugin.(ConnectorProviderContributor); ok {
			providers, err := contributor.ConnectorProviders(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q connector providers: %w", pluginLabel(ref), err)
			}
			for _, provider := range providers {
				out.ConnectorProviders = append(out.ConnectorProviders, ConnectorProviderContribution{
					Source:   source,
					Provider: provider,
				})
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
	}
	return out, nil
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
