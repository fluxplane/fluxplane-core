package datasource

import (
	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	runtimedata "github.com/fluxplane/agentruntime/runtime/data"
)

// EntityOf derives a datasource entity spec from exported fields of T.
func EntityOf[T any](typ coredatasource.EntityType, description string) coredatasource.EntitySpec {
	dataSpec := runtimedata.SourceEntityOf[T](coredata.EntityType(typ), description)
	spec := coredatasource.EntitySpec{Type: typ, Description: description, Fields: make([]coredatasource.FieldSpec, 0, len(dataSpec.Fields))}
	for _, field := range dataSpec.Fields {
		spec.Fields = append(spec.Fields, coredatasource.FieldSpec{
			Name:        field.Name,
			Type:        datasourceFieldType(field.Type),
			Description: field.Description,
			Required:    field.Required,
			Searchable:  field.Searchable,
			Filterable:  field.Filterable,
			Sortable:    field.Sortable,
			Identifier:  field.Identifier,
			URL:         field.URL,
			Corpus:      field.Corpus,
		})
	}
	return spec
}

func datasourceFieldType(typ coredata.FieldType) coredatasource.FieldType {
	switch typ {
	case coredata.FieldString:
		return coredatasource.FieldString
	case coredata.FieldBoolean:
		return coredatasource.FieldBoolean
	case coredata.FieldNumber:
		return coredatasource.FieldNumber
	case coredata.FieldArray:
		return coredatasource.FieldArray
	case coredata.FieldObject:
		return coredatasource.FieldObject
	case coredata.FieldAny:
		return coredatasource.FieldAny
	default:
		return coredatasource.FieldType(typ)
	}
}
