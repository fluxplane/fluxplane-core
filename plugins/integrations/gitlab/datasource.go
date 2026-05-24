package gitlab

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/resource"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	"github.com/fluxplane/fluxplane-core/runtime/datasource/semantic"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

const defaultPageSize = 20

type gitlabDatasourceProvider struct {
	system        system.System
	ref           resource.PluginRef
	config        Config
	secrets       runtimesecret.Resolver
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
		spec:              spec,
		system:            p.system,
		ref:               p.ref,
		config:            p.config,
		secrets:           p.secrets,
		clientFactory:     p.clientFactory,
		index:             p.index,
		entities:          entities,
		membershipSources: &gitLabMembershipSourceCache{},
	}, nil
}

type gitlabAccessor struct {
	spec              coredatasource.Spec
	system            system.System
	ref               resource.PluginRef
	config            Config
	secrets           runtimesecret.Resolver
	clientFactory     gitlabClientFactory
	index             *semantic.Index
	entities          []coredatasource.EntitySpec
	membershipSources *gitLabMembershipSourceCache
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
		projects, err := searchProjects(ctx, client, req.Query, gitlabDefaultFilters(entity, req.Filters), req.Limit, 1)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(projects, a.projectRecord), -1), nil
	case ActivityEntity:
		activities, err := searchActivity(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(activities, a.activityRecord), -1), nil
	case MembershipEntity:
		memberships, err := a.searchMemberships(ctx, client, req.Query, gitlabDefaultFilters(entity, req.Filters), req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(memberships, a.membershipRecord), -1), nil
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
	case MergeRequestDiffLineEntity:
		lines, err := searchMergeRequestDiffLines(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(lines, a.diffLineRecord), -1), nil
	case MergeRequestNoteEntity:
		notes, err := searchMergeRequestNotes(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(notes, a.noteRecord), -1), nil
	case MergeRequestApprovalEntity:
		approval, err := searchMergeRequestApproval(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, []coredatasource.Record{a.approvalRecord(approval)}, -1), nil
	case MergeRequestChangeEntity:
		change, err := searchMergeRequestChange(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, []coredatasource.Record{a.changeRecord(change)}, -1), nil
	case MergeRequestReviewContextEntity:
		context, err := searchMergeRequestReviewContext(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, []coredatasource.Record{a.reviewContextRecord(context)}, -1), nil
	case DiscussionEntity:
		discussions, err := searchDiscussions(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(discussions, a.discussionRecord), -1), nil
	case AwardEmojiEntity:
		awards, err := searchAwardEmoji(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(awards, a.awardEmojiRecord), -1), nil
	case PipelineEntity:
		pipelines, err := searchPipelines(ctx, client, req.Query, req.Filters, req.Limit)
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
	case RepositoryFileEntity:
		files, err := searchRepositoryFiles(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(files, a.repositoryFileRecord), -1), nil
	case CompareEntity:
		compare, err := searchCompare(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, []coredatasource.Record{a.compareRecord(compare)}, -1), nil
	case BlameEntity:
		blame, err := searchBlame(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, []coredatasource.Record{a.blameRecord(blame)}, -1), nil
	case BlobSearchEntity:
		results, err := searchBlobs(ctx, client, req.Query, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(results, a.blobSearchRecord), -1), nil
	case ProjectLanguageEntity:
		languages, err := searchProjectLanguages(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(languages, a.projectLanguageRecord), -1), nil
	case ProjectContributorEntity:
		contributors, err := searchProjectContributors(ctx, client, req.Filters, req.Limit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(contributors, a.projectContributorRecord), -1), nil
	case JobTraceEntity:
		trace, err := searchJobTrace(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, []coredatasource.Record{a.jobTraceRecord(trace)}, -1), nil
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
	case SnippetFileEntity:
		files, err := searchSnippetFiles(ctx, client, req.Query, req.Filters)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(files, a.snippetFileRecord), -1), nil
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
		Filters:    gitlabDefaultFilters(entity, req.Filters),
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

func (a gitlabAccessor) indexedUserMemberships(ctx context.Context, userID int64, sourceType string, limit int, cursor string, filters map[string]string) (indexedMembershipPage, error) {
	limit = normalizedLimit(limit)
	filters = cloneFilters(filters)
	filters["user_id"] = strconv.FormatInt(userID, 10)
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
		projects, err := searchProjects(ctx, client, "", gitlabDefaultFilters(entity, req.Filters), limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(projects, a.projectRecord), len(projects), limit, page), nil
	case ActivityEntity:
		activities, err := searchActivity(ctx, client, req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(activities, a.activityRecord), len(activities), limit, page), nil
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
	case MergeRequestEntity:
		mrs, err := searchMergeRequests(ctx, client, "", req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(mrs, a.mergeRequestRecord), len(mrs), limit, page), nil
	case MergeRequestDiffEntity:
		diffs, err := searchMergeRequestDiffs(ctx, client, req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(diffs, a.diffRecord), len(diffs), limit, page), nil
	case MergeRequestDiffLineEntity:
		lines, err := searchMergeRequestDiffLines(ctx, client, "", req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(lines, a.diffLineRecord), len(lines), limit, page), nil
	case MergeRequestNoteEntity:
		notes, err := searchMergeRequestNotes(ctx, client, "", req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(notes, a.noteRecord), len(notes), limit, page), nil
	case MergeRequestApprovalEntity:
		approval, err := searchMergeRequestApproval(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, []coredatasource.Record{a.approvalRecord(approval)}, 1, limit, page), nil
	case MergeRequestChangeEntity:
		change, err := searchMergeRequestChange(ctx, client, req.Filters)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, []coredatasource.Record{a.changeRecord(change)}, 1, limit, page), nil
	case MergeRequestReviewContextEntity:
		context, err := searchMergeRequestReviewContext(ctx, client, "", req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, []coredatasource.Record{a.reviewContextRecord(context)}, 1, limit, page), nil
	case DiscussionEntity:
		discussions, err := searchDiscussions(ctx, client, "", req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(discussions, a.discussionRecord), len(discussions), limit, page), nil
	case AwardEmojiEntity:
		awards, err := searchAwardEmoji(ctx, client, req.Filters, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(awards, a.awardEmojiRecord), len(awards), limit, page), nil
	case PipelineEntity:
		pipelines, err := listPipelines(ctx, client, req.Filters, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(pipelines, a.pipelineRecord), len(pipelines), limit, page), nil
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
		filters := gitlabDefaultFilters(entity, req.Filters)
		if a.spec.Index.Enabled {
			memberships, err := a.indexedUserMemberships(ctx, userID, "", limit, req.Cursor, filters)
			if err != nil {
				return coredatasource.ListResult{}, err
			}
			return runtimedatasource.ListResult(a.spec.Name, entity, runtimedatasource.RecordsFrom(memberships.memberships, a.membershipRecord), -1, memberships.nextCursor), nil
		}
		memberships, err := allUserMemberships(ctx, client, userID)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		memberships = filterMemberships(memberships, filters)
		memberships = pageMemberships(memberships, limit, page)
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(memberships, a.membershipRecord), len(memberships), limit, page), nil
	case SnippetEntity:
		snippets, err := listSnippets(ctx, client, limit, page)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultPage(a.spec.Name, entity, runtimedatasource.RecordsFrom(snippets, a.snippetRecord), len(snippets), limit, page), nil
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
	case MergeRequestChangeEntity:
		project, iid, err := parseMergeRequestChangeID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		change, err := mergeRequestChange(ctx, client, project, iid, "")
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.changeRecord(change), nil
	case MergeRequestApprovalEntity:
		project, iid, err := parseMergeRequestApprovalID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		approval, err := mergeRequestApproval(ctx, client, project, iid)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.approvalRecord(approval), nil
	case MergeRequestReviewContextEntity:
		project, iid, err := parseMergeRequestReviewContextID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		context, err := mergeRequestReviewContext(ctx, client, project, iid, defaultPageSize)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.reviewContextRecord(context), nil
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
	case DiscussionEntity:
		project, iid, child, err := parseMergeRequestChildID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		discussions, err := listDiscussions(ctx, client, project, iid, defaultPageSize)
		if err != nil {
			return coredatasource.Record{}, err
		}
		for _, discussion := range discussions {
			if discussion.DiscussionID == child || discussion.ID == req.ID {
				return a.discussionRecord(discussion), nil
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
	case BlobSearchEntity:
		project, ref, path, line, err := parseBlobSearchID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		file, err := getRepositoryFile(ctx, client, project, ref, path)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.blobSearchRecord(blobSearchResultFromRepositoryFile(project, ref, file, line)), nil
	case BlameEntity:
		project, ref, path, err := parseBlameID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		blame, err := getBlame(ctx, client, project, ref, path, nil)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.blameRecord(blame), nil
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
		trace, err := getJobTrace(ctx, client, project, jobID, 0)
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
	case SnippetEntity:
		snippetID, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
		if err != nil {
			return coredatasource.Record{}, fmt.Errorf("gitlab snippet id must be numeric")
		}
		snippet, err := getSnippet(ctx, client, snippetID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.snippetRecord(snippet), nil
	case SnippetFileEntity:
		snippetID, path, err := parseSnippetFileID(req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		files, err := snippetFiles(ctx, client, snippetID, path, 0)
		if err != nil {
			return coredatasource.Record{}, err
		}
		if len(files) == 0 {
			return coredatasource.Record{}, coredatasource.ErrNotFound
		}
		return a.snippetFileRecord(files[0]), nil
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
		case "activity":
			activities, err := searchActivity(ctx, client, map[string]string{"project_id": projectLabel}, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, ActivityEntity, runtimedatasource.RecordsFrom(activities, a.activityRecord), len(activities), limit)
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
		case "languages":
			languages, err := projectLanguages(ctx, client, project, projectLabel)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, ProjectLanguageEntity, runtimedatasource.RecordsFrom(languages, a.projectLanguageRecord), len(languages), limit)
		case "contributors":
			contributors, err := projectContributors(ctx, client, project, projectLabel, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, ProjectContributorEntity, runtimedatasource.RecordsFrom(contributors, a.projectContributorRecord), len(contributors), limit)
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
		case "approvals":
			approval, err := mergeRequestApproval(ctx, client, project, iid)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestApprovalEntity, []coredatasource.Record{a.approvalRecord(approval)}, 1, limit)
		case "changes":
			change, err := mergeRequestChange(ctx, client, project, iid, "")
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestChangeEntity, []coredatasource.Record{a.changeRecord(change)}, 1, limit)
		case "review_context":
			context, err := mergeRequestReviewContext(ctx, client, project, iid, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestReviewContextEntity, []coredatasource.Record{a.reviewContextRecord(context)}, 1, limit)
		case "commits":
			commits, err := client.GetMergeRequestCommits(ctx, project, iid, &gitlab.GetMergeRequestCommitsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(limit), Page: int64(cursorPage(req.Cursor))}})
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			projectLabel := projectIDLabel(project)
			records := make([]coredatasource.Record, 0, len(commits))
			for _, commit := range commits {
				records = append(records, a.commitRecord(commitFromGitLab(projectLabel, commit)))
			}
			return a.relationResult(req, CommitEntity, records, len(commits), limit)
		case "discussions":
			discussions, err := listDiscussions(ctx, client, project, iid, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, DiscussionEntity, runtimedatasource.RecordsFrom(discussions, a.discussionRecord), len(discussions), limit)
		case "award_emoji":
			awards, err := listAwardEmoji(ctx, client, project, iid, 0, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, AwardEmojiEntity, runtimedatasource.RecordsFrom(awards, a.awardEmojiRecord), len(awards), limit)
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
	case MergeRequestDiffEntity:
		project, iid, child, err := parseMergeRequestChildID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		switch req.Relation {
		case "lines":
			lines, err := mergeRequestDiffLines(ctx, client, project, iid, child, "", 0, 3, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, MergeRequestDiffLineEntity, runtimedatasource.RecordsFrom(lines, a.diffLineRecord), len(lines), limit)
		}
	case MergeRequestNoteEntity:
		project, iid, child, err := parseMergeRequestChildID(req.ID)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		noteID, err := strconv.ParseInt(child, 10, 64)
		if err != nil {
			return coredatasource.RelationResult{}, fmt.Errorf("invalid note id %q", child)
		}
		switch req.Relation {
		case "award_emoji":
			awards, err := listAwardEmoji(ctx, client, project, iid, noteID, limit)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			return a.relationResult(req, AwardEmojiEntity, runtimedatasource.RecordsFrom(awards, a.awardEmojiRecord), len(awards), limit)
		}
	case UserEntity:
		userID, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
		if err != nil {
			return coredatasource.RelationResult{}, fmt.Errorf("gitlab user id must be numeric")
		}
		switch req.Relation {
		case "memberships":
			if a.spec.Index.Enabled {
				memberships, err := a.indexedUserMemberships(ctx, userID, "", limit, req.Cursor, nil)
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
				memberships, err := a.indexedUserMemberships(ctx, userID, "Namespace", limit, req.Cursor, nil)
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
				memberships, err := a.indexedUserMemberships(ctx, userID, "Project", limit, req.Cursor, nil)
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
			trace, err := getJobTrace(ctx, client, project, jobID, 0)
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
	case SnippetEntity:
		snippetID, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
		if err != nil {
			return coredatasource.RelationResult{}, fmt.Errorf("gitlab snippet id must be numeric")
		}
		switch req.Relation {
		case "files":
			files, err := snippetFiles(ctx, client, snippetID, "", 0)
			if err != nil {
				return coredatasource.RelationResult{}, err
			}
			if len(files) > limit {
				files = files[:limit]
			}
			return a.relationResult(req, SnippetFileEntity, runtimedatasource.RecordsFrom(files, a.snippetFileRecord), len(files), limit)
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
		projects, err := searchProjects(ctx, client, "", nil, limit, page)
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
		memberships, next, err := a.listMembershipCorpusPage(ctx, client, req.Limit, req.Cursor)
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
		if a.secrets != nil {
			return newOfficialClientWithResolver(ctx, a.system, a.secrets, a.ref, a.config)
		}
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
			"archived":            strconv.FormatBool(project.Archived),
			"star_count":          strconv.FormatInt(project.StarCount, 10),
			"forks_count":         strconv.FormatInt(project.ForksCount, 10),
			"last_activity_at":    project.LastActivityAt,
			"topics":              strings.Join(project.Topics, ","),
		},
		Raw: project,
	}
}

func (a gitlabAccessor) activityRecord(activity Activity) coredatasource.Record {
	return coredatasource.Record{
		ID:         activity.ID,
		Datasource: a.spec.Name,
		Entity:     ActivityEntity,
		Title:      firstNonEmpty(activity.ProjectPath, activity.ProjectName),
		Content: fmt.Sprintf(
			"commits=%d merge_requests=%d pipelines=%d",
			activity.RecentCommitCount,
			activity.RecentMRCount,
			activity.RecentPipelineCount,
		),
		URL: activity.WebURL,
		Metadata: map[string]string{
			"id":                         activity.ID,
			"project_id":                 strconv.FormatInt(activity.ProjectID, 10),
			"project_path":               activity.ProjectPath,
			"project_name":               activity.ProjectName,
			"last_activity_at":           activity.LastActivityAt,
			"recent_commit_count":        strconv.Itoa(activity.RecentCommitCount),
			"recent_merge_request_count": strconv.Itoa(activity.RecentMRCount),
			"recent_pipeline_count":      strconv.Itoa(activity.RecentPipelineCount),
			"partial_aggregation_errors": strings.Join(activity.Errors, "; "),
		},
		Raw: activity,
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
			"project_path":          mr.ProjectPath,
			"iid":                   strconv.FormatInt(mr.IID, 10),
			"state":                 mr.State,
			"merge_status":          mr.MergeStatus,
			"detailed_merge_status": mr.DetailedMergeStatus,
			"has_conflicts":         strconv.FormatBool(mr.HasConflicts),
			"changes_count":         mr.ChangesCount,
			"user_notes_count":      strconv.FormatInt(mr.UserNotesCount, 10),
			"source_branch":         mr.SourceBranch,
			"target_branch":         mr.TargetBranch,
			"author":                mr.AuthorUsername,
			"merged_at":             mr.MergedAt,
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
			"project_id":        projectMetadataID(diff.Project, diff.ProjectID),
			"merge_request_iid": strconv.FormatInt(diff.MergeRequest, 10),
			"new_path":          diff.NewPath,
			"old_path":          diff.OldPath,
		},
		Raw: diff,
	}
}

func (a gitlabAccessor) diffLineRecord(line MergeRequestDiffLine) coredatasource.Record {
	return coredatasource.Record{
		ID:         line.ID,
		Datasource: a.spec.Name,
		Entity:     MergeRequestDiffLineEntity,
		Title:      fmt.Sprintf("%s:%d", firstNonEmpty(line.NewPath, line.OldPath), firstNonZero(line.NewLine, line.OldLine)),
		Content:    line.Content,
		Metadata: map[string]string{
			"id":                line.ID,
			"project_id":        line.ProjectID,
			"merge_request_iid": strconv.FormatInt(line.MergeRequestIID, 10),
			"new_path":          line.NewPath,
			"old_path":          line.OldPath,
			"line_type":         line.LineType,
			"new_line":          strconv.FormatInt(line.NewLine, 10),
			"old_line":          strconv.FormatInt(line.OldLine, 10),
			"hunk_header":       line.HunkHeader,
		},
		Raw: line,
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

func (a gitlabAccessor) approvalRecord(approval MergeRequestApproval) coredatasource.Record {
	return coredatasource.Record{
		ID:         approval.ID,
		Datasource: a.spec.Name,
		Entity:     MergeRequestApprovalEntity,
		Title:      fmt.Sprintf("MR !%d approvals", approval.MergeRequestIID),
		Content:    strings.Join(approval.ApprovedBy, " "),
		Metadata: map[string]string{
			"id":                 approval.ID,
			"project_id":         strconv.FormatInt(approval.ProjectID, 10),
			"merge_request_iid":  strconv.FormatInt(approval.MergeRequestIID, 10),
			"approvals_required": strconv.FormatInt(approval.ApprovalsRequired, 10),
			"approvals_left":     strconv.FormatInt(approval.ApprovalsLeft, 10),
			"approved":           strconv.FormatBool(approval.Approved),
			"approved_by":        strings.Join(approval.ApprovedBy, ","),
		},
		Raw: approval,
	}
}

func (a gitlabAccessor) changeRecord(change MergeRequestChange) coredatasource.Record {
	return coredatasource.Record{
		ID:         change.ID,
		Datasource: a.spec.Name,
		Entity:     MergeRequestChangeEntity,
		Title:      fmt.Sprintf("MR !%d changes", change.MergeRequestIID),
		Content:    change.DiffPreview,
		Metadata: map[string]string{
			"id":                change.ID,
			"project_id":        projectMetadataID(change.Project, change.ProjectID),
			"merge_request_iid": strconv.FormatInt(change.MergeRequestIID, 10),
			"additions":         strconv.Itoa(change.Additions),
			"deletions":         strconv.Itoa(change.Deletions),
			"files":             strconv.Itoa(len(change.Files)),
			"truncated":         strconv.FormatBool(change.Truncated),
		},
		Raw: change,
	}
}

func (a gitlabAccessor) reviewContextRecord(context MergeRequestReviewContext) coredatasource.Record {
	return coredatasource.Record{
		ID:         context.ID,
		Datasource: a.spec.Name,
		Entity:     MergeRequestReviewContextEntity,
		Title:      fmt.Sprintf("MR !%d review context", context.MergeRequest.IID),
		Content: strings.Join(cleaned([]string{
			context.MergeRequest.Title,
			context.MergeRequest.Description,
			context.Change.DiffPreview,
		}), "\n"),
		URL: context.MergeRequest.WebURL,
		Metadata: map[string]string{
			"id":                          context.ID,
			"project_id":                  strconv.FormatInt(context.MergeRequest.ProjectID, 10),
			"project_path":                firstNonEmpty(context.MergeRequest.ProjectPath, projectIDLabel(context.MergeRequest.ProjectID)),
			"merge_request_iid":           strconv.FormatInt(context.MergeRequest.IID, 10),
			"source_branch":               context.MergeRequest.SourceBranch,
			"target_branch":               context.MergeRequest.TargetBranch,
			"sha":                         context.MergeRequest.SHA,
			"state":                       context.MergeRequest.State,
			"author":                      context.MergeRequest.AuthorUsername,
			"changed_files":               strconv.Itoa(len(context.Change.Files)),
			"approved":                    strconv.FormatBool(context.Approval.Approved),
			"approvals_required":          strconv.FormatInt(context.Approval.ApprovalsRequired, 10),
			"approvals_left":              strconv.FormatInt(context.Approval.ApprovalsLeft, 10),
			"latest_pipeline_id":          strconv.FormatInt(context.LatestPipeline.ID, 10),
			"latest_pipeline_status":      context.LatestPipeline.Status,
			"latest_pipeline_sha":         context.LatestPipeline.SHA,
			"jobs":                        strconv.Itoa(len(context.Jobs)),
			"discussions":                 strconv.Itoa(len(context.Discussions)),
			"unresolved_discussion_count": strconv.Itoa(context.UnresolvedCount),
			"system_notes_only":           strconv.FormatBool(context.SystemNotesOnly),
		},
		Raw: context,
	}
}

func (a gitlabAccessor) discussionRecord(discussion Discussion) coredatasource.Record {
	content := make([]string, 0, len(discussion.Notes))
	for _, note := range discussion.Notes {
		content = append(content, note.Body)
	}
	return coredatasource.Record{
		ID:         discussion.ID,
		Datasource: a.spec.Name,
		Entity:     DiscussionEntity,
		Title:      discussion.DiscussionID,
		Content:    strings.Join(cleaned(content), "\n"),
		Metadata: map[string]string{
			"id":                discussion.ID,
			"discussion_id":     discussion.DiscussionID,
			"project_id":        strconv.FormatInt(discussion.ProjectID, 10),
			"merge_request_iid": strconv.FormatInt(discussion.MergeRequestIID, 10),
			"resolvable":        strconv.FormatBool(discussion.Resolvable),
			"resolved":          strconv.FormatBool(discussion.Resolved),
			"new_path":          discussion.NewPath,
			"old_path":          discussion.OldPath,
			"new_line":          strconv.FormatInt(discussion.NewLine, 10),
			"old_line":          strconv.FormatInt(discussion.OldLine, 10),
		},
		Raw: discussion,
	}
}

func (a gitlabAccessor) awardEmojiRecord(award AwardEmoji) coredatasource.Record {
	return coredatasource.Record{
		ID:         award.ID,
		Datasource: a.spec.Name,
		Entity:     AwardEmojiEntity,
		Title:      award.Name,
		Content:    strings.TrimSpace(award.Name + " " + award.User),
		Metadata: map[string]string{
			"id":                award.ID,
			"award_id":          strconv.FormatInt(award.AwardID, 10),
			"name":              award.Name,
			"user":              award.User,
			"project_id":        award.ProjectID,
			"merge_request_iid": strconv.FormatInt(award.MergeRequestIID, 10),
			"note_id":           strconv.FormatInt(award.NoteID, 10),
			"awardable_id":      strconv.FormatInt(award.AwardableID, 10),
			"awardable_type":    award.AwardableType,
			"created_at":        award.CreatedAt,
		},
		Raw: award,
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
			"project_id":  strconv.FormatInt(pipeline.ProjectID, 10),
			"status":      pipeline.Status,
			"source":      pipeline.Source,
			"ref":         pipeline.Ref,
			"sha":         pipeline.SHA,
			"user":        pipeline.User,
			"duration":    strconv.FormatInt(pipeline.Duration, 10),
			"started_at":  pipeline.StartedAt,
			"finished_at": pipeline.FinishedAt,
			"updated_at":  pipeline.UpdatedAt,
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
			"additions":        strconv.FormatInt(commit.Additions, 10),
			"deletions":        strconv.FormatInt(commit.Deletions, 10),
			"total":            strconv.FormatInt(commit.Total, 10),
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

func (a gitlabAccessor) compareRecord(compare Compare) coredatasource.Record {
	return coredatasource.Record{
		ID:         compare.ID,
		Datasource: a.spec.Name,
		Entity:     CompareEntity,
		Title:      fmt.Sprintf("%s %s..%s", compare.ProjectID, compare.From, compare.To),
		Content:    strings.Join(cleaned([]string{compare.HeadCommit.Title, compare.DiffPreview}), "\n"),
		URL:        compare.WebURL,
		Metadata: map[string]string{
			"id":         compare.ID,
			"project_id": compare.ProjectID,
			"from":       compare.From,
			"to":         compare.To,
			"straight":   strconv.FormatBool(compare.Straight),
			"path":       compare.Path,
			"timeout":    strconv.FormatBool(compare.Timeout),
			"same_ref":   strconv.FormatBool(compare.SameRef),
			"additions":  strconv.Itoa(compare.Additions),
			"deletions":  strconv.Itoa(compare.Deletions),
			"truncated":  strconv.FormatBool(compare.Truncated),
		},
		Raw: compare,
	}
}

func (a gitlabAccessor) blameRecord(blame Blame) coredatasource.Record {
	var content []string
	for _, item := range blame.Ranges {
		content = append(content, item.CommitTitle, strings.Join(item.Lines, "\n"))
	}
	return coredatasource.Record{
		ID:         blame.ID,
		Datasource: a.spec.Name,
		Entity:     BlameEntity,
		Title:      blame.Path,
		Content:    strings.Join(cleaned(content), "\n"),
		Metadata: map[string]string{
			"id":         blame.ID,
			"project_id": blame.ProjectID,
			"ref":        blame.Ref,
			"path":       blame.Path,
		},
		Raw: blame,
	}
}

func (a gitlabAccessor) blobSearchRecord(result BlobSearchResult) coredatasource.Record {
	return coredatasource.Record{
		ID:         result.ID,
		Datasource: a.spec.Name,
		Entity:     BlobSearchEntity,
		Title:      result.Path,
		Content:    result.Snippet,
		Metadata: map[string]string{
			"id":         result.ID,
			"project_id": strconv.FormatInt(result.ProjectID, 10),
			"basename":   result.Basename,
			"filename":   result.Filename,
			"path":       result.Path,
			"ref":        result.Ref,
			"start_line": strconv.FormatInt(result.StartLine, 10),
			"truncated":  strconv.FormatBool(result.Truncated),
		},
		Raw: result,
	}
}

func (a gitlabAccessor) projectLanguageRecord(language ProjectLanguage) coredatasource.Record {
	return coredatasource.Record{
		ID:         language.ID,
		Datasource: a.spec.Name,
		Entity:     ProjectLanguageEntity,
		Title:      language.Language,
		Content:    fmt.Sprintf("%s %.2f%%", language.Language, language.Share),
		Metadata: map[string]string{
			"id":         language.ID,
			"project_id": language.ProjectID,
			"language":   language.Language,
			"share":      strconv.FormatFloat(float64(language.Share), 'f', -1, 32),
		},
		Raw: language,
	}
}

func (a gitlabAccessor) projectContributorRecord(contributor ProjectContributor) coredatasource.Record {
	return coredatasource.Record{
		ID:         contributor.ID,
		Datasource: a.spec.Name,
		Entity:     ProjectContributorEntity,
		Title:      firstNonEmpty(contributor.Name, contributor.Email),
		Content:    strings.Join(cleaned([]string{contributor.Name, contributor.Email}), " "),
		Metadata: map[string]string{
			"id":         contributor.ID,
			"project_id": contributor.ProjectID,
			"name":       contributor.Name,
			"email":      contributor.Email,
			"commits":    strconv.FormatInt(contributor.Commits, 10),
			"additions":  strconv.FormatInt(contributor.Additions, 10),
			"deletions":  strconv.FormatInt(contributor.Deletions, 10),
		},
		Raw: contributor,
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

func (a gitlabAccessor) snippetRecord(snippet Snippet) coredatasource.Record {
	return coredatasource.Record{
		ID:         strconv.FormatInt(snippet.ID, 10),
		Datasource: a.spec.Name,
		Entity:     SnippetEntity,
		Title:      snippet.Title,
		Content:    strings.Join(cleaned([]string{snippet.Title, snippet.Description, snippet.AuthorUsername}), " "),
		URL:        snippet.WebURL,
		Metadata: map[string]string{
			"id":              strconv.FormatInt(snippet.ID, 10),
			"snippet_id":      strconv.FormatInt(snippet.ID, 10),
			"title":           snippet.Title,
			"visibility":      snippet.Visibility,
			"author_username": snippet.AuthorUsername,
			"raw_url":         snippet.RawURL,
			"created_at":      snippet.CreatedAt,
			"updated_at":      snippet.UpdatedAt,
		},
		Raw: snippet,
	}
}

func (a gitlabAccessor) snippetFileRecord(file SnippetFile) coredatasource.Record {
	return coredatasource.Record{
		ID:         file.ID,
		Datasource: a.spec.Name,
		Entity:     SnippetFileEntity,
		Title:      file.FilePath,
		Content:    file.Content,
		URL:        file.RawURL,
		Metadata: map[string]string{
			"id":         file.ID,
			"snippet_id": strconv.FormatInt(file.SnippetID, 10),
			"file_path":  file.FilePath,
			"raw_url":    file.RawURL,
			"truncated":  strconv.FormatBool(file.Truncated),
		},
		Raw: file,
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
		"id":              membership.ID,
		"user_id":         strconv.FormatInt(membership.UserID, 10),
		"source_id":       strconv.FormatInt(membership.SourceID, 10),
		"source_name":     membership.SourceName,
		"source_type":     membership.SourceType,
		"source_path":     membership.SourcePath,
		"source_url":      membership.SourceURL,
		"access_level":    membership.AccessLevel,
		"role":            membership.Role,
		"direct":          strconv.FormatBool(membership.Direct),
		"source_archived": strconv.FormatBool(membership.SourceArchived),
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
		ID:             id,
		UserID:         userID,
		SourceID:       sourceID,
		SourceName:     record.Metadata["source_name"],
		SourceType:     sourceType,
		SourcePath:     record.Metadata["source_path"],
		SourceURL:      firstNonEmpty(record.Metadata["source_url"], record.URL),
		AccessLevel:    record.Metadata["access_level"],
		Role:           record.Metadata["role"],
		Direct:         parseMetadataBool(record.Metadata, "direct"),
		SourceArchived: parseMetadataBool(record.Metadata, "source_archived"),
	}, nil
}

func parseMetadataBool(metadata map[string]string, key string) bool {
	value := strings.TrimSpace(metadata[key])
	if value == "" {
		return false
	}
	parsed, _ := strconv.ParseBool(value)
	return parsed
}

func gitlabDefaultFilters(entity coredatasource.EntityType, filters map[string]string) map[string]string {
	out := cloneFilters(filters)
	switch entity {
	case ProjectEntity:
		if !hasExplicitFilter(out, "archived") {
			out["archived"] = "false"
		}
	case MembershipEntity:
		if !hasExplicitFilter(out, "source_archived") {
			out["source_archived"] = "false"
		}
	}
	return out
}

func cloneFilters(filters map[string]string) map[string]string {
	if len(filters) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(filters))
	for key, value := range filters {
		out[key] = value
	}
	return out
}

func hasExplicitFilter(filters map[string]string, key string) bool {
	value, ok := filters[key]
	return ok && strings.TrimSpace(value) != ""
}

func (a gitlabAccessor) searchMemberships(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) ([]Membership, error) {
	limit = normalizedLimit(limit)
	if userIDText := strings.TrimSpace(filters["user_id"]); userIDText != "" {
		userID, err := strconv.ParseInt(userIDText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitlab user_id filter must be numeric")
		}
		memberships, err := allUserMemberships(ctx, client, userID)
		if err != nil {
			return nil, err
		}
		return limitMemberships(filterMembershipSearch(filterMemberships(memberships, filters), query), limit), nil
	}
	var out []Membership
	cursor := ""
	for len(out) < limit {
		page, next, err := a.listMembershipCorpusPage(ctx, client, membershipCorpusDefaultSize, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, filterMembershipSearch(filterMemberships(page, filters), query)...)
		if next == "" {
			break
		}
		cursor = next
	}
	sortMemberships(out)
	return limitMemberships(out, limit), nil
}

func filterMemberships(memberships []Membership, filters map[string]string) []Membership {
	if len(filters) == 0 {
		return memberships
	}
	out := make([]Membership, 0, len(memberships))
	for _, membership := range memberships {
		if membershipMatchesFilters(membership, filters) {
			out = append(out, membership)
		}
	}
	return out
}

func membershipMatchesFilters(membership Membership, filters map[string]string) bool {
	for key, want := range filters {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		got, ok := membershipFilterValue(membership, key)
		if !ok || !strings.EqualFold(strings.TrimSpace(got), want) {
			return false
		}
	}
	return true
}

func membershipFilterValue(membership Membership, key string) (string, bool) {
	switch key {
	case "id":
		return membership.ID, true
	case "user_id":
		return strconv.FormatInt(membership.UserID, 10), true
	case "source_id":
		return strconv.FormatInt(membership.SourceID, 10), true
	case "source_name":
		return membership.SourceName, true
	case "source_type":
		return membership.SourceType, true
	case "source_path":
		return membership.SourcePath, true
	case "access_level":
		return membership.AccessLevel, true
	case "role":
		return membership.Role, true
	case "direct":
		return strconv.FormatBool(membership.Direct), true
	case "source_archived":
		return strconv.FormatBool(membership.SourceArchived), true
	default:
		return "", false
	}
}

func filterMembershipSearch(memberships []Membership, query string) []Membership {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return memberships
	}
	out := make([]Membership, 0, len(memberships))
	for _, membership := range memberships {
		text := strings.ToLower(strings.Join([]string{
			membership.ID,
			membership.SourceName,
			membership.SourceType,
			membership.SourcePath,
			membership.AccessLevel,
			membership.Role,
		}, " "))
		if strings.Contains(text, query) {
			out = append(out, membership)
		}
	}
	return out
}

func limitMemberships(memberships []Membership, limit int) []Membership {
	if limit <= 0 || len(memberships) <= limit {
		return memberships
	}
	return memberships[:limit]
}

func searchProjects(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]Project, error) {
	if client == nil {
		return nil, fmt.Errorf("gitlabplugin: client is nil")
	}
	search := strings.TrimSpace(query)
	simple := true
	var searchParam *string
	if search != "" {
		searchParam = &search
	}
	opt := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
		Search:      searchParam,
		Simple:      &simple,
	}
	if value := strings.TrimSpace(filters["archived"]); value != "" {
		archived, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid archived filter %q", value)
		}
		opt.Archived = &archived
	}
	for key, dest := range map[string]**bool{
		"owned":      &opt.Owned,
		"starred":    &opt.Starred,
		"membership": &opt.Membership,
	} {
		if value := strings.TrimSpace(filters[key]); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("invalid %s filter %q", key, value)
			}
			*dest = &parsed
		}
	}
	if value := strings.TrimSpace(filters["visibility"]); value != "" {
		visibility := gitlab.VisibilityValue(value)
		opt.Visibility = &visibility
	}
	if value := strings.TrimSpace(filters["order_by"]); value != "" {
		if value == "activity" {
			value = "last_activity_at"
		}
		if value == "created" {
			value = "created_at"
		}
		opt.OrderBy = &value
	}
	if value := strings.TrimSpace(filters["sort"]); value != "" {
		opt.Sort = &value
	}
	projects, err := client.ListProjects(ctx, opt)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(projects))
	groupPath := strings.Trim(strings.TrimSpace(filters["group_path"]), "/")
	for _, project := range projects {
		converted := projectFromGitLab(project)
		if groupPath != "" && converted.PathWithNamespace != groupPath && !strings.HasPrefix(converted.PathWithNamespace, groupPath+"/") {
			continue
		}
		out = append(out, converted)
	}
	return out, nil
}

func searchActivity(ctx context.Context, client gitlabClient, filters map[string]string, limit int) ([]Activity, error) {
	limit = normalizedLimit(limit)
	if override := strings.TrimSpace(filters["limit"]); override != "" {
		if parsed, err := strconv.Atoi(override); err == nil && parsed > 0 {
			limit = normalizedLimit(parsed)
		}
	}
	var projects []Project
	if project := strings.TrimSpace(firstNonEmpty(filters["project_id"], filters["project"])); project != "" {
		resolved, err := getProject(ctx, client, project)
		if err != nil {
			return nil, err
		}
		projects = []Project{resolved}
	} else {
		listFilters := gitlabDefaultFilters(ProjectEntity, map[string]string{"archived": filters["archived"]})
		projectsPage, err := searchProjects(ctx, client, "", listFilters, limit, 1)
		if err != nil {
			return nil, err
		}
		groupPath := strings.TrimSpace(filters["group_path"])
		for _, project := range projectsPage {
			if groupPath != "" && !strings.HasPrefix(project.PathWithNamespace, groupPath+"/") && project.PathWithNamespace != groupPath {
				continue
			}
			projects = append(projects, project)
		}
	}
	since, err := gitlabTimeFilter(filters["since"])
	if err != nil {
		return nil, err
	}
	return aggregateActivity(ctx, client, projects, since)
}

func aggregateActivity(ctx context.Context, client gitlabClient, projects []Project, since *time.Time) ([]Activity, error) {
	type job struct {
		index   int
		project Project
	}
	jobs := make(chan job)
	out := make([]Activity, len(projects))
	workers := 4
	if len(projects) < workers {
		workers = len(projects)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				out[item.index] = activityForProject(ctx, client, item.project, since)
			}
		}()
	}
	for i, project := range projects {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		case jobs <- job{index: i, project: project}:
		}
	}
	close(jobs)
	wg.Wait()
	return out, nil
}

func activityForProject(ctx context.Context, client gitlabClient, project Project, since *time.Time) Activity {
	activity := activityFromProject(project)
	projectRef := projectID(projectIDString(project))
	commitOpts := &gitlab.ListCommitsOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}, Since: since}
	commits, err := client.ListCommits(ctx, projectRef, commitOpts)
	if err != nil {
		activity.Errors = append(activity.Errors, "commits: "+err.Error())
	} else {
		activity.RecentCommitCount = len(commits)
	}
	mrOpts := &gitlab.ListProjectMergeRequestsOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}}
	if since != nil {
		mrOpts.UpdatedAfter = since
	}
	mrs, err := client.ListProjectMergeRequests(ctx, projectRef, mrOpts)
	if err != nil {
		activity.Errors = append(activity.Errors, "merge_requests: "+err.Error())
	} else {
		activity.RecentMRCount = len(mrs)
	}
	pipelineOpts := &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}, UpdatedAfter: since}
	pipelines, err := client.ListProjectPipelines(ctx, projectRef, pipelineOpts)
	if err != nil {
		activity.Errors = append(activity.Errors, "pipelines: "+err.Error())
	} else {
		activity.RecentPipelineCount = len(pipelines)
	}
	return activity
}

func gitlabTimeFilter(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	if len(value) >= 2 {
		unit := value[len(value)-1]
		number := value[:len(value)-1]
		n, err := strconv.Atoi(number)
		if err == nil && n >= 0 {
			var duration time.Duration
			switch unit {
			case 'm':
				duration = time.Duration(n) * time.Minute
			case 'h':
				duration = time.Duration(n) * time.Hour
			case 'd':
				duration = time.Duration(n) * 24 * time.Hour
			default:
				return nil, fmt.Errorf("invalid time filter %q", value)
			}
			parsed := time.Now().UTC().Add(-duration)
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid time filter %q", value)
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
	if value := strings.TrimSpace(firstNonEmpty(filters["ref"], filters["ref_name"], filters["branch"])); value != "" {
		opt.RefName = &value
	}
	if value := strings.TrimSpace(filters["path"]); value != "" {
		opt.Path = &value
	}
	if value := strings.TrimSpace(filters["author"]); value != "" {
		opt.Author = &value
	}
	if value := strings.TrimSpace(filters["since"]); value != "" {
		since, err := gitlabTimeFilter(value)
		if err != nil {
			return nil, err
		}
		opt.Since = since
	}
	if value := strings.TrimSpace(filters["until"]); value != "" {
		until, err := gitlabTimeFilter(value)
		if err != nil {
			return nil, err
		}
		opt.Until = until
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

func searchRepositoryFiles(ctx context.Context, client gitlabClient, filters map[string]string) ([]RepositoryFile, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(firstNonEmpty(filters["ref"], "HEAD"))
	path := strings.TrimSpace(filters["path"])
	if path == "" {
		return nil, fmt.Errorf("gitlab %s path filter is required", RepositoryFileEntity)
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	file, err := getRepositoryFile(ctx, client, projectID, ref, path)
	if err != nil {
		return nil, err
	}
	file.ProjectID = projectLabel
	include := false
	if value := strings.TrimSpace(filters["include_content"]); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid include_content filter %q", value)
		}
		include = parsed
	}
	if !include {
		file.ContentPreview = ""
	} else if maxBytes := maxBytesFilter(filters, 4*1024); maxBytes > 0 {
		file.ContentPreview, _ = boundedText(file.ContentPreview, maxBytes)
	}
	return []RepositoryFile{file}, nil
}

func searchCompare(ctx context.Context, client gitlabClient, filters map[string]string) (Compare, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return Compare{}, err
	}
	from := strings.TrimSpace(filters["from"])
	if from == "" {
		return Compare{}, fmt.Errorf("gitlab %s from filter is required", CompareEntity)
	}
	to := strings.TrimSpace(filters["to"])
	if to == "" {
		return Compare{}, fmt.Errorf("gitlab %s to filter is required", CompareEntity)
	}
	straight := false
	if value := strings.TrimSpace(filters["straight"]); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Compare{}, fmt.Errorf("gitlab %s straight filter must be boolean", CompareEntity)
		}
		straight = parsed
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	opts := &gitlab.CompareOptions{From: &from, To: &to, Straight: &straight}
	compare, err := client.CompareRefs(ctx, projectID, opts)
	if err != nil {
		return Compare{}, err
	}
	path := strings.TrimSpace(filters["path"])
	includeDiff := path != ""
	if value := strings.TrimSpace(filters["include_diff"]); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Compare{}, fmt.Errorf("gitlab %s include_diff filter must be boolean", CompareEntity)
		}
		includeDiff = parsed && path != ""
	}
	return compareFromGitLab(projectLabel, from, to, path, straight, includeDiff, maxBytesFilter(filters, 16*1024), compare), nil
}

func searchBlame(ctx context.Context, client gitlabClient, filters map[string]string) (Blame, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return Blame{}, err
	}
	ref := strings.TrimSpace(firstNonEmpty(filters["ref"], "HEAD"))
	path := strings.TrimSpace(filters["path"])
	if path == "" {
		return Blame{}, fmt.Errorf("gitlab %s path filter is required", BlameEntity)
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	opts, err := blameOptions(ref, filters)
	if err != nil {
		return Blame{}, err
	}
	blame, err := getBlame(ctx, client, projectID, ref, path, opts)
	if err != nil {
		return Blame{}, err
	}
	blame.ProjectID = projectLabel
	blame.ID = blameID(projectLabel, ref, path)
	return blame, nil
}

func getBlame(ctx context.Context, client gitlabClient, project any, ref, path string, opts *gitlab.GetFileBlameOptions) (Blame, error) {
	if opts == nil {
		opts = &gitlab.GetFileBlameOptions{Ref: &ref}
	}
	ranges, err := client.GetFileBlame(ctx, project, path, opts)
	if err != nil {
		return Blame{}, err
	}
	return blameFromGitLab(projectIDLabel(project), ref, path, ranges), nil
}

func blameOptions(ref string, filters map[string]string) (*gitlab.GetFileBlameOptions, error) {
	opts := &gitlab.GetFileBlameOptions{Ref: &ref}
	if value := strings.TrimSpace(filters["range_start"]); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitlab %s range_start filter must be numeric", BlameEntity)
		}
		opts.RangeStart = &parsed
	}
	if value := strings.TrimSpace(filters["range_end"]); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitlab %s range_end filter must be numeric", BlameEntity)
		}
		opts.RangeEnd = &parsed
	}
	return opts, nil
}

func searchBlobs(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) ([]BlobSearchResult, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(firstNonEmpty(query, filters["query"]))
	if query == "" {
		return nil, fmt.Errorf("gitlab %s query filter is required", BlobSearchEntity)
	}
	projectID, _ := resolveProjectIdentifier(ctx, client, project)
	blobs, err := client.SearchBlobsByProject(ctx, projectID, query, &gitlab.SearchOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
	if err != nil {
		return nil, err
	}
	out := make([]BlobSearchResult, 0, len(blobs))
	for _, blob := range blobs {
		out = append(out, blobSearchResultFromGitLab(blob))
	}
	return out, nil
}

func searchProjectLanguages(ctx context.Context, client gitlabClient, filters map[string]string) ([]ProjectLanguage, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	return projectLanguages(ctx, client, projectID, projectLabel)
}

func projectLanguages(ctx context.Context, client gitlabClient, project any, projectLabel string) ([]ProjectLanguage, error) {
	languages, err := client.GetProjectLanguages(ctx, project)
	if err != nil {
		return nil, err
	}
	if languages == nil {
		return nil, nil
	}
	out := make([]ProjectLanguage, 0, len(*languages))
	for language, share := range *languages {
		out = append(out, projectLanguageFromGitLab(projectLabel, language, share))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Share == out[j].Share {
			return out[i].Language < out[j].Language
		}
		return out[i].Share > out[j].Share
	})
	return out, nil
}

func searchProjectContributors(ctx context.Context, client gitlabClient, filters map[string]string, limit int) ([]ProjectContributor, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	projectID, projectLabel := resolveProjectIdentifier(ctx, client, project)
	if value := strings.TrimSpace(filters["max_count"]); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("gitlab %s max_count filter must be numeric", ProjectContributorEntity)
		}
		limit = parsed
	}
	return projectContributors(ctx, client, projectID, projectLabel, limit)
}

func projectContributors(ctx context.Context, client gitlabClient, project any, projectLabel string, limit int) ([]ProjectContributor, error) {
	contributors, err := client.ListProjectContributors(ctx, project, &gitlab.ListContributorsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
	if err != nil {
		return nil, err
	}
	out := make([]ProjectContributor, 0, len(contributors))
	for _, contributor := range contributors {
		out = append(out, projectContributorFromGitLab(projectLabel, contributor))
	}
	return out, nil
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
	var jobs []*gitlab.Job
	pipelineID, err := int64Filter(JobEntity, filters, "pipeline_id", false)
	if err != nil {
		return nil, err
	}
	if pipelineID != 0 {
		jobs, err = client.ListPipelineJobs(ctx, projectID, pipelineID, opt)
	} else {
		jobs, err = client.ListProjectJobs(ctx, projectID, opt)
	}
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(jobs))
	jobName := strings.TrimSpace(filters["job_name"])
	for _, job := range jobs {
		converted := jobFromGitLab(projectLabel, job)
		if jobName != "" && converted.Name != jobName {
			continue
		}
		out = append(out, converted)
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
	if value := strings.TrimSpace(filters["status"]); value != "" {
		states := []gitlab.BuildStateValue{gitlab.BuildStateValue(value)}
		opt.Scope = &states
	}
	return opt, nil
}

func searchJobTrace(ctx context.Context, client gitlabClient, filters map[string]string) (JobTrace, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return JobTrace{}, err
	}
	projectID, _ := resolveProjectIdentifier(ctx, client, project)
	maxBytes := maxBytesFilter(filters, 64*1024)
	jobID, err := int64Filter(JobTraceEntity, filters, "job_id", false)
	if err != nil {
		return JobTrace{}, err
	}
	if jobID != 0 {
		return getJobTrace(ctx, client, projectID, jobID, maxBytes)
	}
	pipelineID, err := int64Filter(JobTraceEntity, filters, "pipeline_id", true)
	if err != nil {
		return JobTrace{}, err
	}
	jobName := strings.TrimSpace(filters["job_name"])
	if jobName == "" {
		return JobTrace{}, fmt.Errorf("gitlab %s job_name filter is required", JobTraceEntity)
	}
	jobs, err := client.ListPipelineJobs(ctx, projectID, pipelineID, &gitlab.ListJobsOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}})
	if err != nil {
		return JobTrace{}, err
	}
	var matches []int64
	var names []string
	for _, job := range jobs {
		if job == nil {
			continue
		}
		names = append(names, job.Name)
		if job.Name == jobName {
			matches = append(matches, job.ID)
		}
	}
	switch len(matches) {
	case 0:
		return JobTrace{}, fmt.Errorf("gitlab %s job_name %q not found; available jobs: %s", JobTraceEntity, jobName, strings.Join(cleaned(names), ", "))
	case 1:
		return getJobTrace(ctx, client, projectID, matches[0], maxBytes)
	default:
		ids := make([]string, 0, len(matches))
		for _, id := range matches {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		return JobTrace{}, fmt.Errorf("gitlab %s job_name %q is ambiguous; matching job ids: %s", JobTraceEntity, jobName, strings.Join(ids, ", "))
	}
}

func getJobTrace(ctx context.Context, client gitlabClient, project any, jobID int64, maxBytes int) (JobTrace, error) {
	data, err := client.GetTraceFile(ctx, project, jobID)
	if err != nil {
		return JobTrace{}, err
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	trace := string(data)
	trace, truncated := boundedText(trace, maxBytes)
	projectLabel := projectIDLabel(project)
	return JobTrace{ID: jobTraceID(projectLabel, jobID), ProjectID: projectLabel, JobID: jobID, Trace: trace, Truncated: truncated}, nil
}

func listSnippets(ctx context.Context, client gitlabClient, perPage, page int) ([]Snippet, error) {
	snippets, err := client.ListSnippets(ctx, &gitlab.ListSnippetsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Snippet, 0, len(snippets))
	for _, snippet := range snippets {
		out = append(out, snippetFromGitLab(snippet))
	}
	return out, nil
}

func getSnippet(ctx context.Context, client gitlabClient, snippetID int64) (Snippet, error) {
	snippet, err := client.GetSnippet(ctx, snippetID)
	if err != nil {
		return Snippet{}, err
	}
	return snippetFromGitLab(snippet), nil
}

func searchSnippetFiles(ctx context.Context, client gitlabClient, query string, filters map[string]string) ([]SnippetFile, error) {
	snippetID, err := int64Filter(SnippetFileEntity, filters, "snippet_id", true)
	if err != nil {
		return nil, err
	}
	files, err := snippetFiles(ctx, client, snippetID, strings.TrimSpace(filters["file_path"]), maxBytesFilter(filters, 8192))
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(firstNonEmpty(query, filters["query"])))
	if query == "" {
		return files, nil
	}
	out := make([]SnippetFile, 0, len(files))
	for _, file := range files {
		if strings.Contains(strings.ToLower(file.FilePath), query) || strings.Contains(strings.ToLower(file.Content), query) {
			out = append(out, file)
		}
	}
	return out, nil
}

func snippetFiles(ctx context.Context, client gitlabClient, snippetID int64, path string, maxBytes int) ([]SnippetFile, error) {
	if snippetID == 0 {
		return nil, fmt.Errorf("gitlab %s snippet_id filter is required", SnippetFileEntity)
	}
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	snippet, err := client.GetSnippet(ctx, snippetID)
	if err != nil {
		return nil, err
	}
	content, err := client.GetSnippetContent(ctx, snippetID)
	if err != nil {
		return nil, err
	}
	text, truncated := boundedText(string(content), maxBytes)
	metadataFiles := []gitlab.SnippetFile{{Path: "snippet", RawURL: ""}}
	if snippet != nil && len(snippet.Files) > 0 {
		metadataFiles = snippet.Files
	}
	out := make([]SnippetFile, 0, len(metadataFiles))
	for i, file := range metadataFiles {
		if strings.TrimSpace(path) != "" && file.Path != path {
			continue
		}
		fileContent := ""
		fileTruncated := false
		if i == 0 {
			fileContent = text
			fileTruncated = truncated
		}
		out = append(out, snippetFileFromGitLab(snippetID, file, fileContent, fileTruncated))
	}
	return out, nil
}

func int64Filter(entity coredatasource.EntityType, filters map[string]string, key string, required bool) (int64, error) {
	value := strings.TrimSpace(filters[key])
	if value == "" {
		if required {
			return 0, fmt.Errorf("gitlab %s %s filter is required", entity, key)
		}
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gitlab %s %s filter must be numeric", entity, key)
	}
	return parsed, nil
}

func maxBytesFilter(filters map[string]string, fallback int) int {
	value := strings.TrimSpace(filters["max_bytes"])
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
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

func resolveProjectIdentifier(_ context.Context, _ gitlabClient, id string) (any, string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	return projectID(id), id
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
	projects, searchErr := searchProjects(ctx, client, id, nil, defaultPageSize, 1)
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
	members, err := client.ListGroupMembers(ctx, group, &gitlab.ListGroupMembersOptions{
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
	memberships, err := allUserMemberships(ctx, client, userID)
	if err != nil {
		return nil, err
	}
	return pageMemberships(memberships, normalizedLimit(perPage), page), nil
}

func allUserMemberships(ctx context.Context, client gitlabClient, userID int64) ([]Membership, error) {
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
		members, err := client.ListGroupMembers(ctx, projectID(groupIDString(group)), &gitlab.ListGroupMembersOptions{
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
		members, err := client.ListProjectMembers(ctx, projectID(projectIDString(project)), &gitlab.ListProjectMembersOptions{
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
	return out, nil
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
	membershipCorpusDefaultSize = 100
	membershipSourcePageSize    = 100
)

type gitLabMembershipSourceCache struct {
	mu            sync.Mutex
	groupsReady   bool
	groups        []Group
	projectsReady bool
	projects      []Project
}

func (c *gitLabMembershipSourceCache) groupPage(ctx context.Context, client gitlabClient, page int) ([]Group, bool, error) {
	if c == nil {
		return listMembershipGroupsPage(ctx, client, page)
	}
	groups, err := c.allGroups(ctx, client)
	if err != nil {
		return nil, false, err
	}
	pageGroups, hasNext := membershipSourcePage(groups, page)
	return pageGroups, hasNext, nil
}

func (c *gitLabMembershipSourceCache) projectPage(ctx context.Context, client gitlabClient, page int) ([]Project, bool, error) {
	if c == nil {
		projects, err := listMembershipProjectsPage(ctx, client, page)
		return projects, len(projects) >= membershipSourcePageSize, err
	}
	projects, err := c.allProjects(ctx, client)
	if err != nil {
		return nil, false, err
	}
	pageProjects, hasNext := membershipSourcePage(projects, page)
	return pageProjects, hasNext, nil
}

func (c *gitLabMembershipSourceCache) allGroups(ctx context.Context, client gitlabClient) ([]Group, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.groupsReady {
		return append([]Group(nil), c.groups...), nil
	}
	groups, err := listMembershipGroups(ctx, client)
	if err != nil {
		return nil, err
	}
	c.groups = append([]Group(nil), groups...)
	c.groupsReady = true
	return append([]Group(nil), c.groups...), nil
}

func (c *gitLabMembershipSourceCache) allProjects(ctx context.Context, client gitlabClient) ([]Project, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.projectsReady {
		return append([]Project(nil), c.projects...), nil
	}
	projects, err := listMembershipProjects(ctx, client)
	if err != nil {
		return nil, err
	}
	c.projects = append([]Project(nil), projects...)
	c.projectsReady = true
	return append([]Project(nil), c.projects...), nil
}

func membershipSourcePage[T any](items []T, page int) ([]T, bool) {
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * membershipSourcePageSize
	if start >= len(items) {
		return nil, false
	}
	end := start + membershipSourcePageSize
	if end > len(items) {
		end = len(items)
	}
	return append([]T(nil), items[start:end]...), end < len(items)
}

func (a gitlabAccessor) listMembershipCorpusPage(ctx context.Context, client gitlabClient, limit int, cursor string) ([]Membership, string, error) {
	limit = normalizedMembershipCorpusLimit(limit)
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
			next, err := a.appendGroupMembershipCorpus(ctx, client, &state, limit-len(out), &out)
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
			next, err := a.appendProjectMembershipCorpus(ctx, client, &state, limit-len(out), &out)
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

func normalizedMembershipCorpusLimit(limit int) int {
	if limit <= 0 {
		return membershipCorpusDefaultSize
	}
	return normalizedLimit(limit)
}

func (a gitlabAccessor) appendGroupMembershipCorpus(ctx context.Context, client gitlabClient, state *membershipCorpusCursor, limit int, out *[]Membership) (string, error) {
	for {
		groups, hasNextPage, err := a.membershipSources.groupPage(ctx, client, state.sourcePage)
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
		members, err := client.ListGroupMembers(ctx, projectID(groupIDString(group)), &gitlab.ListGroupMembersOptions{
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

func (a gitlabAccessor) appendProjectMembershipCorpus(ctx context.Context, client gitlabClient, state *membershipCorpusCursor, limit int, out *[]Membership) (string, error) {
	for {
		projects, hasNextPage, err := a.membershipSources.projectPage(ctx, client, state.sourcePage)
		if err != nil {
			return "", err
		}
		if state.source >= len(projects) {
			if hasNextPage {
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
		members, err := client.ListProjectMembers(ctx, projectID(projectIDString(project)), &gitlab.ListProjectMembersOptions{
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
	projects, err := client.ListProjects(ctx, &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(page)},
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
	for page := 1; ; page++ {
		projects, err := client.ListProjects(ctx, &gitlab.ListProjectsOptions{
			ListOptions: gitlab.ListOptions{PerPage: 100, Page: int64(page)},
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
			Archived:          membership.SourceArchived,
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
	if project, iid, ok := mergeRequestRefQuery(query); ok {
		mr, err := client.GetMergeRequest(ctx, project, iid, nil)
		if err != nil {
			return nil, err
		}
		return []MergeRequest{mergeRequestFromFull(mr)}, nil
	}
	project := firstNonEmpty(filters["project_id"], filters["project"])
	if iid, ok, err := mergeRequestIIDFilter(filters); err != nil {
		return nil, err
	} else if ok {
		if project == "" {
			return nil, fmt.Errorf("project_id filter is required with iid for gitlab.merge_request")
		}
		projectID, _ := resolveProjectIdentifier(ctx, client, project)
		mr, err := client.GetMergeRequest(ctx, projectID, iid, nil)
		if err != nil {
			return nil, err
		}
		return []MergeRequest{mergeRequestFromFull(mr)}, nil
	}
	if project != "" {
		projectID, _ := resolveProjectIdentifier(ctx, client, project)
		return listProjectMergeRequests(ctx, client, projectID, query, filters, perPage, page)
	}
	return listMergeRequests(ctx, client, query, filters, perPage, page)
}

func mergeRequestIIDFilter(filters map[string]string) (int64, bool, error) {
	value := strings.TrimSpace(firstNonEmpty(filters["iid"], filters["merge_request_iid"]))
	if value == "" {
		return 0, false, nil
	}
	iid, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("invalid merge request iid %q", value)
	}
	return iid, true, nil
}

func mergeRequestRefQuery(query string) (any, int64, bool) {
	query = strings.TrimSpace(query)
	if query == "" || strings.ContainsAny(query, " \t\r\n") {
		return nil, 0, false
	}
	project, iid, err := parseMergeRequestID(query)
	if err != nil {
		return nil, 0, false
	}
	return project, iid, true
}

func listMergeRequests(ctx context.Context, client gitlabClient, query string, filters map[string]string, perPage, page int) ([]MergeRequest, error) {
	opt, err := mergeRequestListOptions(query, filters, perPage, page)
	if err != nil {
		return nil, err
	}
	mrs, err := client.ListMergeRequests(ctx, &gitlab.ListMergeRequestsOptions{
		ListOptions:      opt.ListOptions,
		State:            opt.State,
		OrderBy:          opt.OrderBy,
		Sort:             opt.Sort,
		Labels:           opt.Labels,
		Search:           opt.Search,
		SourceBranch:     opt.SourceBranch,
		TargetBranch:     opt.TargetBranch,
		Scope:            opt.Scope,
		AuthorUsername:   opt.AuthorUsername,
		ReviewerUsername: opt.ReviewerUsername,
		Draft:            opt.Draft,
	})
	if err != nil {
		return nil, err
	}
	return mergeRequestsWithConflictFilter(ctx, client, mrs, filters)
}

func listProjectMergeRequests(ctx context.Context, client gitlabClient, project any, query string, filters map[string]string, perPage, page int) ([]MergeRequest, error) {
	opt, err := mergeRequestListOptions(query, filters, perPage, page)
	if err != nil {
		return nil, err
	}
	mrs, err := client.ListProjectMergeRequests(ctx, project, opt)
	if err != nil {
		return nil, err
	}
	return mergeRequestsWithConflictFilter(ctx, client, mrs, filters)
}

func mergeRequestListOptions(query string, filters map[string]string, perPage, page int) (*gitlab.ListProjectMergeRequestsOptions, error) {
	opt := &gitlab.ListProjectMergeRequestsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(perPage)), Page: int64(page)}}
	if value := strings.TrimSpace(query); value != "" {
		opt.Search = &value
	}
	if value := strings.TrimSpace(filters["state"]); value != "" {
		opt.State = &value
	} else if strings.TrimSpace(filters["scope"]) == "" && strings.TrimSpace(filters["draft"]) == "" && strings.TrimSpace(filters["include_wip"]) == "" {
		state := "opened"
		opt.State = &state
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
	if value := strings.TrimSpace(filters["labels"]); value != "" {
		labels := gitlab.LabelOptions(cleaned(strings.Split(value, ",")))
		opt.Labels = &labels
	}
	if value := strings.TrimSpace(filters["author_username"]); value != "" {
		opt.AuthorUsername = &value
	}
	if value := strings.TrimSpace(filters["reviewer_username"]); value != "" {
		opt.ReviewerUsername = &value
	}
	if value := strings.TrimSpace(filters["order_by"]); value != "" {
		opt.OrderBy = &value
	}
	if value := strings.TrimSpace(filters["sort"]); value != "" {
		opt.Sort = &value
	}
	for key, dest := range map[string]**time.Time{
		"created_after":  &opt.CreatedAfter,
		"created_before": &opt.CreatedBefore,
		"updated_after":  &opt.UpdatedAfter,
		"updated_before": &opt.UpdatedBefore,
	} {
		if value := strings.TrimSpace(filters[key]); value != "" {
			parsed, err := gitlabTimeFilter(value)
			if err != nil {
				return nil, err
			}
			*dest = parsed
		}
	}
	if value := strings.TrimSpace(filters["draft"]); value != "" {
		draft, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid draft filter %q", value)
		}
		opt.Draft = &draft
	} else if value := strings.TrimSpace(filters["include_wip"]); value != "" {
		includeWIP, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid include_wip filter %q", value)
		}
		if !includeWIP {
			draft := false
			opt.Draft = &draft
		}
	} else if includeWIP := strings.TrimSpace(filters["include_wip"]); includeWIP == "" {
		draft := false
		opt.Draft = &draft
	}
	return opt, nil
}

func mergeRequestsWithConflictFilter(ctx context.Context, client gitlabClient, mrs []*gitlab.BasicMergeRequest, filters map[string]string) ([]MergeRequest, error) {
	conflictsOnly := false
	if value := strings.TrimSpace(filters["conflicts_only"]); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid conflicts_only filter %q", value)
		}
		conflictsOnly = parsed
	}
	if !conflictsOnly {
		return mergeRequestsFromGitLab(mrs), nil
	}
	out := make([]MergeRequest, 0, len(mrs))
	for _, mr := range mrs {
		if mr == nil {
			continue
		}
		full, err := client.GetMergeRequest(ctx, mr.ProjectID, mr.IID, nil)
		if err != nil {
			return nil, err
		}
		converted := mergeRequestFromFull(full)
		if converted.HasConflicts {
			out = append(out, converted)
		}
	}
	return out, nil
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

func searchMergeRequestDiffLines(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) ([]MergeRequestDiffLine, error) {
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(firstNonEmpty(filters["path"], filters["new_path"], filters["old_path"], filters["file_path"]))
	line, err := int64Filter(MergeRequestDiffLineEntity, filters, "line", false)
	if err != nil {
		return nil, err
	}
	contextCount := 3
	if value := strings.TrimSpace(filters["context"]); value != "" {
		contextCount, err = strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("gitlab %s context filter must be numeric", MergeRequestDiffLineEntity)
		}
	}
	return mergeRequestDiffLines(ctx, client, project, iid, path, firstNonEmpty(query, filters["query"]), line, contextCount, limit)
}

func mergeRequestDiffLines(ctx context.Context, client gitlabClient, project any, iid int64, path string, query string, line int64, contextCount int, limit int) ([]MergeRequestDiffLine, error) {
	diffs, err := client.ListMergeRequestDiffs(ctx, project, iid, &gitlab.ListMergeRequestDiffsOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}})
	if err != nil {
		return nil, err
	}
	var lines []MergeRequestDiffLine
	for _, diff := range diffs {
		if diff == nil || (path != "" && diff.NewPath != path && diff.OldPath != path) {
			continue
		}
		lines = append(lines, parseMergeRequestDiffLines(project, iid, diff)...)
	}
	if len(lines) == 0 {
		if path == "" {
			return nil, fmt.Errorf("merge request !%d has no parsed diff lines", iid)
		}
		return nil, fmt.Errorf("file %q is not present in the merge request diff", path)
	}
	selected, err := selectDiffLines(lines, query, line, contextCount)
	if err != nil {
		return nil, err
	}
	if line == 0 && len(selected) > normalizedLimit(limit) {
		selected = selected[:normalizedLimit(limit)]
	}
	return selected, nil
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

func searchMergeRequestApproval(ctx context.Context, client gitlabClient, filters map[string]string) (MergeRequestApproval, error) {
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return MergeRequestApproval{}, err
	}
	return mergeRequestApproval(ctx, client, project, iid)
}

func mergeRequestApproval(ctx context.Context, client gitlabClient, project any, iid int64) (MergeRequestApproval, error) {
	approval, err := client.GetMergeRequestApprovals(ctx, project, iid)
	if err != nil {
		return MergeRequestApproval{}, err
	}
	return approvalFromGitLab(approval), nil
}

func searchMergeRequestChange(ctx context.Context, client gitlabClient, filters map[string]string) (MergeRequestChange, error) {
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return MergeRequestChange{}, err
	}
	return mergeRequestChange(ctx, client, project, iid, strings.TrimSpace(filters["path"]))
}

func mergeRequestChange(ctx context.Context, client gitlabClient, project any, iid int64, path string) (MergeRequestChange, error) {
	if _, err := client.GetMergeRequestChanges(ctx, project, iid, &gitlab.GetMergeRequestChangesOptions{}); err != nil {
		return MergeRequestChange{}, err
	}
	diffs, err := client.ListMergeRequestDiffs(ctx, project, iid, &gitlab.ListMergeRequestDiffsOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}})
	if err != nil {
		return MergeRequestChange{}, err
	}
	return changeFromDiffs(project, iid, diffs, path), nil
}

func searchMergeRequestReviewContext(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) (MergeRequestReviewContext, error) {
	if project, iid, ok := mergeRequestRefQuery(query); ok {
		return mergeRequestReviewContext(ctx, client, project, iid, limit)
	}
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return MergeRequestReviewContext{}, err
	}
	return mergeRequestReviewContext(ctx, client, project, iid, limit)
}

func mergeRequestReviewContext(ctx context.Context, client gitlabClient, project any, iid int64, limit int) (MergeRequestReviewContext, error) {
	mr, err := client.GetMergeRequest(ctx, project, iid, nil)
	if err != nil {
		return MergeRequestReviewContext{}, err
	}
	change, err := mergeRequestChange(ctx, client, project, iid, "")
	if err != nil {
		return MergeRequestReviewContext{}, err
	}
	approval, err := mergeRequestApproval(ctx, client, project, iid)
	if err != nil {
		return MergeRequestReviewContext{}, err
	}
	pipelines, err := pipelinesForMRProject(ctx, client, project, iid, limit)
	if err != nil {
		return MergeRequestReviewContext{}, err
	}
	var latest Pipeline
	var jobs []Job
	if len(pipelines) > 0 {
		latest = pipelines[0]
		jobs, err = listPipelineJobs(ctx, client, project, latest.ID, limit, 1)
		if err != nil {
			return MergeRequestReviewContext{}, err
		}
	}
	discussions, err := listDiscussions(ctx, client, project, iid, limit)
	if err != nil {
		return MergeRequestReviewContext{}, err
	}
	unresolved := 0
	systemNotesOnly := true
	for _, discussion := range discussions {
		if discussion.Resolvable && !discussion.Resolved {
			unresolved++
		}
		for _, note := range discussion.Notes {
			if !note.System {
				systemNotesOnly = false
			}
		}
	}
	converted := mergeRequestFromFull(mr)
	if converted.ProjectPath == "" {
		converted.ProjectPath = projectIDLabel(project)
	}
	return MergeRequestReviewContext{
		ID:              fmt.Sprintf("%s!review_context", mergeRequestID(projectIDLabel(project), iid)),
		MergeRequest:    converted,
		Change:          change,
		Approval:        approval,
		Pipelines:       pipelines,
		LatestPipeline:  latest,
		Jobs:            jobs,
		Discussions:     discussions,
		UnresolvedCount: unresolved,
		SystemNotesOnly: systemNotesOnly,
	}, nil
}

func searchDiscussions(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) ([]Discussion, error) {
	project, iid, err := projectAndMR(filters)
	if err != nil {
		return nil, err
	}
	discussions, err := listDiscussions(ctx, client, project, iid, limit)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return discussions, nil
	}
	out := discussions[:0]
	for _, discussion := range discussions {
		text := strings.ToLower(discussion.DiscussionID)
		for _, note := range discussion.Notes {
			text += " " + strings.ToLower(note.Body)
		}
		if strings.Contains(text, query) {
			out = append(out, discussion)
		}
	}
	return out, nil
}

func listDiscussions(ctx context.Context, client gitlabClient, project any, iid int64, limit int) ([]Discussion, error) {
	discussions, err := client.ListMergeRequestDiscussions(ctx, project, iid, &gitlab.ListMergeRequestDiscussionsOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}})
	if err != nil {
		return nil, err
	}
	out := make([]Discussion, 0, len(discussions))
	for _, discussion := range discussions {
		out = append(out, discussionFromGitLab(project, iid, discussion))
	}
	return out, nil
}

func searchAwardEmoji(ctx context.Context, client gitlabClient, filters map[string]string, limit int) ([]AwardEmoji, error) {
	project, err := projectFilter(filters)
	if err != nil {
		return nil, err
	}
	iid, err := int64Filter(AwardEmojiEntity, filters, "merge_request_iid", true)
	if err != nil {
		return nil, err
	}
	noteID, err := int64Filter(AwardEmojiEntity, filters, "note_id", false)
	if err != nil {
		return nil, err
	}
	projectID, _ := resolveProjectIdentifier(ctx, client, project)
	return listAwardEmoji(ctx, client, projectID, iid, noteID, limit)
}

func listAwardEmoji(ctx context.Context, client gitlabClient, project any, iid, noteID int64, limit int) ([]AwardEmoji, error) {
	opts := &gitlab.ListAwardEmojiOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: 1}}
	var (
		awards []*gitlab.AwardEmoji
		err    error
	)
	if noteID != 0 {
		awards, err = client.ListMergeRequestAwardEmojiOnNote(ctx, project, iid, noteID, opts)
	} else {
		awards, err = client.ListMergeRequestAwardEmoji(ctx, project, iid, opts)
	}
	if err != nil {
		return nil, err
	}
	out := make([]AwardEmoji, 0, len(awards))
	for _, award := range awards {
		out = append(out, awardEmojiFromGitLab(project, iid, noteID, award))
	}
	return out, nil
}

func searchPipelines(ctx context.Context, client gitlabClient, query string, filters map[string]string, limit int) ([]Pipeline, error) {
	if project, iid, ok := mergeRequestRefQuery(query); ok {
		return pipelinesForMRProject(ctx, client, project, iid, limit)
	}
	return listPipelines(ctx, client, filters, limit, 1)
}

func listPipelines(ctx context.Context, client gitlabClient, filters map[string]string, limit, page int) ([]Pipeline, error) {
	if ref := strings.TrimSpace(filters["merge_request"]); ref != "" {
		project, iid, err := parseMergeRequestID(ref)
		if err != nil {
			return nil, err
		}
		return pipelinesForMRProject(ctx, client, project, iid, limit)
	}
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
		return pipelinesForMRProject(ctx, client, projectID, iid, limit)
	}
	opt := &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: int64(normalizedLimit(limit)), Page: int64(page)}}
	if value := strings.TrimSpace(filters["status"]); value != "" {
		status := gitlab.BuildStateValue(value)
		opt.Status = &status
	}
	for key, dest := range map[string]**string{
		"ref":      &opt.Ref,
		"source":   &opt.Source,
		"sha":      &opt.SHA,
		"order_by": &opt.OrderBy,
		"sort":     &opt.Sort,
	} {
		if value := strings.TrimSpace(filters[key]); value != "" {
			*dest = &value
		}
	}
	if value := strings.TrimSpace(filters["updated_after"]); value != "" {
		updatedAfter, err := gitlabTimeFilter(value)
		if err != nil {
			return nil, err
		}
		opt.UpdatedAfter = updatedAfter
	}
	if value := strings.TrimSpace(filters["updated_before"]); value != "" {
		updatedBefore, err := gitlabTimeFilter(value)
		if err != nil {
			return nil, err
		}
		opt.UpdatedBefore = updatedBefore
	}
	pipelines, err := client.ListProjectPipelines(ctx, projectID, opt)
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
	out := make([]MergeRequestDiff, 0, len(diffs))
	for _, diff := range diffs {
		out = append(out, diffFromGitLab(project, iid, diff))
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

func pipelinesForMRProject(ctx context.Context, client gitlabClient, project any, iid int64, limit int) ([]Pipeline, error) {
	pipelines, err := client.ListMergeRequestPipelines(ctx, project, iid)
	if err != nil {
		return nil, err
	}
	return limitPipelines(pipelinesFromInfo(pipelines), limit), nil
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
