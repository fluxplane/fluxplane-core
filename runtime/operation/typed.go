package operationruntime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/fluxplane/engine/core/operation"
	"github.com/invopop/jsonschema"
)

// TypedHandler is the typed operation handler shape used by NewTyped.
type TypedHandler[I, O any] func(operation.Context, I) (O, error)

// TypedResultHandler is the typed operation handler shape used when the handler
// needs to decide the final operation status itself.
type TypedResultHandler[I, O any] func(operation.Context, I) operation.Result

// TypedIntentHandler derives an operation's safety intent from typed input.
type TypedIntentHandler[I any] func(operation.Context, I) (operation.IntentSet, error)

// TypedAccessHandler derives an operation's authorization targets from typed
// input.
type TypedAccessHandler[I any] func(operation.Context, I) ([]AccessDescriptor, error)

type typedConfig[I any] struct {
	intent TypedIntentHandler[I]
	access TypedAccessHandler[I]
}

// TypedOption configures a typed operation adapter.
type TypedOption[I any] func(*typedConfig[I])

// WithIntent attaches typed safety-intent derivation to a typed operation.
func WithIntent[I any](handler TypedIntentHandler[I]) TypedOption[I] {
	return func(cfg *typedConfig[I]) {
		cfg.intent = handler
	}
}

// WithAccess attaches typed authorization target derivation to a typed
// operation.
func WithAccess[I any](handler TypedAccessHandler[I]) TypedOption[I] {
	return func(cfg *typedConfig[I]) {
		cfg.access = handler
	}
}

// NewTyped adapts a typed Go handler into an operation and derives JSON Schema
// for the input and output contracts when the spec does not already provide
// them.
func NewTyped[I, O any](spec operation.Spec, handler TypedHandler[I, O], opts ...TypedOption[I]) operation.Operation {
	spec = WithTypedContract[I, O](spec)
	op := operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
		var zero O
		in, err := Bind[I](input)
		if err != nil {
			return operation.Failed("invalid_"+string(spec.Ref.Name)+"_input", err.Error(), nil)
		}
		if handler == nil {
			return operation.OK(zero)
		}
		out, err := handler(ctx, in)
		if err != nil {
			return operation.Failed(string(spec.Ref.Name)+"_failed", err.Error(), nil)
		}
		return operation.OK(out)
	})
	return withTypedOptions[I](op, opts...)
}

// NewTypedResult adapts a typed Go handler into an operation while preserving
// the handler's ability to return rejected, failed, canceled, or ok results.
func NewTypedResult[I, O any](spec operation.Spec, handler TypedResultHandler[I, O], opts ...TypedOption[I]) operation.Operation {
	spec = WithTypedContract[I, O](spec)
	op := operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
		in, err := Bind[I](input)
		if err != nil {
			return operation.Failed("invalid_"+string(spec.Ref.Name)+"_input", err.Error(), nil)
		}
		if handler == nil {
			var zero O
			return operation.OK(zero)
		}
		return handler(ctx, in)
	})
	return withTypedOptions[I](op, opts...)
}

func withTypedOptions[I any](op operation.Operation, opts ...TypedOption[I]) operation.Operation {
	cfg := typedConfig[I]{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.intent == nil {
		if cfg.access == nil {
			return op
		}
		return typedAccessOperation[I]{Operation: op, access: cfg.access}
	}
	op = typedIntentOperation[I]{Operation: op, intent: cfg.intent}
	if cfg.access != nil {
		op = typedAccessIntentOperation[I]{Operation: op, access: cfg.access}
	}
	return op
}

type typedIntentOperation[I any] struct {
	operation.Operation
	intent TypedIntentHandler[I]
}

func (o typedIntentOperation[I]) Intent(ctx operation.Context, input operation.Value) (operation.IntentSet, error) {
	in, err := Bind[I](input)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return o.intent(ctx, in)
}

type typedAccessOperation[I any] struct {
	operation.Operation
	access TypedAccessHandler[I]
}

func (o typedAccessOperation[I]) Access(ctx operation.Context, input operation.Value) ([]AccessDescriptor, error) {
	in, err := Bind[I](input)
	if err != nil {
		return nil, err
	}
	return o.access(ctx, in)
}

type typedAccessIntentOperation[I any] struct {
	operation.Operation
	access TypedAccessHandler[I]
}

func (o typedAccessIntentOperation[I]) Access(ctx operation.Context, input operation.Value) ([]AccessDescriptor, error) {
	in, err := Bind[I](input)
	if err != nil {
		return nil, err
	}
	return o.access(ctx, in)
}

func (o typedAccessIntentOperation[I]) Intent(ctx operation.Context, input operation.Value) (operation.IntentSet, error) {
	provider, ok := o.Operation.(operation.IntentProvider)
	if !ok {
		return operation.IntentSet{}, nil
	}
	return provider.Intent(ctx, input)
}

// WithTypedContract fills empty input/output contracts on spec from I and O.
func WithTypedContract[I, O any](spec operation.Spec) operation.Spec {
	if spec.Input.IsZero() {
		spec.Input = TypeOf[I](string(spec.Ref.Name) + "_input")
	}
	if spec.Output.IsZero() {
		spec.Output = TypeOf[O](string(spec.Ref.Name) + "_output")
	}
	return spec
}

// TypeOf returns a core operation type contract for T.
func TypeOf[T any](name string) operation.Type {
	goType := reflect.TypeOf((*T)(nil)).Elem()
	return operation.Type{
		Name:   name,
		Schema: SchemaForType(goType),
	}
}

// SchemaFor returns a core operation schema for T.
func SchemaFor[T any]() operation.Schema {
	return SchemaForType(reflect.TypeOf((*T)(nil)).Elem())
}

// OneOf returns a JSON Schema oneOf composition over the supplied schemas.
func OneOf(schemas ...operation.Schema) operation.Schema {
	cases := make([]any, 0, len(schemas))
	for _, schema := range schemas {
		if schema.Format != "" && schema.Format != "json-schema" {
			continue
		}
		var value any
		if len(schema.Data) == 0 || json.Unmarshal(schema.Data, &value) != nil {
			continue
		}
		cases = append(cases, value)
	}
	raw, err := json.Marshal(map[string]any{"oneOf": cases})
	if err != nil {
		return operation.Schema{}
	}
	return operation.Schema{Format: "json-schema", Data: raw}
}

// WithArrayItems replaces the JSON Schema items schema of one array property
// on typ. It is intended for reflected outer structs whose item type needs a
// composed schema such as oneOf.
func WithArrayItems(typ operation.Type, property string, items operation.Schema) operation.Type {
	typ.Schema = schemaWithArrayItems(typ.Schema, property, items)
	return typ
}

func schemaWithArrayItems(schema operation.Schema, property string, items operation.Schema) operation.Schema {
	if schema.Format != "" && schema.Format != "json-schema" {
		return schema
	}
	var root map[string]any
	if len(schema.Data) == 0 || json.Unmarshal(schema.Data, &root) != nil {
		return schema
	}
	var itemValue any
	if len(items.Data) == 0 || json.Unmarshal(items.Data, &itemValue) != nil {
		return schema
	}
	properties, ok := root["properties"].(map[string]any)
	if !ok {
		return schema
	}
	prop, ok := properties[property].(map[string]any)
	if !ok {
		return schema
	}
	prop["items"] = itemValue
	raw, err := json.Marshal(root)
	if err != nil {
		return schema
	}
	return operation.Schema{Format: "json-schema", Data: raw}
}

// SchemaForType returns an inert JSON Schema document for t when possible.
func SchemaForType(t reflect.Type) operation.Schema {
	if t == nil || !jsonSchemaEligible(t, map[reflect.Type]bool{}) {
		return operation.Schema{}
	}
	reflector := jsonschema.Reflector{
		DoNotReference:             true,
		Anonymous:                  true,
		AllowAdditionalProperties:  false,
		RequiredFromJSONSchemaTags: true,
	}
	ptr := reflect.New(t)
	if t.Kind() == reflect.Ptr {
		ptr = reflect.New(t.Elem())
	}
	schema := reflector.Reflect(ptr.Interface())
	if schema == nil {
		return operation.Schema{}
	}
	schema.Version = ""
	raw, err := json.Marshal(schema)
	if err != nil {
		return operation.Schema{}
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err == nil {
		delete(normalized, "$schema")
		delete(normalized, "$id")
		delete(normalized, "$defs")
		normalized = injectRequiredFromTags(t, normalized)
		if raw, err = json.Marshal(normalized); err != nil {
			return operation.Schema{}
		}
	}
	return operation.Schema{Format: "json-schema", Data: raw}
}

// Bind converts an operation input value into I.
func Bind[I any](input operation.Value) (I, error) {
	var out I
	if input == nil {
		return out, nil
	}
	if typed, ok := input.(I); ok {
		return typed, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return out, fmt.Errorf("encode input: %w", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decode input: %w", err)
	}
	return out, nil
}

func jsonSchemaEligible(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	case reflect.Slice, reflect.Array:
		return jsonSchemaEligible(t.Elem(), seen)
	case reflect.Map:
		return t.Key().Kind() == reflect.String && jsonSchemaEligible(t.Elem(), seen)
	case reflect.Interface:
		return t.NumMethod() == 0
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			if !jsonSchemaEligible(field.Type, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func injectRequiredFromTags(t reflect.Type, schema map[string]any) map[string]any {
	if schema == nil || t == nil {
		return schema
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return schema
	}
	required, _ := schema["required"].([]any)
	seen := map[string]bool{}
	for _, value := range required {
		if name, ok := value.(string); ok {
			seen[name] = true
		}
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !hasRequiredToken(field.Tag.Get("jsonschema")) {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" || seen[name] {
			continue
		}
		required = append(required, name)
		seen[name] = true
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func hasRequiredToken(tag string) bool {
	for len(tag) > 0 {
		i := 0
		for i < len(tag) {
			if tag[i] == '\\' {
				i += 2
				continue
			}
			if tag[i] == ',' {
				break
			}
			i++
		}
		if strings.TrimSpace(tag[:i]) == "required" {
			return true
		}
		if i >= len(tag) {
			break
		}
		tag = tag[i+1:]
	}
	return false
}
