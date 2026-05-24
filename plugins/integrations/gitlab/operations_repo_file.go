package gitlab

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type RepoFileActionInput struct {
	Op              string `json:"op" jsonschema:"description=Repository file action.,enum=create,enum=update,enum=delete,required"`
	ProjectID       string `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	FilePath        string `json:"file_path" jsonschema:"description=Repository file path.,required"`
	Branch          string `json:"branch" jsonschema:"description=Target branch.,required"`
	Content         string `json:"content,omitempty" jsonschema:"description=File content for create or update."`
	CommitMessage   string `json:"commit_message" jsonschema:"description=Commit message.,required"`
	StartBranch     string `json:"start_branch,omitempty" jsonschema:"description=Optional source branch when creating target branch."`
	Encoding        string `json:"encoding,omitempty" jsonschema:"description=Content encoding, such as text or base64."`
	AuthorEmail     string `json:"author_email,omitempty" jsonschema:"description=Commit author email."`
	AuthorName      string `json:"author_name,omitempty" jsonschema:"description=Commit author name."`
	LastCommitID    string `json:"last_commit_id,omitempty" jsonschema:"description=Expected last commit id for update or delete."`
	ExecuteFilemode *bool  `json:"execute_filemode,omitempty" jsonschema:"description=Whether the file should be executable."`
}

type RepoFileActionResult struct {
	Op        string `json:"op"`
	ProjectID string `json:"project_id,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (p Plugin) repoFileOperationSpec() operation.Spec {
	return gitlabWriteSpec(
		p.operationName(repoFileOp),
		"Create, update, or delete files in a GitLab repository.",
		operation.RiskHigh,
		operationruntime.TypeOf[RepoFileActionInput](p.operationName(repoFileOp)+"_input"),
		operationruntime.TypeOf[RepoFileActionResult](p.operationName(repoFileOp)+"_output"),
	)
}

func (p Plugin) repoFileOperation() operation.Operation {
	return operationruntime.NewTypedResult[RepoFileActionInput, RepoFileActionResult](
		p.repoFileOperationSpec(),
		p.runRepoFileAction,
		operationruntime.WithAccess(p.repoFileAccess),
	)
}

func (p Plugin) runRepoFileAction(ctx operation.Context, req RepoFileActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.FilePath = strings.TrimSpace(req.FilePath)
	req.Branch = strings.TrimSpace(req.Branch)
	if req.ProjectID == "" || req.FilePath == "" || req.Branch == "" || strings.TrimSpace(req.CommitMessage) == "" {
		return operation.Failed("invalid_"+p.operationName(repoFileOp)+"_input", "project_id, file_path, branch, and commit_message are required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(repoFileOp)+"_failed", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	base := RepoFileActionResult{Op: req.Op, ProjectID: projectIDLabel(project), FilePath: req.FilePath, Branch: req.Branch}
	switch req.Op {
	case "create":
		if req.Content == "" {
			return operation.Failed("invalid_"+p.operationName(repoFileOp)+"_input", "content is required for create", nil)
		}
		info, err := client.CreateFile(ctx, project, req.FilePath, createFileOptions(req))
		if err != nil {
			return operation.Failed(p.operationName(repoFileOp)+"_failed", err.Error(), nil)
		}
		return operation.OK(repoFileResult(base, info, "repository file created"))
	case "update":
		if req.Content == "" {
			return operation.Failed("invalid_"+p.operationName(repoFileOp)+"_input", "content is required for update", nil)
		}
		info, err := client.UpdateFile(ctx, project, req.FilePath, updateFileOptions(req))
		if err != nil {
			return operation.Failed(p.operationName(repoFileOp)+"_failed", err.Error(), nil)
		}
		return operation.OK(repoFileResult(base, info, "repository file updated"))
	case "delete":
		if err := client.DeleteFile(ctx, project, req.FilePath, deleteFileOptions(req)); err != nil {
			return operation.Failed(p.operationName(repoFileOp)+"_failed", err.Error(), nil)
		}
		base.Message = "repository file deleted"
		return operation.OK(base)
	default:
		return operation.Failed("invalid_"+p.operationName(repoFileOp)+"_input", fmt.Sprintf("unsupported op %q", req.Op), nil)
	}
}

func createFileOptions(req RepoFileActionInput) *gitlab.CreateFileOptions {
	opts := &gitlab.CreateFileOptions{
		Branch:          gitlab.Ptr(req.Branch),
		Content:         gitlab.Ptr(req.Content),
		CommitMessage:   gitlab.Ptr(strings.TrimSpace(req.CommitMessage)),
		ExecuteFilemode: boolPtr(req.ExecuteFilemode),
	}
	if value := strings.TrimSpace(req.StartBranch); value != "" {
		opts.StartBranch = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.Encoding); value != "" {
		opts.Encoding = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorEmail); value != "" {
		opts.AuthorEmail = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorName); value != "" {
		opts.AuthorName = gitlab.Ptr(value)
	}
	return opts
}

func updateFileOptions(req RepoFileActionInput) *gitlab.UpdateFileOptions {
	opts := &gitlab.UpdateFileOptions{
		Branch:          gitlab.Ptr(req.Branch),
		Content:         gitlab.Ptr(req.Content),
		CommitMessage:   gitlab.Ptr(strings.TrimSpace(req.CommitMessage)),
		ExecuteFilemode: boolPtr(req.ExecuteFilemode),
	}
	if value := strings.TrimSpace(req.StartBranch); value != "" {
		opts.StartBranch = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.Encoding); value != "" {
		opts.Encoding = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorEmail); value != "" {
		opts.AuthorEmail = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorName); value != "" {
		opts.AuthorName = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.LastCommitID); value != "" {
		opts.LastCommitID = gitlab.Ptr(value)
	}
	return opts
}

func deleteFileOptions(req RepoFileActionInput) *gitlab.DeleteFileOptions {
	opts := &gitlab.DeleteFileOptions{
		Branch:        gitlab.Ptr(req.Branch),
		CommitMessage: gitlab.Ptr(strings.TrimSpace(req.CommitMessage)),
	}
	if value := strings.TrimSpace(req.StartBranch); value != "" {
		opts.StartBranch = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorEmail); value != "" {
		opts.AuthorEmail = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.AuthorName); value != "" {
		opts.AuthorName = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.LastCommitID); value != "" {
		opts.LastCommitID = gitlab.Ptr(value)
	}
	return opts
}

func repoFileResult(base RepoFileActionResult, info *gitlab.FileInfo, message string) RepoFileActionResult {
	if info != nil {
		base.FilePath = info.FilePath
		base.Branch = info.Branch
	}
	base.Message = message
	return base
}

func (p Plugin) repoFileAccess(operation.Context, RepoFileActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
