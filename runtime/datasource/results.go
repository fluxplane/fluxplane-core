package datasource

import (
	"fmt"
	"strconv"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

// SelectEntities resolves requested entity types against a provider's available
// entity specs while preserving request order.
func SelectEntities(provider string, available []coredatasource.EntitySpec, requested []coredatasource.EntityType) ([]coredatasource.EntitySpec, error) {
	byType := map[coredatasource.EntityType]coredatasource.EntitySpec{}
	for _, entity := range available {
		byType[entity.Type] = entity
	}
	out := make([]coredatasource.EntitySpec, 0, len(requested))
	for _, typ := range requested {
		entity, ok := byType[typ]
		if !ok {
			return nil, fmt.Errorf("unsupported %s datasource entity %q", provider, typ)
		}
		out = append(out, entity)
	}
	return out, nil
}

// HasEntity reports whether a selected entity set exposes typ.
func HasEntity(entities []coredatasource.EntitySpec, typ coredatasource.EntityType) bool {
	for _, entity := range entities {
		if entity.Type == typ {
			return true
		}
	}
	return false
}

// RecordsFrom converts provider values to datasource records.
func RecordsFrom[T any](values []T, convert func(T) coredatasource.Record) []coredatasource.Record {
	records := make([]coredatasource.Record, 0, len(values))
	for _, value := range values {
		records = append(records, convert(value))
	}
	return records
}

// NonEmptyRecordsFrom converts provider values and drops records without IDs.
func NonEmptyRecordsFrom[T any](values []T, convert func(T) coredatasource.Record) []coredatasource.Record {
	records := make([]coredatasource.Record, 0, len(values))
	for _, value := range values {
		record := convert(value)
		if record.ID != "" {
			records = append(records, record)
		}
	}
	return records
}

// RecordsToCorpusDocuments adapts normalized records into indexable corpus
// documents.
func RecordsToCorpusDocuments(records []coredatasource.Record) []coredatasource.CorpusDocument {
	documents := make([]coredatasource.CorpusDocument, 0, len(records))
	for _, record := range records {
		documents = append(documents, coredatasource.CorpusDocument{
			Ref: coredatasource.RecordRef{
				Datasource: record.Datasource,
				Entity:     record.Entity,
				ID:         record.ID,
				URL:        record.URL,
			},
			Title:    record.Title,
			Body:     record.Content,
			URL:      record.URL,
			Metadata: record.Metadata,
		})
	}
	return documents
}

// PageNextCursor returns the next one-based page cursor for page-sized results.
func PageNextCursor(count, limit, page int) string {
	if limit > 0 && count >= limit {
		return strconv.Itoa(page + 1)
	}
	return ""
}

// OffsetNextCursor returns the next offset cursor for offset-sized results.
func OffsetNextCursor(start, count, total, limit int) string {
	if limit > 0 && start+count < total && count >= limit {
		return strconv.Itoa(start + limit)
	}
	return ""
}

// SearchResult builds a normalized datasource search result.
func SearchResult(datasource coredatasource.Name, entity coredatasource.EntityType, records []coredatasource.Record, total int) coredatasource.SearchResult {
	if total < 0 {
		total = len(records)
	}
	return coredatasource.SearchResult{Datasource: datasource, Entity: entity, Records: records, Total: total}
}

// ListResult builds a normalized datasource list result with an explicit cursor.
func ListResult(datasource coredatasource.Name, entity coredatasource.EntityType, records []coredatasource.Record, total int, next string) coredatasource.ListResult {
	if total < 0 {
		total = len(records)
	}
	return coredatasource.ListResult{Datasource: datasource, Entity: entity, Records: records, Total: total, NextCursor: next, Complete: next == ""}
}

// ListResultPage builds a normalized datasource list result with page cursors.
func ListResultPage(datasource coredatasource.Name, entity coredatasource.EntityType, records []coredatasource.Record, total, limit, page int) coredatasource.ListResult {
	return ListResult(datasource, entity, records, total, PageNextCursor(total, limit, page))
}

// ListResultOffset builds a normalized datasource list result with offset cursors.
func ListResultOffset(datasource coredatasource.Name, entity coredatasource.EntityType, records []coredatasource.Record, total, start, limit int) coredatasource.ListResult {
	return ListResult(datasource, entity, records, total, OffsetNextCursor(start, len(records), total, limit))
}

// RelationResult builds a normalized datasource relation result with an explicit cursor.
func RelationResult(datasource coredatasource.Name, req coredatasource.RelationRequest, target coredatasource.EntityType, records []coredatasource.Record, total int, next string, exact bool) coredatasource.RelationResult {
	if total < 0 {
		total = len(records)
	}
	return coredatasource.RelationResult{
		Datasource:   datasource,
		Entity:       req.Entity,
		ID:           req.ID,
		Relation:     req.Relation,
		TargetEntity: target,
		Records:      records,
		Total:        total,
		NextCursor:   next,
		Complete:     next == "",
		Exact:        exact,
	}
}

// RelationResultPage builds a normalized datasource relation result with page cursors.
func RelationResultPage(datasource coredatasource.Name, req coredatasource.RelationRequest, target coredatasource.EntityType, records []coredatasource.Record, total, limit, page int, exact bool) coredatasource.RelationResult {
	return RelationResult(datasource, req, target, records, total, PageNextCursor(total, limit, page), exact)
}

// CorpusPageFromRecords builds an indexable corpus page from normalized records.
func CorpusPageFromRecords(records []coredatasource.Record, count, limit, page int) coredatasource.CorpusPage {
	next := PageNextCursor(count, limit, page)
	return coredatasource.CorpusPage{Documents: RecordsToCorpusDocuments(records), NextCursor: next, Complete: next == ""}
}
