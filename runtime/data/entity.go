package data

import (
	"reflect"
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
)

// SourceEntityOf derives a data source entity spec from exported fields of T.
func SourceEntityOf[T any](typ coredata.EntityType, description string) coredata.EntitySpec {
	return coredata.EntitySpec{
		Type:        typ,
		Description: description,
		Fields:      fieldsOf[T](false),
	}
}

// ViewOf derives a materialized view spec from exported fields of T.
func ViewOf[T any](name coredata.ViewName, source coredata.EntityType, options ...ViewOption) coredata.ViewSpec {
	spec := coredata.ViewSpec{
		Name:   name,
		Entity: source,
		Source: source,
		Fields: fieldsOf[T](true),
	}
	for _, option := range options {
		option(&spec)
	}
	return spec
}

// ViewOption configures a reflected materialized view spec.
type ViewOption func(*coredata.ViewSpec)

// WithViewEntity sets the model-facing entity type returned by the view.
func WithViewEntity(entity coredata.EntityType) ViewOption {
	return func(spec *coredata.ViewSpec) {
		spec.Entity = entity
	}
}

// WithViewDescription sets the view description.
func WithViewDescription(description string) ViewOption {
	return func(spec *coredata.ViewSpec) {
		spec.Description = description
	}
}

// WithViewIncludes sets relation includes for the view.
func WithViewIncludes(includes ...coredata.RelationIncludeSpec) ViewOption {
	return func(spec *coredata.ViewSpec) {
		spec.Includes = append([]coredata.RelationIncludeSpec(nil), includes...)
	}
}

// WithViewQueryHints sets query hints for the view.
func WithViewQueryHints(hints ...coredata.QueryHint) ViewOption {
	return func(spec *coredata.ViewSpec) {
		spec.QueryHints = append([]coredata.QueryHint(nil), hints...)
	}
}

// WithViewAnnotations sets annotations for the view.
func WithViewAnnotations(annotations map[string]string) ViewOption {
	return func(spec *coredata.ViewSpec) {
		spec.Annotations = cloneStringMap(annotations)
	}
}

func fieldsOf[T any](nested bool) []coredata.FieldSpec {
	goType := reflect.TypeOf((*T)(nil)).Elem()
	for goType.Kind() == reflect.Ptr {
		goType = goType.Elem()
	}
	if goType.Kind() != reflect.Struct {
		return nil
	}
	return fieldsFromStruct(goType, "", nested)
}

func fieldsFromStruct(goType reflect.Type, prefix string, nested bool) []coredata.FieldSpec {
	var fields []coredata.FieldSpec
	for i := 0; i < goType.NumField(); i++ {
		field := goType.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := jsonName(field)
		if name == "" || name == "-" {
			continue
		}
		path := joinedPath(prefix, name)
		if nested {
			if child, ok := childStructType(field.Type); ok {
				fields = append(fields, fieldsFromStruct(child, path, true)...)
				continue
			}
		}
		fields = append(fields, fieldSpec(field, path))
	}
	return fields
}

func fieldSpec(field reflect.StructField, name string) coredata.FieldSpec {
	return coredata.FieldSpec{
		Name:        name,
		Type:        fieldType(field.Type),
		Description: jsonSchemaDescription(field.Tag.Get("jsonschema")),
		Required:    hasTagToken(field.Tag.Get("jsonschema"), "required"),
		Searchable:  hasTagToken(field.Tag.Get("datasource"), "searchable"),
		Filterable:  hasTagToken(field.Tag.Get("datasource"), "filterable"),
		Sortable:    hasTagToken(field.Tag.Get("datasource"), "sortable"),
		Identifier:  hasTagToken(field.Tag.Get("datasource"), "id"),
		URL:         hasTagToken(field.Tag.Get("datasource"), "url"),
		Corpus:      field.Tag.Get("corpus") != "",
	}
}

func childStructType(t reflect.Type) (reflect.Type, bool) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return nil, false
	}
	return t, true
}

func jsonName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	return strings.TrimSpace(strings.Split(tag, ",")[0])
}

func fieldType(t reflect.Type) coredata.FieldType {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return coredata.FieldString
	case reflect.Bool:
		return coredata.FieldBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64:
		return coredata.FieldNumber
	case reflect.Slice, reflect.Array:
		return coredata.FieldArray
	case reflect.Map, reflect.Struct:
		return coredata.FieldObject
	default:
		return coredata.FieldAny
	}
}

func jsonSchemaDescription(tag string) string {
	for _, token := range splitTagTokens(tag) {
		if before, after, ok := strings.Cut(token, "="); ok && strings.TrimSpace(before) == "description" {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

func hasTagToken(tag, want string) bool {
	for _, token := range splitTagTokens(tag) {
		if strings.TrimSpace(token) == want {
			return true
		}
	}
	return false
}

func splitTagTokens(tag string) []string {
	var tokens []string
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
		tokens = append(tokens, tag[:i])
		if i >= len(tag) {
			break
		}
		tag = tag[i+1:]
	}
	return tokens
}

func joinedPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
