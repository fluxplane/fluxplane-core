package data

import (
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

// RecordFromCorpusDocument converts provider corpus data into the durable data
// mirror record shape used by datasource tools.
func RecordFromCorpusDocument(doc coredatasource.CorpusDocument, entity coredatasource.EntitySpec) coredata.Record {
	source := coredata.SourceName(doc.Ref.Datasource)
	entityType := coredata.EntityType(doc.Ref.Entity)
	fields := map[string][]string{}
	add := func(name string, values ...string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			fields[name] = appendUniqueString(fields[name], value)
		}
	}
	add("id", doc.Ref.ID)
	add("title", doc.Title)
	add("body", doc.Body)
	add("url", firstNonEmpty(doc.URL, doc.Ref.URL))
	for key, value := range doc.Metadata {
		add(key, value)
	}
	for _, field := range entity.Fields {
		if field.Identifier {
			add(field.Name, fields["id"]...)
		}
	}
	return coredata.Record{
		Ref: coredata.Ref{
			Source: source,
			Entity: entityType,
			View:   coredata.ViewName(entityType),
			ID:     coredata.RecordID(strings.TrimSpace(doc.Ref.ID)),
		},
		Title:       doc.Title,
		Content:     doc.Body,
		URL:         firstNonEmpty(doc.URL, doc.Ref.URL),
		Fields:      fields,
		Metadata:    cloneMetadata(doc.Metadata),
		Fingerprint: strings.TrimSpace(doc.Fingerprint),
		UpdatedAt:   strings.TrimSpace(doc.UpdatedAt),
	}
}

// RefFromDatasourceRef converts a datasource ref into a data-store ref.
func RefFromDatasourceRef(ref coredatasource.RecordRef) coredata.Ref {
	entity := coredata.EntityType(ref.Entity)
	return coredata.Ref{
		Source: coredata.SourceName(ref.Datasource),
		Entity: entity,
		View:   coredata.ViewName(entity),
		ID:     coredata.RecordID(strings.TrimSpace(ref.ID)),
	}
}

// RecordToDatasourceRecord converts a durable data record into the model-facing
// datasource record shape.
func RecordToDatasourceRecord(record coredata.Record) coredatasource.Record {
	metadata := cloneMetadata(record.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	for key, values := range record.Fields {
		if _, exists := metadata[key]; exists || len(values) == 0 {
			continue
		}
		metadata[key] = strings.Join(values, ",")
	}
	return coredatasource.Record{
		ID:         string(record.Ref.ID),
		Datasource: coredatasource.Name(record.Ref.Source),
		Entity:     coredatasource.EntityType(record.Ref.Entity),
		Title:      record.Title,
		Content:    record.Content,
		URL:        record.URL,
		Score:      record.Score,
		Metadata:   metadata,
		Raw:        record.Raw,
	}
}

func RecordsToDatasourceRecords(records []coredata.Record) []coredatasource.Record {
	out := make([]coredatasource.Record, 0, len(records))
	for _, record := range records {
		out = append(out, RecordToDatasourceRecord(record))
	}
	return out
}

// RelationsFromCorpusDocument converts provider-supplied relation metadata into
// normalized data-store relation edges. Providers opt in with metadata keys of
// the form relation.<id>.source_entity, relation.<id>.source_id,
// relation.<id>.name, relation.<id>.target_entity, relation.<id>.target_id,
// plus optional relation.<id>.target_title and relation.<id>.target_field.<name>.
func RelationsFromCorpusDocument(doc coredatasource.CorpusDocument) []coredata.Relation {
	type relationDoc struct {
		sourceEntity string
		sourceID     string
		name         string
		targetEntity string
		targetID     string
		targetTitle  string
		targetFields map[string]string
	}
	byID := map[string]*relationDoc{}
	for key, value := range doc.Metadata {
		rest, ok := strings.CutPrefix(key, "relation.")
		if !ok {
			continue
		}
		id, field, ok := strings.Cut(rest, ".")
		if !ok || id == "" || field == "" {
			continue
		}
		current := byID[id]
		if current == nil {
			current = &relationDoc{targetFields: map[string]string{}}
			byID[id] = current
		}
		switch {
		case field == "source_entity":
			current.sourceEntity = value
		case field == "source_id":
			current.sourceID = value
		case field == "name":
			current.name = value
		case field == "target_entity":
			current.targetEntity = value
		case field == "target_id":
			current.targetID = value
		case field == "target_title":
			current.targetTitle = value
		case strings.HasPrefix(field, "target_field."):
			name := strings.TrimPrefix(field, "target_field.")
			if name != "" && strings.TrimSpace(value) != "" {
				current.targetFields[name] = strings.TrimSpace(value)
			}
		}
	}
	var relations []coredata.Relation
	for _, item := range byID {
		if item.sourceEntity == "" || item.sourceID == "" || item.name == "" || item.targetEntity == "" || item.targetID == "" {
			continue
		}
		relations = append(relations, coredata.Relation{
			Source: coredata.Ref{
				Source: coredata.SourceName(doc.Ref.Datasource),
				Entity: coredata.EntityType(item.sourceEntity),
				View:   coredata.ViewName(item.sourceEntity),
				ID:     coredata.RecordID(item.sourceID),
			},
			Name: coredata.RelationName(item.name),
			Target: coredata.Ref{
				Source: coredata.SourceName(doc.Ref.Datasource),
				Entity: coredata.EntityType(item.targetEntity),
				View:   coredata.ViewName(item.targetEntity),
				ID:     coredata.RecordID(item.targetID),
			},
			Summary: coredata.Summary{
				Ref: coredata.Ref{
					Source: coredata.SourceName(doc.Ref.Datasource),
					Entity: coredata.EntityType(item.targetEntity),
					View:   coredata.ViewName(item.targetEntity),
					ID:     coredata.RecordID(item.targetID),
				},
				Title:  item.targetTitle,
				Fields: item.targetFields,
			},
		})
	}
	return relations
}

func appendUniqueString(values []string, candidates ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		values = append(values, candidate)
		seen[candidate] = true
	}
	return values
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
