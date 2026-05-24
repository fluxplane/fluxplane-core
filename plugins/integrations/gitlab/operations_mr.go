package gitlab

import (
	"fmt"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type MRActionInput struct {
	Op                       string   `json:"op" jsonschema:"description=Merge request action.,enum=create,enum=edit,enum=close,enum=open,enum=reopen,enum=comment,enum=inline_comment,enum=reply_discussion,enum=resolve_discussion,enum=react,enum=approve,enum=unapprove,enum=merge,enum=rebase,required"`
	ProjectID                string   `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	MergeRequestIID          int64    `json:"merge_request_iid,omitempty" jsonschema:"description=Project-local merge request iid. Required except for create."`
	Title                    string   `json:"title,omitempty" jsonschema:"description=Merge request title for create."`
	Description              string   `json:"description,omitempty" jsonschema:"description=Merge request description for create."`
	SourceBranch             string   `json:"source_branch,omitempty" jsonschema:"description=Source branch for create."`
	TargetBranch             string   `json:"target_branch,omitempty" jsonschema:"description=Target branch for create."`
	Labels                   []string `json:"labels,omitempty" jsonschema:"description=Labels for create."`
	LabelsToAdd              []string `json:"labels_to_add,omitempty" jsonschema:"description=Labels to add for edit."`
	LabelsToRemove           []string `json:"labels_to_remove,omitempty" jsonschema:"description=Labels to remove for edit."`
	AssigneeIDs              []int64  `json:"assignee_ids,omitempty" jsonschema:"description=Assignee user ids for create."`
	ReviewerIDs              []int64  `json:"reviewer_ids,omitempty" jsonschema:"description=Reviewer user ids for create."`
	Draft                    *bool    `json:"draft,omitempty" jsonschema:"description=Whether the merge request should be a draft for create or edit."`
	Body                     string   `json:"body,omitempty" jsonschema:"description=Comment body for comment."`
	Internal                 bool     `json:"internal,omitempty" jsonschema:"description=Whether a comment should be internal."`
	FilePath                 string   `json:"file_path,omitempty" jsonschema:"description=File path for inline_comment."`
	Line                     int64    `json:"line,omitempty" jsonschema:"description=New-file line number for inline_comment unless line_side is old."`
	LineSide                 string   `json:"line_side,omitempty" jsonschema:"description=Line side for inline_comment: new or old."`
	Context                  int      `json:"context,omitempty" jsonschema:"description=Surrounding diff lines to include in inline_comment result."`
	DiscussionID             string   `json:"discussion_id,omitempty" jsonschema:"description=GitLab discussion id for replying to or resolving a discussion."`
	Resolved                 *bool    `json:"resolved,omitempty" jsonschema:"description=Whether a discussion should be marked resolved."`
	Emoji                    string   `json:"emoji,omitempty" jsonschema:"description=Award emoji name for react."`
	NoteID                   int64    `json:"note_id,omitempty" jsonschema:"description=Optional note id for note-level emoji reactions."`
	Reason                   string   `json:"reason,omitempty" jsonschema:"description=Optional reason note to create before close or reopen."`
	SHA                      string   `json:"sha,omitempty" jsonschema:"description=Expected head sha for approve or merge."`
	Squash                   *bool    `json:"squash,omitempty" jsonschema:"description=Whether merge should squash commits."`
	ShouldRemoveSourceBranch *bool    `json:"should_remove_source_branch,omitempty" jsonschema:"description=Whether merge should remove the source branch."`
	MergeCommitMessage       string   `json:"merge_commit_message,omitempty" jsonschema:"description=Merge commit message for merge."`
	SquashCommitMessage      string   `json:"squash_commit_message,omitempty" jsonschema:"description=Squash commit message for merge."`
	AutoMerge                *bool    `json:"auto_merge,omitempty" jsonschema:"description=Whether merge should auto-merge when possible."`
	SkipCI                   *bool    `json:"skip_ci,omitempty" jsonschema:"description=Whether rebase should skip CI."`
}

type MRActionResult struct {
	Op              string `json:"op"`
	ProjectID       string `json:"project_id,omitempty"`
	MergeRequestIID int64  `json:"merge_request_iid,omitempty"`
	NoteID          int64  `json:"note_id,omitempty"`
	DiscussionID    string `json:"discussion_id,omitempty"`
	AwardEmojiID    int64  `json:"award_emoji_id,omitempty"`
	AwardEmojiName  string `json:"award_emoji_name,omitempty"`
	TargetPath      string `json:"target_path,omitempty"`
	TargetLine      int64  `json:"target_line,omitempty"`
	TargetLineType  string `json:"target_line_type,omitempty"`
	State           string `json:"state,omitempty"`
	Status          string `json:"status,omitempty"`
	WebURL          string `json:"web_url,omitempty"`
	Message         string `json:"message,omitempty"`
}

func (p Plugin) mrOperationSpec() operation.Spec {
	return gitlabWriteSpec(
		p.operationName(mergeRequestOp),
		"Create, update, review, merge, rebase, or comment on GitLab merge requests.",
		operation.RiskHigh,
		operationruntime.TypeOf[MRActionInput](p.operationName(mergeRequestOp)+"_input"),
		operationruntime.TypeOf[MRActionResult](p.operationName(mergeRequestOp)+"_output"),
	)
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
	case "edit":
		return editMR(ctx, client, project, req, base)
	case "close":
		return updateMRState(ctx, client, project, req, "close", base)
	case "open", "reopen":
		base.Op = "reopen"
		return updateMRState(ctx, client, project, req, "reopen", base)
	case "comment":
		return commentMR(ctx, client, project, req, base)
	case "inline_comment":
		return inlineCommentMR(ctx, client, project, req, base)
	case "reply_discussion":
		return replyDiscussionMR(ctx, client, project, req, base)
	case "resolve_discussion":
		return resolveDiscussionMR(ctx, client, project, req, base)
	case "react":
		return reactMR(ctx, client, project, req, base)
	case "approve":
		return approveMR(ctx, client, project, req, base)
	case "unapprove":
		return unapproveMR(ctx, client, project, req, base)
	case "merge":
		return mergeMR(ctx, client, project, req, base)
	case "rebase":
		return rebaseMR(ctx, client, project, req, base)
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
	if req.Draft != nil && *req.Draft {
		opts.Title = gitlab.Ptr(ensureDraftTitle(*opts.Title))
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
	opts.Squash = req.Squash
	opts.RemoveSourceBranch = req.ShouldRemoveSourceBranch
	mr, err := client.CreateMergeRequest(ctx, project, opts)
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromMR(base, mergeRequestFromFull(mr), "merge request created"), nil
}

func editMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for edit")
	}
	opts := &gitlab.UpdateMergeRequestOptions{}
	updates := 0
	if title := strings.TrimSpace(req.Title); title != "" {
		if req.Draft != nil {
			if *req.Draft {
				title = ensureDraftTitle(title)
			} else {
				title = removeDraftTitle(title)
			}
		}
		opts.Title = gitlab.Ptr(title)
		updates++
	}
	if description := strings.TrimSpace(req.Description); description != "" {
		opts.Description = gitlab.Ptr(description)
		updates++
	}
	if target := strings.TrimSpace(req.TargetBranch); target != "" {
		opts.TargetBranch = gitlab.Ptr(target)
		updates++
	}
	if len(req.Labels) > 0 {
		labels := gitlab.LabelOptions(cleaned(req.Labels))
		opts.Labels = &labels
		updates++
	}
	if len(req.LabelsToAdd) > 0 {
		labels := gitlab.LabelOptions(cleaned(req.LabelsToAdd))
		opts.AddLabels = &labels
		updates++
	}
	if len(req.LabelsToRemove) > 0 {
		labels := gitlab.LabelOptions(cleaned(req.LabelsToRemove))
		opts.RemoveLabels = &labels
		updates++
	}
	if len(req.AssigneeIDs) > 0 {
		opts.AssigneeIDs = &req.AssigneeIDs
		updates++
	}
	if len(req.ReviewerIDs) > 0 {
		opts.ReviewerIDs = &req.ReviewerIDs
		updates++
	}
	if req.Squash != nil {
		opts.Squash = req.Squash
		updates++
	}
	if req.ShouldRemoveSourceBranch != nil {
		opts.RemoveSourceBranch = req.ShouldRemoveSourceBranch
		updates++
	}
	if req.Draft != nil && opts.Title == nil {
		current, err := client.GetMergeRequest(ctx, project, req.MergeRequestIID, nil)
		if err != nil {
			return MRActionResult{}, err
		}
		if current == nil {
			return MRActionResult{}, coredatasource.ErrNotFound
		}
		title := current.Title
		if *req.Draft {
			title = ensureDraftTitle(title)
		} else {
			title = removeDraftTitle(title)
		}
		opts.Title = gitlab.Ptr(title)
		updates++
	}
	if updates == 0 {
		return MRActionResult{}, fmt.Errorf("at least one edit field is required")
	}
	mr, err := client.UpdateMergeRequest(ctx, project, req.MergeRequestIID, opts)
	if err != nil {
		return MRActionResult{}, err
	}
	return resultFromMR(base, mergeRequestFromFull(mr), "merge request updated"), nil
}

func updateMRState(ctx operation.Context, client gitlabClient, project any, req MRActionInput, state string, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for %s", req.Op)
	}
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		note, err := client.CreateMergeRequestNote(ctx, project, req.MergeRequestIID, &gitlab.CreateMergeRequestNoteOptions{
			Body:     gitlab.Ptr(reason),
			Internal: gitlab.Ptr(req.Internal),
		})
		if err != nil {
			return MRActionResult{}, err
		}
		base.NoteID = note.ID
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

func inlineCommentMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for inline_comment")
	}
	path := strings.TrimSpace(req.FilePath)
	if path == "" {
		return MRActionResult{}, fmt.Errorf("file_path is required for inline_comment")
	}
	if req.Line == 0 {
		return MRActionResult{}, fmt.Errorf("line is required for inline_comment")
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return MRActionResult{}, fmt.Errorf("body is required for inline_comment")
	}
	diffs, err := client.ListMergeRequestDiffs(ctx, project, req.MergeRequestIID, &gitlab.ListMergeRequestDiffsOptions{ListOptions: gitlab.ListOptions{PerPage: defaultPageSize, Page: 1}})
	if err != nil {
		return MRActionResult{}, err
	}
	versions, err := client.GetMergeRequestDiffVersions(ctx, project, req.MergeRequestIID, &gitlab.GetMergeRequestDiffVersionsOptions{ListOptions: gitlab.ListOptions{PerPage: 1, Page: 1}})
	if err != nil {
		return MRActionResult{}, err
	}
	target, err := validateInlineTarget(project, req.MergeRequestIID, path, req.Line, req.LineSide, diffs, versions, req.Context)
	if err != nil {
		return MRActionResult{}, err
	}
	if target.BaseSHA == "" || target.StartSHA == "" || target.HeadSHA == "" {
		return MRActionResult{}, fmt.Errorf("merge request diff versions did not include base/start/head sha values")
	}
	position := &gitlab.PositionOptions{
		BaseSHA:      gitlab.Ptr(target.BaseSHA),
		StartSHA:     gitlab.Ptr(target.StartSHA),
		HeadSHA:      gitlab.Ptr(target.HeadSHA),
		OldPath:      gitlab.Ptr(firstNonEmpty(target.Path, path)),
		NewPath:      gitlab.Ptr(firstNonEmpty(target.Path, path)),
		PositionType: gitlab.Ptr("text"),
	}
	for _, diff := range diffs {
		if diff != nil && (diff.NewPath == path || diff.OldPath == path) {
			position.OldPath = gitlab.Ptr(firstNonEmpty(diff.OldPath, diff.NewPath))
			position.NewPath = gitlab.Ptr(firstNonEmpty(diff.NewPath, diff.OldPath))
			break
		}
	}
	if strings.EqualFold(target.LineSide, "old") {
		position.OldLine = gitlab.Ptr(target.OldLine)
	} else {
		position.NewLine = gitlab.Ptr(target.NewLine)
	}
	discussion, err := client.CreateMergeRequestDiscussion(ctx, project, req.MergeRequestIID, &gitlab.CreateMergeRequestDiscussionOptions{
		Body:     gitlab.Ptr(body),
		Position: position,
	})
	if err != nil {
		return MRActionResult{}, err
	}
	base.TargetPath = path
	base.TargetLine = req.Line
	base.TargetLineType = target.LineType
	if discussion != nil {
		base.DiscussionID = discussion.ID
		if len(discussion.Notes) > 0 && discussion.Notes[0] != nil {
			base.NoteID = discussion.Notes[0].ID
		}
	}
	base.Message = "merge request inline comment created"
	return base, nil
}

func replyDiscussionMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for reply_discussion")
	}
	discussionID := strings.TrimSpace(req.DiscussionID)
	if discussionID == "" {
		return MRActionResult{}, fmt.Errorf("discussion_id is required for reply_discussion")
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return MRActionResult{}, fmt.Errorf("body is required for reply_discussion")
	}
	note, err := client.AddMergeRequestDiscussionNote(ctx, project, req.MergeRequestIID, discussionID, &gitlab.AddMergeRequestDiscussionNoteOptions{
		Body: gitlab.Ptr(body),
	})
	if err != nil {
		return MRActionResult{}, err
	}
	base.DiscussionID = discussionID
	if note != nil {
		base.NoteID = note.ID
	}
	base.Message = "merge request discussion reply created"
	return base, nil
}

func resolveDiscussionMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for resolve_discussion")
	}
	discussionID := strings.TrimSpace(req.DiscussionID)
	if discussionID == "" {
		return MRActionResult{}, fmt.Errorf("discussion_id is required for resolve_discussion")
	}
	if req.Resolved == nil {
		return MRActionResult{}, fmt.Errorf("resolved is required for resolve_discussion")
	}
	discussion, err := client.ResolveMergeRequestDiscussion(ctx, project, req.MergeRequestIID, discussionID, &gitlab.ResolveMergeRequestDiscussionOptions{
		Resolved: req.Resolved,
	})
	if err != nil {
		return MRActionResult{}, err
	}
	base.DiscussionID = discussionID
	if discussion != nil && discussion.ID != "" {
		base.DiscussionID = discussion.ID
	}
	if *req.Resolved {
		base.Status = "resolved"
		base.Message = "merge request discussion resolved"
	} else {
		base.Status = "unresolved"
		base.Message = "merge request discussion unresolved"
	}
	return base, nil
}

func reactMR(ctx operation.Context, client gitlabClient, project any, req MRActionInput, base MRActionResult) (MRActionResult, error) {
	if req.MergeRequestIID == 0 {
		return MRActionResult{}, fmt.Errorf("merge_request_iid is required for react")
	}
	emoji := strings.Trim(strings.TrimSpace(req.Emoji), ":")
	if emoji == "" {
		return MRActionResult{}, fmt.Errorf("emoji is required for react")
	}
	opts := &gitlab.CreateAwardEmojiOptions{Name: emoji}
	var (
		award *gitlab.AwardEmoji
		err   error
	)
	if req.NoteID != 0 {
		award, err = client.CreateMergeRequestAwardEmojiOnNote(ctx, project, req.MergeRequestIID, req.NoteID, opts)
	} else {
		award, err = client.CreateMergeRequestAwardEmoji(ctx, project, req.MergeRequestIID, opts)
	}
	if err != nil {
		return MRActionResult{}, err
	}
	base.NoteID = req.NoteID
	base.AwardEmojiName = emoji
	if award != nil {
		base.AwardEmojiID = award.ID
		if award.Name != "" {
			base.AwardEmojiName = award.Name
		}
	}
	base.Message = "merge request award emoji created"
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

func (p Plugin) mrAccess(operation.Context, MRActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}

func ensureDraftTitle(title string) string {
	title = strings.TrimSpace(title)
	lower := strings.ToLower(title)
	if strings.HasPrefix(lower, "draft:") || strings.HasPrefix(lower, "wip:") {
		return title
	}
	return "Draft: " + title
}

func removeDraftTitle(title string) string {
	title = strings.TrimSpace(title)
	lower := strings.ToLower(title)
	for _, prefix := range []string{"draft:", "wip:"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(title[len(prefix):])
		}
	}
	return title
}
