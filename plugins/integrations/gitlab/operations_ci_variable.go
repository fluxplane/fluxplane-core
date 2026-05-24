package gitlab

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type CIVariableActionInput struct {
	Op               string `json:"op" jsonschema:"description=CI variable action.,enum=create,enum=update,enum=delete,required"`
	ProjectID        string `json:"project_id" jsonschema:"description=Numeric project id or path-with-namespace.,required"`
	Key              string `json:"key" jsonschema:"description=Variable key.,required"`
	Value            string `json:"value,omitempty" jsonschema:"description=Variable value for create or update."`
	Description      string `json:"description,omitempty" jsonschema:"description=Variable description."`
	EnvironmentScope string `json:"environment_scope,omitempty" jsonschema:"description=Environment scope."`
	Masked           *bool  `json:"masked,omitempty" jsonschema:"description=Whether the variable should be masked."`
	MaskedAndHidden  *bool  `json:"masked_and_hidden,omitempty" jsonschema:"description=Whether the variable should be masked and hidden for create."`
	Protected        *bool  `json:"protected,omitempty" jsonschema:"description=Whether the variable is protected."`
	Raw              *bool  `json:"raw,omitempty" jsonschema:"description=Whether the variable is raw."`
	VariableType     string `json:"variable_type,omitempty" jsonschema:"description=Variable type.,enum=env_var,enum=file"`
}

type CIVariableActionResult struct {
	Op               string `json:"op"`
	ProjectID        string `json:"project_id,omitempty"`
	Key              string `json:"key,omitempty"`
	VariableType     string `json:"variable_type,omitempty"`
	EnvironmentScope string `json:"environment_scope,omitempty"`
	Masked           bool   `json:"masked,omitempty"`
	Protected        bool   `json:"protected,omitempty"`
	Raw              bool   `json:"raw,omitempty"`
	Message          string `json:"message,omitempty"`
}

func (p Plugin) ciVariableOperationSpec() operation.Spec {
	return gitlabWriteSpec(
		p.operationName(ciVariableOp),
		"Create, update, or delete GitLab project CI/CD variables.",
		operation.RiskCritical,
		operationruntime.TypeOf[CIVariableActionInput](p.operationName(ciVariableOp)+"_input"),
		operationruntime.TypeOf[CIVariableActionResult](p.operationName(ciVariableOp)+"_output"),
	)
}

func (p Plugin) ciVariableOperation() operation.Operation {
	return operationruntime.NewTypedResult[CIVariableActionInput, CIVariableActionResult](
		p.ciVariableOperationSpec(),
		p.runCIVariableAction,
		operationruntime.WithAccess(p.ciVariableAccess),
	)
}

func (p Plugin) runCIVariableAction(ctx operation.Context, req CIVariableActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.Key = strings.TrimSpace(req.Key)
	if req.ProjectID == "" || req.Key == "" {
		return operation.Failed("invalid_"+p.operationName(ciVariableOp)+"_input", "project_id and key are required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(ciVariableOp)+"_failed", err.Error(), nil)
	}
	project := projectID(req.ProjectID)
	base := CIVariableActionResult{Op: req.Op, ProjectID: projectIDLabel(project), Key: req.Key}
	switch req.Op {
	case "create":
		if req.Value == "" {
			return operation.Failed("invalid_"+p.operationName(ciVariableOp)+"_input", "value is required for create", nil)
		}
		variable, err := client.CreateVariable(ctx, project, createVariableOptions(req))
		if err != nil {
			return operation.Failed(p.operationName(ciVariableOp)+"_failed", err.Error(), nil)
		}
		return operation.OK(ciVariableResult(base, variable, "ci variable created"))
	case "update":
		if req.Value == "" {
			return operation.Failed("invalid_"+p.operationName(ciVariableOp)+"_input", "value is required for update", nil)
		}
		variable, err := client.UpdateVariable(ctx, project, req.Key, updateVariableOptions(req))
		if err != nil {
			return operation.Failed(p.operationName(ciVariableOp)+"_failed", err.Error(), nil)
		}
		return operation.OK(ciVariableResult(base, variable, "ci variable updated"))
	case "delete":
		if err := client.RemoveVariable(ctx, project, req.Key, removeVariableOptions(req)); err != nil {
			return operation.Failed(p.operationName(ciVariableOp)+"_failed", err.Error(), nil)
		}
		base.EnvironmentScope = strings.TrimSpace(req.EnvironmentScope)
		base.Message = "ci variable deleted"
		return operation.OK(base)
	default:
		return operation.Failed("invalid_"+p.operationName(ciVariableOp)+"_input", fmt.Sprintf("unsupported op %q", req.Op), nil)
	}
}

func createVariableOptions(req CIVariableActionInput) *gitlab.CreateProjectVariableOptions {
	opts := &gitlab.CreateProjectVariableOptions{
		Key:             gitlab.Ptr(req.Key),
		Value:           gitlab.Ptr(req.Value),
		Masked:          boolPtr(req.Masked),
		MaskedAndHidden: boolPtr(req.MaskedAndHidden),
		Protected:       boolPtr(req.Protected),
		Raw:             boolPtr(req.Raw),
	}
	if value := strings.TrimSpace(req.Description); value != "" {
		opts.Description = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.EnvironmentScope); value != "" {
		opts.EnvironmentScope = gitlab.Ptr(value)
	}
	if typ := variableType(req.VariableType); typ != "" {
		opts.VariableType = &typ
	}
	return opts
}

func updateVariableOptions(req CIVariableActionInput) *gitlab.UpdateProjectVariableOptions {
	opts := &gitlab.UpdateProjectVariableOptions{
		Value:     gitlab.Ptr(req.Value),
		Masked:    boolPtr(req.Masked),
		Protected: boolPtr(req.Protected),
		Raw:       boolPtr(req.Raw),
		Filter:    variableFilter(req.EnvironmentScope),
	}
	if value := strings.TrimSpace(req.Description); value != "" {
		opts.Description = gitlab.Ptr(value)
	}
	if value := strings.TrimSpace(req.EnvironmentScope); value != "" {
		opts.EnvironmentScope = gitlab.Ptr(value)
	}
	if typ := variableType(req.VariableType); typ != "" {
		opts.VariableType = &typ
	}
	return opts
}

func removeVariableOptions(req CIVariableActionInput) *gitlab.RemoveProjectVariableOptions {
	return &gitlab.RemoveProjectVariableOptions{Filter: variableFilter(req.EnvironmentScope)}
}

func variableFilter(scope string) *gitlab.VariableFilter {
	if scope = strings.TrimSpace(scope); scope == "" {
		return nil
	}
	return &gitlab.VariableFilter{EnvironmentScope: scope}
}

func variableType(value string) gitlab.VariableTypeValue {
	switch strings.TrimSpace(value) {
	case string(gitlab.FileVariableType):
		return gitlab.FileVariableType
	case string(gitlab.EnvVariableType):
		return gitlab.EnvVariableType
	default:
		return ""
	}
}

func ciVariableResult(base CIVariableActionResult, variable *gitlab.ProjectVariable, message string) CIVariableActionResult {
	if variable != nil {
		base.Key = variable.Key
		base.VariableType = string(variable.VariableType)
		base.EnvironmentScope = variable.EnvironmentScope
		base.Masked = variable.Masked
		base.Protected = variable.Protected
		base.Raw = variable.Raw
	}
	base.Message = message
	return base
}

func (p Plugin) ciVariableAccess(operation.Context, CIVariableActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
