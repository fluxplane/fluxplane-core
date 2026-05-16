// Package task projects core task events and provides task execution helpers.
package task

import (
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coretask "github.com/fluxplane/agentruntime/core/task"
)

// State is the projected user-facing task plus its execution attempts.
type State struct {
	Task             coretask.Task                               `json:"task,omitempty"`
	CurrentExecution coretask.ExecutionID                        `json:"current_execution,omitempty"`
	Executions       map[coretask.ExecutionID]coretask.Execution `json:"executions,omitempty"`
}

// Project applies event records in order and returns the resulting task state.
func Project(records []event.Record) State {
	var state State
	for _, record := range records {
		state = Apply(state, record.Payload, record.Time)
	}
	return state
}

// Apply applies one task event payload to state.
func Apply(state State, payload event.Event, at time.Time) State {
	state = cloneState(state)
	switch evt := payload.(type) {
	case coretask.Created:
		state.Task = cloneTask(evt.Task)
		if state.Task.ID == "" {
			state.Task.ID = evt.TaskID
		}
		if state.Task.Status == "" {
			state.Task.Status = coretask.StatusDraft
		}
		if state.Task.CreatedAt.IsZero() {
			state.Task.CreatedAt = at
		}
		state.Task.UpdatedAt = eventTime(at, state.Task.UpdatedAt)
	case coretask.Revised:
		next := cloneTask(evt.Task)
		if next.ID == "" {
			next.ID = evt.TaskID
		}
		if next.Status == "" {
			next.Status = state.Task.Status
		}
		if next.CreatedAt.IsZero() {
			next.CreatedAt = state.Task.CreatedAt
		}
		next.UpdatedAt = eventTime(at, next.UpdatedAt)
		state.Task = next
		state.reconcileExecutions()
	case coretask.StatusChanged:
		state.Task.ID = firstTaskID(state.Task.ID, evt.TaskID)
		state.Task.Status = evt.Current
		state.Task.UpdatedAt = eventTime(at, state.Task.UpdatedAt)
	case coretask.ExecutionStarted:
		executionID := evt.ExecutionID
		if executionID == "" {
			executionID = evt.Execution.ID
		}
		if executionID == "" {
			return state
		}
		exec := cloneExecution(evt.Execution)
		exec.ID = executionID
		if exec.TaskID == "" {
			exec.TaskID = evt.TaskID
		}
		if exec.Status == "" {
			exec.Status = coretask.StatusRunning
		}
		if exec.StartedAt.IsZero() {
			exec.StartedAt = at
		}
		exec.Steps = ensureExecutionSteps(state.Task.Steps, exec.Steps)
		state.Task.ID = firstTaskID(state.Task.ID, evt.TaskID)
		state.Task.Status = coretask.StatusRunning
		state.CurrentExecution = exec.ID
		state.setExecution(exec)
	case coretask.ExecutionInterrupted:
		exec := state.execution(evt.ExecutionID)
		exec.ID = evt.ExecutionID
		exec.TaskID = evt.TaskID
		exec.Status = coretask.StatusInterrupted
		exec.Error = &operation.Error{Code: "task_execution_interrupted", Message: evt.Reason}
		state.Task.Status = coretask.StatusInterrupted
		state.CurrentExecution = evt.ExecutionID
		state.setExecution(exec)
	case coretask.StepDispatched:
		exec := state.execution(evt.ExecutionID)
		step := exec.Steps[evt.StepID]
		step.StepID = evt.StepID
		step.Status = coretask.StepStatusRunning
		step.Assignee = evt.Assignee
		step.Profile = evt.Profile
		step.ExternalID = evt.ExternalID
		step.StartedAt = eventTime(at, step.StartedAt)
		step.UpdatedAt = eventTime(at, step.UpdatedAt)
		exec.Steps[evt.StepID] = step
		exec.Status = coretask.StatusRunning
		state.CurrentExecution = evt.ExecutionID
		state.setExecution(exec)
	case coretask.StepProgressed:
		exec := state.execution(evt.ExecutionID)
		step := exec.Steps[evt.StepID]
		step.StepID = evt.StepID
		if step.Status == "" {
			step.Status = coretask.StepStatusRunning
		}
		step.LastProgress = evt.Message
		step.UpdatedAt = eventTime(at, step.UpdatedAt)
		exec.Steps[evt.StepID] = step
		state.CurrentExecution = evt.ExecutionID
		state.setExecution(exec)
	case coretask.StepCompleted:
		state = applyStepTerminal(state, evt.ExecutionID, evt.StepID, coretask.StepStatusCompleted, evt.Output, nil, at)
	case coretask.StepFailed:
		state = applyStepTerminal(state, evt.ExecutionID, evt.StepID, coretask.StepStatusFailed, nil, evt.Error, at)
	case coretask.StepCancelled:
		err := &operation.Error{Code: "task_step_cancelled", Message: evt.Reason}
		state = applyStepTerminal(state, evt.ExecutionID, evt.StepID, coretask.StepStatusCancelled, nil, err, at)
	case coretask.ExecutionCompleted:
		state = applyExecutionTerminal(state, evt.ExecutionID, coretask.StatusCompleted, evt.Output, nil, at)
	case coretask.ExecutionFailed:
		state = applyExecutionTerminal(state, evt.ExecutionID, coretask.StatusFailed, nil, evt.Error, at)
	case coretask.ExecutionCancelled:
		err := &operation.Error{Code: "task_execution_cancelled", Message: evt.Reason}
		state = applyExecutionTerminal(state, evt.ExecutionID, coretask.StatusCancelled, nil, err, at)
	}
	return state
}

// ReadySteps returns task steps whose dependencies are completed and whose
// execution status is waiting or empty.
func ReadySteps(state State) []coretask.Step {
	exec, ok := state.Executions[state.CurrentExecution]
	if !ok {
		return nil
	}
	var ready []coretask.Step
	for _, step := range state.Task.Steps {
		status := exec.Steps[step.ID].Status
		if status != "" && status != coretask.StepStatusWaiting {
			continue
		}
		if dependenciesCompleted(step, exec) {
			ready = append(ready, step)
		}
	}
	return ready
}

// AllStepsTerminal reports whether the current execution has no runnable or
// running steps left.
func AllStepsTerminal(state State) bool {
	exec, ok := state.Executions[state.CurrentExecution]
	if !ok || len(state.Task.Steps) == 0 {
		return false
	}
	for _, step := range state.Task.Steps {
		if !coretask.StepTerminal(exec.Steps[step.ID].Status) {
			return false
		}
	}
	return true
}

// FailedStepIDs returns failed current-execution step ids.
func FailedStepIDs(state State) []coretask.StepID {
	exec, ok := state.Executions[state.CurrentExecution]
	if !ok {
		return nil
	}
	var failed []coretask.StepID
	for _, step := range state.Task.Steps {
		if exec.Steps[step.ID].Status == coretask.StepStatusFailed {
			failed = append(failed, step.ID)
		}
	}
	return failed
}

// CancelWaitingDependents marks waiting dependents of failed steps cancelled.
func CancelWaitingDependents(state State, reason string, at time.Time) State {
	state = cloneState(state)
	exec, ok := state.Executions[state.CurrentExecution]
	if !ok {
		return state
	}
	failed := map[coretask.StepID]bool{}
	for id, step := range exec.Steps {
		if step.Status == coretask.StepStatusFailed {
			failed[id] = true
		}
	}
	for {
		changed := false
		for _, step := range state.Task.Steps {
			current := exec.Steps[step.ID]
			if current.Status != coretask.StepStatusWaiting && current.Status != "" {
				continue
			}
			for _, dep := range step.DependsOn {
				if !failed[dep] {
					continue
				}
				current.StepID = step.ID
				current.Status = coretask.StepStatusCancelled
				current.Error = &operation.Error{Code: "task_step_cancelled", Message: firstNonEmpty(reason, "dependency failed: "+string(dep))}
				current.CompletedAt = eventTime(at, current.CompletedAt)
				exec.Steps[step.ID] = current
				failed[step.ID] = true
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	state.Executions[state.CurrentExecution] = exec
	return state
}

// MarkInterrupted marks a running execution interrupted when its external
// runner is no longer active.
func MarkInterrupted(state State, reason string, at time.Time) State {
	state = cloneState(state)
	exec, ok := state.Executions[state.CurrentExecution]
	if !ok || exec.Status != coretask.StatusRunning {
		return state
	}
	exec.Status = coretask.StatusInterrupted
	exec.Error = &operation.Error{Code: "task_execution_interrupted", Message: reason}
	state.Task.Status = coretask.StatusInterrupted
	state.Task.UpdatedAt = eventTime(at, state.Task.UpdatedAt)
	state.Executions[state.CurrentExecution] = exec
	return state
}

func applyStepTerminal(state State, executionID coretask.ExecutionID, stepID coretask.StepID, status coretask.StepStatus, output operation.Value, err *operation.Error, at time.Time) State {
	exec := state.execution(executionID)
	step := exec.Steps[stepID]
	step.StepID = stepID
	step.Status = status
	step.Output = output
	step.Error = err
	step.UpdatedAt = eventTime(at, step.UpdatedAt)
	step.CompletedAt = eventTime(at, step.CompletedAt)
	exec.Steps[stepID] = step
	state.CurrentExecution = executionID
	state.setExecution(exec)
	return state
}

func applyExecutionTerminal(state State, executionID coretask.ExecutionID, status coretask.Status, output operation.Value, err *operation.Error, at time.Time) State {
	exec := state.execution(executionID)
	exec.Status = status
	exec.Output = output
	exec.Error = err
	exec.CompletedAt = eventTime(at, exec.CompletedAt)
	state.Task.Status = status
	state.Task.UpdatedAt = eventTime(at, state.Task.UpdatedAt)
	state.CurrentExecution = executionID
	state.setExecution(exec)
	return state
}

func dependenciesCompleted(step coretask.Step, exec coretask.Execution) bool {
	for _, dep := range step.DependsOn {
		if exec.Steps[dep].Status != coretask.StepStatusCompleted {
			return false
		}
	}
	return true
}

func ensureExecutionSteps(steps []coretask.Step, existing map[coretask.StepID]coretask.StepExecution) map[coretask.StepID]coretask.StepExecution {
	out := cloneStepExecutions(existing)
	if out == nil {
		out = map[coretask.StepID]coretask.StepExecution{}
	}
	for _, step := range steps {
		exec := out[step.ID]
		if exec.StepID == "" {
			exec.StepID = step.ID
		}
		if exec.Status == "" {
			exec.Status = coretask.StepStatusWaiting
		}
		if exec.Assignee == "" {
			exec.Assignee = step.Assignee
		}
		if exec.Profile == "" {
			exec.Profile = step.Profile
		}
		out[step.ID] = exec
	}
	return out
}

func reconcileExecutionSteps(steps []coretask.Step, existing map[coretask.StepID]coretask.StepExecution) map[coretask.StepID]coretask.StepExecution {
	out := map[coretask.StepID]coretask.StepExecution{}
	for _, step := range steps {
		exec := existing[step.ID]
		if exec.StepID == "" {
			exec.StepID = step.ID
		}
		if exec.Status == "" {
			exec.Status = coretask.StepStatusWaiting
		}
		if exec.Assignee == "" {
			exec.Assignee = step.Assignee
		}
		if exec.Profile == "" {
			exec.Profile = step.Profile
		}
		out[step.ID] = exec
	}
	return out
}

func (s *State) setExecution(exec coretask.Execution) {
	if s.Executions == nil {
		s.Executions = map[coretask.ExecutionID]coretask.Execution{}
	}
	s.Executions[exec.ID] = exec
}

func (s *State) reconcileExecutions() {
	if s.Executions == nil {
		return
	}
	for id, exec := range s.Executions {
		exec.Steps = reconcileExecutionSteps(s.Task.Steps, exec.Steps)
		s.Executions[id] = exec
	}
}

func (s State) execution(id coretask.ExecutionID) coretask.Execution {
	exec, ok := s.Executions[id]
	if !ok {
		exec = coretask.Execution{ID: id, TaskID: s.Task.ID}
	}
	exec.Steps = cloneStepExecutions(exec.Steps)
	if exec.Steps == nil {
		exec.Steps = map[coretask.StepID]coretask.StepExecution{}
	}
	return exec
}

func cloneState(in State) State {
	out := in
	out.Task = cloneTask(in.Task)
	if in.Executions != nil {
		out.Executions = make(map[coretask.ExecutionID]coretask.Execution, len(in.Executions))
		for id, exec := range in.Executions {
			out.Executions[id] = cloneExecution(exec)
		}
	}
	return out
}

func cloneTask(in coretask.Task) coretask.Task {
	out := in
	out.AcceptanceCriteria = append([]string(nil), in.AcceptanceCriteria...)
	out.Labels = append([]string(nil), in.Labels...)
	if len(in.Metadata) > 0 {
		out.Metadata = map[string]string{}
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
	}
	if len(in.Steps) > 0 {
		out.Steps = make([]coretask.Step, len(in.Steps))
		for i, step := range in.Steps {
			out.Steps[i] = step
			out.Steps[i].AcceptanceCriteria = append([]string(nil), step.AcceptanceCriteria...)
			out.Steps[i].DependsOn = append([]coretask.StepID(nil), step.DependsOn...)
			out.Steps[i].Scope = append([]string(nil), step.Scope...)
			if len(step.Metadata) > 0 {
				out.Steps[i].Metadata = map[string]string{}
				for k, v := range step.Metadata {
					out.Steps[i].Metadata[k] = v
				}
			}
		}
	}
	return out
}

func cloneExecution(in coretask.Execution) coretask.Execution {
	out := in
	out.Steps = cloneStepExecutions(in.Steps)
	if len(in.Metadata) > 0 {
		out.Metadata = map[string]string{}
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
	}
	return out
}

func cloneStepExecutions(in map[coretask.StepID]coretask.StepExecution) map[coretask.StepID]coretask.StepExecution {
	if len(in) == 0 {
		return nil
	}
	out := make(map[coretask.StepID]coretask.StepExecution, len(in))
	for id, step := range in {
		if len(step.Metadata) > 0 {
			step.Metadata = cloneStringMap(step.Metadata)
		}
		out[id] = step
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func eventTime(at, fallback time.Time) time.Time {
	if !at.IsZero() {
		return at
	}
	return fallback
}

func firstTaskID(a, b coretask.ID) coretask.ID {
	if a != "" {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
