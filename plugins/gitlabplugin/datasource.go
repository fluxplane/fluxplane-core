package gitlabplugin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

const defaultPageSize = 20

type gitlabDatasourceProvider struct {
	system        system.System
	ref           resource.PluginRef
	config        Config
	clientFactory gitlabClientFactory
	index         *semantic.Index
}

func (p gitlabDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p gitlabDatasourceProvider) WithSemanticIndex(index *semantic.Index) coredatasource.Provider {
	p.index = index
	return p
}

func (p gitlabDatasourceProvider) Open(ctx context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if strings.TrimSpace(spec.Kind) != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	entities, err := selectedEntities(spec.Entities)
	if err != nil {
		return nil, err
	}
	instance := strings.TrimSpace(spec.Config["instance"])
	if instance != "" && instance != p.ref.InstanceName() {
		return nil, fmt.Errorf("gitlab datasource instance %q does not match plugin instance %q", instance, p.ref.InstanceName())
	}
	return gitlabAccessor{
		spec:          spec,
		system:        p.system,
		ref:           p.ref,
		config:        p.config,
		clientFactory: p.clientFactory,
		index:         p.index,
		entities:      entities,
	}, nil
}

type gitlabAccessor struct {
	spec          coredatasource.Spec
	system        system.System
	ref           resource.PluginRef
	config        Config
	clientFactory gitlabClientFactory
	index         *semantic.Index
	entities      []coredatasource.EntitySpec
}

func (a gitlabAccessor) Spec() coredatasource.Spec { return a.spec }

func (a gitlabAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a gitlabAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = ProjectEntity
	}
	if !a.hasEntity(entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	if a.spec.Index.Enabled && req.Mode != "provider" && req.Mode != "live" {
		return a.indexSearch(ctx, entity, req)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	switch entity {
	case ProjectEntity:
		projects, err := searchProjects(ctx, client, req.Query, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(projects, a.projectRecord)), nil
	case MergeRequestEntity:
		mrs, err := searchMergeRequests(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(mrs, a.mergeRequestRecord)), nil
	case MergeRequestDiffEntity:
		diffs, err := searchMergeRequestDiffs(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(diffs, a.diffRecord)), nil
	case MergeRequestNoteEntity:
		notes, err := searchMergeRequestNotes(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(notes, a.noteRecord)), nil
	case PipelineEntity:
		pipelines, err := searchPipelines(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(pipelines, a.pipelineRecord)), nil
	case UserEntity:
		users, err := searchUsers(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(users, a.userRecord)), nil
	case GroupEntity:
		groups, err := searchGroups(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return a.searchResult(entity, recordsFrom(groups, a.groupRecord)), nil
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q entity %q does not support search", a.spec.Name, entity)
	}
}

func (a gitlabAccessor) indexSearch(ctx context.Context, entity coredatasource.EntityType, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if a.index == nil {
		return coredatasource.SearchResult{}, fmt.Errorf("gitlab datasource %q index is not configured; run agentsdk datasource index build", a.spec.Name)
	}
	if len(req.Filters) > 0 {
		return coredatasource.SearchResult{}, fmt.Errorf("gitlab datasource %q indexed search does not support provider filters; use mode=provider for live GitLab API search", a.spec.Name)
	}
	status, err := a.index.Status(ctx, semantic.StatusRequest{Datasource: a.spec.Name, Entity: entity})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	if len(status.Records) == 0 {
		return coredatasource.SearchResult{}, fmt.Errorf("gitlab datasource %q index is not built for %s; run agentsdk datasource index build --datasource %s --entity %s", a.spec.Name, entity, a.spec.Name, entity)
	}
	result, err := a.index.SearchFields(ctx, semantic.FieldSearchRequest{
		Query:       req.Query,
		Datasources: []coredatasource.Name{a.spec.Name},
		Entities:    []coredatasource.EntityType{entity},
		Limit:       req.Limit,
	})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	records := make([]coredatasource.Record, 0, len(result.Hits))
	for _, hit := range result.Hits {
		records = append(records, hit.Record)
	}
	return a.searchResult(entity, records), nil
}

func (a gitlabAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if !a.hasEntity(req.Entity) {
		return coredatasource.Record{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.Record{}, err
	}
	switch req.Entity {
	case ProjectEntity:
		project, err := getProject(ctx, client, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.projectRecord(project), nil
	case MergeRequestEntity:
		project, iid, err := parseMergeRequestID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		mr, err := client.GetMergeRequest(ctx, project, iid, nil)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.mergeRequestRecord(mergeRequestFromFull(mr)), nil
	case MergeRequestDiffEntity:
		project, iid, child, err := parseMergeRequestChildID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		diffs, err := listDiffs(ctx, client, project, iid, defaultPageSize)
		if err != nil {
			return coredatasource.Record{}, err
		}
		for _, diff := range diffs {
			if diff.ID == req.ID || diff.NewPath == child || diff.OldPath == child {
				return a.diffRecord(diff), nil
			}
		}
		return coredatasource.Record{}, coredatasource.ErrNotFound
	case MergeRequestNoteEntity:
		project, iid, child, err := parseMergeRequestChildID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		noteID, err := strconv.ParseInt(child, 10, 64)
		if err != nil {
			return coredatasource.Record{}, fmt.Errorf("invalid note id %q", child)
		}
		notes, err := listNotes(ctx, client, project, iid, defaultPageSize)
		if err != nil {
			return coredatasource.Record{}, err
		}
		for _, note := range notes {
			if note.ID == noteID {
				return a.noteRecord(note), nil
			}
		}
		return coredatasource.Record{}, coredatasource.ErrNotFound
	case PipelineEntity:
		project, pipelineID, err := parseProjectChildID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		pipeline, err := client.GetPipeline(ctx, project, pipelineID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.pipelineRecord(pipelineFromFull(pipeline)), nil
	case UserEntity:
		user, err := getUser(ctx, client, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.userRecord(user), nil
	case GroupEntity:
		group, err := getGroup(ctx, client, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.groupRecord(group), nil
	default:
		return coredatasource.Record{}, fmt.Errorf("datasource %q entity %q does not support get", a.spec.Name, req.Entity)
	}
}

func (a gitlabAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	result := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
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

func (a gitlabAccessor) Relation(ctx context.Context, req coredatasource.RelationRequest) (coredatasource.RelationResult, error) {
	if !a.hasEntity(req.Entity) {
		return coredatasource.RelationResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.RelationResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch req.Entity {
	case ProjectEntity:
		project := projectID(req.ID)
		switch req.Relation {
		case "merge_requests":
			mrs, err := listProjectMergeRequests(ctx, client, project, "", nil, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestEntity, recordsFrom(mrs, a.mergeRequestRecord), len(mrs), limit)
		case "pipelines":
			pipelines, err := client.ListProjectPipelines(ctx, project, &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(limit), Page: int64(cursorPage(req.Cursor))}})
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, PipelineEntity, recordsFrom(pipelinesFromInfo(pipelines), a.pipelineRecord), len(pipelines), limit)
		}
	case MergeRequestEntity:
		project, iid, err := parseMergeRequestID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "diffs":
			diffs, err := listDiffs(ctx, client, project, iid, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestDiffEntity, recordsFrom(diffs, a.diffRecord), len(diffs), limit)
		case "notes":
			notes, err := listNotes(ctx, client, project, iid, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestNoteEntity, recordsFrom(notes, a.noteRecord), len(notes), limit)
		case "pipelines":
			pipelines, err := client.ListMergeRequestPipelines(ctx, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := recordsFrom(limitPipelines(pipelinesFromInfo(pipelines), limit), a.pipelineRecord)
			return a.relationResult(req, PipelineEntity, records, len(pipelines), limit)
		case "participants":
			users, err := client.GetMergeRequestParticipants(ctx, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := recordsFrom(limitUsers(usersFromBasic(users, "participant"), limit), a.userRecord)
			return a.relationResult(req, UserEntity, records, len(users), limit)
		case "reviewers":
			reviewers, err := client.GetMergeRequestReviewers(ctx, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := recordsFrom(limitUsers(usersFromReviewers(reviewers), limit), a.userRecord)
			return a.relationResult(req, UserEntity, records, len(reviewers), limit)
		}
	case UserEntity:
		userID, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
		if err != nil {
			return coredatasource.RelationResult{}, fmt.Errorf("gitlab user id must be numeric")
		}
		switch req.Relation {
		case "groups":
			groups, err := listUserGroups(ctx, client, userID, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, GroupEntity, recordsFrom(groups, a.groupRecord), len(groups), limit)
		}
	}
	return coredatasource.RelationResult{}, fmt.Errorf("datasource %q entity %q does not expose relation %q", a.spec.Name, req.Entity, req.Relation)
}

func (a gitlabAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	entity := req.Entity
	if entity == "" {
		entity = ProjectEntity
	}
	if !a.hasEntity(entity) {
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	limit := normalizedLimit(req.Limit)
	page := cursorPage(req.Cursor)
	switch entity {
	case ProjectEntity:
		projects, err := searchProjects(ctx, client, "", limit, page)
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return corpusPage(recordsFrom(projects, a.projectRecord), len(projects), limit, page), nil
	case UserEntity:
		users, err := searchUsers(ctx, client, "", nil, limit, page)
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return corpusPage(recordsFrom(users, a.userRecord), len(users), limit, page), nil
	case GroupEntity:
		groups, err := searchGroups(ctx, client, "", nil, limit, page)
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return corpusPage(recordsFrom(groups, a.groupRecord), len(groups), limit, page), nil
	default:
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q entity %q is not indexed", a.spec.Name, entity)
	}
}

func (a gitlabAccessor) client(ctx context.Context) (gitlabClient, error) {
	factory := a.clientFactory
	if factory == nil {
		factory = newOfficialClient
	}
	return factory(ctx, a.system, a.ref, a.config)
}

func (a gitlabAccessor) hasEntity(entity coredatasource.EntityType) bool {
	for _, spec := range a.entities {
		if spec.Type == entity {
			return true
		}
	}
	return false
}

func (a gitlabAccessor) searchResult(entity coredatasource.EntityType, records []coredatasource.Record) coredatasource.SearchResult {
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: entity, Records: records, Total: len(records)}
}

func (a gitlabAccessor) relationResult(req coredatasource.RelationRequest, target coredatasource.EntityType, records []coredatasource.Record, total, limit int) (coredatasource.RelationResult, error) {
	next := ""
	if total >= limit {
		next = strconv.Itoa(cursorPage(req.Cursor) + 1)
	}
	return coredatasource.RelationResult{
		Datasource:   a.spec.Name,
		Entity:       req.Entity,
		ID:           req.ID,
		Relation:     req.Relation,
		TargetEntity: target,
		Records:      records,
		Total:        total,
		NextCursor:   next,
		Complete:     next == "",
		Exact:        true,
	}, nil
}

func (a gitlabAccessor) projectRecord(project Project) coredatasource.Record {
	return coredatasource.Record{
		ID:         projectIDString(project),
		Datasource: a.spec.Name,
		Entity:     ProjectEntity,
		Title:      projectTitle(project),
		Content:    project.Description,
		URL:        project.WebURL,
		Metadata: map[string]string{
			"id":                  strconv.FormatInt(project.ID, 10),
			"name":                project.Name,
			"path_with_namespace": project.PathWithNamespace,
			"visibility":          project.Visibility,
			"default_branch":      project.DefaultBranch,
		},
		Raw: project,
	}
}

func (a gitlabAccessor) mergeRequestRecord(mr MergeRequest) coredatasource.Record {
	return coredatasource.Record{
		ID:         mergeRequestID(mr.ProjectID, mr.IID),
		Datasource: a.spec.Name,
		Entity:     MergeRequestEntity,
		Title:      mr.Title,
		Content:    mr.Description,
		URL:        mr.WebURL,
		Metadata: map[string]string{
			"project_id":            strconv.FormatInt(mr.ProjectID, 10),
			"iid":                   strconv.FormatInt(mr.IID, 10),
			"state":                 mr.State,
			"detailed_merge_status": mr.DetailedMergeStatus,
			"source_branch":         mr.SourceBranch,
			"target_branch":         mr.TargetBranch,
			"author":                mr.AuthorUsername,
			"updated_at":            mr.UpdatedAt,
		},
		Raw: mr,
	}
}

func (a gitlabAccessor) diffRecord(diff MergeRequestDiff) coredatasource.Record {
	return coredatasource.Record{
		ID:         diff.ID,
		Datasource: a.spec.Name,
		Entity:     MergeRequestDiffEntity,
		Title:      firstNonEmpty(diff.NewPath, diff.OldPath),
		Content:    diff.Summary,
		Metadata: map[string]string{
			"project_id":        strconv.FormatInt(diff.ProjectID, 10),
			"merge_request_iid": strconv.FormatInt(diff.MergeRequest, 10),
			"new_path":          diff.NewPath,
			"old_path":          diff.OldPath,
		},
		Raw: diff,
	}
}

func (a gitlabAccessor) noteRecord(note MergeRequestNote) coredatasource.Record {
	return coredatasource.Record{
		ID:         fmt.Sprintf("%d!%d!%d", note.ProjectID, note.MergeRequestIID, note.ID),
		Datasource: a.spec.Name,
		Entity:     MergeRequestNoteEntity,
		Title:      fmt.Sprintf("MR !%d note %d", note.MergeRequestIID, note.ID),
		Content:    note.Body,
		Metadata: map[string]string{
			"project_id":        strconv.FormatInt(note.ProjectID, 10),
			"merge_request_iid": strconv.FormatInt(note.MergeRequestIID, 10),
			"author":            note.AuthorUsername,
			"created_at":        note.CreatedAt,
			"system":            strconv.FormatBool(note.System),
			"internal":          strconv.FormatBool(note.Internal),
		},
		Raw: note,
	}
}

func (a gitlabAccessor) pipelineRecord(pipeline Pipeline) coredatasource.Record {
	return coredatasource.Record{
		ID:         fmt.Sprintf("%d!%d", pipeline.ProjectID, pipeline.ID),
		Datasource: a.spec.Name,
		Entity:     PipelineEntity,
		Title:      firstNonEmpty(pipeline.Name, fmt.Sprintf("Pipeline %d", pipeline.ID)),
		Content:    strings.Join(cleaned([]string{pipeline.Status, pipeline.Source, pipeline.Ref, pipeline.SHA}), " "),
		URL:        pipeline.WebURL,
		Metadata: map[string]string{
			"project_id": strconv.FormatInt(pipeline.ProjectID, 10),
			"status":     pipeline.Status,
			"source":     pipeline.Source,
			"ref":        pipeline.Ref,
			"sha":        pipeline.SHA,
			"updated_at": pipeline.UpdatedAt,
		},
		Raw: pipeline,
	}
}

func (a gitlabAccessor) userRecord(user User) coredatasource.Record {
	return coredatasource.Record{
		ID:         strconv.FormatInt(user.ID, 10),
		Datasource: a.spec.Name,
		Entity:     UserEntity,
		Title:      firstNonEmpty(user.Username, user.Name),
		Content:    strings.Join(cleaned([]string{user.Name, user.Username, user.Role}), " "),
		URL:        user.WebURL,
		Metadata: map[string]string{
			"id":       strconv.FormatInt(user.ID, 10),
			"username": user.Username,
			"name":     user.Name,
			"state":    user.State,
			"web_url":  user.WebURL,
			"role":     user.Role,
		},
		Raw: user,
	}
}

func (a gitlabAccessor) groupRecord(group Group) coredatasource.Record {
	return coredatasource.Record{
		ID:         groupIDString(group),
		Datasource: a.spec.Name,
		Entity:     GroupEntity,
		Title:      groupTitle(group),
		Content:    strings.Join(cleaned([]string{group.Description, group.FullName, group.FullPath, group.Role}), " "),
		URL:        group.WebURL,
		Metadata: map[string]string{
			"id":          strconv.FormatInt(group.ID, 10),
			"name":        group.Name,
			"path":        group.Path,
			"full_path":   group.FullPath,
			"full_name":   group.FullName,
			"description": group.Description,
			"visibility":  group.Visibility,
			"parent_id":   strconv.FormatInt(group.ParentID, 10),
			"web_url":     group.WebURL,
			"role":        group.Role,
		},
		Raw: group,
	}
}

func searchProjects(ctx context.Context, client gitlabClient, query string, perPage, page int) ([]Project, error) {
	if client == nil {
		return nil, fmt.Errorf("gitlabplugin: client is nil")
	}
	search := strings.TrimSpace(query)
	simple := true
	var searchParam *string
	if search != "" {
		searchParam = &search
	}
	projects, err := client.ListProjects(ctx, &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
		Search:      searchParam,
		Simple:      &simple,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, projectFromGitLab(project))
	}
	return out, nil
}

func searchGroups(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]Group, error) {
	if client == nil {
		return nil, fmt.Errorf("gitlabplugin: client is nil")
	}
	opt := &gitlab.ListGroupsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(query); value != "" {
		opt.Search = &value
	}
	if value := strings.TrimSpace(filters["visibility"]); value != "" {
		visibility := gitlab.VisibilityValue(value)
		opt.Visibility = &visibility
	}
	if value := strings.TrimSpace(filters["owned"]); value != "" {
		owned, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid owned filter %q", value)
		}
		opt.Owned = &owned
	}
	if value := strings.TrimSpace(filters["top_level_only"]); value != "" {
		topLevelOnly, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid top_level_only filter %q", value)
		}
		opt.TopLevelOnly = &topLevelOnly
	}
	if value := strings.TrimSpace(filters["active"]); value != "" {
		active, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid active filter %q", value)
		}
		opt.Active = &active
	}
	if value := strings.TrimSpace(filters["archived"]); value != "" {
		archived, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid archived filter %q", value)
		}
		opt.Archived = &archived
	}
	groups, err := client.ListGroups(ctx, opt)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		out = append(out, groupFromGitLab(group))
	}
	return out, nil
}

func getGroup(ctx context.Context, client gitlabClient, id string) (Group, error) {
	if client == nil {
		return Group{}, fmt.Errorf("gitlabplugin: client is nil")
	}
	group, err := client.GetGroup(ctx, projectID(id), nil)
	if err != nil {
		return Group{}, err
	}
	return groupFromGitLab(group), nil
}

func getProject(ctx context.Context, client gitlabClient, id string) (Project, error) {
	if client == nil {
		return Project{}, fmt.Errorf("gitlabplugin: client is nil")
	}
	project, err := client.GetProject(ctx, projectID(id), nil)
	if err != nil {
		return Project{}, err
	}
	return projectFromGitLab(project), nil
}

func listUserGroups(ctx context.Context, client gitlabClient, userID int64, perPage, page int) ([]Group, error) {
	sourceType := "Namespace"
	memberships, err := client.GetUserMemberships(ctx, userID, &gitlab.GetUserMembershipOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
		Type:        &sourceType,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(memberships))
	for _, membership := range memberships {
		if membership == nil || !strings.EqualFold(membership.SourceType, "Namespace") {
			continue
		}
		out = append(out, groupFromUserMembership(membership))
	}
	return out, nil
}

func searchUsers(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]User, error) {
	if client == nil {
		return nil, fmt.Errorf("gitlabplugin: client is nil")
	}
	opt := &gitlab.ListUsersOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(query); value != "" {
		opt.Search = &value
	}
	if value := strings.TrimSpace(filters["username"]); value != "" {
		opt.Username = &value
	}
	if value := strings.TrimSpace(filters["public_email"]); value != "" {
		opt.PublicEmail = &value
	}
	users, err := client.ListUsers(ctx, opt)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(users))
	for _, user := range users {
		out = append(out, userFromGitLab(user))
	}
	return out, nil
}

func getUser(ctx context.Context, client gitlabClient, id string) (User, error) {
	if client == nil {
		return User{}, fmt.Errorf("gitlabplugin: client is nil")
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil {
		return User{}, fmt.Errorf("gitlab user id must be numeric")
	}
	user, err := client.GetUser(ctx, userID, nil)
	if err != nil {
		return User{}, err
	}
	return userFromGitLab(user), nil
}

func searchMergeRequests(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]MergeRequest, error) {
	project := firstNonEmpty(filters["project_id"], filters["project"])
	if project != "" {
		return listProjectMergeRequests(ctx, client, projectID(project), query, filters, perPage, page)
	}
	return listMergeRequests(ctx, client, query, filters, perPage, page)
}

func listMergeRequests(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]MergeRequest, error) {
	opt := mergeRequestListOptions(query, filters, perPage, page)
	mrs, err := client.ListMergeRequests(ctx, &gitlab.ListMergeRequestsOptions{
		ListOptions:  opt.ListOptions,
		State:        opt.State,
		Search:       opt.Search,
		SourceBranch: opt.SourceBranch,
		TargetBranch: opt.TargetBranch,
		Scope:        opt.Scope,
	})
	if err != nil {
		return nil, err
	}
	return mergeRequestsFromGitLab(mrs), nil
}

func listProjectMergeRequests(ctx context.Context, client gitlabClient, project any, query string, filters map[string]string, perPage, page int) ([]MergeRequest, error) {
	opt := mergeRequestListOptions(query, filters, perPage, page)
	mrs, err := client.ListProjectMergeRequests(ctx, project, opt)
	if err != nil {
		return nil, err
	}
	return mergeRequestsFromGitLab(mrs), nil
}

func mergeRequestListOptions(query string, filters map[string]string, perPage, page int) *gitlab.ListProjectMergeRequestsOptions {
	opt := &gitlab.ListProjectMergeRequestsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(query); value != "" {
		opt.Search = &value
	}
	if value := strings.TrimSpace(filters["state"]); value != "" {
		opt.State = &value
	}
	if value := strings.TrimSpace(filters["source_branch"]); value != "" {
		opt.SourceBranch = &value
	}
	if value := strings.TrimSpace(filters["target_branch"]); value != "" {
		opt.TargetBranch = &value
	}
	if value := strings.TrimSpace(filters["scope"]); value != "" {
		opt.Scope = &value
	}
	return opt
}

func mergeRequestsFromGitLab(mrs []*gitlab.BasicMergeRequest) []MergeRequest {
	out := make([]MergeRequest, 0, len(mrs))
	for _, mr := range mrs {
		out = append(out, mergeRequestFromBasic(mr))
	}
	return out
}

func searchMergeRequestDiffs(ctx context.Context, client gitlabClient, filters map[string]string, limit int) ([]MergeRequestDiff, error) {
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return nil, err
	}
	return listDiffs(ctx, client, project, iid, limit)
}

func searchMergeRequestNotes(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) ([]MergeRequestNote, error) {
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return nil, err
	}
	notes, err := listNotes(ctx, client, project, iid, limit)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return notes, nil
	}
	out := notes[:0]
	for _, note := range notes {
		if strings.Contains(strings.ToLower(note.Body), query) {
			out = append(out, note)
		}
	}
	return out, nil
}

func searchPipelines(ctx context.Context, client gitlabClient, filters map[string]string, limit int) ([]Pipeline, error) {
	project := firstNonEmpty(filters["project_id"], filters["project"])
	if project == "" {
		return nil, fmt.Errorf("project_id filter is required for gitlab.pipeline search")
	}
	if mr := strings.TrimSpace(filters["merge_request_iid"]); mr != "" {
		iid, err := strconv.ParseInt(mr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid merge_request_iid %q", mr)
		}
		return pipelinesForMRProject(ctx, client, projectID(project), iid, limit), nil
	}
	pipelines, err := client.ListProjectPipelines(ctx, projectID(project), &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
	if err != nil {
		return nil, err
	}
	return pipelinesFromInfo(pipelines), nil
}

func listDiffs(ctx context.Context, client gitlabClient, project any, iid int64, limit int) ([]MergeRequestDiff, error) {
	diffs, err := client.ListMergeRequestDiffs(ctx, project, iid, &gitlab.ListMergeRequestDiffsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
	if err != nil {
		return nil, err
	}
	var projectNum int64
	if n, ok := project.(int64); ok {
		projectNum = n
	}
	out := make([]MergeRequestDiff, 0, len(diffs))
	for _, diff := range diffs {
		out = append(out, diffFromGitLab(projectNum, iid, diff))
	}
	return out, nil
}

func listNotes(ctx context.Context, client gitlabClient, project any, iid int64, limit int) ([]MergeRequestNote, error) {
	notes, err := client.ListMergeRequestNotes(ctx, project, iid, &gitlab.ListMergeRequestNotesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
	if err != nil {
		return nil, err
	}
	out := make([]MergeRequestNote, 0, len(notes))
	for _, note := range notes {
		out = append(out, noteFromGitLab(note))
	}
	return out, nil
}

func pipelinesForMRProject(ctx context.Context, client gitlabClient, project any, iid int64, limit int) []Pipeline {
	pipelines, err := client.ListMergeRequestPipelines(ctx, project, iid)
	if err != nil {
		return nil
	}
	return limitPipelines(pipelinesFromInfo(pipelines), limit)
}

func projectAndMR(filters map[string]string) (any, int64, error) {
	if ref := strings.TrimSpace(filters["merge_request"]); ref != "" {
		return parseMergeRequestID(ref)
	}
	project := firstNonEmpty(filters["project_id"], filters["project"])
	mr := strings.TrimSpace(filters["merge_request_iid"])
	if project == "" || mr == "" {
		return nil, 0, fmt.Errorf("project_id and merge_request_iid filters are required")
	}
	iid, err := strconv.ParseInt(mr, 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid merge_request_iid %q", mr)
	}
	return projectID(project), iid, nil
}

func selectedEntities(entities []coredatasource.EntityType) ([]coredatasource.EntitySpec, error) {
	available := map[coredatasource.EntityType]coredatasource.EntitySpec{}
	for _, entity := range entitySpecs() {
		available[entity.Type] = entity
	}
	var out []coredatasource.EntitySpec
	for _, requested := range entities {
		entity, ok := available[requested]
		if !ok {
			return nil, fmt.Errorf("unsupported gitlab datasource entity %q", requested)
		}
		out = append(out, entity)
	}
	return out, nil
}

func corpusPage(records []coredatasource.Record, count, limit, page int) coredatasource.CorpusPage {
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
	next := ""
	if count >= limit {
		next = strconv.Itoa(page + 1)
	}
	return coredatasource.CorpusPage{Documents: documents, NextCursor: next, Complete: next == ""}
}

func recordsFrom[T any](values []T, convert func(T) coredatasource.Record) []coredatasource.Record {
	records := make([]coredatasource.Record, 0, len(values))
	for _, value := range values {
		records = append(records, convert(value))
	}
	return records
}

func pipelinesFromInfo(values []*gitlab.PipelineInfo) []Pipeline {
	out := make([]Pipeline, 0, len(values))
	for _, value := range values {
		out = append(out, pipelineFromInfo(value))
	}
	return out
}

func usersFromBasic(values []*gitlab.BasicUser, role string) []User {
	out := make([]User, 0, len(values))
	for _, value := range values {
		out = append(out, userFromBasic(value, role))
	}
	return out
}

func usersFromReviewers(values []*gitlab.MergeRequestReviewer) []User {
	out := make([]User, 0, len(values))
	for _, value := range values {
		out = append(out, userFromReviewer(value))
	}
	return out
}

func limitPipelines(values []Pipeline, limit int) []Pipeline {
	limit = normalizedLimit(limit)
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitUsers(values []User, limit int) []User {
	limit = normalizedLimit(limit)
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	return limit
}

func cursorPage(cursor string) int {
	page, err := strconv.Atoi(strings.TrimSpace(cursor))
	if err != nil || page <= 0 {
		return 1
	}
	return page
}

func cleaned(values []string) []string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
