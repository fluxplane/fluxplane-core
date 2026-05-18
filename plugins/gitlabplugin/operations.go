package gitlabplugin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

const mergeRequestOp = "mr"

type MRActionInput struct {
	Op                       string   `json:"op" jsonschema:"description=Merge request action.,enum=create,enum=close,enum=open,enum=reopen,enum=comment,enum=approve,enum=unapprove,enum=merge,enum=rebase,enum=retry_pipeline,enum=cancel_pipeline,required"`
	ProjectID                string   `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	MergeRequestIID          int64    `json:"merge_request_iid,omitempty" jsonschema:"description=Project-local merge request iid. Required except for create."`
	Title                    string   `json:"title,omitempty" jsonschema:"description=Merge request title for create."`
	Description              string   `json:"description,omitempty" jsonschema:"description=Merge request description for create."`
	SourceBranch             string   `json:"source_branch,omitempty" jsonschema:"description=Source branch for create."`
	TargetBranch             string   `json:"target_branch,omitempty" jsonschema:"description=Target branch for create."`
	Labels                   []string `json:"labels,omitempty" jsonschema:"description=Labels for create."`
	AssigneeIDs              []int64  `json:"assignee_ids,omitempty" jsonschema:"description=Assignee user ids for create."`
	ReviewerIDs              []int64  `json:"reviewer_ids,omitempty" jsonschema:"description=Reviewer user ids for create."`
	Body                     string   `json:"body,omitempty" jsonschema:"description=Comment body for comment."`
	Internal                 bool     `json:"internal,omitempty" jsonschema:"description=Whether a comment should be internal."`
	SHA                      string   `json:"sha,omitempty" jsonschema:"description=Expected head sha for approve or merge."`
	Squash                   *bool    `json:"squash,omitempty" jsonschema:"description=Whether merge should squash commits."`
	ShouldRemoveSourceBranch *bool    `json:"should_remove_source_branch,omitempty" jsonschema:"description=Whether merge should remove the source branch."`
	MergeCommitMessage       string   `json:"merge_commit_message,omitempty" jsonschema:"description=Merge commit message for merge."`
	SquashCommitMessage      string   `json:"squash_commit_message,omitempty" jsonschema:"description=Squash commit message for merge."`
	AutoMerge                *bool    `json:"auto_merge,omitempty" jsonschema:"description=Whether merge should auto-merge when possible."`
	SkipCI                   *bool    `json:"skip_ci,omitempty" jsonschema:"description=Whether rebase should skip CI."`
	PipelineID               int64    `json:"pipeline_id,omitempty" jsonschema:"description=Pipeline id for retry_pipeline or cancel_pipeline."`
}

type MRActionResult struct {
	Op              string `json:"op"`
	ProjectID       string `json:"project_id,omitempty"`
	MergeRequestIID int64  `json:"merge_request_iid,omitempty"`
	PipelineID      int64  `json:"pipeline_id,omitempty"`
	NoteID          int64  `json:"note_id,omitempty"`
	State           string `json:"state,omitempty"`
	Status          string `json:"status,omitempty"`
	WebURL          string `json:"web_url,omitempty"`
	Message         string `json:"message,omitempty"`
}

func (p Plugin) mrOperationSpec() operation.Spec {
	return operationruntime.WithTypedContract[MRActionInput, MRActionResult](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(p.operationName(mergeRequestOp))},
		Description: "Create, update, review, merge, rebase, or comment on GitLab merge requests.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects: operation.EffectSet{
				operation.EffectNetwork,
				operation.EffectWriteExternal,
				operation.EffectCreate,
				operation.EffectUpdate,
			},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskHigh,
		},
	})
}

func (p Plugin) mrOperation() operation.Operation {
	return operationruntime.NewTypedResult[MRActionInput, MRActionResult](
		p.mrOperationSpec(),
		p.runMRAction,
		operationruntime.WithAccess(p.mrAccess),
	)
}

func (p Plugin) runMRAction(ctx operation.Context, req MRActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.ProjectID == "" {
		return operation.Failed("invalid_"+p.operationName(mergeRequestOp)+"_input", "project_id is required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(mergeRequestOp)+"_failed", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	result, err := p.executeMRAction(ctx, client, project, req)
	if err != nil {
		return operation.Failed(p.operationName(mergeRequestOp)+"_failed", err.Error(), nil)
	}
	return operation.OK(result)
}

func (p Plugin) executeMRAction(ctx operation.Context, client gitlabClient, project any, req MRActionInput) (MRActionResult, error) {
	base := MRActionResult{Op: req.Op, ProjectID: req.ProjectID, MergeRequestIID: req.MergeRequestIID}
	switch req.Op {
	case "create":
		return createMR(ctx, client, project, req, base)
	case "close":
		return updateMRState(ctx, client, project, req, "close", base)
	case "open", "reopen":
		base.Op = "reopen"
		return updateMRState(ctx, client, project, req, "reopen", base)
	case "comment":
		return commentMR(ctx, client, project, req, base)
	case "approve":
		return approveMR(ctx, client, project, req, base)
	case "unapprove":
		return unapproveMR(ctx, client, project, req, base)
	case "merge":
		return mergeMR(ctx, client, project, req, base)
	case "rebase":
		return rebaseMR(ctx, client, project, req, base)
	case "retry_pipeline":
		return retryPipeline(ctx, client, project, req, base)
	case "cancel_pipeline":
		return cancelPipeline(ctx, client, project, req, base)
	default:
		return MRActionResult{}, fmt.Errorf("unsupported op %q", req.Op)
	}
}

func createMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.SourceBranch) == "" || strings.TrimSpace(req.TargetBranch) == "" {
		return MRActionResult{}, fmt.Errorf("title, source_branch, and target_branch are required for create")
	}
	opts := &gitlab.CreateMergeRequestOptions{
		Title:        gitlab.Ptr(strings.TrimSpace(req.Title)),
		SourceBranch: gitlab.Ptr(strings.TrimSpace(req.SourceBranch)),
		TargetBranch: gitlab.Ptr(strings.TrimSpace(req.TargetBranch)),
	}
	if strings.TrimSpace(req.Description) != "" {
		opts.Description = gitlab.Ptr(strings.TrimSpace(req.Description))
	}
	if len(req.Labels) > 0 {
		labels := gitlab.LabelOptions(cleaned(req.Labels))
		opts.Labels = &labels
	}
	if len(req.AssigneeIDs) > 0 {
		opts.AssigneeIDs = &req.AssigneeIDs
	}
	if len(req.ReviewerIDs) > 0 {
		opts.ReviewerIDs = &req.ReviewerIDs
	}
	mr, err := client.CreateMergeRequest(ctx, project, opts)
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromMR(base, mergeRequestFromFull(mr), "merge request created"), nil
}

func updateMRState(ctx operation.Context, client gitlabClient, project any, req MRActionInput, state string, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for %s", req.Op)
	}
	mr, err := client.UpdateMergeRequest(ctx, project, req.MergeRequestIID, &gitlab.UpdateMergeRequestOptions{StateEvent: gitlab.Ptr(state)})
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromMR(base, mergeRequestFromFull(mr), "merge request "+state+" requested"), nil
}

func commentMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for comment")
	}
	if strings.TrimSpace(req.Body) == "" {
		return MRActionResult{}, fmt.Errorf("body is required for comment")
	}
	note, err := client.CreateMergeRequestNote(ctx, project, req.MergeRequestIID, &gitlab.CreateMergeRequestNoteOptions{
		Body:     gitlab.Ptr(strings.TrimSpace(req.Body)),
		Internal: gitlab.Ptr(req.Internal),
	})
	if err != nil {
		return MRActionResult{}, err
	}
	base.NoteID = note.ID
	base.Message = "merge request comment created"
	return base, nil
}

func approveMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for approve")
	}
	opts := &gitlab.ApproveMergeRequestOptions{}
	if strings.TrimSpace(req.SHA) != "" {
		opts.SHA = gitlab.Ptr(strings.TrimSpace(req.SHA))
	}
	if _, err := client.ApproveMergeRequest(ctx, project, req.MergeRequestIID, opts); err != nil {
		return MRActionResult{}, err
	}
	base.Status = "approved"
	base.Message = "merge request approved"
	return base, nil
}

func unapproveMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for unapprove")
	}
	if err := client.UnapproveMergeRequest(ctx, project, req.MergeRequestIID); err != nil {
		return MRActionResult{}, err
	}
	base.Status = "unapproved"
	base.Message = "merge request unapproved"
	return base, nil
}

func mergeMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for merge")
	}
	opts := &gitlab.AcceptMergeRequestOptions{
		Squash:                   req.Squash,
		ShouldRemoveSourceBranch: req.ShouldRemoveSourceBranch,
		AutoMerge:                req.AutoMerge,
	}
	if strings.TrimSpace(req.SHA) != "" {
		opts.SHA = gitlab.Ptr(strings.TrimSpace(req.SHA))
	}
	if strings.TrimSpace(req.MergeCommitMessage) != "" {
		opts.MergeCommitMessage = gitlab.Ptr(strings.TrimSpace(req.MergeCommitMessage))
	}
	if strings.TrimSpace(req.SquashCommitMessage) != "" {
		opts.SquashCommitMessage = gitlab.Ptr(strings.TrimSpace(req.SquashCommitMessage))
	}
	mr, err := client.AcceptMergeRequest(ctx, project, req.MergeRequestIID, opts)
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromMR(base, mergeRequestFromFull(mr), "merge request merged"), nil
}

func rebaseMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for rebase")
	}
	if err := client.RebaseMergeRequest(ctx, project, req.MergeRequestIID, &gitlab.RebaseMergeRequestOptions{SkipCI: req.SkipCI}); err != nil {
		return MRActionResult{}, err
	}
	base.Status = "rebase_started"
	base.Message = "merge request rebase started"
	return base, nil
}

func retryPipeline(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.PipelineID == 0 {
		return MRActionResult{}, fmt.Errorf("pipeline_id is required for retry_pipeline")
	}
	pipeline, err := client.RetryPipelineBuild(ctx, project, req.PipelineID)
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromPipeline(base, pipelineFromFull(pipeline), "pipeline retried"), nil
}

func cancelPipeline(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.PipelineID == 0 {
		return MRActionResult{}, fmt.Errorf("pipeline_id is required for cancel_pipeline")
	}
	pipeline, err := client.CancelPipelineBuild(ctx, project, req.PipelineID)
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromPipeline(base, pipelineFromFull(pipeline), "pipeline canceled"), nil
}

func resultFromMR(base MRActionResult, mr MergeRequest, message string) MRActionResult {
	if mr.ProjectID != 0 {
		base.ProjectID = strconv.FormatInt(mr.ProjectID, 10)
	}
	if mr.IID != 0 {
		base.MergeRequestIID = mr.IID
	}
	base.State = mr.State
	base.Status = mr.DetailedMergeStatus
	base.WebURL = mr.WebURL
	base.Message = message
	return base
}

func resultFromPipeline(base MRActionResult, pipeline Pipeline, message string) MRActionResult {
	if pipeline.ProjectID != 0 {
		base.ProjectID = strconv.FormatInt(pipeline.ProjectID, 10)
	}
	base.PipelineID = pipeline.ID
	base.Status = pipeline.Status
	base.WebURL = pipeline.WebURL
	base.Message = message
	return base
}

func (p Plugin) mrAccess(operation.Context, MRActionInput) ([]operationruntime.AccessDescriptor, error) {
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(p.config().baseURL(), policy.ActionNetworkFetch)}, nil
}
