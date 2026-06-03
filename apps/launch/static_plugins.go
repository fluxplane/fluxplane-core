package launch

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/contrib/datasource"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	"github.com/fluxplane/fluxplane-policy"
)

// StaticPluginOptions configures plugin contribution materialization for
// inspection-only surfaces such as describe and discover.
type StaticPluginOptions struct {
	Bundles                          []resource.ContributionBundle
	Launch                           distribution.LaunchConfig
	Plugins                          func(PluginFactoryContext) []contributions.Provider
	IncludeConfigSchemaContributions bool
}

// StaticPluginResult is the inspection-only contribution set resolved from
// declared and implicit plugin refs.
type StaticPluginResult struct {
	Bundles         []resource.ContributionBundle
	Diagnostics     []resource.Diagnostic
	ImplicitPlugins map[string]bool
}

// BundlesWithStaticPluginContributions returns bundles plus static
// contribution bundles from declared plugins. It never asks plugins for
// executable runtime implementations.
func BundlesWithStaticPluginContributions(ctx context.Context, opts StaticPluginOptions) ([]resource.ContributionBundle, []resource.Diagnostic) {
	result := StaticPluginView(ctx, opts)
	return result.Bundles, result.Diagnostics
}

// StaticPluginView returns bundles, diagnostics, and presentation metadata for
// static plugin contribution inspection.
func StaticPluginView(ctx context.Context, opts StaticPluginOptions) StaticPluginResult {
	bundles, implicit := staticPluginBaseBundles(opts.Bundles)
	pluginBundles, diagnostics := staticPluginContributions(ctx, bundles, opts)
	bundles = append(bundles, pluginBundles...)
	return StaticPluginResult{Bundles: bundles, Diagnostics: diagnostics, ImplicitPlugins: implicit}
}

// StaticPluginContributions resolves declared plugin refs to inert resource
// contribution bundles for inspection.
func StaticPluginContributions(ctx context.Context, opts StaticPluginOptions) ([]resource.ContributionBundle, []resource.Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	bundles, _ := staticPluginBaseBundles(opts.Bundles)
	return staticPluginContributions(ctx, bundles, opts)
}

func staticPluginBaseBundles(bundles []resource.ContributionBundle) ([]resource.ContributionBundle, map[string]bool) {
	out := cloneBundles(bundles)
	implicit := map[string]bool{}
	if hasAnyDatasource(out) && !bundleHasPlugin(out, datasource.Name) {
		ensurePluginRef(out, datasource.Name)
		implicit[datasource.Name] = true
	}
	return out, implicit
}

func staticPluginContributions(ctx context.Context, bundles []resource.ContributionBundle, opts StaticPluginOptions) ([]resource.ContributionBundle, []resource.Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	available := availableStaticPlugins(opts)
	byName, err := pluginsByName(available)
	if err != nil {
		return nil, []resource.Diagnostic{staticPluginDiagnostic(err)}
	}
	var out []resource.ContributionBundle
	for _, ref := range staticPluginRefs(bundles) {
		plugin, ok := byName[ref.Name]
		if !ok {
			err := fmt.Errorf("plugin %q is not available", ref.Key())
			return out, []resource.Diagnostic{staticPluginDiagnostic(err)}
		}
		pluginCtx := contributions.Context{Ref: ref}
		pluginCtx, err = contributions.PrepareContext(ctx, plugin, pluginCtx)
		if err != nil {
			return out, []resource.Diagnostic{staticPluginDiagnostic(err)}
		}
		resolvedPlugin := plugin
		if factory, ok := plugin.(contributions.InstanceFactory); ok {
			resolvedPlugin, err = factory.Instantiate(ctx, pluginCtx)
			if err != nil {
				err := fmt.Errorf("plugin %q instantiate: %w", ref.Key(), err)
				return out, []resource.Diagnostic{staticPluginDiagnostic(err)}
			}
			if resolvedPlugin == nil {
				err := fmt.Errorf("plugin %q instantiate returned nil", ref.Key())
				return out, []resource.Diagnostic{staticPluginDiagnostic(err)}
			}
		}
		bundle, err := resolvedPlugin.Contributions(ctx, pluginCtx)
		if err != nil {
			err := fmt.Errorf("plugin %q contributions: %w", ref.Key(), err)
			return out, []resource.Diagnostic{staticPluginDiagnostic(err)}
		}
		if opts.IncludeConfigSchemaContributions {
			contributor, ok := resolvedPlugin.(contributions.ConfigSchemaContributor)
			if ok {
				schemaBundle, err := contributor.ConfigSchemaContributions(ctx, pluginCtx)
				if err != nil {
					err := fmt.Errorf("plugin %q config schema contributions: %w", ref.Key(), err)
					return out, []resource.Diagnostic{staticPluginDiagnostic(err)}
				}
				bundle.Append(schemaBundle)
			}
		}
		if bundle.Source.ID == "" {
			bundle.Source = staticPluginSource(ref)
		}
		out = append(out, bundle)
	}
	return out, nil
}

func staticPluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	seen := map[string]bool{}
	var out []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			key := ref.Key()
			if ref.Name == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ref)
		}
	}
	return out
}

func availableStaticPlugins(opts StaticPluginOptions) []contributions.Provider {
	if opts.Plugins != nil {
		return opts.Plugins(PluginFactoryContext{})
	}
	plugins := availablePlugins(nil, nil, nil, nil, "", false)
	if hasAnyDatasource(opts.Bundles) {
		plugins = append(plugins, datasource.New(nil))
	}
	return plugins
}

func pluginsByName(plugins []contributions.Provider) (map[string]contributions.Provider, error) {
	out := map[string]contributions.Provider{}
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		name := strings.TrimSpace(plugin.Manifest().Name)
		if name == "" {
			return nil, fmt.Errorf("plugin name is empty")
		}
		out[name] = plugin
	}
	return out, nil
}

func staticPluginSource(ref resource.PluginRef) resource.SourceRef {
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

func staticPluginDiagnostic(err error) resource.Diagnostic {
	return resource.Diagnostic{
		Severity: resource.SeverityWarning,
		Message:  "resolve static plugin contributions: " + err.Error(),
	}
}
