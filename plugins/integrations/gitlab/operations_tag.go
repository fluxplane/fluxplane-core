package gitlab

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type TagActionInput struct {
	Op        string `json:"op" jsonschema:"description=Tag action.,enum=create,enum=delete,required"`
	ProjectID string `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	TagName   string `json:"tag_name" jsonschema:"description=Tag name.,required"`
	Ref       string `json:"ref,omitempty" jsonschema:"description=Source ref for create."`
	Message   string `json:"message,omitempty" jsonschema:"description=Optional annotated tag message."`
}

type TagActionResult struct {
	Op        string `json:"op"`
	ProjectID string `json:"project_id,omitempty"`
	TagName   string `json:"tag_name,omitempty"`
	Target    string `json:"target,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (p Plugin) tagOperationSpec() operation.Spec {
	return gitlabWriteSpec(
		p.operationName(tagOp),
		"Create or delete GitLab repository tags.",
		operation.RiskHigh,
		operationruntime.TypeOf[TagActionInput](p.operationName(tagOp)+"_input"),
		operationruntime.TypeOf[TagActionResult](p.operationName(tagOp)+"_output"),
	)
}

func (p Plugin) tagOperation() operation.Operation {
	return operationruntime.NewTypedResult[TagActionInput, TagActionResult](
		p.tagOperationSpec(),
		p.runTagAction,
		operationruntime.WithAccess(p.tagAccess),
	)
}

func (p Plugin) runTagAction(ctx operation.Context, req TagActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.TagName = strings.TrimSpace(req.TagName)
	req.Ref = strings.TrimSpace(req.Ref)
	if req.ProjectID == "" || req.TagName == "" {
		return operation.Failed("invalid_"+p.operationName(tagOp)+"_input", "project_id and tag_name are required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(tagOp)+"_failed", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	base := TagActionResult{Op: req.Op, ProjectID: projectIDLabel(project), TagName: req.TagName}
	switch req.Op {
	case "create":
		if req.Ref == "" {
			return operation.Failed("invalid_"+p.operationName(tagOp)+"_input", "ref is required for create", nil)
		}
		opts := &gitlab.CreateTagOptions{TagName: gitlab.Ptr(req.TagName), Ref: gitlab.Ptr(req.Ref)}
		if value := strings.TrimSpace(req.Message); value != "" {
			opts.Message = gitlab.Ptr(value)
		}
		tag, err := client.CreateTag(ctx, project, opts)
		if err != nil {
			return operation.Failed(p.operationName(tagOp)+"_failed", err.Error(), nil)
		}
		return operation.OK(tagResult(base, tag, "tag created"))
	case "delete":
		if err := client.DeleteTag(ctx, project, req.TagName); err != nil {
			return operation.Failed(p.operationName(tagOp)+"_failed", err.Error(), nil)
		}
		base.Message = "tag deleted"
		return operation.OK(base)
	default:
		return operation.Failed("invalid_"+p.operationName(tagOp)+"_input", fmt.Sprintf("unsupported op %q", req.Op), nil)
	}
}

func tagResult(base TagActionResult, tag *gitlab.Tag, message string) TagActionResult {
	if tag != nil {
		base.TagName = tag.Name
		base.Target = tag.Target
	}
	base.Message = message
	return base
}

func (p Plugin) tagAccess(operation.Context, TagActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
