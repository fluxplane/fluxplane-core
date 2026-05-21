package pluginhost

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/engine/core/resource"
	coresecret "github.com/fluxplane/engine/core/secret"
)

// AuthTarget is one app-declared plugin instance that exposes auth methods.
type AuthTarget struct {
	Ref     resource.PluginRef
	Context Context
	Plugin  Plugin
	Methods []coresecret.AuthMethodSpec
}

// ResolveAuthTargets resolves declared plugin refs against available plugin
// implementations and returns the subset that exposes auth methods.
func ResolveAuthTargets(ctx context.Context, refs []resource.PluginRef, available []Plugin) ([]AuthTarget, error) {
	plugins, err := pluginsByName(available)
	if err != nil {
		return nil, err
	}
	var out []AuthTarget
	seen := map[string]bool{}
	for _, ref := range refs {
		ref.Name = strings.TrimSpace(ref.Name)
		ref.Instance = strings.TrimSpace(ref.Instance)
		if ref.Name == "" || seen[ref.Key()] {
			continue
		}
		seen[ref.Key()] = true
		plugin, ok := plugins[ref.Name]
		if !ok {
			return nil, fmt.Errorf("pluginhost: plugin %q is not available", ref.Key())
		}
		target, ok, err := resolveAuthTarget(ctx, ref, plugin)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, target)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Ref.Key() < out[j].Ref.Key()
	})
	return out, nil
}

func resolveAuthTarget(ctx context.Context, ref resource.PluginRef, plugin Plugin) (AuthTarget, bool, error) {
	pluginCtx := Context{Ref: ref}
	pluginCtx, err := PrepareContext(ctx, plugin, pluginCtx)
	if err != nil {
		return AuthTarget{}, false, err
	}
	resolved := plugin
	if factory, ok := plugin.(InstanceFactory); ok {
		resolved, err = factory.Instantiate(ctx, pluginCtx)
		if err != nil {
			return AuthTarget{}, false, fmt.Errorf("pluginhost: plugin %q instantiate: %w", ref.Key(), err)
		}
		if resolved == nil {
			return AuthTarget{}, false, fmt.Errorf("pluginhost: plugin %q instantiate returned nil", ref.Key())
		}
	}
	contributor, ok := resolved.(AuthMethodContributor)
	if !ok {
		return AuthTarget{}, false, nil
	}
	methods, err := contributor.AuthMethods(ctx, pluginCtx)
	if err != nil {
		return AuthTarget{}, false, fmt.Errorf("pluginhost: plugin %q auth methods: %w", ref.Key(), err)
	}
	for _, method := range methods {
		if err := coresecret.ValidateAuthMethod(method); err != nil {
			return AuthTarget{}, false, fmt.Errorf("pluginhost: plugin %q auth method: %w", ref.Key(), err)
		}
	}
	if len(methods) == 0 {
		return AuthTarget{}, false, nil
	}
	return AuthTarget{Ref: ref, Context: pluginCtx, Plugin: resolved, Methods: methods}, true, nil
}

func pluginsByName(plugins []Plugin) (map[string]Plugin, error) {
	out := map[string]Plugin{}
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		name := strings.TrimSpace(plugin.Manifest().Name)
		if name == "" {
			return nil, fmt.Errorf("pluginhost: plugin has empty name")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("pluginhost: plugin %q is registered more than once", name)
		}
		out[name] = plugin
	}
	return out, nil
}
