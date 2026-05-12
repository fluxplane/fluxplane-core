package pluginhost

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
)

// Manifest describes one plugin implementation.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// Context is passed to a plugin when resolving contributions.
type Context struct {
	Ref resource.PluginRef `json:"ref"`
}

// Plugin contributes resources during app composition.
type Plugin interface {
	Manifest() Manifest
	Contributions(context.Context, Context) (resource.ContributionBundle, error)
}

// OperationContributor is implemented by plugins that provide executable
// operation implementations in addition to pure resource contributions.
type OperationContributor interface {
	Operations(context.Context, Context) ([]operation.Operation, error)
}

// OperationContribution is one executable operation contributed by a plugin.
type OperationContribution struct {
	Source    resource.SourceRef
	Operation operation.Operation
}

// Resolution is the complete contribution set resolved for plugin refs.
type Resolution struct {
	Bundles    []resource.ContributionBundle
	Operations []OperationContribution
}

// Host resolves plugin refs through registered plugin implementations.
type Host struct {
	plugins map[string]Plugin
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
			return Resolution{}, fmt.Errorf("pluginhost: plugin %q is not registered", ref.Name)
		}
		pluginCtx := Context{Ref: ref}
		bundle, err := plugin.Contributions(ctx, pluginCtx)
		if err != nil {
			return Resolution{}, fmt.Errorf("pluginhost: plugin %q contributions: %w", ref.Name, err)
		}
		source := bundle.Source
		if source.ID == "" {
			source = pluginSource(ref)
			bundle.Source = source
		}
		out.Bundles = append(out.Bundles, bundle)
		if contributor, ok := plugin.(OperationContributor); ok {
			ops, err := contributor.Operations(ctx, pluginCtx)
			if err != nil {
				return Resolution{}, fmt.Errorf("pluginhost: plugin %q operations: %w", ref.Name, err)
			}
			for _, op := range ops {
				out.Operations = append(out.Operations, OperationContribution{
					Source:    source,
					Operation: op,
				})
			}
		}
	}
	return out, nil
}

func pluginSource(ref resource.PluginRef) resource.SourceRef {
	return resource.SourceRef{
		ID:        "plugin:" + ref.Name,
		Ecosystem: "plugin",
		Scope:     resource.ScopeEmbedded,
		Ref:       ref.Name,
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	}
}
