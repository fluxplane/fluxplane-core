// Package workflow runs core workflow specs through orchestration-provided
// dispatch callbacks.
package workflow

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	coreworkflow "github.com/fluxplane/engine/core/workflow"
)

// OperationRunner executes one operation workflow step.
type OperationRunner func(context.Context, coreworkflow.Step, operation.Value, operation.CallID) (operation.Result, error)

// AgentRunner executes one agent workflow step.
type AgentRunner func(context.Context, coreworkflow.Step, operation.Value) (operation.Value, error)

// Config wires a workflow run to concrete dispatchers.
type Config struct {
	Spec         coreworkflow.Spec
	RunID        coreworkflow.RunID
	Input        operation.Value
	Events       event.Sink
	RunOperation OperationRunner
	RunAgent     AgentRunner
}

// StepResult records one terminal workflow step outcome.
type StepResult struct {
	Status coreworkflow.Status `json:"status"`
	Output operation.Value     `json:"output,omitempty"`
	Error  *operation.Error    `json:"error,omitempty"`
}

// Result records the terminal workflow run outcome.
type Result struct {
	Status coreworkflow.Status                `json:"status"`
	Output operation.Value                    `json:"output,omitempty"`
	Error  *operation.Error                   `json:"error,omitempty"`
	Steps  map[coreworkflow.StepID]StepResult `json:"steps,omitempty"`
}

// Run executes cfg.Spec as a dependency-ordered workflow.
func Run(ctx context.Context, cfg Config) Result {
	ctx = ensureContext(ctx)
	events := cfg.Events
	if events == nil {
		events = event.Discard()
	}
	if cfg.RunID == "" {
		cfg.RunID = coreworkflow.RunID("workflow")
	}
	if err := cfg.Spec.Validate(); err != nil {
		return fail(events, cfg, operation.Failed("workflow_invalid", err.Error(), nil), nil)
	}
	events.Emit(coreworkflow.Queued{RunID: cfg.RunID, Workflow: cfg.Spec.Name, Input: cfg.Input})
	events.Emit(coreworkflow.Started{RunID: cfg.RunID, Workflow: cfg.Spec.Name})

	done := map[coreworkflow.StepID]bool{}
	results := make(map[coreworkflow.StepID]StepResult, len(cfg.Spec.Steps))
	for len(done) < len(cfg.Spec.Steps) {
		if err := ctx.Err(); err != nil {
			return cancel(events, cfg, operation.Canceled(err.Error()), results)
		}
		progress := false
		for _, step := range cfg.Spec.Steps {
			if done[step.ID] || !dependenciesDone(step, done) {
				continue
			}
			progress = true
			if !conditionMatches(step.When, results) {
				results[step.ID] = StepResult{Status: coreworkflow.StatusSucceeded}
				done[step.ID] = true
				continue
			}
			stepResult, terminal := runStep(ctx, events, cfg, step, results)
			results[step.ID] = stepResult
			done[step.ID] = true
			if terminal != nil {
				return fail(events, cfg, *terminal, results)
			}
		}
		if !progress {
			err := operation.Failed("workflow_stalled", "no runnable workflow steps remain", map[string]any{
				"workflow": string(cfg.Spec.Name),
			})
			return fail(events, cfg, err, results)
		}
	}
	output := workflowOutput(results)
	events.Emit(coreworkflow.Completed{RunID: cfg.RunID, Workflow: cfg.Spec.Name, Output: output})
	return Result{Status: coreworkflow.StatusSucceeded, Output: output, Steps: results}
}

func runStep(ctx context.Context, events event.Sink, cfg Config, step coreworkflow.Step, results map[coreworkflow.StepID]StepResult) (StepResult, *operation.Result) {
	kind := stepKind(step)
	input := stepInput(step, cfg.Input, results)
	events.Emit(coreworkflow.StepStarted{
		RunID:     cfg.RunID,
		Workflow:  cfg.Spec.Name,
		StepID:    step.ID,
		Kind:      kind,
		Operation: step.Operation,
		Agent:     step.Agent,
		Input:     input,
		Attempt:   1,
	})

	var (
		output operation.Value
		result operation.Result
		err    error
	)
	switch kind {
	case coreworkflow.StepOperation:
		if cfg.RunOperation == nil {
			result = operation.Failed("workflow_operation_runner_missing", "workflow operation runner is not configured", nil)
		} else {
			result, err = cfg.RunOperation(ctx, step, input, stepCallID(cfg.RunID, step.ID))
		}
		output = result.Output
	case coreworkflow.StepAgent:
		if cfg.RunAgent == nil {
			result = operation.Failed("workflow_agent_runner_missing", "workflow agent runner is not configured", nil)
		} else {
			output, err = cfg.RunAgent(ctx, step, input)
			result = operation.OK(output)
		}
	default:
		result = operation.Failed("workflow_step_kind_invalid", fmt.Sprintf("workflow step %q kind %q is invalid", step.ID, kind), nil)
	}
	if err != nil {
		result = operation.Failed("workflow_step_failed", err.Error(), map[string]any{"step_id": string(step.ID)})
	}
	if result.Status == "" {
		result.Status = operation.StatusOK
	}
	if result.IsError() {
		events.Emit(coreworkflow.StepFailed{
			RunID:     cfg.RunID,
			Workflow:  cfg.Spec.Name,
			StepID:    step.ID,
			Kind:      kind,
			Operation: step.Operation,
			Agent:     step.Agent,
			Error:     result.Error,
			Attempt:   1,
		})
		stepResult := StepResult{Status: coreworkflow.StatusFailed, Error: result.Error}
		if step.ErrorPolicy == coreworkflow.StepErrorContinue {
			return stepResult, nil
		}
		return stepResult, &result
	}
	events.Emit(coreworkflow.StepCompleted{
		RunID:     cfg.RunID,
		Workflow:  cfg.Spec.Name,
		StepID:    step.ID,
		Kind:      kind,
		Operation: step.Operation,
		Agent:     step.Agent,
		Output:    output,
		Attempt:   1,
	})
	return StepResult{Status: coreworkflow.StatusSucceeded, Output: output}, nil
}

func dependenciesDone(step coreworkflow.Step, done map[coreworkflow.StepID]bool) bool {
	for _, dep := range step.DependsOn {
		if !done[dep] {
			return false
		}
	}
	return true
}

func conditionMatches(condition coreworkflow.Condition, results map[coreworkflow.StepID]StepResult) bool {
	if condition.StepID == "" && condition.Equals == nil && !condition.Exists {
		return true
	}
	result, ok := results[condition.StepID]
	value := operation.Value(nil)
	if ok {
		value = stepOutputValue(result)
	}
	matches := true
	if condition.Exists {
		matches = ok && value != nil
	}
	if condition.Equals != nil {
		matches = ok && valuesEqual(value, condition.Equals)
	}
	if condition.Not {
		return !matches
	}
	return matches
}

func valuesEqual(got, want operation.Value) bool {
	gotString, gotOK := got.(string)
	wantString, wantOK := want.(string)
	if gotOK && wantOK {
		return strings.TrimSpace(gotString) == strings.TrimSpace(wantString)
	}
	return reflect.DeepEqual(got, want)
}

func stepKind(step coreworkflow.Step) coreworkflow.StepKind {
	if step.Kind != "" {
		return step.Kind
	}
	if step.Agent.Name != "" {
		return coreworkflow.StepAgent
	}
	return coreworkflow.StepOperation
}

func stepInput(step coreworkflow.Step, runInput operation.Value, results map[coreworkflow.StepID]StepResult) operation.Value {
	if len(step.InputMap) > 0 {
		out := map[string]operation.Value{}
		if step.Input != nil {
			if mapped, ok := step.Input.(map[string]any); ok {
				for key, value := range mapped {
					out[key] = value
				}
			} else {
				out["input"] = step.Input
			}
		}
		for key, ref := range step.InputMap {
			out[key] = mappedInputValue(ref, runInput, results)
		}
		return out
	}
	if step.Input != nil {
		return step.Input
	}
	return runInput
}

func mappedInputValue(ref string, runInput operation.Value, results map[coreworkflow.StepID]StepResult) operation.Value {
	ref = strings.TrimSpace(ref)
	switch ref {
	case "$input", "workflow.input":
		return runInput
	case "$steps":
		out := make(map[string]operation.Value, len(results))
		for stepID, result := range results {
			out[string(stepID)] = stepOutputValue(result)
		}
		return out
	}
	ref = strings.TrimPrefix(ref, "steps.")
	ref = strings.TrimSuffix(ref, ".output")
	if result, ok := results[coreworkflow.StepID(ref)]; ok {
		return stepOutputValue(result)
	}
	return nil
}

func stepOutputValue(result StepResult) operation.Value {
	if result.Error != nil {
		return map[string]any{"error": result.Error}
	}
	return result.Output
}

func stepCallID(runID coreworkflow.RunID, stepID coreworkflow.StepID) operation.CallID {
	return operation.CallID(strings.Trim(string(runID)+":workflow:"+string(stepID), ":"))
}

func workflowOutput(results map[coreworkflow.StepID]StepResult) operation.Value {
	out := make(map[string]operation.Value, len(results))
	for stepID, result := range results {
		if result.Error != nil {
			out[string(stepID)] = map[string]any{"error": result.Error}
			continue
		}
		out[string(stepID)] = result.Output
	}
	return out
}

func fail(events event.Sink, cfg Config, result operation.Result, steps map[coreworkflow.StepID]StepResult) Result {
	events.Emit(coreworkflow.Failed{RunID: cfg.RunID, Workflow: cfg.Spec.Name, Error: result.Error})
	return Result{Status: coreworkflow.StatusFailed, Error: result.Error, Steps: steps}
}

func cancel(events event.Sink, cfg Config, result operation.Result, steps map[coreworkflow.StepID]StepResult) Result {
	events.Emit(coreworkflow.Canceled{RunID: cfg.RunID, Workflow: cfg.Spec.Name, Error: result.Error})
	return Result{Status: coreworkflow.StatusCanceled, Error: result.Error, Steps: steps}
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
