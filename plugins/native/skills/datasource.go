package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreskill "github.com/fluxplane/agentruntime/core/skill"
	runtimeskill "github.com/fluxplane/agentruntime/runtime/skill"
)

type datasourceProvider struct {
	repo *runtimeskill.Repository
}

func (p datasourceProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{skillEntitySpec(), referenceEntitySpec()}
}

func (p datasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if spec.Kind != DatasourceName && spec.Name != DatasourceName {
		return nil, fmt.Errorf("skillplugin: unsupported datasource kind %q", spec.Kind)
	}
	return datasourceAccessor{spec: spec, repo: p.repo}, nil
}

type datasourceAccessor struct {
	spec coredatasource.Spec
	repo *runtimeskill.Repository
}

func (a datasourceAccessor) Spec() coredatasource.Spec { return a.spec }
func (a datasourceAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{skillEntitySpec(), referenceEntitySpec()}
}

func (a datasourceAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	repo, state := a.repoForContext(ctx)
	if repo == nil {
		return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: req.Entity}, nil
	}
	entity := normalizeEntity(req.Entity)
	query := strings.ToLower(strings.TrimSpace(req.Query))
	limit := req.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	var records []coredatasource.Record
	for _, spec := range repo.List() {
		if entity == "" || entity == SkillEntity {
			record := skillRecord(a.spec.Name, spec, state)
			if recordMatches(record, query, spec.Triggers) {
				records = append(records, record)
			}
		}
		if entity == "" || entity == ReferenceEntity {
			for _, ref := range spec.References {
				record := referenceRecord(a.spec.Name, spec, ref, state)
				if recordMatches(record, query, ref.Triggers) {
					records = append(records, record)
				}
			}
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	total := len(records)
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	if entity == "" {
		entity = SkillEntity
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: entity, Records: records, Total: total}, nil
}

func (a datasourceAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	repo, state := a.repoForContext(ctx)
	if repo == nil {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	entity := normalizeEntity(req.Entity)
	id := strings.TrimSpace(req.ID)
	if entity == "" || entity == SkillEntity {
		if !strings.Contains(id, ":") {
			spec, ok := repo.Get(id)
			if !ok {
				return coredatasource.Record{}, coredatasource.ErrNotFound
			}
			return skillRecord(a.spec.Name, spec, state), nil
		}
	}
	if entity == "" || entity == ReferenceEntity {
		skillName, refPath, ok := strings.Cut(id, ":")
		if !ok {
			return coredatasource.Record{}, coredatasource.ErrNotFound
		}
		spec, ok := repo.Get(skillName)
		if !ok {
			return coredatasource.Record{}, coredatasource.ErrNotFound
		}
		ref, ok := repo.GetReference(skillName, refPath)
		if !ok {
			return coredatasource.Record{}, coredatasource.ErrNotFound
		}
		return referenceRecord(a.spec.Name, spec, ref, state), nil
	}
	return coredatasource.Record{}, coredatasource.ErrNotFound
}

func (a datasourceAccessor) repoForContext(ctx context.Context) (*runtimeskill.Repository, *runtimeskill.ActivationState) {
	if state, ok := runtimeskill.StateFromContext(ctx); ok {
		return state.Repository(), state
	}
	return a.repo, nil
}

func skillEntitySpec() coredatasource.EntitySpec {
	return coredatasource.EntitySpec{
		Type:         SkillEntity,
		Description:  "Agent skill.",
		Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet},
		Fields: []coredatasource.FieldSpec{
			{Name: "id", Type: coredatasource.FieldString, Identifier: true, Filterable: true},
			{Name: "name", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
			{Name: "description", Type: coredatasource.FieldString, Searchable: true},
			{Name: "content", Type: coredatasource.FieldString, Searchable: true},
			{Name: "status", Type: coredatasource.FieldString, Filterable: true},
		},
	}
}

func referenceEntitySpec() coredatasource.EntitySpec {
	return coredatasource.EntitySpec{
		Type:         ReferenceEntity,
		Description:  "Skill reference.",
		Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet},
		Fields: []coredatasource.FieldSpec{
			{Name: "id", Type: coredatasource.FieldString, Identifier: true, Filterable: true},
			{Name: "skill", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
			{Name: "path", Type: coredatasource.FieldString, Searchable: true, Filterable: true},
			{Name: "content", Type: coredatasource.FieldString, Searchable: true},
		},
	}
}

func skillRecord(datasource coredatasource.Name, spec coreskill.Spec, state *runtimeskill.ActivationState) coredatasource.Record {
	status := runtimeskill.StatusInactive
	if state != nil {
		status = state.Status(string(spec.Name))
	}
	metadata := map[string]string{
		"name":   string(spec.Name),
		"status": string(status),
	}
	if spec.Source.URI != "" {
		metadata["source"] = spec.Source.URI
	}
	if len(spec.Triggers) > 0 {
		metadata["triggers"] = strings.Join(spec.Triggers, ",")
	}
	return coredatasource.Record{
		ID:         string(spec.Name),
		Datasource: datasource,
		Entity:     SkillEntity,
		Title:      string(spec.Name),
		Content:    strings.TrimSpace(strings.Join([]string{spec.Description, spec.Body}, "\n\n")),
		URL:        spec.Source.URI,
		Metadata:   metadata,
	}
}

func referenceRecord(datasource coredatasource.Name, spec coreskill.Spec, ref coreskill.ReferenceSpec, state *runtimeskill.ActivationState) coredatasource.Record {
	status := "inactive"
	if state != nil {
		for _, active := range state.ActiveReferences(string(spec.Name)) {
			if active.Path == ref.Path {
				status = "active"
				break
			}
		}
	}
	metadata := map[string]string{
		"skill":  string(spec.Name),
		"path":   ref.Path,
		"status": status,
	}
	if len(ref.Triggers) > 0 {
		metadata["triggers"] = strings.Join(ref.Triggers, ",")
	}
	return coredatasource.Record{
		ID:         string(spec.Name) + ":" + ref.Path,
		Datasource: datasource,
		Entity:     ReferenceEntity,
		Title:      string(spec.Name) + " " + ref.Path,
		Content:    strings.TrimSpace(ref.Body),
		URL:        spec.Source.URI,
		Metadata:   metadata,
	}
}

func normalizeEntity(entity coredatasource.EntityType) coredatasource.EntityType {
	switch entity {
	case "", SkillEntity, ReferenceEntity:
		return entity
	default:
		return entity
	}
}

func recordMatches(record coredatasource.Record, query string, triggers []string) bool {
	if query == "" {
		return true
	}
	haystack := strings.ToLower(record.ID + "\n" + record.Title + "\n" + record.Content + "\n" + strings.Join(triggers, "\n"))
	for _, value := range record.Metadata {
		haystack += "\n" + strings.ToLower(value)
	}
	return strings.Contains(haystack, query)
}
