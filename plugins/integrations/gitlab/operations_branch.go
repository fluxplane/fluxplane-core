package gitlab

import (
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/operation"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type BranchActionInput struct {
	Op        string `json:"op" jsonschema:"description=Branch action.,enum=create,enum=delete,enum=delete_merged,required"`
	ProjectID string `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	Branch    string `json:"branch,omitempty" jsonschema:"description=Branch name for create or delete."`
	Ref       string `json:"ref,omitempty" jsonschema:"description=Source ref for create."`
}

type BranchActionResult struct {
	Op        string `json:"op"`
	ProjectID string `json:"project_id,omitempty"`
	Branch    string `json:"branch,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (p Plugin) branchOperationSpec() operation.Spec {
	return gitlabWriteSpec(
		p.operationName(branchOp),
		"Create or delete GitLab repository branches.",
		operation.RiskHigh,
		operationruntime.TypeOf[BranchActionInput](p.operationName(branchOp)+"_input"),
		operationruntime.TypeOf[BranchActionResult](p.operationName(branchOp)+"_output"),
	)
}

func (p Plugin) branchOperation() operation.Operation {
	return operationruntime.NewTypedResult[BranchActionInput, BranchActionResult](
		p.branchOperationSpec(),
		p.runBranchAction,
		operationruntime.WithAccess(p.branchAccess),
	)
}

func (p Plugin) runBranchAction(ctx operation.Context, req BranchActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.Branch = strings.TrimSpace(req.Branch)
	req.Ref = strings.TrimSpace(req.Ref)
	if req.ProjectID == "" {
		return operation.Failed("invalid_"+p.operationName(branchOp)+"_input", "project_id is required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(branchOp)+"_failed", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	base := BranchActionResult{Op: req.Op, ProjectID: projectIDLabel(project), Branch: req.Branch}
	switch req.Op {
	case "create":
		if req.Branch == "" || req.Ref == "" {
			return operation.Failed("invalid_"+p.operationName(branchOp)+"_input", "branch and ref are required for create", nil)
		}
		branch, err := client.CreateBranch(ctx, project, &gitlab.CreateBranchOptions{Branch: gitlab.Ptr(req.Branch), Ref: gitlab.Ptr(req.Ref)})
		if err != nil {
			return operation.Failed(p.operationName(branchOp)+"_failed", err.Error(), nil)
		}
		return operation.OK(branchResult(base, branch, "branch created"))
	case "delete":
		if req.Branch == "" {
			return operation.Failed("invalid_"+p.operationName(branchOp)+"_input", "branch is required for delete", nil)
		}
		if err := client.DeleteBranch(ctx, project, req.Branch); err != nil {
			return operation.Failed(p.operationName(branchOp)+"_failed", err.Error(), nil)
		}
		base.Message = "branch deleted"
		return operation.OK(base)
	case "delete_merged":
		if err := client.DeleteMergedBranches(ctx, project); err != nil {
			return operation.Failed(p.operationName(branchOp)+"_failed", err.Error(), nil)
		}
		base.Branch = ""
		base.Message = "merged branches deletion requested"
		return operation.OK(base)
	default:
		return operation.Failed("invalid_"+p.operationName(branchOp)+"_input", fmt.Sprintf("unsupported op %q", req.Op), nil)
	}
}

func branchResult(base BranchActionResult, branch *gitlab.Branch, message string) BranchActionResult {
	if branch != nil {
		base.Branch = branch.Name
		base.WebURL = branch.WebURL
	}
	base.Message = message
	return base
}

func (p Plugin) branchAccess(operation.Context, BranchActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
