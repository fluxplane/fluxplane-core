package connectorplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	connectoroperation "github.com/codewandler/connectors/operation"
	"github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Executor is the connector runtime boundary used by connector-backed tools.
type Executor interface {
	ExecWithInstance(ctx context.Context, instanceID, opName, role string, params map[string]any) (connectoroperation.Result, error)
}

// Instance is one configured connector instance available to plugin actions.
type Instance struct {
	ID   string
	Kind string
}

// Action describes one connector operation exposed as an agent tool.
type Action struct {
	Kind        string
	Operation   string
	Role        string
	Suffix      string
	Description string
	Spec        func(name string) operation.Spec
}

// Output is the stable result envelope returned from connector-backed tools.
type Output struct {
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Data       any    `json:"data,omitempty"`
}

// Specs materializes inert operation specs for all matching instances/actions.
func Specs(instances []Instance, actions []Action) ([]operation.Spec, error) {
	var specs []operation.Spec
	for _, materialized := range materialize(instances, actions) {
		spec := materialized.action.operationSpec(materialized.toolName)
		specs = append(specs, spec)
	}
	return specs, nil
}

// Operations materializes executable operations for all matching instances/actions.
func Operations(executor Executor, instances []Instance, actions []Action) ([]operation.Operation, error) {
	if executor == nil {
		return nil, nil
	}
	var out []operation.Operation
	for _, materialized := range materialize(instances, actions) {
		instance := materialized.instance
		action := materialized.action
		toolName := materialized.toolName
		spec := action.operationSpec(toolName)
		out = append(out, operation.New(spec, func(ctx operation.Context, input operation.Value) operation.Result {
			params, err := inputMap(input)
			if err != nil {
				return operation.Failed("invalid_"+toolName+"_input", err.Error(), nil)
			}
			result, err := executor.ExecWithInstance(ctx, instance.ID, action.Operation, action.Role, params)
			if err != nil {
				return operation.Failed(toolName+"_failed", err.Error(), map[string]any{
					"instance":  instance.ID,
					"operation": action.Operation,
				})
			}
			if result.Status != connectoroperation.StatusOK {
				details := map[string]any{
					"instance":    instance.ID,
					"operation":   action.Operation,
					"status":      string(result.Status),
					"http_status": result.HTTPStatus,
					"data":        result.Data,
				}
				message := string(result.Status)
				if result.Error != nil {
					message = result.Error.Error()
				}
				return operation.Failed(toolName+"_failed", message, details)
			}
			return operation.OK(Output{
				Status:     string(result.Status),
				HTTPStatus: result.HTTPStatus,
				Data:       result.Data,
			})
		}))
	}
	return out, nil
}

// ToolName returns the materialized tool name for one instance/action pair.
func ToolName(instance Instance, action Action) string {
	prefix := Normalize(instance.ID)
	if prefix == "" {
		prefix = Normalize(action.Kind)
	}
	suffix := Normalize(action.Suffix)
	if suffix == "" {
		suffix = Normalize(strings.TrimPrefix(action.Operation, action.Kind+"."))
	}
	if suffix == "" {
		return prefix
	}
	return prefix + "_" + suffix
}

// Normalize converts connector instance IDs and operation suffixes to tool-safe names.
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = toolNamePattern.ReplaceAllString(value, "_")
	return strings.Trim(value, "_")
}

var toolNamePattern = regexp.MustCompile(`[^a-z0-9]+`)

type materializedAction struct {
	instance Instance
	action   Action
	toolName string
}

func materialize(instances []Instance, actions []Action) []materializedAction {
	var out []materializedAction
	for _, instance := range instances {
		if strings.TrimSpace(instance.ID) == "" || strings.TrimSpace(instance.Kind) == "" {
			continue
		}
		for _, action := range actions {
			if strings.TrimSpace(action.Kind) == "" || strings.TrimSpace(action.Operation) == "" {
				continue
			}
			if action.Kind != instance.Kind {
				continue
			}
			toolName := ToolName(instance, action)
			if toolName == "" {
				continue
			}
			out = append(out, materializedAction{instance: instance, action: action, toolName: toolName})
		}
	}
	return out
}

func (a Action) operationSpec(name string) operation.Spec {
	if a.Spec != nil {
		return a.Spec(name)
	}
	return operationruntime.WithTypedContract[map[string]any, Output](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: a.Description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskLow,
		},
	})
}

func inputMap(input operation.Value) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	if params, ok := input.(map[string]any); ok {
		out := make(map[string]any, len(params))
		for k, v := range params {
			out[k] = v
		}
		return out, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode input: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
