package gitlabplugin

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
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
	entities, err := runtimedatasource.SelectEntities(Name, entitySpecs(), spec.Entities)
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

func (a gitlabAccessor) entitySupports(entity coredatasource.EntityType, capability coredatasource.EntityCapability) bool {
	for _, spec := range a.entities {
		if spec.Type == entity {
			return spec.Supports(capability)
		}
	}
	return false
}

func (a gitlabAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = ProjectEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	if a.spec.Index.Enabled && req.Mode != "provider" && req.Mode != "live" && a.entitySupports(entity, coredatasource.EntityCapabilityIndex) {
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
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(projects, a.projectRecord), -1), nil
	case MergeRequestEntity:
		mrs, err := searchMergeRequests(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(mrs, a.mergeRequestRecord), -1), nil
	case MergeRequestDiffEntity:
		diffs, err := searchMergeRequestDiffs(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(diffs, a.diffRecord), -1), nil
	case MergeRequestNoteEntity:
		notes, err := searchMergeRequestNotes(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(notes, a.noteRecord), -1), nil
	case PipelineEntity:
		pipelines, err := searchPipelines(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(pipelines, a.pipelineRecord), -1), nil
	case BranchEntity:
		branches, err := searchBranches(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(branches, a.branchRecord), -1), nil
	case TagEntity:
		tags, err := searchTags(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(tags, a.tagRecord), -1), nil
	case CommitEntity:
		commits, err := searchCommits(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(commits, a.commitRecord), -1), nil
	case UserEntity:
		users, err := searchUsers(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(users, a.userRecord), -1), nil
	case GroupEntity:
		groups, err := searchGroups(ctx, client, req.Query, req.Filters, req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(groups, a.groupRecord), -1), nil
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q entity %q does not support search", a.spec.Name, entity)
	}
}

func (a gitlabAccessor) indexSearch(ctx context.Context, entity coredatasource.EntityType, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	result, err := semantic.SearchFieldIndex(ctx, semantic.FieldLookupRequest{
		Index:      a.index,
		Datasource: a.spec.Name,
		Entity:     entity,
		Query:      req.Query,
		Filters:    req.Filters,
		Limit:      req.Limit,
	})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	return runtimedatasource.SearchResult(a.spec.Name, entity, result.Records, -1), nil
}

func (a gitlabAccessor) indexedUserMembership(ctx context.Context, userID int64, sourceType string, sourceID int64) (Membership, error) {
	record, err := semantic.GetFieldRecord(ctx, a.index, a.spec.Name, MembershipEntity, membershipID(userID, sourceType, sourceID))
	if err != nil {
		return Membership{}, err
	}
	membership, err := membershipFromRecord(record)
	if err != nil {
		return Membership{}, err
	}
	sourceType = normalizedMembershipSourceType(sourceType)
	if membership.SourceID == sourceID && strings.EqualFold(normalizedMembershipSourceType(membership.SourceType), sourceType) {
		return membership, nil
	}
	return Membership{}, coredatasource.ErrNotFound
}

type indexedMembershipPage struct {
	memberships []Membership
	nextCursor  string
}

func (a gitlabAccessor) indexedUserMemberships(ctx context.Context, userID int64, sourceType string, limit int, cursor string) (indexedMembershipPage, error) {
	limit = normalizedLimit(limit)
	filters := map[string]string{"user_id": strconv.FormatInt(userID, 10)}
	if strings.TrimSpace(sourceType) != "" {
		filters["source_type"] = normalizedMembershipSourceType(sourceType)
	}
	result, err := semantic.SearchFieldIndex(ctx, semantic.FieldLookupRequest{
		Index:      a.index,
		Datasource: a.spec.Name,
		Entity:     MembershipEntity,
		Filters:    filters,
		Limit:      limit,
		Cursor:     cursor,
	})
	if err != nil {
		return indexedMembershipPage{}, err
	}
	memberships := make([]Membership, 0, len(result.Records))
	for _, record := range result.Records {
		membership, err := membershipFromRecord(record)
		if err != nil {
			return indexedMembershipPage{}, err
		}
		memberships = append(memberships, membership)
	}
	sortMemberships(memberships)
	return indexedMembershipPage{memberships: memberships, nextCursor: result.NextCursor}, nil
}

func (a gitlabAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = ProjectEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	page := cursorPage(req.Cursor)
	switch entity {
	case ProjectEntity:
		projects, err := searchProjects(ctx, client, "", limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(projects, a.projectRecord), len(projects), limit, page), nil
	case UserEntity:
		users, err := searchUsers(ctx, client, "", req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(users, a.userRecord), len(users), limit, page), nil
	case GroupEntity:
		groups, err := searchGroups(ctx, client, "", req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(groups, a.groupRecord), len(groups), limit, page), nil
	case BranchEntity:
		branches, err := searchBranches(ctx, client, "", req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(branches, a.branchRecord), len(branches), limit, page), nil
	case TagEntity:
		tags, err := searchTags(ctx, client, "", req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(tags, a.tagRecord), len(tags), limit, page), nil
	case CommitEntity:
		commits, err := searchCommits(ctx, client, "", req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(commits, a.commitRecord), len(commits), limit, page), nil
	case RepositoryTreeEntity:
		entries, err := listRepositoryTree(ctx, client, req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(entries, a.repositoryTreeRecord), len(entries), limit, page), nil
	case JobEntity:
		jobs, err := listProjectJobs(ctx, client, req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(jobs, a.jobRecord), len(jobs), limit, page), nil
	case MembershipEntity:
		userID, err := membershipUserIDFilter(req.Filters)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		if a.spec.Index.Enabled {
			memberships, err := a.indexedUserMemberships(ctx, userID, "", limit, req.Cursor)
			if err != nil {
				return coredatasource.ListResult{}, err
			}
			return runtimedatasource.ListResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(memberships.memberships, a.membershipRecord), -1, memberships.nextCursor), nil
		}
		memberships, err := listUserMemberships(ctx, client, userID, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(memberships, a.membershipRecord), len(memberships), limit, page), nil
	default:
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q entity %q does not support list", a.spec.Name, entity)
	}
}

func (a gitlabAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if !runtimedatasource.HasEntity(a.entities, req.Entity) {
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
	case BranchEntity:
		project, branch, err := parseProjectTextChildID(req.ID, "branch")
		if err != nil {
			return coredatasource.Record{}, err
		}
		out, err := client.GetBranch(ctx, project, branch)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.branchRecord(branchFromGitLab(projectIDLabel(project), out)), nil
	case TagEntity:
		project, tag, err := parseProjectTextChildID(req.ID, "tag")
		if err != nil {
			return coredatasource.Record{}, err
		}
		out, err := client.GetTag(ctx, project, tag)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.tagRecord(tagFromGitLab(projectIDLabel(project), out)), nil
	case CommitEntity:
		project, sha, err := parseProjectTextChildID(req.ID, "commit")
		if err != nil {
			return coredatasource.Record{}, err
		}
		commit, err := client.GetCommit(ctx, project, sha, nil)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.commitRecord(commitFromGitLab(projectIDLabel(project), commit)), nil
	case RepositoryTreeEntity:
		project, ref, path, err := parseProjectRefPathID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		entries, err := listRepositoryTree(ctx, client, map[string]string{"project_id": projectIDLabel(project), "ref": ref, "path": path}, defaultPageSize, 1)
		if err != nil {
			return coredatasource.Record{}, err
		}
		for _, entry := range entries {
			if entry.ID == req.ID || entry.Path == path {
				return a.repositoryTreeRecord(entry), nil
			}
		}
		return coredatasource.Record{}, coredatasource.ErrNotFound
	case RepositoryFileEntity:
		project, ref, path, err := parseProjectRefPathID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		file, err := getRepositoryFile(ctx, client, project, ref, path)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.repositoryFileRecord(file), nil
	case JobEntity:
		project, jobID, err := parseProjectChildID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		job, err := client.GetJob(ctx, project, jobID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.jobRecord(jobFromGitLab(projectIDLabel(project), job)), nil
	case JobTraceEntity:
		project, jobID, err := parseTraceID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		trace, err := getJobTrace(ctx, client, project, jobID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.jobTraceRecord(trace), nil
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
	case MembershipEntity:
		userID, sourceType, sourceID, err := parseMembershipID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		if a.spec.Index.Enabled {
			membership, err := a.indexedUserMembership(ctx, userID, sourceType, sourceID)
			if err != nil {
				return coredatasource.Record{}, err
			}
			return a.membershipRecord(membership), nil
		}
		membership, err := getUserMembership(ctx, client, userID, sourceType, sourceID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.membershipRecord(membership), nil
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
	if !runtimedatasource.HasEntity(a.entities, req.Entity) {
		return coredatasource.RelationResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	client, err := a.client(ctx)
	if err != nil {
		return coredatasource.RelationResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch req.Entity {
	case ProjectEntity:
		project, projectLabel := resolveProjectIdentifier(ctx, client, req.ID)
		switch req.Relation {
		case "merge_requests":
			mrs, err := listProjectMergeRequests(ctx, client, project, "", nil, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestEntity, runtimedatasource.RecordsFrom(mrs, a.mergeRequestRecord), len(mrs), limit)
		case "pipelines":
			pipelines, err := client.ListProjectPipelines(ctx, project, &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(limit), Page: int64(cursorPage(req.Cursor))}})
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, PipelineEntity, runtimedatasource.RecordsFrom(pipelinesFromInfo(pipelines), a.pipelineRecord), len(pipelines), limit)
		case "branches":
			branches, err := searchBranches(ctx, client, "", map[string]string{"project_id": projectLabel}, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, BranchEntity, runtimedatasource.RecordsFrom(branches, a.branchRecord), len(branches), limit)
		case "tags":
			tags, err := searchTags(ctx, client, "", map[string]string{"project_id": projectLabel}, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, TagEntity, runtimedatasource.RecordsFrom(tags, a.tagRecord), len(tags), limit)
		case "commits":
			commits, err := searchCommits(ctx, client, "", map[string]string{"project_id": projectLabel}, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, CommitEntity, runtimedatasource.RecordsFrom(commits, a.commitRecord), len(commits), limit)
		case "repository_tree":
			entries, err := listRepositoryTree(ctx, client, map[string]string{"project_id": projectLabel}, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, RepositoryTreeEntity, runtimedatasource.RecordsFrom(entries, a.repositoryTreeRecord), len(entries), limit)
		case "jobs":
			jobs, err := listProjectJobs(ctx, client, map[string]string{"project_id": projectLabel}, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, JobEntity, runtimedatasource.RecordsFrom(jobs, a.jobRecord), len(jobs), limit)
		case "users":
			users, err := listProjectUsers(ctx, client, project, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, UserEntity, runtimedatasource.RecordsFrom(users, a.userRecord), len(users), limit)
		case "groups":
			groups, err := listProjectGroups(ctx, client, project, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, GroupEntity, runtimedatasource.RecordsFrom(groups, a.groupRecord), len(groups), limit)
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
			return a.relationResult(req, MergeRequestDiffEntity, runtimedatasource.RecordsFrom(diffs, a.diffRecord), len(diffs), limit)
		case "notes":
			notes, err := listNotes(ctx, client, project, iid, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestNoteEntity, runtimedatasource.RecordsFrom(notes, a.noteRecord), len(notes), limit)
		case "pipelines":
			pipelines, err := client.ListMergeRequestPipelines(ctx, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := runtimedatasource.RecordsFrom(limitPipelines(pipelinesFromInfo(pipelines), limit), a.pipelineRecord)
			return a.relationResult(req, PipelineEntity, records, len(pipelines), limit)
		case "participants":
			users, err := client.GetMergeRequestParticipants(ctx, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := runtimedatasource.RecordsFrom(limitUsers(usersFromBasic(users, "participant"), limit), a.userRecord)
			return a.relationResult(req, UserEntity, records, len(users), limit)
		case "reviewers":
			reviewers, err := client.GetMergeRequestReviewers(ctx, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := runtimedatasource.RecordsFrom(limitUsers(usersFromReviewers(reviewers), limit), a.userRecord)
			return a.relationResult(req, UserEntity, records, len(reviewers), limit)
		}
	case UserEntity:
		userID, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
		if err != nil {
			return coredatasource.RelationResult{}, fmt.Errorf("gitlab user id must be numeric")
		}
		switch req.Relation {
		case "memberships":
			if a.spec.Index.Enabled {
				memberships, err := a.indexedUserMemberships(ctx, userID, "", limit, req.Cursor)
				if err != nil {
					return coredatasource.RelationResult{}, err
				}
				return a.relationResultWithCursor(req, MembershipEntity, runtimedatasource.RecordsFrom(memberships.memberships, a.membershipRecord), memberships.nextCursor)
			}
			memberships, err := listUserMemberships(ctx, client, userID, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MembershipEntity, runtimedatasource.RecordsFrom(memberships, a.membershipRecord), len(memberships), limit)
		case "groups":
			if a.spec.Index.Enabled {
				memberships, err := a.indexedUserMemberships(ctx, userID, "Namespace", limit, req.Cursor)
				if err != nil {
					return coredatasource.RelationResult{}, err
				}
				groups := groupsFromMemberships(memberships.memberships)
				return a.relationResultWithCursor(req, GroupEntity, runtimedatasource.RecordsFrom(groups, a.groupRecord), memberships.nextCursor)
			}
			memberships, err := listUserMemberships(ctx, client, userID, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			groups := groupsFromMemberships(memberships)
			return a.relationResult(req, GroupEntity, runtimedatasource.RecordsFrom(groups, a.groupRecord), len(groups), limit)
		case "projects":
			if a.spec.Index.Enabled {
				memberships, err := a.indexedUserMemberships(ctx, userID, "Project", limit, req.Cursor)
				if err != nil {
					return coredatasource.RelationResult{}, err
				}
				projects := projectsFromMemberships(memberships.memberships)
				return a.relationResultWithCursor(req, ProjectEntity, runtimedatasource.RecordsFrom(projects, a.projectRecord), memberships.nextCursor)
			}
			memberships, err := listUserMemberships(ctx, client, userID, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			projects := projectsFromMemberships(memberships)
			return a.relationResult(req, ProjectEntity, runtimedatasource.RecordsFrom(projects, a.projectRecord), len(projects), limit)
		}
	case PipelineEntity:
		project, pipelineID, err := parseProjectChildID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "jobs":
			jobs, err := listPipelineJobs(ctx, client, project, pipelineID, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, JobEntity, runtimedatasource.RecordsFrom(jobs, a.jobRecord), len(jobs), limit)
		case "commit":
			pipeline, err := client.GetPipeline(ctx, project, pipelineID)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			sha := strings.TrimSpace(pipeline.SHA)
			if sha == "" {
				return a.relationResult(req, CommitEntity, nil, 0, limit)
			}
			commit, err := client.GetCommit(ctx, project, sha, nil)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, CommitEntity, []coredatasource.Record{a.commitRecord(commitFromGitLab(projectIDLabel(project), commit))}, 1, limit)
		}
	case BranchEntity:
		project, branch, err := parseProjectTextChildID(req.ID, "branch")
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "commit":
			out, err := client.GetBranch(ctx, project, branch)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			commit := commitFromGitLab(projectIDLabel(project), out.Commit)
			if commit.SHA == "" {
				return a.relationResult(req, CommitEntity, nil, 0, limit)
			}
			return a.relationResult(req, CommitEntity, []coredatasource.Record{a.commitRecord(commit)}, 1, limit)
		}
	case TagEntity:
		project, tag, err := parseProjectTextChildID(req.ID, "tag")
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "commit":
			out, err := client.GetTag(ctx, project, tag)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			commit := commitFromGitLab(projectIDLabel(project), out.Commit)
			if commit.SHA == "" {
				return a.relationResult(req, CommitEntity, nil, 0, limit)
			}
			return a.relationResult(req, CommitEntity, []coredatasource.Record{a.commitRecord(commit)}, 1, limit)
		}
	case CommitEntity:
		project, sha, err := parseProjectTextChildID(req.ID, "commit")
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "merge_requests":
			mrs, err := client.ListMergeRequestsByCommit(ctx, project, sha)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			records := runtimedatasource.RecordsFrom(limitMergeRequests(mergeRequestsFromGitLab(mrs), limit), a.mergeRequestRecord)
			return a.relationResult(req, MergeRequestEntity, records, len(mrs), limit)
		case "pipelines":
			pipelines, err := client.ListProjectPipelines(ctx, project, &gitlab.ListProjectPipelinesOptions{
				ListOptions: gitlab.ListOptions{PerPage: int64(limit), Page: int64(cursorPage(req.Cursor))},
				SHA:         &sha,
			})
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, PipelineEntity, runtimedatasource.RecordsFrom(pipelinesFromInfo(pipelines), a.pipelineRecord), len(pipelines), limit)
		}
	case RepositoryTreeEntity:
		project, ref, path, err := parseProjectRefPathID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "file":
			file, err := getRepositoryFile(ctx, client, project, ref, path)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, RepositoryFileEntity, []coredatasource.Record{a.repositoryFileRecord(file)}, 1, limit)
		}
	case JobEntity:
		project, jobID, err := parseProjectChildID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "trace":
			trace, err := getJobTrace(ctx, client, project, jobID)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, JobTraceEntity, []coredatasource.Record{a.jobTraceRecord(trace)}, 1, limit)
		case "pipeline":
			job, err := client.GetJob(ctx, project, jobID)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			if job.Pipeline.ID == 0 {
				return a.relationResult(req, PipelineEntity, nil, 0, limit)
			}
			pipeline, err := client.GetPipeline(ctx, project, job.Pipeline.ID)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, PipelineEntity, []coredatasource.Record{a.pipelineRecord(pipelineFromFull(pipeline))}, 1, limit)
		case "commit":
			job, err := client.GetJob(ctx, project, jobID)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			commit := commitFromGitLab(projectIDLabel(project), job.Commit)
			if commit.SHA == "" {
				return a.relationResult(req, CommitEntity, nil, 0, limit)
			}
			return a.relationResult(req, CommitEntity, []coredatasource.Record{a.commitRecord(commit)}, 1, limit)
		}
	case GroupEntity:
		group := projectID(req.ID)
		switch req.Relation {
		case "parent":
			parent, ok, err := getGroupParent(ctx, client, req.ID)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			if !ok {
				return a.relationResult(req, GroupEntity, nil, 0, limit)
			}
			return a.relationResult(req, GroupEntity, []coredatasource.Record{a.groupRecord(parent)}, 1, limit)
		case "subgroups":
			groups, err := listGroupSubgroups(ctx, client, group, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, GroupEntity, runtimedatasource.RecordsFrom(groups, a.groupRecord), len(groups), limit)
		case "descendant_groups":
			groups, err := listGroupDescendants(ctx, client, group, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, GroupEntity, runtimedatasource.RecordsFrom(groups, a.groupRecord), len(groups), limit)
		case "projects":
			projects, err := listGroupProjects(ctx, client, group, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, ProjectEntity, runtimedatasource.RecordsFrom(projects, a.projectRecord), len(projects), limit)
		case "users":
			users, err := listGroupUsers(ctx, client, group, limit, cursorPage(req.Cursor))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, UserEntity, runtimedatasource.RecordsFrom(users, a.userRecord), len(users), limit)
		}
	case MembershipEntity:
		_, sourceType, sourceID, err := parseMembershipID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "group":
			if !isNamespaceMembership(sourceType) {
				return a.relationResult(req, GroupEntity, nil, 0, limit)
			}
			group, err := getGroup(ctx, client, strconv.FormatInt(sourceID, 10))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, GroupEntity, []coredatasource.Record{a.groupRecord(group)}, 1, limit)
		case "project":
			if !isProjectMembership(sourceType) {
				return a.relationResult(req, ProjectEntity, nil, 0, limit)
			}
			project, err := getProject(ctx, client, strconv.FormatInt(sourceID, 10))
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, ProjectEntity, []coredatasource.Record{a.projectRecord(project)}, 1, limit)
		}
	}
	return coredatasource.RelationResult{}, fmt.Errorf("datasource %q entity %q does not expose relation %q", a.spec.Name, req.Entity, req.Relation)
}

func (a gitlabAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	entity := req.Entity
	if entity == "" {
		entity = ProjectEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
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
		return runtimedatasource.CorpusPageFromRecords(runtimedatasource.RecordsFrom(projects, a.projectRecord), len(projects), limit, page), nil
	case UserEntity:
		users, err := searchUsers(ctx, client, "", nil, limit, page)
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return runtimedatasource.CorpusPageFromRecords(runtimedatasource.RecordsFrom(users, a.userRecord), len(users), limit, page), nil
	case GroupEntity:
		groups, err := searchGroups(ctx, client, "", nil, limit, page)
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return runtimedatasource.CorpusPageFromRecords(runtimedatasource.RecordsFrom(groups, a.groupRecord), len(groups), limit, page), nil
	case MembershipEntity:
		memberships, next, err := listMembershipCorpusPage(ctx, client, limit, req.Cursor)
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return coredatasource.CorpusPage{
			Documents:  runtimedatasource.RecordsToCorpusDocuments(runtimedatasource.RecordsFrom(memberships, a.membershipRecord)),
			NextCursor: next,
			Complete:   next == "",
		}, nil
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

func (a gitlabAccessor) relationResult(req coredatasource.RelationRequest, target coredatasource.EntityType, records []coredatasource.Record, total, limit int) (coredatasource.RelationResult, error) {
	return runtimedatasource.RelationResultPage(a.spec.Name, req, target, records, total, limit, cursorPage(req.Cursor), true), nil
}

func (a gitlabAccessor) relationResultWithCursor(req coredatasource.RelationRequest, target coredatasource.EntityType, records []coredatasource.Record, next string) (coredatasource.RelationResult, error) {
	return runtimedatasource.RelationResult(a.spec.Name, req, target, records, -1, next, true), nil
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
			"project_id":          strconv.FormatInt(project.ID, 10),
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

func (a gitlabAccessor) branchRecord(branch Branch) coredatasource.Record {
	return coredatasource.Record{
		ID:         branch.ID,
		Datasource: a.spec.Name,
		Entity:     BranchEntity,
		Title:      branch.Name,
		Content:    strings.Join(cleaned([]string{branch.Name, branch.CommitID}), " "),
		URL:        branch.WebURL,
		Metadata: map[string]string{
			"id":         branch.ID,
			"project_id": branch.ProjectID,
			"name":       branch.Name,
			"default":    strconv.FormatBool(branch.Default),
			"protected":  strconv.FormatBool(branch.Protected),
			"merged":     strconv.FormatBool(branch.Merged),
			"can_push":   strconv.FormatBool(branch.CanPush),
			"commit_id":  branch.CommitID,
		},
		Raw: branch,
	}
}

func (a gitlabAccessor) tagRecord(tag Tag) coredatasource.Record {
	return coredatasource.Record{
		ID:         tag.ID,
		Datasource: a.spec.Name,
		Entity:     TagEntity,
		Title:      tag.Name,
		Content:    strings.Join(cleaned([]string{tag.Name, tag.Message, tag.Target, tag.CommitID}), " "),
		Metadata: map[string]string{
			"id":         tag.ID,
			"project_id": tag.ProjectID,
			"name":       tag.Name,
			"target":     tag.Target,
			"protected":  strconv.FormatBool(tag.Protected),
			"commit_id":  tag.CommitID,
			"created_at": tag.CreatedAt,
		},
		Raw: tag,
	}
}

func (a gitlabAccessor) commitRecord(commit Commit) coredatasource.Record {
	return coredatasource.Record{
		ID:         commit.ID,
		Datasource: a.spec.Name,
		Entity:     CommitEntity,
		Title:      firstNonEmpty(commit.Title, commit.ShortID, commit.SHA),
		Content:    strings.Join(cleaned([]string{commit.Title, commit.Message, commit.AuthorName, commit.ShortID, commit.SHA}), " "),
		URL:        commit.WebURL,
		Metadata: map[string]string{
			"id":               commit.ID,
			"project_id":       commit.ProjectID,
			"sha":              commit.SHA,
			"short_id":         commit.ShortID,
			"author_name":      commit.AuthorName,
			"author_email":     commit.AuthorEmail,
			"committed_date":   commit.CommittedDate,
			"last_pipeline_id": strconv.FormatInt(commit.LastPipelineID, 10),
		},
		Raw: commit,
	}
}

func (a gitlabAccessor) repositoryTreeRecord(entry RepositoryTreeEntry) coredatasource.Record {
	return coredatasource.Record{
		ID:         entry.ID,
		Datasource: a.spec.Name,
		Entity:     RepositoryTreeEntity,
		Title:      entry.Path,
		Content:    strings.Join(cleaned([]string{entry.Path, entry.Name, entry.Type}), " "),
		Metadata: map[string]string{
			"id":         entry.ID,
			"project_id": entry.ProjectID,
			"ref":        entry.Ref,
			"name":       entry.Name,
			"path":       entry.Path,
			"type":       entry.Type,
			"mode":       entry.Mode,
			"sha":        entry.SHA,
		},
		Raw: entry,
	}
}

func (a gitlabAccessor) repositoryFileRecord(file RepositoryFile) coredatasource.Record {
	return coredatasource.Record{
		ID:         file.ID,
		Datasource: a.spec.Name,
		Entity:     RepositoryFileEntity,
		Title:      file.FilePath,
		Content:    file.ContentPreview,
		Metadata: map[string]string{
			"id":             file.ID,
			"project_id":     file.ProjectID,
			"ref":            file.Ref,
			"file_name":      file.FileName,
			"file_path":      file.FilePath,
			"size":           strconv.FormatInt(file.Size, 10),
			"encoding":       file.Encoding,
			"blob_id":        file.BlobID,
			"commit_id":      file.CommitID,
			"last_commit_id": file.LastCommitID,
			"sha256":         file.SHA256,
		},
		Raw: file,
	}
}

func (a gitlabAccessor) jobRecord(job Job) coredatasource.Record {
	return coredatasource.Record{
		ID:         job.ID,
		Datasource: a.spec.Name,
		Entity:     JobEntity,
		Title:      firstNonEmpty(job.Name, fmt.Sprintf("Job %d", job.JobID)),
		Content:    strings.Join(cleaned([]string{job.Name, job.Stage, job.Status, job.Ref, job.FailureReason}), " "),
		URL:        job.WebURL,
		Metadata: map[string]string{
			"id":              job.ID,
			"project_id":      job.ProjectID,
			"job_id":          strconv.FormatInt(job.JobID, 10),
			"pipeline_id":     strconv.FormatInt(job.PipelineID, 10),
			"name":            job.Name,
			"stage":           job.Stage,
			"status":          job.Status,
			"ref":             job.Ref,
			"commit_id":       job.CommitID,
			"failure_reason":  job.FailureReason,
			"allow_failure":   strconv.FormatBool(job.AllowFailure),
			"duration":        strconv.FormatFloat(job.Duration, 'f', -1, 64),
			"queued_duration": strconv.FormatFloat(job.QueuedDuration, 'f', -1, 64),
			"created_at":      job.CreatedAt,
			"started_at":      job.StartedAt,
			"finished_at":     job.FinishedAt,
			"runner":          job.Runner,
			"user":            job.User,
		},
		Raw: job,
	}
}

func (a gitlabAccessor) jobTraceRecord(trace JobTrace) coredatasource.Record {
	return coredatasource.Record{
		ID:         trace.ID,
		Datasource: a.spec.Name,
		Entity:     JobTraceEntity,
		Title:      fmt.Sprintf("Job %d trace", trace.JobID),
		Content:    trace.Trace,
		Metadata: map[string]string{
			"id":         trace.ID,
			"project_id": trace.ProjectID,
			"job_id":     strconv.FormatInt(trace.JobID, 10),
			"truncated":  strconv.FormatBool(trace.Truncated),
		},
		Raw: trace,
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

func (a gitlabAccessor) membershipRecord(membership Membership) coredatasource.Record {
	metadata := map[string]string{
		"id":           membership.ID,
		"user_id":      strconv.FormatInt(membership.UserID, 10),
		"source_id":    strconv.FormatInt(membership.SourceID, 10),
		"source_name":  membership.SourceName,
		"source_type":  membership.SourceType,
		"source_path":  membership.SourcePath,
		"source_url":   membership.SourceURL,
		"access_level": membership.AccessLevel,
		"role":         membership.Role,
	}
	addMembershipRelationMetadata(metadata, membership)
	return coredatasource.Record{
		ID:         membership.ID,
		Datasource: a.spec.Name,
		Entity:     MembershipEntity,
		Title:      membershipTitle(membership),
		Content:    strings.Join(cleaned([]string{membership.SourceName, membership.SourcePath, membership.Role}), " "),
		URL:        membership.SourceURL,
		Metadata:   metadata,
		Raw:        membership,
	}
}

func addMembershipRelationMetadata(metadata map[string]string, membership Membership) {
	userID := strconv.FormatInt(membership.UserID, 10)
	sourceID := strconv.FormatInt(membership.SourceID, 10)
	sourceRef := sourceID
	if strings.TrimSpace(membership.SourcePath) != "" {
		sourceRef = strings.TrimSpace(membership.SourcePath)
	}
	sourceType := normalizedMembershipSourceType(membership.SourceType)
	switch sourceType {
	case "Namespace":
		addRelationMetadata(metadata, "user_group", UserEntity, userID, "groups", GroupEntity, sourceRef, membership.SourceName, map[string]string{
			"id":           sourceID,
			"path":         membership.SourcePath,
			"full_path":    membership.SourcePath,
			"name":         membership.SourceName,
			"access_level": membership.AccessLevel,
			"role":         membership.Role,
		})
		addRelationMetadata(metadata, "group_user", GroupEntity, sourceRef, "users", UserEntity, userID, userID, map[string]string{
			"id":           userID,
			"access_level": membership.AccessLevel,
			"role":         membership.Role,
		})
	case "Project":
		addRelationMetadata(metadata, "user_project", UserEntity, userID, "projects", ProjectEntity, sourceRef, membership.SourceName, map[string]string{
			"id":           sourceID,
			"path":         membership.SourcePath,
			"full_path":    membership.SourcePath,
			"name":         membership.SourceName,
			"access_level": membership.AccessLevel,
			"role":         membership.Role,
		})
	}
}

func addRelationMetadata(metadata map[string]string, id string, sourceEntity coredatasource.EntityType, sourceID, name string, targetEntity coredatasource.EntityType, targetID, targetTitle string, fields map[string]string) {
	prefix := "relation." + id + "."
	metadata[prefix+"source_entity"] = string(sourceEntity)
	metadata[prefix+"source_id"] = sourceID
	metadata[prefix+"name"] = name
	metadata[prefix+"target_entity"] = string(targetEntity)
	metadata[prefix+"target_id"] = targetID
	metadata[prefix+"target_title"] = targetTitle
	for key, value := range fields {
		if strings.TrimSpace(value) != "" {
			metadata[prefix+"target_field."+key] = strings.TrimSpace(value)
		}
	}
}

func membershipFromRecord(record coredatasource.Record) (Membership, error) {
	userID, err := strconv.ParseInt(strings.TrimSpace(record.Metadata["user_id"]), 10, 64)
	if err != nil {
		return Membership{}, fmt.Errorf("gitlab membership record %q has invalid user_id", record.ID)
	}
	sourceID, err := strconv.ParseInt(strings.TrimSpace(record.Metadata["source_id"]), 10, 64)
	if err != nil {
		return Membership{}, fmt.Errorf("gitlab membership record %q has invalid source_id", record.ID)
	}
	sourceType := normalizedMembershipSourceType(record.Metadata["source_type"])
	id := strings.TrimSpace(record.Metadata["id"])
	if id == "" {
		id = strings.TrimSpace(record.ID)
	}
	if id == "" {
		id = membershipID(userID, sourceType, sourceID)
	}
	return Membership{
		ID:          id,
		UserID:      userID,
		SourceID:    sourceID,
		SourceName:  record.Metadata["source_name"],
		SourceType:  sourceType,
		SourcePath:  record.Metadata["source_path"],
		SourceURL:   firstNonEmpty(record.Metadata["source_url"], record.URL),
		AccessLevel: record.Metadata["access_level"],
		Role:        record.Metadata["role"],
	}, nil
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

func searchBranches(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]Branch, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	opt := &gitlab.ListBranchesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(query); value != "" {
		opt.Search = &value
	}
	branches, err := client.ListBranches(ctx, projectID, opt)
	if err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(branches))
	for _, branch := range branches {
		out = append(out, branchFromGitLab(projectLabel, branch))
	}
	return out, nil
}

func searchTags(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]Tag, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	opt := &gitlab.ListTagsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(query); value != "" {
		opt.Search = &value
	}
	tags, err := client.ListTags(ctx, projectID, opt)
	if err != nil {
		return nil, err
	}
	out := make([]Tag, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tagFromGitLab(projectLabel, tag))
	}
	return out, nil
}

func searchCommits(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]Commit, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	opt := &gitlab.ListCommitsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(filters["ref"]); value != "" {
		opt.RefName = &value
	}
	if value := strings.TrimSpace(filters["path"]); value != "" {
		opt.Path = &value
	}
	if value := strings.TrimSpace(filters["author"]); value != "" {
		opt.Author = &value
	}
	commits, err := client.ListCommits(ctx, projectID, opt)
	if err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(commits))
	query = strings.ToLower(strings.TrimSpace(query))
	for _, commit := range commits {
		value := commitFromGitLab(projectLabel, commit)
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{value.SHA, value.ShortID, value.Title, value.Message, value.AuthorName, value.AuthorEmail}, " ")), query) {
			continue
		}
		out = append(out, value)
	}
	return out, nil
}

func listRepositoryTree(ctx context.Context, client gitlabClient, filters map[string]string, perPage, page int) ([]RepositoryTreeEntry, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	ref := strings.TrimSpace(filters["ref"])
	opt := &gitlab.ListTreeOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(filters["path"]); value != "" {
		opt.Path = &value
	}
	if ref != "" {
		opt.Ref = &ref
	}
	if value := strings.TrimSpace(filters["recursive"]); value != "" {
		recursive, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid recursive filter %q", value)
		}
		opt.Recursive = &recursive
	}
	nodes, err := client.ListTree(ctx, projectID, opt)
	if err != nil {
		return nil, err
	}
	out := make([]RepositoryTreeEntry, 0, len(nodes))
	refLabel := firstNonEmpty(ref, "HEAD")
	for _, node := range nodes {
		out = append(out, repositoryTreeEntryFromGitLab(projectLabel, refLabel, node))
	}
	return out, nil
}

func getRepositoryFile(ctx context.Context, client gitlabClient, project any, ref, path string) (RepositoryFile, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return RepositoryFile{}, fmt.Errorf("gitlab repository file ref is required")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return RepositoryFile{}, fmt.Errorf("gitlab repository file path is required")
	}
	file, err := client.GetFile(ctx, project, path, &gitlab.GetFileOptions{Ref: &ref})
	if err != nil {
		return RepositoryFile{}, err
	}
	return repositoryFileFromGitLab(projectIDLabel(project), ref, file), nil
}

func listProjectJobs(ctx context.Context, client gitlabClient, filters map[string]string, perPage, page int) ([]Job, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	opt, err := listJobsOptions(filters, perPage, page)
	if err != nil {
		return nil, err
	}
	jobs, err := client.ListProjectJobs(ctx, projectID, opt)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, jobFromGitLab(projectLabel, job))
	}
	return out, nil
}

func listPipelineJobs(ctx context.Context, client gitlabClient, project any, pipelineID int64, perPage, page int) ([]Job, error) {
	jobs, err := client.ListPipelineJobs(ctx, project, pipelineID, &gitlab.ListJobsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, jobFromGitLab(projectIDLabel(project), job))
	}
	return out, nil
}

func listJobsOptions(filters map[string]string, perPage, page int) (*gitlab.ListJobsOptions, error) {
	opt := &gitlab.ListJobsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(filters["include_retried"]); value != "" {
		includeRetried, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid include_retried filter %q", value)
		}
		opt.IncludeRetried = &includeRetried
	}
	if value := strings.TrimSpace(filters["scope"]); value != "" {
		states := []gitlab.BuildStateValue{gitlab.BuildStateValue(value)}
		opt.Scope = &states
	}
	return opt, nil
}

func getJobTrace(ctx context.Context, client gitlabClient, project any, jobID int64) (JobTrace, error) {
	data, err := client.GetTraceFile(ctx, project, jobID)
	if err != nil {
		return JobTrace{}, err
	}
	trace := string(data)
	truncated := len(trace) > 20000
	trace = boundedText(trace, 20000)
	projectLabel := projectIDLabel(project)
	return JobTrace{ID: jobTraceID(projectLabel, jobID), ProjectID: projectLabel, JobID: jobID, Trace: trace, Truncated: truncated}, nil
}

func projectFilter(filters map[string]string) (string, error) {
	project := strings.TrimSpace(filters["project_id"])
	if project == "" {
		project = strings.TrimSpace(filters["project"])
	}
	if project == "" {
		return "", fmt.Errorf("gitlab project_id filter is required")
	}
	return project, nil
}

func resolveProjectIdentifier(ctx context.Context, client gitlabClient, id string) (any, string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	if _, err := strconv.ParseInt(id, 10, 64); err == nil {
		return projectID(id), id
	}
	project, err := getProject(ctx, client, id)
	if err != nil || project.ID == 0 {
		return projectID(id), id
	}
	label := strconv.FormatInt(project.ID, 10)
	return project.ID, label
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
	if err == nil && project != nil {
		return projectFromGitLab(project), nil
	}
	if _, numericErr := strconv.ParseInt(strings.TrimSpace(id), 10, 64); numericErr == nil {
		if err != nil {
			return Project{}, err
		}
		return Project{}, coredatasource.ErrNotFound
	}
	projects, searchErr := searchProjects(ctx, client, id, defaultPageSize, 1)
	if searchErr != nil {
		if err != nil {
			return Project{}, err
		}
		return Project{}, searchErr
	}
	for _, candidate := range projects {
		if projectMatches(candidate, id) {
			return candidate, nil
		}
	}
	if err != nil {
		return Project{}, err
	}
	return Project{}, coredatasource.ErrNotFound
}

func projectMatches(project Project, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if project.ID != 0 && id == strconv.FormatInt(project.ID, 10) {
		return true
	}
	return strings.EqualFold(project.PathWithNamespace, id) || strings.EqualFold(project.Name, id)
}

func listProjectUsers(ctx context.Context, client gitlabClient, project any, perPage, page int) ([]User, error) {
	users, err := client.ListProjectUsers(ctx, project, &gitlab.ListProjectUserOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
	})
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(users))
	for _, user := range users {
		out = append(out, userFromProject(user))
	}
	return out, nil
}

func listProjectGroups(ctx context.Context, client gitlabClient, project any, perPage, page int) ([]Group, error) {
	withShared := true
	groups, err := client.ListProjectGroups(ctx, project, &gitlab.ListProjectGroupOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
		WithShared:  &withShared,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		out = append(out, groupFromProject(group))
	}
	return out, nil
}

func listGroupProjects(ctx context.Context, client gitlabClient, group any, perPage, page int) ([]Project, error) {
	simple := true
	includeSubGroups := true
	projects, err := client.ListGroupProjects(ctx, group, &gitlab.ListGroupProjectsOptions{
		ListOptions:      gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
		Simple:           &simple,
		IncludeSubGroups: &includeSubGroups,
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

func getGroupParent(ctx context.Context, client gitlabClient, id string) (Group, bool, error) {
	group, err := getGroup(ctx, client, id)
	if err != nil {
		return Group{}, false, err
	}
	if group.ParentID == 0 {
		return Group{}, false, nil
	}
	parent, err := getGroup(ctx, client, strconv.FormatInt(group.ParentID, 10))
	if err != nil {
		return Group{}, false, err
	}
	return parent, true, nil
}

func listGroupSubgroups(ctx context.Context, client gitlabClient, group any, perPage, page int) ([]Group, error) {
	groups, err := client.ListSubGroups(ctx, group, &gitlab.ListSubGroupsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		out = append(out, groupFromGitLab(group))
	}
	return out, nil
}

func listGroupDescendants(ctx context.Context, client gitlabClient, group any, perPage, page int) ([]Group, error) {
	groups, err := client.ListDescendantGroups(ctx, group, &gitlab.ListDescendantGroupsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		out = append(out, groupFromGitLab(group))
	}
	return out, nil
}

func listGroupUsers(ctx context.Context, client gitlabClient, group any, perPage, page int) ([]User, error) {
	members, err := client.ListAllGroupMembers(ctx, group, &gitlab.ListGroupMembersOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
	})
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(members))
	for _, member := range members {
		out = append(out, userFromGroupMember(member))
	}
	return out, nil
}

func listUserMemberships(ctx context.Context, client gitlabClient, userID int64, perPage, page int) ([]Membership, error) {
	limit := normalizedLimit(perPage)
	groups, err := listMembershipGroups(ctx, client)
	if err != nil {
		return nil, err
	}
	projects, err := listMembershipProjects(ctx, client)
	if err != nil {
		return nil, err
	}
	byID := map[string]Membership{}
	userIDs := []int64{userID}
	for _, group := range groups {
		if group.ID == 0 && group.FullPath == "" {
			continue
		}
		members, err := client.ListAllGroupMembers(ctx, projectID(groupIDString(group)), &gitlab.ListGroupMembersOptions{
			ListOptions: gitlab.ListOptions{PerPage: 1, Page: 1},
			UserIDs:     &userIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab group %s members: %w", groupIDString(group), err)
		}
		if len(members) == 0 {
			continue
		}
		membership := membershipFromGroupMember(userID, group, members[0])
		mergeMembership(byID, membership)
	}
	for _, project := range projects {
		if project.ID == 0 && project.PathWithNamespace == "" {
			continue
		}
		members, err := client.ListAllProjectMembers(ctx, projectID(projectIDString(project)), &gitlab.ListProjectMembersOptions{
			ListOptions: gitlab.ListOptions{PerPage: 1, Page: 1},
			UserIDs:     &userIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab project %s members: %w", projectIDString(project), err)
		}
		if len(members) == 0 {
			continue
		}
		membership := membershipFromProjectMember(userID, project, members[0])
		mergeMembership(byID, membership)
	}
	out := make([]Membership, 0, len(byID))
	for _, membership := range byID {
		out = append(out, membership)
	}
	sortMemberships(out)
	return pageMemberships(out, limit, page), nil
}

func listMembershipGroups(ctx context.Context, client gitlabClient) ([]Group, error) {
	seen := map[string]bool{}
	var out []Group
	addGroup := func(value Group) bool {
		key := groupIDString(value)
		if key == "" || seen[key] {
			return false
		}
		seen[key] = true
		out = append(out, value)
		return true
	}
	for page := 1; ; page++ {
		groups, err := client.ListGroups(ctx, &gitlab.ListGroupsOptions{
			ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(page)},
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab visible groups for membership resolution: %w", err)
		}
		for _, group := range groups {
			value := groupFromGitLab(group)
			if !addGroup(value) {
				continue
			}
			groupID := groupIDString(value)
			if groupID == "" {
				continue
			}
			for descendantPage := 1; ; descendantPage++ {
				descendants, err := client.ListDescendantGroups(ctx, projectID(groupID), &gitlab.ListDescendantGroupsOptions{
					ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(descendantPage)},
				})
				if err != nil {
					return nil, fmt.Errorf("gitlab descendant groups for %s: %w", groupID, err)
				}
				for _, descendant := range descendants {
					addGroup(groupFromGitLab(descendant))
				}
				if len(descendants) < 100 {
					break
				}
			}
		}
		if len(groups) < 100 {
			break
		}
	}
	return out, nil
}

type membershipCorpusCursor struct {
	kind       string
	sourcePage int
	source     int
	memberPage int
}

const (
	membershipCorpusKindGroup   = "group"
	membershipCorpusKindProject = "project"
)

func listMembershipCorpusPage(ctx context.Context, client gitlabClient, limit int, cursor string) ([]Membership, string, error) {
	limit = normalizedLimit(limit)
	state, err := parseMembershipCorpusCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	out := make([]Membership, 0, limit)
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		switch state.kind {
		case membershipCorpusKindGroup:
			next, err := appendGroupMembershipCorpus(ctx, client, &state, limit-len(out), &out)
			if err != nil {
				return nil, "", err
			}
			if next == "" {
				return out, "", nil
			}
			if len(out) >= limit {
				return out, next, nil
			}
		case membershipCorpusKindProject:
			next, err := appendProjectMembershipCorpus(ctx, client, &state, limit-len(out), &out)
			if err != nil {
				return nil, "", err
			}
			if next == "" {
				return out, "", nil
			}
			if len(out) >= limit {
				return out, next, nil
			}
		default:
			return nil, "", fmt.Errorf("invalid gitlab membership corpus cursor kind %q", state.kind)
		}
	}
	return out, state.String(), nil
}

func appendGroupMembershipCorpus(ctx context.Context, client gitlabClient, state *membershipCorpusCursor, limit int, out *[]Membership) (string, error) {
	for {
		groups, hasNextPage, err := listMembershipGroupsPage(ctx, client, state.sourcePage)
		if err != nil {
			return "", err
		}
		if state.source >= len(groups) {
			if hasNextPage {
				state.sourcePage++
				state.source = 0
				state.memberPage = 1
				continue
			}
			state.kind = membershipCorpusKindProject
			state.sourcePage = 1
			state.source = 0
			state.memberPage = 1
			return state.String(), nil
		}
		group := groups[state.source]
		if group.ID == 0 && group.FullPath == "" {
			advanceMembershipCorpusSource(state)
			continue
		}
		members, err := client.ListAllGroupMembers(ctx, projectID(groupIDString(group)), &gitlab.ListGroupMembersOptions{
			ListOptions: gitlab.ListOptions{PerPage: int64(limit), Page: int64(state.memberPage)},
		})
		if err != nil {
			return "", fmt.Errorf("gitlab group %s members: %w", groupIDString(group), err)
		}
		added := 0
		for _, member := range members {
			if added >= limit {
				break
			}
			if member == nil || member.ID == 0 {
				continue
			}
			*out = append(*out, membershipFromGroupMember(member.ID, group, member))
			added++
		}
		if len(members) >= limit {
			state.memberPage++
		} else {
			advanceMembershipCorpusSource(state)
		}
		return state.String(), nil
	}
}

func appendProjectMembershipCorpus(ctx context.Context, client gitlabClient, state *membershipCorpusCursor, limit int, out *[]Membership) (string, error) {
	for {
		projects, err := listMembershipProjectsPage(ctx, client, state.sourcePage)
		if err != nil {
			return "", err
		}
		if state.source >= len(projects) {
			if len(projects) >= 100 {
				state.sourcePage++
				state.source = 0
				state.memberPage = 1
				continue
			}
			return "", nil
		}
		project := projects[state.source]
		if project.ID == 0 && project.PathWithNamespace == "" {
			advanceMembershipCorpusSource(state)
			continue
		}
		members, err := client.ListAllProjectMembers(ctx, projectID(projectIDString(project)), &gitlab.ListProjectMembersOptions{
			ListOptions: gitlab.ListOptions{PerPage: int64(limit), Page: int64(state.memberPage)},
		})
		if err != nil {
			return "", fmt.Errorf("gitlab project %s members: %w", projectIDString(project), err)
		}
		added := 0
		for _, member := range members {
			if added >= limit {
				break
			}
			if member == nil || member.ID == 0 {
				continue
			}
			*out = append(*out, membershipFromProjectMember(member.ID, project, member))
			added++
		}
		if len(members) >= limit {
			state.memberPage++
		} else {
			advanceMembershipCorpusSource(state)
		}
		return state.String(), nil
	}
}

func listMembershipGroupsPage(ctx context.Context, client gitlabClient, page int) ([]Group, bool, error) {
	groups, err := client.ListGroups(ctx, &gitlab.ListGroupsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(page)},
	})
	if err != nil {
		return nil, false, fmt.Errorf("gitlab visible groups for membership resolution: %w", err)
	}
	seen := map[string]bool{}
	out := make([]Group, 0, len(groups))
	addGroup := func(value Group) bool {
		key := groupIDString(value)
		if key == "" || seen[key] {
			return false
		}
		seen[key] = true
		out = append(out, value)
		return true
	}
	for _, group := range groups {
		value := groupFromGitLab(group)
		if !addGroup(value) {
			continue
		}
		groupID := groupIDString(value)
		if groupID == "" {
			continue
		}
		for descendantPage := 1; ; descendantPage++ {
			descendants, err := client.ListDescendantGroups(ctx, projectID(groupID), &gitlab.ListDescendantGroupsOptions{
				ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(descendantPage)},
			})
			if err != nil {
				return nil, false, fmt.Errorf("gitlab descendant groups for %s: %w", groupID, err)
			}
			for _, descendant := range descendants {
				addGroup(groupFromGitLab(descendant))
			}
			if len(descendants) < 100 {
				break
			}
		}
	}
	return out, len(groups) >= 100, nil
}

func listMembershipProjectsPage(ctx context.Context, client gitlabClient, page int) ([]Project, error) {
	simple := true
	membership := true
	projects, err := client.ListProjects(ctx, &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(page)},
		Membership:  &membership,
		Simple:      &simple,
	})
	if err != nil {
		return nil, fmt.Errorf("gitlab visible projects for membership resolution: %w", err)
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, projectFromGitLab(project))
	}
	return out, nil
}

func advanceMembershipCorpusSource(state *membershipCorpusCursor) {
	state.source++
	state.memberPage = 1
}

func parseMembershipCorpusCursor(cursor string) (membershipCorpusCursor, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return membershipCorpusCursor{kind: membershipCorpusKindGroup, sourcePage: 1, memberPage: 1}, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 4 {
		return membershipCorpusCursor{}, fmt.Errorf("invalid gitlab membership corpus cursor %q", cursor)
	}
	sourcePage, err := strconv.Atoi(parts[1])
	if err != nil || sourcePage <= 0 {
		return membershipCorpusCursor{}, fmt.Errorf("invalid gitlab membership corpus cursor %q", cursor)
	}
	source, err := strconv.Atoi(parts[2])
	if err != nil || source < 0 {
		return membershipCorpusCursor{}, fmt.Errorf("invalid gitlab membership corpus cursor %q", cursor)
	}
	memberPage, err := strconv.Atoi(parts[3])
	if err != nil || memberPage <= 0 {
		return membershipCorpusCursor{}, fmt.Errorf("invalid gitlab membership corpus cursor %q", cursor)
	}
	return membershipCorpusCursor{kind: parts[0], sourcePage: sourcePage, source: source, memberPage: memberPage}, nil
}

func (c membershipCorpusCursor) String() string {
	if c.kind == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d:%d", c.kind, c.sourcePage, c.source, c.memberPage)
}

func listMembershipProjects(ctx context.Context, client gitlabClient) ([]Project, error) {
	seen := map[string]bool{}
	var out []Project
	simple := true
	membership := true
	for page := 1; ; page++ {
		projects, err := client.ListProjects(ctx, &gitlab.ListProjectsOptions{
			ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(page)},
			Membership:  &membership,
			Simple:      &simple,
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab visible projects for membership resolution: %w", err)
		}
		for _, project := range projects {
			value := projectFromGitLab(project)
			key := projectIDString(value)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, value)
		}
		if len(projects) < 100 {
			break
		}
	}
	return out, nil
}

func getUserMembership(ctx context.Context, client gitlabClient, userID int64, sourceType string, sourceID int64) (Membership, error) {
	memberships, err := listUserMemberships(ctx, client, userID, 100, 1)
	if err != nil {
		return Membership{}, err
	}
	sourceType = normalizedMembershipSourceType(sourceType)
	for _, membership := range memberships {
		if membership.SourceID == sourceID && strings.EqualFold(normalizedMembershipSourceType(membership.SourceType), sourceType) {
			return membership, nil
		}
	}
	return Membership{}, coredatasource.ErrNotFound
}

func groupsFromMemberships(memberships []Membership) []Group {
	out := make([]Group, 0, len(memberships))
	for _, membership := range memberships {
		if !isNamespaceMembership(membership.SourceType) {
			continue
		}
		out = append(out, Group{
			ID:       membership.SourceID,
			Name:     membership.SourceName,
			FullPath: membership.SourcePath,
			FullName: membership.SourceName,
			WebURL:   membership.SourceURL,
			Role:     membership.Role,
		})
	}
	return out
}

func projectsFromMemberships(memberships []Membership) []Project {
	out := make([]Project, 0, len(memberships))
	for _, membership := range memberships {
		if !isProjectMembership(membership.SourceType) {
			continue
		}
		out = append(out, Project{
			ID:                membership.SourceID,
			Name:              membership.SourceName,
			PathWithNamespace: membership.SourcePath,
			WebURL:            membership.SourceURL,
		})
	}
	return out
}

func membershipUserIDFilter(filters map[string]string) (int64, error) {
	value := strings.TrimSpace(filters["user_id"])
	if value == "" {
		return 0, fmt.Errorf("gitlab user_id filter is required to list user memberships")
	}
	userID, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gitlab user_id filter must be numeric")
	}
	return userID, nil
}

func mergeMembership(memberships map[string]Membership, membership Membership) {
	if membership.ID == "" {
		return
	}
	existing, ok := memberships[membership.ID]
	if !ok || accessLevelRank(membership.AccessLevel) > accessLevelRank(existing.AccessLevel) {
		memberships[membership.ID] = membership
	}
}

func sortMemberships(values []Membership) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].SourceType != values[j].SourceType {
			return values[i].SourceType < values[j].SourceType
		}
		return firstNonEmpty(values[i].SourcePath, values[i].SourceName, values[i].ID) < firstNonEmpty(values[j].SourcePath, values[j].SourceName, values[j].ID)
	})
}

func pageMemberships(values []Membership, limit, page int) []Membership {
	limit = normalizedLimit(limit)
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * limit
	if offset >= len(values) {
		return nil
	}
	end := offset + limit
	if end > len(values) {
		end = len(values)
	}
	return values[offset:end]
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
		projectID, _ := resolveProjectIdentifier(ctx, client, project)
		return listProjectMergeRequests(ctx, client, projectID, query, filters, perPage, page)
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
	projectID, _ := resolveProjectIdentifier(ctx, client, project)
	if mr := strings.TrimSpace(filters["merge_request_iid"]); mr != "" {
		iid, err := strconv.ParseInt(mr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid merge_request_iid %q", mr)
		}
		return pipelinesForMRProject(ctx, client, projectID, iid, limit), nil
	}
	pipelines, err := client.ListProjectPipelines(ctx, projectID, &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
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

func limitMergeRequests(values []MergeRequest, limit int) []MergeRequest {
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
