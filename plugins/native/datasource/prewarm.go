package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

const (
	prewarmGetAnnotation       = "prewarm.get"
	prewarmSearchAnnotation    = "prewarm.search"
	prewarmRelationsAnnotation = "prewarm.relations"
	prewarmLimitAnnotation     = "prewarm.limit"
	defaultPrewarmLimit        = 10
	maxPrewarmLimit            = 20
	maxPrewarmRecords          = 20
	maxPrewarmContentBytes     = 64 * 1024
)

type prewarmProvider struct {
	registry *coredatasource.Registry
}

func (p prewarmProvider) Spec() corecontext.ProviderSpec {
	return prewarmContextSpec()
}

func (p prewarmProvider) Build(ctx context.Context, req corecontext.Request) ([]corecontext.Block, error) {
	if p.registry == nil {
		return nil, nil
	}
	input := detectionInputFromObservations(req.Observations)
	if len(input.Sources) == 0 {
		return nil, nil
	}
	accessors := allowedAccessors(ctx, p.registry)
	if len(accessors) == 0 {
		return nil, nil
	}
	refs := runtimedatasource.Detect(ctx, input, accessors, runtimedatasource.DetectOptions{MaxRefs: maxDetectedRefs})
	state := prewarmState{
		ctx:       ctx,
		registry:  p.registry,
		accessors: accessors,
		queue:     refs,
		seenRefs:  map[string]bool{},
		seenRecs:  map[string]bool{},
	}
	records := state.run()
	if len(records) == 0 {
		return nil, nil
	}
	content := truncatePrewarmContent(renderPrewarmedRecords(records))
	data, _ := json.Marshal(records)
	return []corecontext.Block{{
		ID:        PrewarmProvider,
		Provider:  PrewarmProvider,
		Kind:      corecontext.BlockText,
		Title:     "Prewarmed Datasource Context",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
		Metadata: map[string]string{
			"records": string(data),
		},
	}}, nil
}

type prewarmState struct {
	ctx       context.Context
	registry  *coredatasource.Registry
	accessors []coredatasource.Accessor
	queue     []coredatasource.RecordRef
	seenRefs  map[string]bool
	seenRecs  map[string]bool
	records   []coredatasource.Record
}

func (s *prewarmState) run() []coredatasource.Record {
	for len(s.queue) > 0 && len(s.records) < maxPrewarmRecords {
		ref := s.queue[0]
		s.queue = s.queue[1:]
		if s.ctx.Err() != nil || !s.markRef(ref) || !refPrewarmEnabled(ref) {
			continue
		}
		records := s.fetch(ref)
		for _, record := range records {
			if len(s.records) >= maxPrewarmRecords {
				break
			}
			s.addRecord(record, ref)
		}
	}
	return s.records
}

func (s *prewarmState) fetch(ref coredatasource.RecordRef) []coredatasource.Record {
	accessor, entity, ok := s.accessorEntity(ref)
	if !ok {
		return nil
	}
	if ref.ID != "" && truthy(ref.Annotations[prewarmGetAnnotation]) && entitySupports(accessor, entity, coredatasource.EntityCapabilityGet) {
		if getter, ok := accessor.(coredatasource.Getter); ok {
			record, err := getter.Get(s.ctx, coredatasource.GetRequest{Entity: ref.Entity, ID: ref.ID})
			if err == nil {
				return []coredatasource.Record{record}
			}
		}
	}
	if ref.Query != "" && truthy(ref.Annotations[prewarmSearchAnnotation]) && entitySupports(accessor, entity, coredatasource.EntityCapabilitySearch) {
		if searcher, ok := accessor.(coredatasource.Searcher); ok {
			result, err := searcher.Search(s.ctx, coredatasource.SearchRequest{Entity: ref.Entity, Query: ref.Query, Limit: 1})
			if err == nil {
				return result.Records
			}
		}
	}
	return nil
}

func (s *prewarmState) addRecord(record coredatasource.Record, source coredatasource.RecordRef) {
	if record.Datasource == "" {
		record.Datasource = source.Datasource
	}
	if record.Entity == "" {
		record.Entity = source.Entity
	}
	if record.ID == "" {
		record.ID = source.ID
	}
	key := recordKey(record)
	if key == "" || s.seenRecs[key] {
		return
	}
	s.seenRecs[key] = true
	record.Links = removeSelfLinks(record, runtimedatasource.Detect(s.ctx, coredatasource.DetectionInput{
		Sources: []coredatasource.DetectionSource{recordDetectionSource(record)},
		MaxRefs: maxDetectedRefs,
	}, s.accessors, runtimedatasource.DetectOptions{MaxRefs: maxDetectedRefs}))
	s.records = append(s.records, record)
	s.enqueue(record.Links...)
	for _, relation := range prewarmRelations(source) {
		s.addRelationRecords(record, relation, prewarmLimit(source))
	}
}

func (s *prewarmState) addRelationRecords(record coredatasource.Record, relation string, limit int) {
	accessor, entity, ok := s.accessorEntity(coredatasource.RecordRef{Datasource: record.Datasource, Entity: record.Entity})
	if !ok || !entityHasRelation(entity, relation) {
		return
	}
	relationer, ok := accessor.(coredatasource.Relationer)
	if !ok {
		return
	}
	result, err := relationer.Relation(s.ctx, coredatasource.RelationRequest{
		Entity:   record.Entity,
		ID:       record.ID,
		Relation: relation,
		Limit:    limit,
	})
	if err != nil {
		return
	}
	for _, related := range result.Records {
		if len(s.records) >= maxPrewarmRecords {
			return
		}
		s.addRecord(related, coredatasource.RecordRef{Datasource: result.Datasource, Entity: result.TargetEntity, ID: related.ID})
	}
}

func (s *prewarmState) enqueue(refs ...coredatasource.RecordRef) {
	for _, ref := range refs {
		if refPrewarmEnabled(ref) && !s.seenRefs[prewarmRefKey(ref)] {
			s.queue = append(s.queue, ref)
		}
	}
}

func (s *prewarmState) markRef(ref coredatasource.RecordRef) bool {
	key := prewarmRefKey(ref)
	if key == "" || s.seenRefs[key] {
		return false
	}
	s.seenRefs[key] = true
	return true
}

func (s *prewarmState) accessorEntity(ref coredatasource.RecordRef) (coredatasource.Accessor, coredatasource.EntitySpec, bool) {
	if ref.Datasource == "" || ref.Entity == "" {
		return nil, coredatasource.EntitySpec{}, false
	}
	accessor, ok := s.registry.Get(ref.Datasource)
	if !ok {
		return nil, coredatasource.EntitySpec{}, false
	}
	entity, ok := accessorEntity(accessor, ref.Entity)
	if !ok {
		return nil, coredatasource.EntitySpec{}, false
	}
	return accessor, entity, true
}

func renderPrewarmedRecords(records []coredatasource.Record) string {
	lines := []string{"Prewarmed datasource records from detected references:"}
	for _, record := range records {
		header := fmt.Sprintf("- %s %s %s", record.Datasource, record.Entity, record.ID)
		if record.Title != "" && record.Title != record.ID {
			header += " - " + compactInline(record.Title, 160)
		}
		lines = append(lines, header)
		body := strings.TrimSpace(renderRecord(record))
		if body != "" {
			lines = append(lines, indentPrewarmBody(body))
		}
	}
	return strings.Join(lines, "\n")
}

func indentPrewarmBody(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func refPrewarmEnabled(ref coredatasource.RecordRef) bool {
	return truthy(ref.Annotations[prewarmGetAnnotation]) || truthy(ref.Annotations[prewarmSearchAnnotation])
}

func prewarmRelations(ref coredatasource.RecordRef) []string {
	raw := strings.TrimSpace(ref.Annotations[prewarmRelationsAnnotation])
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if relation := strings.TrimSpace(part); relation != "" {
			out = append(out, relation)
		}
	}
	sort.Strings(out)
	return out
}

func prewarmLimit(ref coredatasource.RecordRef) int {
	limit := defaultPrewarmLimit
	if raw := strings.TrimSpace(ref.Annotations[prewarmLimitAnnotation]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > maxPrewarmLimit {
		return maxPrewarmLimit
	}
	return limit
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func prewarmRefKey(ref coredatasource.RecordRef) string {
	return strings.Join([]string{string(ref.Datasource), string(ref.Entity), ref.ID, ref.Query, ref.URL}, "\x00")
}

func recordKey(record coredatasource.Record) string {
	return strings.Join([]string{string(record.Datasource), string(record.Entity), record.ID}, "\x00")
}

func truncatePrewarmContent(content string) string {
	if len(content) <= maxPrewarmContentBytes {
		return content
	}
	return content[:maxPrewarmContentBytes] + "\n[prewarmed context truncated]"
}
