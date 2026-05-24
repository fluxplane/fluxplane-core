// Package sessionworkflow executes workflow-targeted session commands.
package sessionworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	coreworkflow "github.com/fluxplane/fluxplane-core/core/workflow"
	"github.com/fluxplane/fluxplane-core/orchestration/resourcecatalog"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionagent"
	"github.com/fluxplane/fluxplane-core/orchestration/sessioncontrol"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	workflowruntime "github.com/fluxplane/fluxplane-core/orchestration/workflow"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
)

// WorkflowCatalog binds canonical workflow resource IDs to workflow specs.
type WorkflowCatalog = resourcecatalog.WorkflowCatalog

// Status classifies workflow command execution.
type Status string

const (
	StatusOK     Status = "ok"
	StatusFailed Status = "failed"
)

// Error describes a workflow command failure.
type Error struct {
	Code    string
	Message string
	Details map[string]any
}

// Result is the normalized workflow command result.
type Result struct {
	Status Status
	Output any
	Error  *Error
}

// Config wires workflow command execution to the parent session.
type Config struct {
	WorkflowCatalog   WorkflowCatalog
	Resolver          *sessioncontrol.Resolver
	OperationExecutor operationruntime.Executor
	SessionAgents     *sessionagent.Runner
	Delegation        coresession.DelegationPolicy
	Thread            corethread.Ref
	Events            event.Sink
	AppendEvents      func(context.Context, ...event.Event) error
	EmitLive          func(event.Event)
	ResolveOperation  func(ref string, scope sessioncontrol.ResourceID) (operation.Operation, error)
}

// Execute runs a workflow-targeted command.
func Execute(ctx context.Context, cfg Config, runID string, commandInput any, targetID sessioncontrol.ResourceID, spec command.Spec) Result {
	workflowBinding, err := resolveWorkflowBinding(cfg, targetID, spec)
	if err != nil {
		return Result{
			Status: StatusFailed,
			Error: &Error{
				Code:    "workflow_resolution_failed",
				Message: err.Error(),
				Details: map[string]any{
					"workflow": string(spec.Target.Workflow),
				},
			},
		}
	}
	result := workflowruntime.Run(ctx, workflowruntime.Config{
		Spec:   workflowBinding.Spec,
		RunID:  coreworkflow.RunID(runID),
		Input:  firstNonNil(commandInput, spec.Target.Input),
		Events: eventSink(cfg.Events),
		RunOperation: func(ctx context.Context, step coreworkflow.Step, input operation.Value, callID operation.CallID) (operation.Result, error) {
			return runOperationStep(ctx, cfg, runID, workflowBinding.ID, step, input, callID)
		},
		RunAgent: func(ctx context.Context, step coreworkflow.Step, input operation.Value) (operation.Value, error) {
			return runAgentStep(ctx, cfg, runID, step, input)
		},
	})
	return workflowResult(workflowBinding.Spec, result)
}

func resolveWorkflowBinding(cfg Config, targetID sessioncontrol.ResourceID, spec command.Spec) (resourcecatalog.Binding[coreworkflow.Spec], error) {
	if !targetID.IsZero() {
		if workflowBinding, ok := cfg.WorkflowCatalog[targetID.Address()]; ok {
			return workflowBinding, nil
		}
		return resourcecatalog.Binding[coreworkflow.Spec]{}, fmt.Errorf("workflow %q is not bound", targetID.Address())
	}
	if cfg.Resolver == nil {
		return resourcecatalog.Binding[coreworkflow.Spec]{}, fmt.Errorf("workflow resolver is nil")
	}
	id, err := cfg.Resolver.Resolve("workflow", string(spec.Target.Workflow))
	if err != nil {
		return resourcecatalog.Binding[coreworkflow.Spec]{}, err
	}
	workflowBinding, ok := cfg.WorkflowCatalog[id.Address()]
	if !ok {
		return resourcecatalog.Binding[coreworkflow.Spec]{}, fmt.Errorf("workflow %q is not bound", id.Address())
	}
	return workflowBinding, nil
}

func workflowResult(spec coreworkflow.Spec, result workflowruntime.Result) Result {
	if result.Status == coreworkflow.StatusSucceeded {
		return Result{Status: StatusOK, Output: result.Output}
	}
	message := "workflow failed"
	code := "workflow_failed"
	if result.Status == coreworkflow.StatusCanceled {
		message = "workflow canceled"
		code = "workflow_canceled"
	}
	if result.Error != nil {
		message = result.Error.Message
		if result.Error.Code != "" {
			code = result.Error.Code
		}
	}
	return Result{
		Status: StatusFailed,
		Output: result.Output,
		Error: &Error{
			Code:    code,
			Message: message,
			Details: map[string]any{
				"workflow": string(spec.Name),
			},
		},
	}
}

func runOperationStep(ctx context.Context, cfg Config, runID string, workflowID sessioncontrol.ResourceID, step coreworkflow.Step, input operation.Value, callID operation.CallID) (operation.Result, error) {
	if cfg.ResolveOperation == nil {
		return operation.Result{}, fmt.Errorf("workflow operation resolver is nil")
	}
	op, err := cfg.ResolveOperation(step.Operation.String(), workflowID)
	if err != nil {
		return operation.Result{}, err
	}
	requested := coresession.OperationRequested{
		RunID:     runID,
		CallID:    callID,
		Operation: step.Operation,
		Input:     input,
	}
	if err := appendEvents(cfg, ctx, requested); err != nil {
		return operation.Result{}, err
	}
	emitLive(cfg, requested)
	result := cfg.OperationExecutor.Execute(operation.NewContext(ensureContext(ctx), eventSink(cfg.Events)), op, input)
	completed := coresession.OperationCompleted{
		RunID:     runID,
		CallID:    callID,
		Operation: step.Operation,
		Result:    result,
	}
	if err := appendEvents(cfg, ctx, completed); err != nil {
		return operation.Result{}, err
	}
	emitLive(cfg, completed)
	return result, nil
}

func runAgentStep(ctx context.Context, cfg Config, runID string, step coreworkflow.Step, input operation.Value) (operation.Value, error) {
	if cfg.SessionAgents == nil {
		return nil, fmt.Errorf("workflow agent step %q requires a session-agent runner", step.ID)
	}
	result, err := cfg.SessionAgents.Run(ctx, sessionagent.Request{
		ID:             sessionagent.ID(string(runID) + ":" + string(step.ID)),
		Agent:          step.Agent,
		Task:           agentTask(step, input),
		TaskID:         string(step.ID),
		Policy:         cfg.Delegation,
		ParentThreadID: cfg.Thread.ID,
		ParentRunID:    runID,
		ParentCallID:   operation.CallID(string(runID) + ":workflow:" + string(step.ID)),
		Events:         eventSink(cfg.Events),
		Approver:       sessionenv.ApproverFromExecutor(cfg.OperationExecutor),
	})
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

func agentTask(step coreworkflow.Step, input operation.Value) string {
	if text, ok := input.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	if input != nil {
		if data, err := json.MarshalIndent(input, "", "  "); err == nil && len(data) > 0 {
			if step.ID != "" {
				return "Run workflow step " + string(step.ID) + " with this input:\n" + string(data)
			}
			return "Run workflow step with this input:\n" + string(data)
		}
		return fmt.Sprintf("%v", input)
	}
	if step.ID != "" {
		return "Run workflow step " + string(step.ID)
	}
	return "Run workflow step"
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func appendEvents(cfg Config, ctx context.Context, events ...event.Event) error {
	if cfg.AppendEvents == nil {
		return nil
	}
	return cfg.AppendEvents(ctx, events...)
}

func emitLive(cfg Config, payload event.Event) {
	if cfg.EmitLive != nil {
		cfg.EmitLive(payload)
	}
}

func eventSink(sink event.Sink) event.Sink {
	if sink == nil {
		return event.Discard()
	}
	return sink
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
