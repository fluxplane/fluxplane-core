package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
)

// ConfigDecoder is implemented by plugin registrations that want pluginhost to
// decode ref config once before instance materialization and contribution
// collection.
type ConfigDecoder interface {
	DecodeConfig(context.Context, Context) (any, error)
}

// Configurable decodes resource.PluginRef config into T when embedded by a
// plugin registration.
type Configurable[T any] struct{}

// DecodeConfig decodes the plugin ref config into T.
func (Configurable[T]) DecodeConfig(_ context.Context, ctx Context) (any, error) {
	cfg, err := DecodeConfig[T](ctx.Ref.Config)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// DecodeConfig converts a raw plugin config map into a typed config object.
func DecodeConfig[T any](raw map[string]any) (T, error) {
	var cfg T
	if len(raw) == 0 {
		return cfg, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("decode plugin config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("decode plugin config: %w", err)
	}
	return cfg, nil
}

// ConfigAs returns the typed config already decoded into ctx.
func ConfigAs[T any](ctx Context) (T, error) {
	cfg, ok := ctx.Config.(T)
	if ok {
		return cfg, nil
	}
	var zero T
	if ctx.Config == nil {
		return zero, fmt.Errorf("pluginhost: plugin config was not decoded")
	}
	return zero, fmt.Errorf("pluginhost: plugin config has type %T", ctx.Config)
}

// PrepareContext returns ctx with Config populated when plugin declares a
// ConfigDecoder.
func PrepareContext(ctx context.Context, plugin Plugin, pluginCtx Context) (Context, error) {
	if decoder, ok := plugin.(ConfigDecoder); ok {
		cfg, err := decoder.DecodeConfig(ctx, pluginCtx)
		if err != nil {
			return pluginCtx, fmt.Errorf("plugin %q config: %w", pluginLabel(pluginCtx.Ref), err)
		}
		pluginCtx.Config = cfg
	}
	return pluginCtx, nil
}
