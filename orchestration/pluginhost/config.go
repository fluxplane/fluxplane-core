package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	invjsonschema "github.com/invopop/jsonschema"
)

// ConfigDecoder is implemented by plugin registrations that want pluginhost to
// decode ref config once before instance materialization and contribution
// collection.
type ConfigDecoder interface {
	DecodeConfig(context.Context, Context) (any, error)
}

// ConfigSchemaProvider is implemented by plugin registrations that can expose
// the JSON Schema for their app manifest config block.
type ConfigSchemaProvider interface {
	ConfigSchema() ([]byte, error)
}

// ConfigSchemaContributor is implemented by plugins that expose inert
// resources which are valid in app manifests for schema and inspection
// purposes, but are not ordinary runtime configuration inputs.
type ConfigSchemaContributor interface {
	ConfigSchemaContributions(context.Context, Context) (resource.ContributionBundle, error)
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

// ConfigSchema returns the JSON Schema for T.
func (Configurable[T]) ConfigSchema() ([]byte, error) {
	return ConfigSchemaFor[T]()
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

// ConfigMap converts a typed plugin config into the raw map stored on
// resource.PluginRef.
func ConfigMap[T any](cfg T) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode plugin config: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("encode plugin config: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return raw, nil
}

// ConfigSchemaFor reflects T into a JSON Schema for plugin app-manifest config.
func ConfigSchemaFor[T any]() ([]byte, error) {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	ptr := reflect.New(typ)
	if typ.Kind() == reflect.Ptr {
		ptr = reflect.New(typ.Elem())
	}
	reflector := invjsonschema.Reflector{
		DoNotReference:             true,
		ExpandedStruct:             true,
		AllowAdditionalProperties:  false,
		RequiredFromJSONSchemaTags: true,
		Mapper:                     configSchemaEnumMapper,
	}
	schema := reflector.Reflect(ptr.Interface())
	if schema == nil {
		return nil, fmt.Errorf("pluginhost: config schema is nil")
	}
	schema.Version = invjsonschema.Version
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: marshal config schema: %w", err)
	}
	return data, nil
}

func configSchemaEnumMapper(t reflect.Type) *invjsonschema.Schema {
	switch t {
	case reflect.TypeOf(coresecret.AuthMethodKind("")):
		schema := enumSchema([]string{string(coresecret.AuthMethodEnv), string(coresecret.AuthMethodOAuth2), string(coresecret.AuthMethodStored)})
		schema.Description = "Credential source for this auth scheme: env, oauth2, or stored secret material."
		return schema
	case reflect.TypeOf(coresecret.Kind("")):
		schema := enumSchema([]string{string(coresecret.KindAPIKey), string(coresecret.KindBearerToken), string(coresecret.KindOAuth2Token), string(coresecret.KindBasic), string(coresecret.KindPKI)})
		schema.Description = "Shape of credential material expected for this auth scheme."
		return schema
	case reflect.TypeOf(coresecret.Scheme("")):
		schema := enumSchema([]string{string(coresecret.SchemeEnv), string(coresecret.SchemePlugin), string(coresecret.SchemeKubernetes)})
		schema.Description = "Secret reference scheme."
		return schema
	default:
		return nil
	}
}

func enumSchema(values []string) *invjsonschema.Schema {
	enum := make([]any, 0, len(values))
	for _, value := range values {
		enum = append(enum, value)
	}
	return &invjsonschema.Schema{Type: "string", Enum: enum}
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
