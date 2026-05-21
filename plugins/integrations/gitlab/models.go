package gitlab

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/engine/core/datasource"
	runtimedatasource "github.com/fluxplane/engine/runtime/datasource"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

const (
	ProjectEntity          coredatasource.EntityType = "gitlab.project"
	MergeRequestEntity     coredatasource.EntityType = "gitlab.merge_request"
	MergeRequestDiffEntity coredatasource.EntityType = "gitlab.merge_request_diff"
	MergeRequestNoteEntity coredatasource.EntityType = "gitlab.merge_request_note"
	PipelineEntity         coredatasource.EntityType = "gitlab.pipeline"
	BranchEntity           coredatasource.EntityType = "gitlab.branch"
	TagEntity              coredatasource.EntityType = "gitlab.tag"
	CommitEntity           coredatasource.EntityType = "gitlab.commit"
	RepositoryTreeEntity   coredatasource.EntityType = "gitlab.repository_tree"
	RepositoryFileEntity   coredatasource.EntityType = "gitlab.repository_file"
	JobEntity              coredatasource.EntityType = "gitlab.job"
	JobTraceEntity         coredatasource.EntityType = "gitlab.job_trace"
	UserEntity             coredatasource.EntityType = "gitlab.user"
	GroupEntity            coredatasource.EntityType = "gitlab.group"
	MembershipEntity       coredatasource.EntityType = "gitlab.user_membership"
)

type Project struct {
	ID                int64  `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab project id."`
	Name              string `json:"name" datasource:"searchable" jsonschema:"description=Project name."`
	PathWithNamespace string `json:"path_with_namespace" datasource:"searchable,filterable" jsonschema:"description=Full project path with namespace."`
	Description       string `json:"description,omitempty" datasource:"searchable" corpus:"body" jsonschema:"description=Project description."`
	WebURL            string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Project web URL."`
	DefaultBranch     string `json:"default_branch,omitempty" datasource:"filterable" jsonschema:"description=Default branch name."`
	Visibility        string `json:"visibility,omitempty" datasource:"filterable" jsonschema:"description=Project visibility."`
	Archived          bool   `json:"archived,omitempty" datasource:"filterable" jsonschema:"description=Whether the project is archived."`
}

type MergeRequest struct {
	ID                  int64    `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab merge request id."`
	IID                 int64    `json:"iid" datasource:"filterable" jsonschema:"description=Project-local merge request iid."`
	ProjectID           int64    `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id."`
	Title               string   `json:"title" datasource:"searchable" jsonschema:"description=Merge request title."`
	Description         string   `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Merge request description."`
	State               string   `json:"state,omitempty" datasource:"filterable" jsonschema:"description=Merge request state."`
	DetailedMergeStatus string   `json:"detailed_merge_status,omitempty" datasource:"filterable" jsonschema:"description=Detailed merge status."`
	SourceBranch        string   `json:"source_branch,omitempty" datasource:"filterable" jsonschema:"description=Source branch."`
	TargetBranch        string   `json:"target_branch,omitempty" datasource:"filterable" jsonschema:"description=Target branch."`
	SHA                 string   `json:"sha,omitempty" datasource:"filterable" jsonschema:"description=Current head sha."`
	AuthorUsername      string   `json:"author_username,omitempty" datasource:"filterable" jsonschema:"description=Author username."`
	AssigneeUsernames   []string `json:"assignee_usernames,omitempty" jsonschema:"description=Assignee usernames."`
	ReviewerUsernames   []string `json:"reviewer_usernames,omitempty" jsonschema:"description=Reviewer usernames."`
	Labels              []string `json:"labels,omitempty" datasource:"filterable" jsonschema:"description=Labels."`
	Draft               bool     `json:"draft,omitempty" datasource:"filterable" jsonschema:"description=Whether the MR is draft."`
	WebURL              string   `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Merge request web URL."`
	CreatedAt           string   `json:"created_at,omitempty" datasource:"filterable" jsonschema:"description=Creation timestamp."`
	UpdatedAt           string   `json:"updated_at,omitempty" datasource:"filterable" jsonschema:"description=Update timestamp."`
}

type MergeRequestDiff struct {
	ID            string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id for this diff file."`
	ProjectID     int64  `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id."`
	MergeRequest  int64  `json:"merge_request_iid" datasource:"filterable" jsonschema:"description=Project-local merge request iid."`
	OldPath       string `json:"old_path,omitempty" datasource:"filterable" jsonschema:"description=Old file path."`
	NewPath       string `json:"new_path" datasource:"searchable,filterable" jsonschema:"description=New file path."`
	Summary       string `json:"summary,omitempty" datasource:"searchable" jsonschema:"description=Bounded compact diff summary."`
	NewFile       bool   `json:"new_file,omitempty" datasource:"filterable" jsonschema:"description=Whether the file is new."`
	RenamedFile   bool   `json:"renamed_file,omitempty" datasource:"filterable" jsonschema:"description=Whether the file is renamed."`
	DeletedFile   bool   `json:"deleted_file,omitempty" datasource:"filterable" jsonschema:"description=Whether the file is deleted."`
	GeneratedFile bool   `json:"generated_file,omitempty" datasource:"filterable" jsonschema:"description=Whether GitLab marks it generated."`
	Collapsed     bool   `json:"collapsed,omitempty" datasource:"filterable" jsonschema:"description=Whether GitLab collapsed the diff."`
	TooLarge      bool   `json:"too_large,omitempty" datasource:"filterable" jsonschema:"description=Whether GitLab omitted an oversized diff."`
}

type MergeRequestNote struct {
	ID              int64  `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab note id."`
	ProjectID       int64  `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id."`
	MergeRequestIID int64  `json:"merge_request_iid" datasource:"filterable" jsonschema:"description=Project-local merge request iid."`
	Body            string `json:"body" datasource:"searchable" jsonschema:"description=Note body."`
	AuthorUsername  string `json:"author_username,omitempty" datasource:"filterable" jsonschema:"description=Author username."`
	System          bool   `json:"system,omitempty" datasource:"filterable" jsonschema:"description=Whether this is a system note."`
	Internal        bool   `json:"internal,omitempty" datasource:"filterable" jsonschema:"description=Whether this note is internal."`
	Resolvable      bool   `json:"resolvable,omitempty" datasource:"filterable" jsonschema:"description=Whether this note is resolvable."`
	Resolved        bool   `json:"resolved,omitempty" datasource:"filterable" jsonschema:"description=Whether this note is resolved."`
	CreatedAt       string `json:"created_at,omitempty" datasource:"filterable" jsonschema:"description=Creation timestamp."`
	UpdatedAt       string `json:"updated_at,omitempty" datasource:"filterable" jsonschema:"description=Update timestamp."`
}

type Pipeline struct {
	ID        int64  `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab pipeline id."`
	IID       int64  `json:"iid,omitempty" datasource:"filterable" jsonschema:"description=Project-local pipeline iid."`
	ProjectID int64  `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id."`
	Status    string `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Pipeline status."`
	Source    string `json:"source,omitempty" datasource:"filterable" jsonschema:"description=Pipeline source."`
	Ref       string `json:"ref,omitempty" datasource:"filterable" jsonschema:"description=Git ref."`
	SHA       string `json:"sha,omitempty" datasource:"filterable" jsonschema:"description=Pipeline sha."`
	Name      string `json:"name,omitempty" datasource:"searchable" jsonschema:"description=Pipeline name."`
	WebURL    string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Pipeline web URL."`
	CreatedAt string `json:"created_at,omitempty" datasource:"filterable" jsonschema:"description=Creation timestamp."`
	UpdatedAt string `json:"updated_at,omitempty" datasource:"filterable" jsonschema:"description=Update timestamp."`
}

type Branch struct {
	ID        string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id project!branch."`
	ProjectID string `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	Name      string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Branch name."`
	Default   bool   `json:"default,omitempty" datasource:"filterable" jsonschema:"description=Whether this is the default branch."`
	Protected bool   `json:"protected,omitempty" datasource:"filterable" jsonschema:"description=Whether this branch is protected."`
	Merged    bool   `json:"merged,omitempty" datasource:"filterable" jsonschema:"description=Whether this branch is merged."`
	CanPush   bool   `json:"can_push,omitempty" datasource:"filterable" jsonschema:"description=Whether the authenticated token can push."`
	CommitID  string `json:"commit_id,omitempty" datasource:"filterable" jsonschema:"description=Latest commit SHA."`
	WebURL    string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Branch web URL."`
}

type Tag struct {
	ID        string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id project!tag."`
	ProjectID string `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	Name      string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Tag name."`
	Target    string `json:"target,omitempty" datasource:"filterable" jsonschema:"description=Target object SHA."`
	Message   string `json:"message,omitempty" datasource:"searchable" jsonschema:"description=Tag message."`
	Protected bool   `json:"protected,omitempty" datasource:"filterable" jsonschema:"description=Whether this tag is protected."`
	CommitID  string `json:"commit_id,omitempty" datasource:"filterable" jsonschema:"description=Commit SHA for this tag."`
	CreatedAt string `json:"created_at,omitempty" datasource:"filterable" jsonschema:"description=Creation timestamp."`
}

type Commit struct {
	ID             string   `json:"id" datasource:"id,filterable" jsonschema:"description=Stable datasource id project!sha."`
	ProjectID      string   `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	SHA            string   `json:"sha" datasource:"searchable,filterable" jsonschema:"description=Commit SHA."`
	ShortID        string   `json:"short_id,omitempty" datasource:"searchable" jsonschema:"description=Short commit SHA."`
	Title          string   `json:"title,omitempty" datasource:"searchable" jsonschema:"description=Commit title."`
	Message        string   `json:"message,omitempty" datasource:"searchable" jsonschema:"description=Commit message."`
	AuthorName     string   `json:"author_name,omitempty" datasource:"filterable" jsonschema:"description=Author name."`
	AuthorEmail    string   `json:"author_email,omitempty" datasource:"filterable" jsonschema:"description=Author email."`
	CommittedDate  string   `json:"committed_date,omitempty" datasource:"filterable" jsonschema:"description=Commit timestamp."`
	ParentIDs      []string `json:"parent_ids,omitempty" jsonschema:"description=Parent commit SHAs."`
	WebURL         string   `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Commit web URL."`
	LastPipelineID int64    `json:"last_pipeline_id,omitempty" datasource:"filterable" jsonschema:"description=Last pipeline id for this commit when returned by GitLab."`
}

type RepositoryTreeEntry struct {
	ID        string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id project!ref!path."`
	ProjectID string `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	Ref       string `json:"ref,omitempty" datasource:"filterable" jsonschema:"description=Git ref used to list this tree entry."`
	Name      string `json:"name" datasource:"searchable" jsonschema:"description=Entry name."`
	Path      string `json:"path" datasource:"searchable,filterable" jsonschema:"description=Entry path."`
	Type      string `json:"type" datasource:"filterable" jsonschema:"description=Entry type, usually tree or blob."`
	Mode      string `json:"mode,omitempty" datasource:"filterable" jsonschema:"description=Git file mode."`
	SHA       string `json:"sha,omitempty" datasource:"filterable" jsonschema:"description=Tree or blob SHA."`
}

type RepositoryFile struct {
	ID             string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id project!ref!path."`
	ProjectID      string `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	Ref            string `json:"ref" datasource:"filterable" jsonschema:"description=Git ref."`
	FileName       string `json:"file_name" datasource:"searchable" jsonschema:"description=File name."`
	FilePath       string `json:"file_path" datasource:"searchable,filterable" jsonschema:"description=Repository file path."`
	Size           int64  `json:"size" datasource:"filterable" jsonschema:"description=File size in bytes."`
	Encoding       string `json:"encoding,omitempty" datasource:"filterable" jsonschema:"description=GitLab content encoding."`
	ContentPreview string `json:"content_preview,omitempty" jsonschema:"description=Bounded decoded content preview."`
	BlobID         string `json:"blob_id,omitempty" datasource:"filterable" jsonschema:"description=Blob SHA."`
	CommitID       string `json:"commit_id,omitempty" datasource:"filterable" jsonschema:"description=Commit SHA."`
	LastCommitID   string `json:"last_commit_id,omitempty" datasource:"filterable" jsonschema:"description=Last commit SHA for this file."`
	SHA256         string `json:"sha256,omitempty" datasource:"filterable" jsonschema:"description=Content SHA256."`
}

type Job struct {
	ID             string  `json:"id" datasource:"id" jsonschema:"description=Stable datasource id project!job_id."`
	ProjectID      string  `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	JobID          int64   `json:"job_id" datasource:"filterable" jsonschema:"description=GitLab job id."`
	PipelineID     int64   `json:"pipeline_id,omitempty" datasource:"filterable" jsonschema:"description=GitLab pipeline id."`
	Name           string  `json:"name" datasource:"searchable" jsonschema:"description=Job name."`
	Stage          string  `json:"stage,omitempty" datasource:"filterable" jsonschema:"description=CI stage."`
	Status         string  `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Job status."`
	Ref            string  `json:"ref,omitempty" datasource:"filterable" jsonschema:"description=Git ref."`
	CommitID       string  `json:"commit_id,omitempty" datasource:"filterable" jsonschema:"description=Commit SHA."`
	FailureReason  string  `json:"failure_reason,omitempty" datasource:"filterable" jsonschema:"description=Failure reason."`
	AllowFailure   bool    `json:"allow_failure,omitempty" datasource:"filterable" jsonschema:"description=Whether this job may fail."`
	Duration       float64 `json:"duration,omitempty" datasource:"filterable" jsonschema:"description=Duration in seconds."`
	QueuedDuration float64 `json:"queued_duration,omitempty" datasource:"filterable" jsonschema:"description=Queued duration in seconds."`
	CreatedAt      string  `json:"created_at,omitempty" datasource:"filterable" jsonschema:"description=Creation timestamp."`
	StartedAt      string  `json:"started_at,omitempty" datasource:"filterable" jsonschema:"description=Start timestamp."`
	FinishedAt     string  `json:"finished_at,omitempty" datasource:"filterable" jsonschema:"description=Finish timestamp."`
	WebURL         string  `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Job web URL."`
	Runner         string  `json:"runner,omitempty" datasource:"filterable" jsonschema:"description=Runner description."`
	User           string  `json:"user,omitempty" datasource:"filterable" jsonschema:"description=User username that created or ran the job."`
}

type JobTrace struct {
	ID        string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id project!job_id!trace."`
	ProjectID string `json:"project_id" datasource:"filterable" jsonschema:"description=GitLab project id or path."`
	JobID     int64  `json:"job_id" datasource:"filterable" jsonschema:"description=GitLab job id."`
	Trace     string `json:"trace" jsonschema:"description=Bounded job trace content."`
	Truncated bool   `json:"truncated,omitempty" jsonschema:"description=Whether the trace was truncated."`
}

type User struct {
	ID       int64  `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab user id."`
	Username string `json:"username" datasource:"searchable,filterable" jsonschema:"description=GitLab username."`
	Name     string `json:"name,omitempty" datasource:"searchable" jsonschema:"description=Display name."`
	State    string `json:"state,omitempty" datasource:"filterable" jsonschema:"description=User state."`
	WebURL   string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=User web URL."`
	Role     string `json:"role,omitempty" datasource:"filterable" jsonschema:"description=Role in the relationship result."`
}

type Group struct {
	ID          int64  `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab group id."`
	Name        string `json:"name" datasource:"searchable" jsonschema:"description=Group name."`
	Path        string `json:"path" datasource:"searchable,filterable" jsonschema:"description=Group path slug."`
	FullPath    string `json:"full_path" datasource:"searchable,filterable" jsonschema:"description=Full group namespace path."`
	FullName    string `json:"full_name,omitempty" datasource:"searchable" jsonschema:"description=Full group display name."`
	Description string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Group description."`
	Visibility  string `json:"visibility,omitempty" datasource:"filterable" jsonschema:"description=Group visibility."`
	ParentID    int64  `json:"parent_id,omitempty" datasource:"filterable" jsonschema:"description=Parent group id."`
	WebURL      string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Group web URL."`
	Role        string `json:"role,omitempty" datasource:"filterable" jsonschema:"description=Role in the relationship result."`
}

type Membership struct {
	ID             string `json:"id" datasource:"id" jsonschema:"description=Stable datasource id for this direct membership."`
	UserID         int64  `json:"user_id" datasource:"filterable" jsonschema:"description=GitLab user id."`
	SourceID       int64  `json:"source_id" datasource:"filterable" jsonschema:"description=GitLab group or project id."`
	SourceName     string `json:"source_name" datasource:"searchable" jsonschema:"description=Membership source display name."`
	SourceType     string `json:"source_type" datasource:"filterable" jsonschema:"description=Membership source type, Namespace or Project."`
	SourcePath     string `json:"source_path,omitempty" datasource:"searchable,filterable" jsonschema:"description=Full group or project path when available."`
	SourceURL      string `json:"source_url,omitempty" datasource:"url" jsonschema:"description=Membership source web URL when available."`
	AccessLevel    string `json:"access_level,omitempty" datasource:"filterable" jsonschema:"description=GitLab access level name."`
	Role           string `json:"role,omitempty" datasource:"filterable" jsonschema:"description=Role name for this membership."`
	Direct         bool   `json:"direct" datasource:"filterable" jsonschema:"description=Whether this edge is a direct GitLab membership."`
	SourceArchived bool   `json:"source_archived,omitempty" datasource:"filterable" jsonschema:"description=Whether the source project is archived."`
}

func entitySpecs() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{
		projectEntitySpec(),
		mergeRequestEntitySpec(),
		mergeRequestDiffEntitySpec(),
		mergeRequestNoteEntitySpec(),
		pipelineEntitySpec(),
		branchEntitySpec(),
		tagEntitySpec(),
		commitEntitySpec(),
		repositoryTreeEntitySpec(),
		repositoryFileEntitySpec(),
		jobEntitySpec(),
		jobTraceEntitySpec(),
		userEntitySpec(),
		groupEntitySpec(),
		membershipEntitySpec(),
	}
}

func projectEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Project](ProjectEntity, "GitLab project.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
		coredatasource.EntityCapabilityIndex,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "merge_requests", Description: "Merge requests in this project.", TargetEntity: MergeRequestEntity},
		{Name: "pipelines", Description: "Pipelines in this project.", TargetEntity: PipelineEntity},
		{Name: "branches", Description: "Repository branches in this project.", TargetEntity: BranchEntity},
		{Name: "tags", Description: "Repository tags in this project.", TargetEntity: TagEntity},
		{Name: "commits", Description: "Recent or filtered repository commits in this project.", TargetEntity: CommitEntity},
		{Name: "repository_tree", Description: "Repository tree entries for this project.", TargetEntity: RepositoryTreeEntity},
		{Name: "jobs", Description: "Recent CI jobs in this project.", TargetEntity: JobEntity},
		{Name: "users", Description: "Users with access to this project.", TargetEntity: UserEntity},
		{Name: "groups", Description: "Groups and namespaces with access to this project.", TargetEntity: GroupEntity},
	}
	entity.Detectors = []coredatasource.DetectorSpec{{
		Name:          "gitlab_project_url",
		Kind:          coredatasource.DetectorURL,
		Pattern:       `https?://[^\s<>"']+/([^/\s<>"']+/[^/\s<>"'#?]+)(?:[/?#][^\s<>"']*)?`,
		QueryTemplate: "$1",
		URLTemplate:   "$0",
		Confidence:    0.8,
	}}
	return entity
}

func mergeRequestEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[MergeRequest](MergeRequestEntity, "GitLab merge request.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "diffs", Description: "Files changed by this merge request.", TargetEntity: MergeRequestDiffEntity},
		{Name: "notes", Description: "Notes on this merge request.", TargetEntity: MergeRequestNoteEntity},
		{Name: "pipelines", Description: "Pipelines for this merge request.", TargetEntity: PipelineEntity},
		{Name: "participants", Description: "Users participating in this merge request.", TargetEntity: UserEntity},
		{Name: "reviewers", Description: "Reviewers assigned to this merge request.", TargetEntity: UserEntity},
	}
	entity.Detectors = []coredatasource.DetectorSpec{{
		Name:          "gitlab_merge_request_url",
		Kind:          coredatasource.DetectorURL,
		Pattern:       `https?://[^/\s<>"']+/([^\s<>"']+)/-/merge_requests/([0-9]+)(?:[/?#][^\s<>"']*)?`,
		QueryTemplate: "$1!$2",
		URLTemplate:   "$0",
		Confidence:    0.9,
	}}
	return entity
}

func mergeRequestDiffEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[MergeRequestDiff](MergeRequestDiffEntity, "GitLab merge request diff file.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityGet,
	}
	return entity
}

func mergeRequestNoteEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[MergeRequestNote](MergeRequestNoteEntity, "GitLab merge request note.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityGet,
	}
	return entity
}

func pipelineEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Pipeline](PipelineEntity, "GitLab pipeline.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "jobs", Description: "CI jobs in this pipeline.", TargetEntity: JobEntity},
		{Name: "commit", Description: "Commit associated with this pipeline.", TargetEntity: CommitEntity},
	}
	return entity
}

func branchEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Branch](BranchEntity, "GitLab repository branch.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "commit", Description: "Latest commit on this branch.", TargetEntity: CommitEntity},
	}
	return entity
}

func tagEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Tag](TagEntity, "GitLab repository tag.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "commit", Description: "Commit targeted by this tag.", TargetEntity: CommitEntity},
	}
	return entity
}

func commitEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Commit](CommitEntity, "GitLab repository commit.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "merge_requests", Description: "Merge requests associated with this commit.", TargetEntity: MergeRequestEntity},
		{Name: "pipelines", Description: "Pipelines for this commit SHA.", TargetEntity: PipelineEntity},
	}
	return entity
}

func repositoryTreeEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[RepositoryTreeEntry](RepositoryTreeEntity, "GitLab repository tree entry.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "file", Description: "Repository file for this blob entry.", TargetEntity: RepositoryFileEntity},
	}
	return entity
}

func repositoryFileEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[RepositoryFile](RepositoryFileEntity, "GitLab repository file.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilityGet,
	}
	return entity
}

func jobEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Job](JobEntity, "GitLab CI job.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "trace", Description: "Bounded job trace log for this job.", TargetEntity: JobTraceEntity},
		{Name: "pipeline", Description: "Pipeline this job belongs to.", TargetEntity: PipelineEntity},
		{Name: "commit", Description: "Commit this job ran for.", TargetEntity: CommitEntity},
	}
	return entity
}

func jobTraceEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[JobTrace](JobTraceEntity, "GitLab CI job trace.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilityGet,
	}
	return entity
}

func userEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[User](UserEntity, "GitLab user.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
		coredatasource.EntityCapabilityIndex,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "memberships", Description: "Namespace and project memberships for this user.", TargetEntity: MembershipEntity},
		{Name: "groups", Description: "Groups and namespaces this user is a member of.", TargetEntity: GroupEntity},
		{Name: "projects", Description: "Projects this user is a member of.", TargetEntity: ProjectEntity},
	}
	return entity
}

func groupEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Group](GroupEntity, "GitLab group namespace.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
		coredatasource.EntityCapabilityIndex,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "parent", Description: "Parent group namespace for this subgroup.", TargetEntity: GroupEntity},
		{Name: "subgroups", Description: "Direct child group namespaces in this group namespace.", TargetEntity: GroupEntity},
		{Name: "descendant_groups", Description: "Recursive child group namespaces in this group namespace.", TargetEntity: GroupEntity},
		{Name: "projects", Description: "Projects in this group namespace.", TargetEntity: ProjectEntity},
		{Name: "users", Description: "Users who are members of this group namespace.", TargetEntity: UserEntity},
	}
	return entity
}

func membershipEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Membership](MembershipEntity, "Direct GitLab user membership.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
		coredatasource.EntityCapabilityIndex,
	}
	entity.Relations = []coredatasource.RelationSpec{
		{Name: "group", Description: "Group namespace target for this membership.", TargetEntity: GroupEntity},
		{Name: "project", Description: "Project target for this membership.", TargetEntity: ProjectEntity},
	}
	return entity
}

func projectFromGitLab(project *gitlab.Project) Project {
	if project == nil {
		return Project{}
	}
	return Project{
		ID:                project.ID,
		Name:              project.Name,
		PathWithNamespace: project.PathWithNamespace,
		Description:       project.Description,
		WebURL:            project.WebURL,
		DefaultBranch:     project.DefaultBranch,
		Visibility:        string(project.Visibility),
		Archived:          project.Archived,
	}
}

func mergeRequestFromBasic(mr *gitlab.BasicMergeRequest) MergeRequest {
	if mr == nil {
		return MergeRequest{}
	}
	return MergeRequest{
		ID:                  mr.ID,
		IID:                 mr.IID,
		ProjectID:           mr.ProjectID,
		Title:               mr.Title,
		Description:         mr.Description,
		State:               mr.State,
		DetailedMergeStatus: mr.DetailedMergeStatus,
		SourceBranch:        mr.SourceBranch,
		TargetBranch:        mr.TargetBranch,
		SHA:                 mr.SHA,
		AuthorUsername:      basicUsername(mr.Author),
		AssigneeUsernames:   basicUsernames(mr.Assignees),
		ReviewerUsernames:   basicUsernames(mr.Reviewers),
		Labels:              labels(mr.Labels),
		Draft:               mr.Draft,
		WebURL:              mr.WebURL,
		CreatedAt:           formatTime(mr.CreatedAt),
		UpdatedAt:           formatTime(mr.UpdatedAt),
	}
}

func mergeRequestFromFull(mr *gitlab.MergeRequest) MergeRequest {
	if mr == nil {
		return MergeRequest{}
	}
	out := mergeRequestFromBasic(&mr.BasicMergeRequest)
	if out.DetailedMergeStatus == "" {
		out.DetailedMergeStatus = mr.DetailedMergeStatus
	}
	return out
}

func diffFromGitLab(projectID, mrIID int64, diff *gitlab.MergeRequestDiff) MergeRequestDiff {
	if diff == nil {
		return MergeRequestDiff{}
	}
	path := firstNonEmpty(diff.NewPath, diff.OldPath)
	id := fmt.Sprintf("%d!%d!%s", projectID, mrIID, path)
	return MergeRequestDiff{
		ID:            id,
		ProjectID:     projectID,
		MergeRequest:  mrIID,
		OldPath:       diff.OldPath,
		NewPath:       diff.NewPath,
		Summary:       compactDiff(diff.Diff),
		NewFile:       diff.NewFile,
		RenamedFile:   diff.RenamedFile,
		DeletedFile:   diff.DeletedFile,
		GeneratedFile: diff.GeneratedFile,
		Collapsed:     diff.Collapsed,
		TooLarge:      diff.TooLarge,
	}
}

func noteFromGitLab(note *gitlab.Note) MergeRequestNote {
	if note == nil {
		return MergeRequestNote{}
	}
	return MergeRequestNote{
		ID:              note.ID,
		ProjectID:       note.ProjectID,
		MergeRequestIID: note.NoteableIID,
		Body:            note.Body,
		AuthorUsername:  note.Author.Username,
		System:          note.System,
		Internal:        note.Internal,
		Resolvable:      note.Resolvable,
		Resolved:        note.Resolved,
		CreatedAt:       formatTime(note.CreatedAt),
		UpdatedAt:       formatTime(note.UpdatedAt),
	}
}

func pipelineFromInfo(pipeline *gitlab.PipelineInfo) Pipeline {
	if pipeline == nil {
		return Pipeline{}
	}
	return Pipeline{
		ID:        pipeline.ID,
		IID:       pipeline.IID,
		ProjectID: pipeline.ProjectID,
		Status:    pipeline.Status,
		Source:    pipeline.Source,
		Ref:       pipeline.Ref,
		SHA:       pipeline.SHA,
		Name:      pipeline.Name,
		WebURL:    pipeline.WebURL,
		CreatedAt: formatTime(pipeline.CreatedAt),
		UpdatedAt: formatTime(pipeline.UpdatedAt),
	}
}

func pipelineFromFull(pipeline *gitlab.Pipeline) Pipeline {
	if pipeline == nil {
		return Pipeline{}
	}
	return Pipeline{
		ID:        pipeline.ID,
		IID:       pipeline.IID,
		ProjectID: pipeline.ProjectID,
		Status:    pipeline.Status,
		Source:    string(pipeline.Source),
		Ref:       pipeline.Ref,
		SHA:       pipeline.SHA,
		Name:      pipeline.Name,
		WebURL:    pipeline.WebURL,
		CreatedAt: formatTime(pipeline.CreatedAt),
		UpdatedAt: formatTime(pipeline.UpdatedAt),
	}
}

func branchFromGitLab(project string, branch *gitlab.Branch) Branch {
	if branch == nil {
		return Branch{ProjectID: project}
	}
	commitID := ""
	if branch.Commit != nil {
		commitID = branch.Commit.ID
	}
	return Branch{
		ID:        projectTextChildID(project, branch.Name),
		ProjectID: project,
		Name:      branch.Name,
		Default:   branch.Default,
		Protected: branch.Protected,
		Merged:    branch.Merged,
		CanPush:   branch.CanPush,
		CommitID:  commitID,
		WebURL:    branch.WebURL,
	}
}

func tagFromGitLab(project string, tag *gitlab.Tag) Tag {
	if tag == nil {
		return Tag{ProjectID: project}
	}
	commitID := ""
	if tag.Commit != nil {
		commitID = tag.Commit.ID
	}
	return Tag{
		ID:        projectTextChildID(project, tag.Name),
		ProjectID: project,
		Name:      tag.Name,
		Target:    tag.Target,
		Message:   tag.Message,
		Protected: tag.Protected,
		CommitID:  commitID,
		CreatedAt: formatTime(tag.CreatedAt),
	}
}

func commitFromGitLab(project string, commit *gitlab.Commit) Commit {
	if commit == nil {
		return Commit{ProjectID: project}
	}
	lastPipelineID := int64(0)
	if commit.LastPipeline != nil {
		lastPipelineID = commit.LastPipeline.ID
	}
	return Commit{
		ID:             projectTextChildID(project, commit.ID),
		ProjectID:      project,
		SHA:            commit.ID,
		ShortID:        commit.ShortID,
		Title:          commit.Title,
		Message:        commit.Message,
		AuthorName:     commit.AuthorName,
		AuthorEmail:    commit.AuthorEmail,
		CommittedDate:  formatTime(commit.CommittedDate),
		ParentIDs:      append([]string(nil), commit.ParentIDs...),
		WebURL:         commit.WebURL,
		LastPipelineID: lastPipelineID,
	}
}

func repositoryTreeEntryFromGitLab(project, ref string, node *gitlab.TreeNode) RepositoryTreeEntry {
	if node == nil {
		return RepositoryTreeEntry{ProjectID: project, Ref: ref}
	}
	return RepositoryTreeEntry{
		ID:        projectRefPathID(project, ref, node.Path),
		ProjectID: project,
		Ref:       ref,
		Name:      node.Name,
		Path:      node.Path,
		Type:      node.Type,
		Mode:      node.Mode,
		SHA:       node.ID,
	}
}

func repositoryFileFromGitLab(project, ref string, file *gitlab.File) RepositoryFile {
	if file == nil {
		return RepositoryFile{ProjectID: project, Ref: ref}
	}
	preview := decodedFilePreview(file.Content, file.Encoding)
	return RepositoryFile{
		ID:             projectRefPathID(project, firstNonEmpty(ref, file.Ref), file.FilePath),
		ProjectID:      project,
		Ref:            firstNonEmpty(ref, file.Ref),
		FileName:       file.FileName,
		FilePath:       file.FilePath,
		Size:           file.Size,
		Encoding:       file.Encoding,
		ContentPreview: preview,
		BlobID:         file.BlobID,
		CommitID:       file.CommitID,
		LastCommitID:   file.LastCommitID,
		SHA256:         file.SHA256,
	}
}

func jobFromGitLab(project string, job *gitlab.Job) Job {
	if job == nil {
		return Job{ProjectID: project}
	}
	commitID := ""
	if job.Commit != nil {
		commitID = job.Commit.ID
	}
	user := ""
	if job.User != nil {
		user = firstNonEmpty(job.User.Username, job.User.Name)
	}
	return Job{
		ID:             projectJobID(project, job.ID),
		ProjectID:      project,
		JobID:          job.ID,
		PipelineID:     job.Pipeline.ID,
		Name:           job.Name,
		Stage:          job.Stage,
		Status:         job.Status,
		Ref:            job.Ref,
		CommitID:       commitID,
		FailureReason:  job.FailureReason,
		AllowFailure:   job.AllowFailure,
		Duration:       job.Duration,
		QueuedDuration: job.QueuedDuration,
		CreatedAt:      formatTime(job.CreatedAt),
		StartedAt:      formatTime(job.StartedAt),
		FinishedAt:     formatTime(job.FinishedAt),
		WebURL:         job.WebURL,
		Runner:         firstNonEmpty(job.Runner.Description, job.Runner.Name),
		User:           user,
	}
}

func userFromBasic(user *gitlab.BasicUser, role string) User {
	if user == nil {
		return User{Role: role}
	}
	return User{ID: user.ID, Username: user.Username, Name: user.Name, State: user.State, WebURL: user.WebURL, Role: role}
}

func userFromGitLab(user *gitlab.User) User {
	if user == nil {
		return User{}
	}
	return User{ID: user.ID, Username: user.Username, Name: user.Name, State: user.State, WebURL: user.WebURL}
}

func userFromGroupMember(user *gitlab.GroupMember) User {
	if user == nil {
		return User{}
	}
	return User{ID: user.ID, Username: user.Username, Name: user.Name, State: user.State, WebURL: user.WebURL, Role: accessLevelName(user.AccessLevel)}
}

func userFromProject(user *gitlab.ProjectUser) User {
	if user == nil {
		return User{}
	}
	return User{ID: user.ID, Username: user.Username, Name: user.Name, State: user.State, WebURL: user.WebURL}
}

func membershipFromGroupMember(userID int64, group Group, member *gitlab.GroupMember) Membership {
	access := gitlab.NoPermissions
	if member != nil {
		access = member.AccessLevel
	}
	return Membership{
		ID:          membershipID(userID, "Namespace", group.ID),
		UserID:      userID,
		SourceID:    group.ID,
		SourceName:  firstNonEmpty(group.FullName, group.Name, group.FullPath),
		SourceType:  "Namespace",
		SourcePath:  group.FullPath,
		SourceURL:   group.WebURL,
		AccessLevel: accessLevelName(access),
		Role:        accessLevelName(access),
		Direct:      true,
	}
}

func membershipFromProjectMember(userID int64, project Project, member *gitlab.ProjectMember) Membership {
	access := gitlab.NoPermissions
	if member != nil {
		access = member.AccessLevel
	}
	return Membership{
		ID:             membershipID(userID, "Project", project.ID),
		UserID:         userID,
		SourceID:       project.ID,
		SourceName:     project.Name,
		SourceType:     "Project",
		SourcePath:     project.PathWithNamespace,
		SourceURL:      project.WebURL,
		AccessLevel:    accessLevelName(access),
		Role:           accessLevelName(access),
		Direct:         true,
		SourceArchived: project.Archived,
	}
}

func groupFromGitLab(group *gitlab.Group) Group {
	if group == nil {
		return Group{}
	}
	return Group{
		ID:          group.ID,
		Name:        group.Name,
		Path:        group.Path,
		FullPath:    group.FullPath,
		FullName:    group.FullName,
		Description: group.Description,
		Visibility:  string(group.Visibility),
		ParentID:    group.ParentID,
		WebURL:      group.WebURL,
	}
}

func groupFromProject(group *gitlab.ProjectGroup) Group {
	if group == nil {
		return Group{}
	}
	return Group{
		ID:       group.ID,
		Name:     group.Name,
		FullPath: group.FullPath,
		FullName: group.FullName,
		WebURL:   group.WebURL,
	}
}

func userFromReviewer(reviewer *gitlab.MergeRequestReviewer) User {
	if reviewer == nil {
		return User{}
	}
	user := userFromBasic(reviewer.User, "reviewer")
	if reviewer.State != "" {
		user.Role = "reviewer:" + reviewer.State
	}
	return user
}

func projectID(id string) any {
	id = strings.TrimSpace(id)
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		return n
	}
	return id
}

func mergeRequestID(project any, iid int64) string {
	return fmt.Sprintf("%v!%d", project, iid)
}

func parseMergeRequestID(id string) (any, int64, error) {
	left, right, ok := strings.Cut(strings.TrimSpace(id), "!")
	if !ok || strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return nil, 0, fmt.Errorf("merge request id must be project!iid")
	}
	iid, err := strconv.ParseInt(strings.TrimSpace(right), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid merge request iid %q", right)
	}
	return projectID(left), iid, nil
}

func parseProjectChildID(id string) (any, int64, error) {
	left, right, ok := strings.Cut(strings.TrimSpace(id), "!")
	if !ok || strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return nil, 0, fmt.Errorf("id must be project!id")
	}
	child, err := strconv.ParseInt(strings.TrimSpace(right), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid child id %q", right)
	}
	return projectID(left), child, nil
}

func parseProjectTextChildID(id, label string) (any, string, error) {
	left, right, ok := strings.Cut(strings.TrimSpace(id), "!")
	if !ok || strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		if strings.TrimSpace(label) == "" {
			label = "child"
		}
		return nil, "", fmt.Errorf("id must be project!%s", label)
	}
	return projectID(left), strings.TrimSpace(right), nil
}

func parseProjectRefPathID(id string) (any, string, string, error) {
	project, rest, ok := strings.Cut(strings.TrimSpace(id), "!")
	if !ok {
		return nil, "", "", fmt.Errorf("id must be project!ref!path")
	}
	ref, path, ok := strings.Cut(rest, "!")
	if !ok || strings.TrimSpace(project) == "" || strings.TrimSpace(ref) == "" || strings.TrimSpace(path) == "" {
		return nil, "", "", fmt.Errorf("id must be project!ref!path")
	}
	return projectID(project), strings.TrimSpace(ref), strings.TrimSpace(path), nil
}

func parseTraceID(id string) (any, int64, error) {
	project, rest, ok := strings.Cut(strings.TrimSpace(id), "!")
	if !ok {
		return nil, 0, fmt.Errorf("id must be project!job_id!trace")
	}
	jobIDText, suffix, ok := strings.Cut(rest, "!")
	if !ok || strings.TrimSpace(suffix) != "trace" {
		return nil, 0, fmt.Errorf("id must be project!job_id!trace")
	}
	jobID, err := strconv.ParseInt(strings.TrimSpace(jobIDText), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid job id %q", jobIDText)
	}
	return projectID(project), jobID, nil
}

func parseMergeRequestChildID(id string) (any, int64, string, error) {
	project, rest, ok := strings.Cut(strings.TrimSpace(id), "!")
	if !ok {
		return nil, 0, "", fmt.Errorf("id must be project!merge_request_iid!child")
	}
	mr, child, ok := strings.Cut(rest, "!")
	if !ok || strings.TrimSpace(child) == "" {
		return nil, 0, "", fmt.Errorf("id must be project!merge_request_iid!child")
	}
	iid, err := strconv.ParseInt(strings.TrimSpace(mr), 10, 64)
	if err != nil {
		return nil, 0, "", fmt.Errorf("invalid merge request iid %q", mr)
	}
	return projectID(project), iid, child, nil
}

func projectIDString(project Project) string {
	if project.ID != 0 {
		return strconv.FormatInt(project.ID, 10)
	}
	if project.PathWithNamespace != "" {
		return project.PathWithNamespace
	}
	return ""
}

func projectIDLabel(project any) string {
	return strings.TrimSpace(fmt.Sprint(project))
}

func projectTextChildID(project, child string) string {
	return strings.TrimSpace(project) + "!" + strings.TrimSpace(child)
}

func projectRefPathID(project, ref, path string) string {
	return strings.TrimSpace(project) + "!" + strings.TrimSpace(ref) + "!" + strings.TrimSpace(path)
}

func projectJobID(project string, jobID int64) string {
	return fmt.Sprintf("%s!%d", strings.TrimSpace(project), jobID)
}

func jobTraceID(project string, jobID int64) string {
	return fmt.Sprintf("%s!%d!trace", strings.TrimSpace(project), jobID)
}

func projectTitle(project Project) string {
	if project.PathWithNamespace != "" {
		return project.PathWithNamespace
	}
	return project.Name
}

func groupIDString(group Group) string {
	if group.FullPath != "" {
		return group.FullPath
	}
	if group.ID != 0 {
		return strconv.FormatInt(group.ID, 10)
	}
	return ""
}

func groupTitle(group Group) string {
	if group.FullPath != "" {
		return group.FullPath
	}
	if group.FullName != "" {
		return group.FullName
	}
	if group.Path != "" {
		return group.Path
	}
	return group.Name
}

func membershipID(userID int64, sourceType string, sourceID int64) string {
	sourceType = strings.ToLower(normalizedMembershipSourceType(sourceType))
	return fmt.Sprintf("%d:%s:%d", userID, sourceType, sourceID)
}

func parseMembershipID(id string) (int64, string, int64, error) {
	parts := strings.Split(strings.TrimSpace(id), ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return 0, "", 0, fmt.Errorf("membership id must be user_id:source_type:source_id")
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", 0, fmt.Errorf("invalid membership user id %q", parts[0])
	}
	sourceID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, "", 0, fmt.Errorf("invalid membership source id %q", parts[2])
	}
	return userID, normalizedMembershipSourceType(parts[1]), sourceID, nil
}

func normalizedMembershipSourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "namespace", "group":
		return "Namespace"
	case "project":
		return "Project"
	default:
		return strings.TrimSpace(value)
	}
}

func isNamespaceMembership(value string) bool {
	return strings.EqualFold(normalizedMembershipSourceType(value), "Namespace")
}

func isProjectMembership(value string) bool {
	return strings.EqualFold(normalizedMembershipSourceType(value), "Project")
}

func membershipTitle(membership Membership) string {
	label := firstNonEmpty(membership.SourceName, membership.SourcePath)
	if label == "" && membership.SourceID != 0 {
		label = strconv.FormatInt(membership.SourceID, 10)
	}
	if membership.SourceType != "" {
		label = membership.SourceType + " " + label
	}
	if membership.Role != "" {
		label += " (" + membership.Role + ")"
	}
	return strings.TrimSpace(label)
}

func formatTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func labels(values gitlab.Labels) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func basicUsername(user *gitlab.BasicUser) string {
	if user == nil {
		return ""
	}
	return user.Username
}

func basicUsernames(users []*gitlab.BasicUser) []string {
	out := make([]string, 0, len(users))
	for _, user := range users {
		if user != nil && strings.TrimSpace(user.Username) != "" {
			out = append(out, user.Username)
		}
	}
	return out
}

func compactDiff(diff string) string {
	const maxLines = 80
	const maxBytes = 12000
	diff = strings.TrimSpace(diff)
	if len(diff) > maxBytes {
		diff = diff[:maxBytes] + "\n... truncated ..."
	}
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}
	return strings.Join(append(lines[:maxLines], "... truncated ..."), "\n")
}

func decodedFilePreview(content, encoding string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var decoded []byte
	if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		data, err := base64.StdEncoding.DecodeString(content)
		if err == nil {
			decoded = data
		}
	}
	if decoded == nil {
		decoded = []byte(content)
	}
	return boundedText(string(decoded), 20000)
}

func boundedText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "\n... truncated ..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func accessLevelName(value gitlab.AccessLevelValue) string {
	switch value {
	case gitlab.MinimalAccessPermissions:
		return "minimal"
	case gitlab.GuestPermissions:
		return "guest"
	case gitlab.PlannerPermissions:
		return "planner"
	case gitlab.ReporterPermissions:
		return "reporter"
	case gitlab.DeveloperPermissions:
		return "developer"
	case gitlab.MaintainerPermissions:
		return "maintainer"
	case gitlab.OwnerPermissions:
		return "owner"
	case gitlab.AdminPermissions:
		return "admin"
	default:
		return ""
	}
}

func accessLevelRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal":
		return int(gitlab.MinimalAccessPermissions)
	case "guest":
		return int(gitlab.GuestPermissions)
	case "planner":
		return int(gitlab.PlannerPermissions)
	case "reporter":
		return int(gitlab.ReporterPermissions)
	case "developer":
		return int(gitlab.DeveloperPermissions)
	case "maintainer":
		return int(gitlab.MaintainerPermissions)
	case "owner":
		return int(gitlab.OwnerPermissions)
	case "admin":
		return int(gitlab.AdminPermissions)
	default:
		rank, _ := strconv.Atoi(value)
		return rank
	}
}
