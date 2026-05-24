package data

import (
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

// SourceFromDatasource adapts existing datasource entity specs into a generic
// data source spec.
func SourceFromDatasource(name coredata.SourceName, kind string, entities []coredatasource.EntitySpec, views ...coredata.ViewSpec) coredata.SourceSpec {
	out := coredata.SourceSpec{
		Name:     name,
		Kind:     kind,
		Entities: make([]coredata.EntitySpec, 0, len(entities)),
		Views:    append([]coredata.ViewSpec(nil), views...),
	}
	for _, entity := range entities {
		out.Entities = append(out.Entities, EntityFromDatasource(entity))
	}
	return out
}

// EntityFromDatasource adapts one datasource entity spec into a generic data
// entity spec.
func EntityFromDatasource(entity coredatasource.EntitySpec) coredata.EntitySpec {
	out := coredata.EntitySpec{
		Type:         coredata.EntityType(entity.Type),
		Description:  entity.Description,
		Capabilities: make([]coredata.EntityCapability, 0, len(entity.Capabilities)),
		Fields:       make([]coredata.FieldSpec, 0, len(entity.Fields)),
		Relations:    make([]coredata.RelationSpec, 0, len(entity.Relations)),
	}
	for _, capability := range entity.Capabilities {
		out.Capabilities = append(out.Capabilities, capabilityFromDatasource(capability))
	}
	for _, field := range entity.Fields {
		out.Fields = append(out.Fields, coredata.FieldSpec{
			Name:        field.Name,
			Type:        fieldTypeFromDatasource(field.Type),
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
	for _, relation := range entity.Relations {
		out.Relations = append(out.Relations, coredata.RelationSpec{
			Name:         coredata.RelationName(relation.Name),
			Description:  relation.Description,
			SourceEntity: coredata.EntityType(entity.Type),
			TargetEntity: coredata.EntityType(relation.TargetEntity),
			Exact:        relation.Exact,
		})
	}
	return out
}

func capabilityFromDatasource(capability coredatasource.EntityCapability) coredata.EntityCapability {
	switch capability {
	case coredatasource.EntityCapabilitySearch:
		return coredata.CapabilitySearch
	case coredatasource.EntityCapabilityList:
		return coredata.CapabilityList
	case coredatasource.EntityCapabilityGet:
		return coredata.CapabilityGet
	case coredatasource.EntityCapabilityRelation:
		return coredata.CapabilityRelation
	case coredatasource.EntityCapabilityIndex:
		return coredata.CapabilityMaterialize
	case coredatasource.EntityCapabilitySemanticSearch:
		return coredata.CapabilitySemanticSearch
	default:
		return coredata.EntityCapability(capability)
	}
}

func fieldTypeFromDatasource(typ coredatasource.FieldType) coredata.FieldType {
	switch typ {
	case coredatasource.FieldString:
		return coredata.FieldString
	case coredatasource.FieldNumber:
		return coredata.FieldNumber
	case coredatasource.FieldBoolean:
		return coredata.FieldBoolean
	case coredatasource.FieldObject:
		return coredata.FieldObject
	case coredatasource.FieldArray:
		return coredata.FieldArray
	case coredatasource.FieldAny:
		return coredata.FieldAny
	default:
		return coredata.FieldType(typ)
	}
}
