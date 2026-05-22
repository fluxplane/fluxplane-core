package gitlab

import (
	coredata "github.com/fluxplane/engine/core/data"
	runtimedata "github.com/fluxplane/engine/runtime/data"
)

const (
	GitLabProjectView         coredata.ViewName = "gitlab.project"
	GitLabUserView            coredata.ViewName = "gitlab.user"
	GitLabGroupView           coredata.ViewName = "gitlab.group"
	MembershipDataView        coredata.ViewName = "gitlab.membership"
	GitLabUserWithGroupsView  coredata.ViewName = "gitlab.user_with_groups"
	GitLabMRReviewContextView coredata.ViewName = "gitlab.merge_request_review_context"
)

// DataSourceSpec describes the GitLab source schema and default materialized views.
func DataSourceSpec() coredata.SourceSpec {
	return runtimedata.SourceFromDatasource("gitlab", Name, entitySpecs(), DataViews()...)
}

// DataViews returns the GitLab materializations the query API should prefer.
func DataViews() []coredata.ViewSpec {
	return []coredata.ViewSpec{
		runtimedata.ViewOf[Project](
			GitLabProjectView,
			coredata.EntityType(ProjectEntity),
			runtimedata.WithViewDescription("GitLab projects by id, path, text, and common filters."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
		),
		runtimedata.ViewOf[User](
			GitLabUserView,
			coredata.EntityType(UserEntity),
			runtimedata.WithViewDescription("GitLab users by id, username, name, and state."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
		),
		runtimedata.ViewOf[Group](
			GitLabGroupView,
			coredata.EntityType(GroupEntity),
			runtimedata.WithViewDescription("GitLab groups by id, path, full path, text, and hierarchy relations."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
		),
		runtimedata.ViewOf[Membership](
			MembershipDataView,
			coredata.EntityType(MembershipEntity),
			runtimedata.WithViewDescription("GitLab user membership edge records with source path and access level."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QueryRelation),
		),
		runtimedata.ViewOf[MergeRequestReviewContext](
			GitLabMRReviewContextView,
			coredata.EntityType(MergeRequestReviewContextEntity),
			runtimedata.WithViewDescription("GitLab merge request review context with metadata, changes, approvals, pipelines, jobs, and discussions."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch),
		),
		runtimedata.ViewOf[gitlabUserWithGroupsView](
			GitLabUserWithGroupsView,
			coredata.EntityType(UserEntity),
			runtimedata.WithViewDescription("GitLab users with minimal group summaries for membership questions."),
			runtimedata.WithViewIncludes(coredata.RelationIncludeSpec{
				Relation: "groups",
				Target:   coredata.EntityType(GroupEntity),
				Fields:   []string{"id", "path", "full_path", "name"},
			}),
			runtimedata.WithViewQueryHints(coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
		),
	}
}

type gitlabUserWithGroupsView struct {
	ID       int64                    `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab user id."`
	Username string                   `json:"username" datasource:"searchable,filterable" jsonschema:"description=GitLab username."`
	Name     string                   `json:"name,omitempty" datasource:"searchable" jsonschema:"description=Display name."`
	State    string                   `json:"state,omitempty" datasource:"filterable" jsonschema:"description=User state."`
	WebURL   string                   `json:"web_url,omitempty" datasource:"url" jsonschema:"description=User web URL."`
	Groups   []gitlabGroupViewSummary `json:"groups"`
}

type gitlabGroupViewSummary struct {
	ID       int64  `json:"id" datasource:"filterable" jsonschema:"description=GitLab group id."`
	Path     string `json:"path" datasource:"searchable,filterable" jsonschema:"description=Group path slug."`
	FullPath string `json:"full_path" datasource:"searchable,filterable" jsonschema:"description=Full group namespace path."`
	Name     string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Group name."`
}
