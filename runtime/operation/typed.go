package operationruntime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/invopop/jsonschema"
)

// TypedHandler is the typed operation handler shape used by NewTyped.
type TypedHandler[I, O any] func(operation.Context, I) (O, error)

// TypedResultHandler is the typed operation handler shape used when the handler
// needs to decide the final operation status itself.
type TypedResultHandler[I, O any] func(operation.Context, I) operation.Result

// NewTyped adapts a typed Go handler into an operation and derives JSON Schema
// for the input and output contracts when the spec does not already provide
// them.
func NewTyped[I, O any](spec operation.Spec, handler TypedHandler[I, O]) operation.Operation {
	spec = WithTypedContract[I, O](spec)
	return operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
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
}

// NewTypedResult adapts a typed Go handler into an operation while preserving
// the handler's ability to return rejected, failed, canceled, or ok results.
func NewTypedResult[I, O any](spec operation.Spec, handler TypedResultHandler[I, O]) operation.Operation {
	spec = WithTypedContract[I, O](spec)
	return operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
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
