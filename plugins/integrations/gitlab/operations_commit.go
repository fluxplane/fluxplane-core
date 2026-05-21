package gitlab

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type CommitActionInput struct {
	ProjectID     string                  `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	Branch        string                  `json:"branch" jsonschema:"description=Target branch.,required"`
	CommitMessage string                  `json:"commit_message" jsonschema:"description=Commit message.,required"`
	Actions       []CommitFileActionInput `json:"actions" jsonschema:"description=File actions for this commit.,required"`
	StartBranch   string                  `json:"start_branch,omitempty" jsonschema:"description=Optional source branch."`
	StartSHA      string                  `json:"start_sha,omitempty" jsonschema:"description=Optional source SHA."`
	StartProject  string                  `json:"start_project,omitempty" jsonschema:"description=Optional source project."`
	AuthorEmail   string                  `json:"author_email,omitempty" jsonschema:"description=Commit author email."`
	AuthorName    string                  `json:"author_name,omitempty" jsonschema:"description=Commit author name."`
	Force         *bool                   `json:"force,omitempty" jsonschema:"description=Whether to force update branch."`
}

type CommitFileActionInput struct {
	Action          string `json:"action" jsonschema:"description=File action.,enum=create,enum=update,enum=delete,enum=move,enum=chmod,required"`
	FilePath        string `json:"file_path" jsonschema:"description=File path.,required"`
	PreviousPath    string `json:"previous_path,omitempty" jsonschema:"description=Previous path for move."`
	Content         string `json:"content,omitempty" jsonschema:"description=File content."`
	Encoding        string `json:"encoding,omitempty" jsonschema:"description=Content encoding."`
	LastCommitID    string `json:"last_commit_id,omitempty" jsonschema:"description=Expected last commit id."`
	ExecuteFilemode *bool  `json:"execute_filemode,omitempty" jsonschema:"description=Whether the file should be executable."`
}

type CommitActionResult struct {
	ProjectID string `json:"project_id,omitempty"`
	Branch    string `json:"branch,omitempty"`
	SHA       string `json:"sha,omitempty"`
	ShortID   string `json:"short_id,omitempty"`
	Title     string `json:"title,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (p Plugin) commitOperationSpec() operation.Spec {
	return gitlabWriteSpec(
		p.operationName(commitOp),
		"Create a GitLab commit with one or more repository file actions.",
		operation.RiskHigh,
		operationruntime.TypeOf[CommitActionInput](p.operationName(commitOp)+"_input"),
		operationruntime.TypeOf[CommitActionResult](p.operationName(commitOp)+"_output"),
	)
}

func (p Plugin) commitOperation() operation.Operation {
	return operationruntime.NewTypedResult[CommitActionInput, CommitActionResult](
		p.commitOperationSpec(),
		p.runCommitAction,
		operationruntime.WithAccess(p.commitAccess),
	)
}

func (p Plugin) runCommitAction(ctx operation.Context, req CommitActionInput) operation.Result {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.Branch = strings.TrimSpace(req.Branch)
	if req.ProjectID == "" || req.Branch == "" || strings.TrimSpace(req.CommitMessage) == "" || len(req.Actions) == 0 {
		return operation.Failed("invalid_"+p.operationName(commitOp)+"_input", "project_id, branch, commit_message, and actions are required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(commitOp)+"_failed", err.Error(), nil)
	}
	opts, err := createCommitOptions(req)
	if err != nil {
		return operation.Failed("invalid_"+p.operationName(commitOp)+"_input", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	commit, err := client.CreateCommit(ctx, project, opts)
	if err != nil {
		return operation.Failed(p.operationName(commitOp)+"_failed", err.Error(), nil)
	}
	return operation.OK(commitActionResult(projectIDLabel(project), req.Branch, commit))
}

func createCommitOptions(req CommitActionInput) (*gitlab.CreateCommitOptions, error) {
	opts := &gitlab.CreateCommitOptions{
		Branch:        gitlab.Ptr(strings.TrimSpace(req.Branch)),
		CommitMessage: gitlab.Ptr(strings.TrimSpace(req.CommitMessage)),
		Force:         boolPtr(req.Force),
		Actions:       make([]*gitlab.CommitActionOptions, 0, len(req.Actions)),
	}
	if value := strings.TrimSpace(req.StartBranch); value != "" {
		opts.StartBranch = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.StartSHA); value != "" {
		opts.StartSHA = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.StartProject); value != "" {
		opts.StartProject = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorEmail); value != "" {
		opts.AuthorEmail = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorName); value != "" {
		opts.AuthorName = gitlab.Ptr(value)
	}
	for i, action := range req.Actions {
		next, err := commitFileActionOptions(action)
		if err != nil {
			return nil, fmt.Errorf("actions[%d]: %w", i, err)
		}
		opts.Actions = append(opts.Actions, next)
	}
	return opts, nil
}

func commitFileActionOptions(input CommitFileActionInput) (*gitlab.CommitActionOptions, error) {
	action := gitlab.FileActionValue(strings.ToLower(strings.TrimSpace(input.Action)))
	switch action {
	case gitlab.FileCreate, gitlab.FileUpdate, gitlab.FileDelete, gitlab.FileMove, gitlab.FileChmod:
	default:
		return nil, fmt.Errorf("unsupported action %q", input.Action)
	}
	filePath := strings.TrimSpace(input.FilePath)
	if filePath == "" {
		return nil, fmt.Errorf("file_path is required")
	}
	opts := &gitlab.CommitActionOptions{Action: &action, FilePath: gitlab.Ptr(filePath), ExecuteFilemode: boolPtr(input.ExecuteFilemode)}
	if value := strings.TrimSpace(input.PreviousPath); value != "" {
		opts.PreviousPath = gitlab.Ptr(value)
	}
	if input.Content != "" {
		opts.Content = gitlab.Ptr(input.Content)
	}
	if value := strings.TrimSpace(input.Encoding); value != "" {
		opts.Encoding = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(input.LastCommitID); value != "" {
		opts.LastCommitID = gitlab.Ptr(value)
	}
	return opts, nil
}

func commitActionResult(projectID, branch string, commit *gitlab.Commit) CommitActionResult {
	out := CommitActionResult{ProjectID: projectID, Branch: branch, Message: "commit created"}
	if commit != nil {
		out.SHA = commit.ID
		out.ShortID = commit.ShortID
		out.Title = commit.Title
		out.WebURL = commit.WebURL
	}
	return out
}

func (p Plugin) commitAccess(operation.Context, CommitActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
