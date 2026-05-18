package datasource

import (
	"reflect"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

// EntityOf derives a datasource entity spec from exported fields of T.
func EntityOf[T any](typ coredatasource.EntityType, description string) coredatasource.EntitySpec {
	goType := reflect.TypeOf((*T)(nil)).Elem()
	for goType.Kind() == reflect.Ptr {
		goType = goType.Elem()
	}
	spec := coredatasource.EntitySpec{Type: typ, Description: description}
	if goType.Kind() != reflect.Struct {
		return spec
	}
	for i := 0; i < goType.NumField(); i++ {
		field := goType.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := jsonName(field)
		if name == "" || name == "-" {
			continue
		}
		spec.Fields = append(spec.Fields, coredatasource.FieldSpec{
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
		})
	}
	return spec
}

func jsonName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	return strings.TrimSpace(strings.Split(tag, ",")[0])
}

func fieldType(t reflect.Type) coredatasource.FieldType {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return coredatasource.FieldString
	case reflect.Bool:
		return coredatasource.FieldBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64:
		return coredatasource.FieldNumber
	case reflect.Slice, reflect.Array:
		return coredatasource.FieldArray
	case reflect.Map, reflect.Struct:
		return coredatasource.FieldObject
	default:
		return coredatasource.FieldAny
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
