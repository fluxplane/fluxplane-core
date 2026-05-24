package gitlab

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type PipelineActionInput struct {
	Op         string                     `json:"op" jsonschema:"description=Pipeline action.,enum=create,enum=retry,enum=cancel,required"`
	ProjectID  string                     `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	PipelineID int64                      `json:"pipeline_id,omitempty" jsonschema:"description=Pipeline id for retry or cancel."`
	Ref        string                     `json:"ref,omitempty" jsonschema:"description=Git ref for create."`
	Variables  []PipelineVariableArgument `json:"variables,omitempty" jsonschema:"description=Pipeline variables for create."`
}

type PipelineVariableArgument struct {
	Key          string `json:"key" jsonschema:"description=Variable key.,required"`
	Value        string `json:"value" jsonschema:"description=Variable value.,required"`
	VariableType string `json:"variable_type,omitempty" jsonschema:"description=Variable type.,enum=env_var,enum=file"`
}

type PipelineActionResult struct {
	Op         string `json:"op"`
	ProjectID  string `json:"project_id,omitempty"`
	PipelineID int64  `json:"pipeline_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Ref        string `json:"ref,omitempty"`
	SHA        string `json:"sha,omitempty"`
	WebURL     string `json:"web_url,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (p Plugin) pipelineOperationSpec() operation.Spec {
	return gitlabWriteSpecWithEffects(
		p.operationName(pipelineOp),
		"Create, retry, or cancel GitLab CI pipelines.",
		operation.RiskHigh,
		operation.EffectSet{
			operation.EffectNetwork,
			operation.EffectWriteExternal,
			operation.EffectCreate,
			operation.EffectUpdate,
		},
		operationruntime.TypeOf[PipelineActionInput](p.operationName(pipelineOp)+"_input"),
		operationruntime.TypeOf[PipelineActionResult](p.operationName(pipelineOp)+"_output"),
	)
}

func (p Plugin) pipelineOperation() operation.Operation {
	return operationruntime.NewTypedResult[PipelineActionInput, PipelineActionResult](
		p.pipelineOperationSpec(),
		p.runPipelineAction,
		operationruntime.WithAccess(p.pipelineAccess),
	)
}

func (p Plugin) runPipelineAction(ctx operation.Context, req PipelineActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.ProjectID == "" {
		return operation.Failed("invalid_"+p.operationName(pipelineOp)+"_input", "project_id is required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(pipelineOp)+"_failed", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	result, err := executePipelineAction(ctx, client, project, req)
	if err != nil {
		return operation.Failed(p.operationName(pipelineOp)+"_failed", err.Error(), nil)
	}
	return operation.OK(result)
}

func executePipelineAction(ctx operation.Context, client gitlabClient, project any, req PipelineActionInput) (PipelineActionResult, error) {
	base := PipelineActionResult{Op: req.Op, ProjectID: req.ProjectID, PipelineID: req.PipelineID}
	switch req.Op {
	case "create":
		if strings.TrimSpace(req.Ref) == "" {
			return PipelineActionResult{}, fmt.Errorf("ref is required for create")
		}
		opts := &gitlab.CreatePipelineOptions{Ref: gitlab.Ptr(strings.TrimSpace(req.Ref))}
		variables, err := pipelineVariables(req.Variables)
		if err != nil {
			return PipelineActionResult{}, err
		}
		if len(variables) > 0 {
			opts.Variables = &variables
		}
		pipeline, err := client.CreatePipeline(ctx, project, opts)
		if err != nil {
			return PipelineActionResult{}, err
		}
		return pipelineActionResult(base, pipelineFromFull(pipeline), "pipeline created"), nil
	case "retry":
		if req.PipelineID == 0 {
			return PipelineActionResult{}, fmt.Errorf("pipeline_id is required for retry")
		}
		pipeline, err := client.RetryPipelineBuild(ctx, project, req.PipelineID)
		if err != nil {
			return PipelineActionResult{}, err
		}
		return pipelineActionResult(base, pipelineFromFull(pipeline), "pipeline retried"), nil
	case "cancel":
		if req.PipelineID == 0 {
			return PipelineActionResult{}, fmt.Errorf("pipeline_id is required for cancel")
		}
		pipeline, err := client.CancelPipelineBuild(ctx, project, req.PipelineID)
		if err != nil {
			return PipelineActionResult{}, err
		}
		return pipelineActionResult(base, pipelineFromFull(pipeline), "pipeline canceled"), nil
	default:
		return PipelineActionResult{}, fmt.Errorf("unsupported op %q", req.Op)
	}
}

func pipelineVariables(values []PipelineVariableArgument) ([]*gitlab.PipelineVariableOptions, error) {
	out := make([]*gitlab.PipelineVariableOptions, 0, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value.Key)
		if key == "" {
			return nil, fmt.Errorf("variables key is required")
		}
		item := &gitlab.PipelineVariableOptions{
			Key:   gitlab.Ptr(key),
			Value: gitlab.Ptr(value.Value),
		}
		switch variableType := strings.TrimSpace(value.VariableType); variableType {
		case "":
		case "env_var", "file":
			typed := gitlab.VariableTypeValue(variableType)
			item.VariableType = &typed
		default:
			return nil, fmt.Errorf("invalid variable_type %q", variableType)
		}
		out = append(out, item)
	}
	return out, nil
}

func pipelineActionResult(base PipelineActionResult, pipeline Pipeline, message string) PipelineActionResult {
	if pipeline.ProjectID != 0 {
		base.ProjectID = strconv.FormatInt(pipeline.ProjectID, 10)
	}
	base.PipelineID = pipeline.ID
	base.Status = pipeline.Status
	base.Ref = pipeline.Ref
	base.SHA = pipeline.SHA
	base.WebURL = pipeline.WebURL
	base.Message = message
	return base
}

func (p Plugin) pipelineAccess(operation.Context, PipelineActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
