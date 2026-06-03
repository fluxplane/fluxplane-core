package contributions

import (
	"context"
	"fmt"
	"sort"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/resource"
)

// AuthTarget is one app-declared plugin instance that exposes auth methods.
type AuthTarget struct {
	Ref      resource.PluginRef
	Context  Context
	Provider Provider
	Methods  []auth.MethodSpec
}

// ResolveAuthTargets resolves declared plugin refs against available plugin
// implementations and returns the subset that exposes auth methods.
func ResolveAuthTargets(ctx context.Context, refs []resource.PluginRef, available []Provider) ([]AuthTarget, error) {
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
			return nil, fmt.Errorf("contributions: plugin %q is not available", ref.Key())
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

func resolveAuthTarget(ctx context.Context, ref resource.PluginRef, plugin Provider) (AuthTarget, bool, error) {
	pluginCtx := Context{Ref: ref}
	pluginCtx, err := PrepareContext(ctx, plugin, pluginCtx)
	if err != nil {
		return AuthTarget{}, false, err
	}
	resolved := plugin
	if factory, ok := plugin.(InstanceFactory); ok {
		resolved, err = factory.Instantiate(ctx, pluginCtx)
		if err != nil {
			return AuthTarget{}, false, fmt.Errorf("contributions: plugin %q instantiate: %w", ref.Key(), err)
		}
		if resolved == nil {
			return AuthTarget{}, false, fmt.Errorf("contributions: plugin %q instantiate returned nil", ref.Key())
		}
	}
	contributor, ok := resolved.(AuthMethodProvider)
	if !ok {
		return AuthTarget{}, false, nil
	}
	methods, err := contributor.AuthMethods(ctx, pluginCtx)
	if err != nil {
		return AuthTarget{}, false, fmt.Errorf("contributions: plugin %q auth methods: %w", ref.Key(), err)
	}
	for _, method := range methods {
		if err := auth.ValidateMethod(method); err != nil {
			return AuthTarget{}, false, fmt.Errorf("contributions: plugin %q auth method: %w", ref.Key(), err)
		}
	}
	if len(methods) == 0 {
		return AuthTarget{}, false, nil
	}
	return AuthTarget{Ref: ref, Context: pluginCtx, Provider: resolved, Methods: methods}, true, nil
}

func pluginsByName(plugins []Provider) (map[string]Provider, error) {
	out := map[string]Provider{}
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		name := strings.TrimSpace(plugin.Manifest().Name)
		if name == "" {
			return nil, fmt.Errorf("contributions: plugin has empty name")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("contributions: plugin %q is registered more than once", name)
		}
		out[name] = plugin
	}
	return out, nil
}
