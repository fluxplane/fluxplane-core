package pluginbridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-plugin/management"
	pluginlocal "github.com/fluxplane/fluxplane-plugin/management/local"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
)

// InstalledRuntimeFactory adapts persisted plugin runtime state into a
// fluxplane-plugin runtime that Core can bridge.
type InstalledRuntimeFactory func(management.Plugin) (pluginruntime.Plugin, error)

// InstalledLoadResult contains Core plugin implementations and the plugin refs
// that should be declared in a contribution bundle to activate them.
type InstalledLoadResult struct {
	Plugins []pluginhost.Plugin
	Refs    []resource.PluginRef
	Bundle  resource.ContributionBundle
}

// InstalledLoadOption configures installed plugin loading.
type InstalledLoadOption func(*installedLoadConfig)

type installedLoadConfig struct {
	store         management.Store
	runtime       InstalledRuntimeFactory
	bridgeOptions []Option
	names         map[string]bool
}

// WithInstalledStore uses store instead of the default local plugin state
// store. This is the injection point for products that keep plugin state
// somewhere other than ~/.fluxplane/plugins/state.json.
func WithInstalledStore(store management.Store) InstalledLoadOption {
	return func(cfg *installedLoadConfig) {
		cfg.store = store
	}
}

// WithInstalledRuntimeFactory overrides how persisted runtime specs become
// executable plugin runtimes. Tests and embedded products can use this to map
// installed refs to direct in-process plugins.
func WithInstalledRuntimeFactory(factory InstalledRuntimeFactory) InstalledLoadOption {
	return func(cfg *installedLoadConfig) {
		cfg.runtime = factory
	}
}

// WithInstalledBridgeOptions applies pluginbridge options to every loaded
// plugin. Use this to provide Core/product host capabilities to stdio/direct
// plugin runtimes.
func WithInstalledBridgeOptions(opts ...Option) InstalledLoadOption {
	return func(cfg *installedLoadConfig) {
		cfg.bridgeOptions = append(cfg.bridgeOptions, opts...)
	}
}

// WithInstalledPluginNames restricts loading to the named installed plugins.
// Empty input means all enabled installed plugins.
func WithInstalledPluginNames(names ...string) InstalledLoadOption {
	return func(cfg *installedLoadConfig) {
		if cfg.names == nil {
			cfg.names = map[string]bool{}
		}
		for _, name := range names {
			if name = strings.TrimSpace(name); name != "" {
				cfg.names[name] = true
			}
		}
	}
}

// LoadInstalled loads enabled installed plugins from the configured state
// provider and adapts them to Core pluginhost plugins.
func LoadInstalled(ctx context.Context, opts ...InstalledLoadOption) (InstalledLoadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := installedLoadConfig{runtime: StdioRuntimeFromInstalledPlugin}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.store == nil {
		store, err := pluginlocal.New()
		if err != nil {
			return InstalledLoadResult{}, err
		}
		cfg.store = store
	}
	if cfg.runtime == nil {
		return InstalledLoadResult{}, fmt.Errorf("pluginbridge: installed runtime factory is nil")
	}
	installed, err := cfg.store.ListPlugins(ctx, management.ListRequest{All: true})
	if err != nil {
		return InstalledLoadResult{}, fmt.Errorf("pluginbridge: list installed plugins: %w", err)
	}
	status, err := cfg.store.PluginStatus(ctx, management.StatusRequest{})
	if err != nil {
		return InstalledLoadResult{}, fmt.Errorf("pluginbridge: load installed plugin status: %w", err)
	}
	instances := installedInstancesByPlugin(status.Instances)
	var result InstalledLoadResult
	seenPlugins := map[string]bool{}
	seenRefs := map[string]bool{}
	for _, installedPlugin := range installed {
		if !installedPlugin.Installed || !installedPlugin.Enabled || !cfg.includes(installedPlugin.Ref.Name) {
			continue
		}
		runtime, err := cfg.runtime(installedPlugin)
		if err != nil {
			return InstalledLoadResult{}, fmt.Errorf("pluginbridge: installed plugin %q runtime: %w", installedPlugin.Ref.Key(), err)
		}
		bridged, err := Load(ctx, runtime, cfg.bridgeOptions...)
		if err != nil {
			return InstalledLoadResult{}, fmt.Errorf("pluginbridge: installed plugin %q manifest: %w", installedPlugin.Ref.Key(), err)
		}
		manifestName := bridged.Manifest().Name
		if manifestName == "" {
			return InstalledLoadResult{}, fmt.Errorf("pluginbridge: installed plugin %q manifest name is empty", installedPlugin.Ref.Key())
		}
		if seenPlugins[manifestName] {
			return InstalledLoadResult{}, fmt.Errorf("pluginbridge: installed plugin %q registers duplicate Core plugin %q", installedPlugin.Ref.Key(), manifestName)
		}
		seenPlugins[manifestName] = true
		refs := refsForInstalledPlugin(installedPlugin, manifestName, instances[installedPlugin.Ref.Key()])
		if len(refs) == 0 {
			continue
		}
		result.Plugins = append(result.Plugins, bridged)
		for _, ref := range refs {
			if ref.Name == "" || seenRefs[ref.Key()] {
				continue
			}
			seenRefs[ref.Key()] = true
			result.Refs = append(result.Refs, ref)
		}
	}
	result.Bundle = InstalledPluginBundle(result.Refs...)
	return result, nil
}

// InstalledPluginBundle declares installed plugin refs as a resource bundle so
// normal Core composition resolves their manifest-derived contributions.
func InstalledPluginBundle(refs ...resource.PluginRef) resource.ContributionBundle {
	return resource.ContributionBundle{
		Source: resource.SourceRef{
			ID:        "fluxplane-plugin:installed",
			Ecosystem: "fluxplane-plugin",
			Scope:     resource.ScopeUser,
			Location:  "~/.fluxplane/plugins",
		},
		Plugins: append([]resource.PluginRef(nil), refs...),
	}
}

// StdioRuntimeFromInstalledPlugin creates a stdio plugin runtime from the
// runtime spec persisted by fluxplane-plugin management.
func StdioRuntimeFromInstalledPlugin(plugin management.Plugin) (pluginruntime.Plugin, error) {
	if strings.TrimSpace(plugin.Ref.Name) == "" {
		return nil, fmt.Errorf("plugin ref name is required")
	}
	kind := strings.TrimSpace(plugin.Runtime.Kind)
	if kind != "" && kind != "stdio" {
		return nil, fmt.Errorf("unsupported runtime kind %q", kind)
	}
	command := strings.TrimSpace(plugin.Runtime.Command)
	if command == "" {
		command = strings.TrimSpace(plugin.Runtime.Path)
	}
	if command == "" {
		return nil, fmt.Errorf("stdio command is required")
	}
	runtime := pluginruntime.Stdio(plugin.Ref.Name, command, plugin.Runtime.Args...)
	runtime.Dir = runtimeWorkingDir(plugin.Runtime)
	return runtime, nil
}

func (cfg installedLoadConfig) includes(name string) bool {
	if len(cfg.names) == 0 {
		return true
	}
	return cfg.names[strings.TrimSpace(name)]
}

func installedInstancesByPlugin(instances []management.Instance) map[string][]management.Instance {
	out := map[string][]management.Instance{}
	for _, instance := range instances {
		if strings.TrimSpace(instance.Plugin.Name) == "" {
			continue
		}
		out[instance.Plugin.Key()] = append(out[instance.Plugin.Key()], instance)
	}
	for key := range out {
		sort.Slice(out[key], func(i, j int) bool {
			return normalizeInstalledInstance(out[key][i].Name) < normalizeInstalledInstance(out[key][j].Name)
		})
	}
	return out
}

func refsForInstalledPlugin(plugin management.Plugin, manifestName string, instances []management.Instance) []resource.PluginRef {
	if len(instances) == 0 {
		return []resource.PluginRef{{Name: manifestName, Instance: management.DefaultInstance}}
	}
	refs := make([]resource.PluginRef, 0, len(instances))
	for _, instance := range instances {
		if !instance.Enabled {
			continue
		}
		refs = append(refs, resource.PluginRef{
			Name:     manifestName,
			Instance: normalizeInstalledInstance(instance.Name),
			Config:   cloneAnyMap(instance.Config),
		})
	}
	return refs
}

func normalizeInstalledInstance(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return management.DefaultInstance
}

func runtimeWorkingDir(runtime management.RuntimeSpec) string {
	path := strings.TrimSpace(runtime.Path)
	command := strings.TrimSpace(runtime.Command)
	if path == "" || path == command {
		return ""
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Clean(path)
	}
	return ""
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
