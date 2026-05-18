package gitlabplugin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/runtime/system"
)

type gitlabDatasourceProvider struct {
	system        system.System
	ref           resource.PluginRef
	config        Config
	clientFactory gitlabClientFactory
}

func (p gitlabDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{projectEntitySpec()}
}

func (p gitlabDatasourceProvider) Open(ctx context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if strings.TrimSpace(spec.Kind) != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	if !supportsProjectEntity(spec.Entities) {
		return nil, fmt.Errorf("unsupported gitlab datasource entities")
	}
	instance := strings.TrimSpace(spec.Config["instance"])
	if instance != "" && instance != p.ref.InstanceName() {
		return nil, fmt.Errorf("gitlab datasource instance %q does not match plugin instance %q", instance, p.ref.InstanceName())
	}
	return gitlabAccessor{
		spec:          spec,
		system:        p.system,
		config:        p.config,
		clientFactory: p.clientFactory,
		entity:        projectEntitySpec(),
	}, nil
}

type gitlabAccessor struct {
	spec          coredatasource.Spec
	system        system.System
	config        Config
	clientFactory gitlabClientFactory
	entity        coredatasource.EntitySpec
}

func (a gitlabAccessor) Spec() coredatasource.Spec { return a.spec }

func (a gitlabAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func (a gitlabAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if req.Entity != "" && req.Entity != ProjectEntity {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	projects, err := searchProjects(ctx, client, req.Query, req.Limit, 1)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	records := make([]coredatasource.Record, 0, len(projects))
	for _, project := range projects {
		records = append(records, projectRecord(a.spec.Name, project))
	}
	return coredatasource.SearchResult{
		Datasource: a.spec.Name,
		Entity:     ProjectEntity,
		Records:    records,
		Total:      len(records),
	}, nil
}

func (a gitlabAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if req.Entity != "" && req.Entity != ProjectEntity {
		return coredatasource.Record{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.Record{}, err
	}
	project, err := getProject(ctx, client, req.ID)
	if err != nil {
		return coredatasource.Record{}, err
	}
	return projectRecord(a.spec.Name, project), nil
}

func (a gitlabAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	result := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: ProjectEntity}
	for _, id := range req.IDs {
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: req.Entity, ID: id})
		if err != nil {
			result.Errors = append(result.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		result.Records = append(result.Records, record)
	}
	return result, nil
}

func (a gitlabAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	page := 1
	if req.Cursor != "" {
		parsed, err := strconv.Atoi(req.Cursor)
		if err != nil {
			return coredatasource.CorpusPage{}, fmt.Errorf("invalid cursor %q", req.Cursor)
		}
		page = parsed
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	projects, err := searchProjects(ctx, client, "", limit, page)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	documents := make([]coredatasource.CorpusDocument, 0, len(projects))
	for _, project := range projects {
		documents = append(documents, coredatasource.CorpusDocument{
			Ref: coredatasource.RecordRef{
				Datasource: a.spec.Name,
				Entity:     ProjectEntity,
				ID:         projectIDString(project),
				URL:        project.WebURL,
			},
			Title: projectTitle(project),
			Body:  project.Description,
			URL:   project.WebURL,
			Metadata: map[string]string{
				"visibility":     project.Visibility,
				"default_branch": project.DefaultBranch,
				"namespace":      project.PathWithNamespace,
			},
		})
	}
	next := ""
	if len(projects) == limit {
		next = strconv.Itoa(page + 1)
	}
	return coredatasource.CorpusPage{Documents: documents, NextCursor: next, Complete: next == ""}, nil
}

func (a gitlabAccessor) client(ctx context.Context) (gitlabClient, error) {
	factory := a.clientFactory
	if factory == nil {
		factory = newOfficialClient
	}
	return factory(ctx, a.system, a.config)
}

func supportsProjectEntity(entities []coredatasource.EntityType) bool {
	for _, entity := range entities {
		if entity == ProjectEntity {
			return true
		}
	}
	return false
}

func projectRecord(datasource coredatasource.Name, project Project) coredatasource.Record {
	return coredatasource.Record{
		ID:         projectIDString(project),
		Datasource: datasource,
		Entity:     ProjectEntity,
		Title:      projectTitle(project),
		Content:    project.Description,
		URL:        project.WebURL,
		Metadata: map[string]string{
			"visibility":     project.Visibility,
			"default_branch": project.DefaultBranch,
			"namespace":      project.PathWithNamespace,
		},
		Raw: project,
	}
}

func projectIDString(project Project) string {
	if project.PathWithNamespace != "" {
		return project.PathWithNamespace
	}
	if project.ID != 0 {
		return strconv.FormatInt(project.ID, 10)
	}
	return ""
}

func projectTitle(project Project) string {
	if project.PathWithNamespace != "" {
		return project.PathWithNamespace
	}
	return project.Name
}
