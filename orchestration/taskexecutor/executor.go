// Package taskexecutor schedules event-sourced tasks onto worker sessions.
package taskexecutor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	coretask "github.com/fluxplane/agentruntime/core/task"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

const (
	defaultReconcileInterval = 2 * time.Second
	defaultMaxParallel       = 2
	defaultWorker            = "worker"
	defaultExplorer          = "explorer"
	defaultReviewer          = "reviewer"
	maxAppendRetries         = 8
)

var errTaskBlocked = errors.New("task blocked")

// WorkerClient runs one task step through a concrete execution backend.
type WorkerClient interface {
	RunStep(context.Context, StepRunRequest) (StepRunResult, error)
}

// RuntimeEventPublisher mirrors scheduler-produced task events into the
// session thread that originated the task.
type RuntimeEventPublisher interface {
	PublishRuntimeEvent(context.Context, corethread.Ref, clientapi.RunID, event.Event) error
}

// RuntimeEventPublisherFunc adapts a function into a RuntimeEventPublisher.
type RuntimeEventPublisherFunc func(context.Context, corethread.Ref, clientapi.RunID, event.Event) error

// PublishRuntimeEvent publishes one runtime event.
func (f RuntimeEventPublisherFunc) PublishRuntimeEvent(ctx context.Context, thread corethread.Ref, runID clientapi.RunID, payload event.Event) error {
	if f == nil {
		return nil
	}
	return f(ctx, thread, runID, payload)
}

// StepRunRequest describes the task or step to run.
type StepRunRequest struct {
	Task        coretask.Task
	ExecutionID coretask.ExecutionID
	Step        coretask.Step
	Profile     string
	ExternalID  string
}

// StepRunResult records worker output.
type StepRunResult struct {
	Output    operation.Value
	Artifacts []coretask.ArtifactSpec
}

// Config wires a Scheduler.
type Config struct {
	Store             runtimetask.Store
	Worker            WorkerClient
	ReconcileInterval time.Duration
	MaxParallel       int
	RoleProfiles      map[coretask.Role]string
	RuntimeEvents     RuntimeEventPublisher
	OnError           func(error)
	Now               func() time.Time
	NewID             func(string) string
}

// SubmitResult reports whether a task run was accepted for asynchronous work.
type SubmitResult struct {
	TaskID  coretask.ID
	Status  coretask.Status
	Started bool
	Running bool
	Summary string
}

// Scheduler claims ready tasks and records worker execution as task events.
type Scheduler struct {
	store             runtimetask.Store
	worker            WorkerClient
	reconcileInterval time.Duration
	maxParallel       int
	roleProfiles      map[coretask.Role]string
	runtimeEvents     RuntimeEventPublisher
	onError           func(error)
	now               func() time.Time
	newID             func(string) string

	mu          sync.Mutex
	enabled     bool
	active      bool
	ctx         context.Context
	running     map[coretask.ID]struct{}
	diagnostics []coretask.Diagnostic
}

// New returns a task scheduler.
func New(cfg Config) (*Scheduler, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("taskexecutor: store is nil")
	}
	if cfg.Worker == nil {
		return nil, fmt.Errorf("taskexecutor: worker is nil")
	}
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = defaultReconcileInterval
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = defaultMaxParallel
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.NewID == nil {
		cfg.NewID = newID
	}
	return &Scheduler{
		store:             cfg.Store,
		worker:            cfg.Worker,
		reconcileInterval: cfg.ReconcileInterval,
		maxParallel:       cfg.MaxParallel,
		roleProfiles:      cloneRoleProfiles(cfg.RoleProfiles),
		runtimeEvents:     cfg.RuntimeEvents,
		onError:           cfg.OnError,
		now:               cfg.Now,
		newID:             cfg.NewID,
		enabled:           true,
		running:           map[coretask.ID]struct{}{},
	}, nil
}

// SetRuntimeEventPublisher configures where scheduler-produced task events are
// mirrored for live session feedback.
func (s *Scheduler) SetRuntimeEventPublisher(publisher RuntimeEventPublisher) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.runtimeEvents = publisher
	s.mu.Unlock()
}

// Start runs the scheduler loop until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.setActive(ctx, true)
	defer s.clearActive()
	ticker := time.NewTicker(s.reconcileInterval)
	defer ticker.Stop()
	for {
		if err := s.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.recordError("task_scheduler_tick_failed", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick reconciles indexed ready tasks until local capacity is full.
func (s *Scheduler) Tick(ctx context.Context) error {
	if !s.isEnabled() {
		return nil
	}
	summaries, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		if ctx.Err() != nil || s.capacity() <= 0 {
			return ctx.Err()
		}
		if summary.ID == "" || summary.Status != coretask.StatusReady {
			continue
		}
		if !s.reserve(summary.ID) {
			continue
		}
		go func(taskID coretask.ID) {
			defer s.release(taskID)
			if err := s.RunTask(ctx, taskID); err != nil && !errors.Is(err, context.Canceled) {
				s.recordError("task_run_failed", err)
			}
		}(summary.ID)
	}
	return nil
}

// SubmitTask starts one ready task asynchronously when local capacity is
// available. It bypasses automatic scheduler enablement so operators can run a
// specific task while the scheduler loop is paused.
func (s *Scheduler) SubmitTask(ctx context.Context, taskID coretask.ID) (SubmitResult, error) {
	return s.submitTask(ctx, taskID)
}

// NotifyTaskReady attempts to run a ready task as part of the event-triggered
// scheduler path. It respects automatic scheduler enablement; manual SubmitTask
// remains available while automatic scheduling is disabled.
func (s *Scheduler) NotifyTaskReady(ctx context.Context, taskID coretask.ID) error {
	if !s.isEnabled() {
		return nil
	}
	_, err := s.submitTask(ctx, taskID)
	return err
}

func (s *Scheduler) submitTask(ctx context.Context, taskID coretask.ID) (SubmitResult, error) {
	if strings.TrimSpace(string(taskID)) == "" {
		return SubmitResult{}, fmt.Errorf("taskexecutor: task id is required")
	}
	state, err := s.store.Project(ctx, taskID)
	if err != nil {
		return SubmitResult{}, err
	}
	if state.Task.ID == "" {
		return SubmitResult{}, fmt.Errorf("taskexecutor: task %q was not found", taskID)
	}
	result := SubmitResult{TaskID: taskID, Status: state.Task.Status}
	if state.Task.Status != coretask.StatusReady {
		result.Summary = fmt.Sprintf("Task %s is %s, not ready.", taskID, state.Task.Status)
		return result, nil
	}
	if !s.reserve(taskID) {
		result.Running = s.isRunning(taskID)
		if result.Running {
			result.Summary = fmt.Sprintf("Task %s is already running.", taskID)
		} else {
			result.Summary = "Task scheduler has no available capacity."
		}
		return result, nil
	}
	runCtx := s.runContext(ctx)
	go func() {
		defer s.release(taskID)
		if err := s.RunTask(runCtx, taskID); err != nil && !errors.Is(err, context.Canceled) {
			s.recordError("task_run_failed", err)
		}
	}()
	result.Started = true
	result.Running = true
	result.Summary = fmt.Sprintf("Task %s scheduled.", taskID)
	return result, nil
}

// Status reports scheduler runtime state.
func (s *Scheduler) Status() coretask.SchedulerStatusResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	running := make([]coretask.ID, 0, len(s.running))
	for id := range s.running {
		running = append(running, id)
	}
	sort.Slice(running, func(i, j int) bool { return running[i] < running[j] })
	return coretask.SchedulerStatusResult{
		Enabled:           s.enabled,
		Active:            s.active,
		Running:           running,
		Capacity:          s.maxParallel - len(s.running),
		MaxParallel:       s.maxParallel,
		ReconcileInterval: s.reconcileInterval.String(),
		Diagnostics:       append([]coretask.Diagnostic(nil), s.diagnostics...),
	}
}

// SetEnabled controls automatic ready-task scheduling.
func (s *Scheduler) SetEnabled(enabled bool) coretask.SchedulerStatusResult {
	s.mu.Lock()
	s.enabled = enabled
	s.mu.Unlock()
	return s.Status()
}

func (s *Scheduler) capacity() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxParallel - len(s.running)
}

func (s *Scheduler) isEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

func (s *Scheduler) isRunning(taskID coretask.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.running[taskID]
	return exists
}

func (s *Scheduler) reserve(taskID coretask.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.running) >= s.maxParallel {
		return false
	}
	if _, exists := s.running[taskID]; exists {
		return false
	}
	s.running[taskID] = struct{}{}
	return true
}

func (s *Scheduler) release(taskID coretask.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, taskID)
}

func (s *Scheduler) setActive(ctx context.Context, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	s.active = active
}

func (s *Scheduler) clearActive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = nil
	s.active = false
}

func (s *Scheduler) runContext(fallback context.Context) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx
	}
	if fallback != nil {
		return fallback
	}
	return context.Background()
}

func (s *Scheduler) recordError(code string, err error) {
	if err == nil {
		return
	}
	if s.onError != nil {
		s.onError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diagnostics = append(s.diagnostics, coretask.Diagnostic{Code: code, Message: err.Error()})
	const maxDiagnostics = 8
	if len(s.diagnostics) > maxDiagnostics {
		s.diagnostics = append([]coretask.Diagnostic(nil), s.diagnostics[len(s.diagnostics)-maxDiagnostics:]...)
	}
}

// RunTask claims and runs one ready task synchronously.
func (s *Scheduler) RunTask(ctx context.Context, taskID coretask.ID) error {
	claimed, err := s.claim(ctx, taskID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	state, err := s.store.Project(ctx, taskID)
	if err != nil {
		return err
	}
	if len(state.Task.Steps) == 0 {
		return s.runWholeTask(ctx, state)
	}
	return s.runStepDAG(ctx, state.Task.ID)
}

func (s *Scheduler) claim(ctx context.Context, taskID coretask.ID) (bool, error) {
	projected, err := s.store.ProjectWithSequence(ctx, taskID)
	if err != nil {
		return false, err
	}
	state := projected.State
	if state.Task.Status != coretask.StatusReady {
		return false, nil
	}
	if resumableExecution(state) {
		exec := state.Executions[state.CurrentExecution]
		exec.Status = coretask.StatusRunning
		exec.Error = nil
		exec.CompletedAt = time.Time{}
		events := []event.Event{
			coretask.ExecutionStarted{TaskID: taskID, ExecutionID: exec.ID, Execution: exec},
		}
		err = s.store.AppendExpected(ctx, taskID, projected.Sequence,
			events...,
		)
		if errors.Is(err, event.ErrAppendConflict) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err := s.index(ctx, taskID); err != nil {
			return false, err
		}
		s.publishTaskRuntimeEvents(ctx, state.Task, events)
		return true, nil
	}
	if executionActive(state) {
		return false, nil
	}
	execID := coretask.ExecutionID(s.newID("exec_"))
	exec := coretask.Execution{
		ID:          execID,
		TaskID:      state.Task.ID,
		Status:      coretask.StatusRunning,
		WorkflowRef: state.Task.WorkflowRef,
		Workflow:    state.Task.Workflow,
		StartedAt:   s.now(),
	}
	events := []event.Event{coretask.ExecutionStarted{TaskID: taskID, ExecutionID: execID, Execution: exec}}
	err = s.store.AppendExpected(ctx, taskID, projected.Sequence, events...)
	if errors.Is(err, event.ErrAppendConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := s.index(ctx, taskID); err != nil {
		return false, err
	}
	s.publishTaskRuntimeEvents(ctx, state.Task, events)
	return true, nil
}

func (s *Scheduler) runWholeTask(ctx context.Context, state runtimetask.State) error {
	execID := state.CurrentExecution
	step := coretask.Step{
		ID:                 coretask.StepID("task"),
		Title:              firstNonEmpty(state.Task.Title, state.Task.Objective, string(state.Task.ID)),
		Description:        state.Task.Description,
		Objective:          state.Task.Objective,
		AcceptanceCriteria: append([]string(nil), state.Task.AcceptanceCriteria...),
		Inputs:             cloneArtifacts(state.Task.Inputs),
		Outputs:            cloneArtifacts(state.Task.Outputs),
		Assignee:           state.Task.Assignee,
		Scope:              append([]string(nil), state.Task.Scope...),
	}
	profile, blocked := s.resolveProfile(state.Task, step)
	if blocked {
		return s.blockTask(ctx, state.Task.ID, execID, "task is assigned to a human")
	}
	result, err := s.worker.RunStep(ctx, StepRunRequest{
		Task:        state.Task,
		ExecutionID: execID,
		Step:        step,
		Profile:     profile,
		ExternalID:  string(execID) + ":task",
	})
	if err != nil {
		opErr := &operation.Error{Code: "task_worker_failed", Message: err.Error()}
		if appendErr := s.appendExecutionTerminal(ctx, state.Task.ID, execID, coretask.ExecutionFailed{TaskID: state.Task.ID, ExecutionID: execID, Error: opErr}); appendErr != nil {
			return appendErr
		}
		return err
	}
	events := artifactEvents(state.Task.ID, execID, "", bindDeclaredOutputs(result, step.Outputs))
	if len(events) > 0 {
		if appended, err := s.withExpectedRetry(ctx, state.Task.ID, func(current runtimetask.State) (bool, []event.Event) {
			exec, ok := current.Executions[execID]
			if current.Task.Status != coretask.StatusRunning || current.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
				return false, nil
			}
			return true, events
		}); err != nil {
			return err
		} else if !appended {
			s.appendSchedulerDiagnostic(ctx, state.Task.ID, execID, "", "task_stale_artifacts_ignored", "worker artifacts ignored because the task execution is no longer running")
			return nil
		}
	}
	projected, err := s.store.Project(ctx, state.Task.ID)
	if err != nil {
		return err
	}
	if validation := validateCompletion(projected); !validation.Completable {
		return s.blockCompletion(ctx, state.Task.ID, execID, validation)
	}
	if err := s.appendExecutionTerminal(ctx, state.Task.ID, execID, coretask.ExecutionCompleted{TaskID: state.Task.ID, ExecutionID: execID, Output: result.Output}); err != nil {
		return err
	}
	return nil
}

func (s *Scheduler) runStepDAG(ctx context.Context, taskID coretask.ID) error {
	for {
		state, err := s.store.Project(ctx, taskID)
		if err != nil {
			return err
		}
		if len(runtimetask.FailedStepIDs(state)) > 0 {
			if err := s.cancelWaitingDependents(ctx, state, "dependency failed"); err != nil {
				return err
			}
			if err := s.appendExecutionTerminal(ctx, taskID, state.CurrentExecution, coretask.ExecutionFailed{
				TaskID:      taskID,
				ExecutionID: state.CurrentExecution,
				Error:       &operation.Error{Code: "task_step_failed", Message: "one or more task steps failed"},
			}); err != nil {
				return err
			}
			return nil
		}
		if runtimetask.AllStepsTerminal(state) {
			if validation := validateCompletion(state); !validation.Completable {
				return s.blockCompletion(ctx, taskID, state.CurrentExecution, validation)
			}
			if err := s.appendExecutionTerminal(ctx, taskID, state.CurrentExecution, coretask.ExecutionCompleted{TaskID: taskID, ExecutionID: state.CurrentExecution}); err != nil {
				return err
			}
			return nil
		}
		ready := runtimetask.ReadySteps(state)
		if len(ready) == 0 {
			if err := s.appendExecutionTerminal(ctx, taskID, state.CurrentExecution, coretask.ExecutionFailed{
				TaskID:      taskID,
				ExecutionID: state.CurrentExecution,
				Error:       &operation.Error{Code: "task_stalled", Message: "no runnable task steps remain"},
			}); err != nil {
				return err
			}
			return nil
		}
		for _, step := range ready {
			if err := s.runStep(ctx, state.Task, state.CurrentExecution, step); errors.Is(err, errTaskBlocked) {
				return nil
			} else if err != nil {
				return err
			}
		}
	}
}

func (s *Scheduler) runStep(ctx context.Context, task coretask.Task, execID coretask.ExecutionID, step coretask.Step) error {
	profile, blocked := s.resolveProfile(task, step)
	if blocked {
		if err := s.blockStep(ctx, task.ID, execID, step.ID, "step is assigned to a human"); err != nil {
			return err
		}
		return errTaskBlocked
	}
	externalID := string(execID) + ":" + string(step.ID)
	dispatched, err := s.appendStepDispatch(ctx, task.ID, execID, step.ID, coretask.StepDispatched{
		TaskID: task.ID, ExecutionID: execID, StepID: step.ID,
		Title:    firstNonEmpty(step.Title, step.Objective, string(step.ID)),
		Assignee: firstRole(step.Assignee, task.Assignee),
		Profile:  profile, ExternalID: externalID,
	})
	if err != nil {
		return err
	}
	if !dispatched {
		return nil
	}
	result, err := s.worker.RunStep(ctx, StepRunRequest{Task: task, ExecutionID: execID, Step: step, Profile: profile, ExternalID: externalID})
	if err != nil {
		return s.appendStepTerminal(ctx, task.ID, execID, step.ID, coretask.StepFailed{
			TaskID: task.ID, ExecutionID: execID, StepID: step.ID,
			Error: &operation.Error{Code: "task_worker_failed", Message: err.Error()},
		})
	}
	events := artifactEvents(task.ID, execID, step.ID, bindDeclaredOutputs(result, step.Outputs))
	events = append(events, coretask.StepCompleted{TaskID: task.ID, ExecutionID: execID, StepID: step.ID, Output: result.Output})
	return s.appendStepTerminal(ctx, task.ID, execID, step.ID, events...)
}

func (s *Scheduler) appendStepDispatch(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, evt coretask.StepDispatched) (bool, error) {
	return s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		if exec.Steps[stepID].Status != coretask.StepStatusWaiting {
			return false, nil
		}
		return true, []event.Event{evt}
	})
}

func (s *Scheduler) appendStepTerminal(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, events ...event.Event) error {
	appended, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		if exec.Steps[stepID].Status != coretask.StepStatusRunning {
			return false, nil
		}
		return true, events
	})
	if err == nil && !appended {
		s.appendSchedulerDiagnostic(ctx, taskID, execID, stepID, "task_stale_step_result_ignored", "worker step result ignored because the task step is no longer running")
	}
	return err
}

func (s *Scheduler) appendExecutionTerminal(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, events ...event.Event) error {
	appended, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		return true, events
	})
	if err == nil && !appended {
		s.appendSchedulerDiagnostic(ctx, taskID, execID, "", "task_stale_execution_result_ignored", "worker execution result ignored because the task execution is no longer running")
	}
	return err
}

func (s *Scheduler) withExpectedRetry(ctx context.Context, taskID coretask.ID, build func(runtimetask.State) (bool, []event.Event)) (bool, error) {
	var lastConflict error
	for attempt := 0; attempt < maxAppendRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		projected, err := s.store.ProjectWithSequence(ctx, taskID)
		if err != nil {
			return false, err
		}
		ok, events := build(projected.State)
		if !ok || len(events) == 0 {
			return false, nil
		}
		err = s.store.AppendExpected(ctx, taskID, projected.Sequence, events...)
		if errors.Is(err, event.ErrAppendConflict) {
			lastConflict = err
			timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if err != nil {
			return false, err
		}
		if err := s.index(ctx, taskID); err != nil {
			return false, err
		}
		s.publishTaskRuntimeEvents(ctx, projected.State.Task, events)
		return true, nil
	}
	if lastConflict != nil {
		err := fmt.Errorf("taskexecutor: append conflict after %d retries: %w", maxAppendRetries, lastConflict)
		s.appendSchedulerDiagnostic(ctx, taskID, "", "", "task_append_conflict", err.Error())
		return false, err
	}
	return false, nil
}

func (s *Scheduler) appendSchedulerDiagnostic(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, code, message string) {
	if taskID == "" || strings.TrimSpace(message) == "" {
		return
	}
	payload := coretask.SchedulerDiagnostic{
		TaskID:      taskID,
		ExecutionID: execID,
		StepID:      stepID,
		Diagnostic: coretask.Diagnostic{
			Code:    code,
			Message: message,
			Target:  string(stepID),
		},
	}
	if payload.Diagnostic.Target == "" {
		payload.Diagnostic.Target = string(execID)
	}
	if err := s.store.Append(context.WithoutCancel(ctx), taskID, payload); err != nil {
		s.recordError("task_scheduler_diagnostic_append_failed", err)
		return
	}
	if err := s.index(context.WithoutCancel(ctx), taskID); err != nil {
		s.recordError("task_scheduler_diagnostic_index_failed", err)
	}
	state, err := s.store.Project(context.WithoutCancel(ctx), taskID)
	if err != nil {
		s.recordError("task_scheduler_diagnostic_project_failed", err)
		return
	}
	s.publishTaskRuntimeEvents(context.WithoutCancel(ctx), state.Task, []event.Event{payload})
}

func (s *Scheduler) cancelWaitingDependents(ctx context.Context, state runtimetask.State, reason string) error {
	before := state
	after := runtimetask.CancelWaitingDependents(state, reason, s.now())
	exec := after.Executions[after.CurrentExecution]
	var events []event.Event
	for _, step := range after.Task.Steps {
		prev := before.Executions[before.CurrentExecution].Steps[step.ID]
		next := exec.Steps[step.ID]
		if prev.Status != next.Status && next.Status == coretask.StepStatusCancelled {
			message := reason
			if next.Error != nil && next.Error.Message != "" {
				message = next.Error.Message
			}
			events = append(events, coretask.StepCancelled{TaskID: after.Task.ID, ExecutionID: after.CurrentExecution, StepID: step.ID, Reason: message})
		}
	}
	if len(events) == 0 {
		return nil
	}
	_, err := s.withExpectedRetry(ctx, after.Task.ID, func(current runtimetask.State) (bool, []event.Event) {
		if current.Task.Status != coretask.StatusRunning || current.CurrentExecution != after.CurrentExecution {
			return false, nil
		}
		currentExec := current.Executions[current.CurrentExecution]
		if currentExec.Status != coretask.StatusRunning {
			return false, nil
		}
		return true, events
	})
	return err
}

func (s *Scheduler) blockStep(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, reason string) error {
	_, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		if exec.Steps[stepID].Status != coretask.StepStatusWaiting {
			return false, nil
		}
		return true, []event.Event{
			coretask.StepStatusChanged{TaskID: taskID, ExecutionID: execID, StepID: stepID, Current: coretask.StepStatusBlocked, Reason: reason},
			coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: execID, Reason: reason},
			coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusBlocked, Reason: reason},
		}
	})
	return err
}

func (s *Scheduler) blockTask(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, reason string) error {
	_, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		return true, []event.Event{
			coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: execID, Reason: reason},
			coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusBlocked, Reason: reason},
		}
	})
	return err
}

func (s *Scheduler) blockCompletion(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, validation coretask.TaskValidationResult) error {
	reason := completionBlockedReason(validation)
	_, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		return true, []event.Event{
			coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: execID, Reason: reason},
			coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusBlocked, Reason: reason},
		}
	})
	return err
}

func (s *Scheduler) index(ctx context.Context, taskID coretask.ID) error {
	state, err := s.store.Project(ctx, taskID)
	if err != nil {
		return err
	}
	return s.store.Index(ctx, summary(state.Task))
}

func (s *Scheduler) publishTaskRuntimeEvents(ctx context.Context, task coretask.Task, events []event.Event) {
	if len(events) == 0 {
		return
	}
	thread, runID, ok := taskOrigin(task)
	if !ok {
		return
	}
	s.mu.Lock()
	publisher := s.runtimeEvents
	s.mu.Unlock()
	if publisher == nil {
		return
	}
	publishCtx := context.WithoutCancel(ctx)
	for _, payload := range events {
		if payload == nil {
			continue
		}
		if err := publisher.PublishRuntimeEvent(publishCtx, thread, runID, payload); err != nil {
			s.recordError("task_runtime_event_publish_failed", err)
			return
		}
	}
}

func taskOrigin(task coretask.Task) (corethread.Ref, clientapi.RunID, bool) {
	threadID := strings.TrimSpace(task.Metadata[coretask.MetadataOriginThreadID])
	if threadID == "" {
		return corethread.Ref{}, "", false
	}
	thread := corethread.Ref{
		ID:       corethread.ID(threadID),
		BranchID: corethread.BranchID(strings.TrimSpace(task.Metadata[coretask.MetadataOriginBranchID])),
	}
	if thread.BranchID == "" {
		thread.BranchID = corethread.MainBranch
	}
	return thread, clientapi.RunID(strings.TrimSpace(task.Metadata[coretask.MetadataOriginRunID])), true
}

func executionActive(state runtimetask.State) bool {
	if state.CurrentExecution == "" {
		return false
	}
	exec, ok := state.Executions[state.CurrentExecution]
	return ok && exec.Status == coretask.StatusRunning
}

func resumableExecution(state runtimetask.State) bool {
	if state.CurrentExecution == "" {
		return false
	}
	exec, ok := state.Executions[state.CurrentExecution]
	return ok && exec.Status == coretask.StatusInterrupted
}

func artifactEvents(taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, artifacts []coretask.ArtifactSpec) []event.Event {
	events := make([]event.Event, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID == "" && artifact.Name == "" && artifact.Value == nil && artifact.Ref == "" {
			continue
		}
		events = append(events, coretask.ArtifactAdded{TaskID: taskID, ExecutionID: execID, StepID: stepID, Artifact: artifact})
	}
	return events
}

func bindDeclaredOutputs(result StepRunResult, outputs []coretask.ArtifactSpec) []coretask.ArtifactSpec {
	artifacts := cloneArtifacts(result.Artifacts)
	for _, output := range outputs {
		if output.ID == "" && output.Name == "" {
			continue
		}
		if artifactSpecSatisfied(output, artifacts) {
			continue
		}
		artifact := output
		if artifact.Kind == "" {
			artifact.Kind = coretask.ArtifactReport
		}
		if artifact.Value == nil && artifact.Ref == "" {
			artifact.Value = result.Output
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}

func validateCompletion(state runtimetask.State) coretask.TaskValidationResult {
	result := coretask.TaskValidationResult{
		TaskID: state.Task.ID,
		Ready:  state.Task.Status == coretask.StatusReady || state.Task.Status == coretask.StatusRunning,
	}
	completable := true
	produced := scopedArtifacts(state)
	for _, output := range state.Task.Outputs {
		if !output.Required {
			continue
		}
		ok := scopedArtifactSatisfied(output, produced)
		if !ok {
			completable = false
		}
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "required_output",
			Message: fmt.Sprintf("required output %s", firstNonEmpty(output.ID, output.Name, output.Description, output.Ref)),
			OK:      ok,
			Target:  output.ID,
		})
	}
	if len(state.Task.Steps) > 0 {
		ok := runtimetask.AllStepsTerminal(state)
		if !ok {
			completable = false
		}
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "steps_terminal",
			Message: "all declared task steps have terminal execution state",
			OK:      ok,
		})
	}
	if len(result.Checks) == 0 {
		result.Checks = append(result.Checks, coretask.TaskCheck{Code: "task_shape", Message: "task has no required outputs or steps", OK: true})
	}
	result.Completable = completable
	return result
}

func scopedArtifacts(state runtimetask.State) []coretask.ScopedArtifact {
	var out []coretask.ScopedArtifact
	for _, artifact := range state.Task.Artifacts {
		out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, Artifact: artifact})
	}
	for execID, exec := range state.Executions {
		for _, artifact := range exec.Artifacts {
			out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, ExecutionID: execID, Artifact: artifact})
		}
		for stepID, step := range exec.Steps {
			for _, artifact := range step.Artifacts {
				out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, ExecutionID: execID, StepID: stepID, Artifact: artifact})
			}
		}
	}
	return out
}

func scopedArtifactSatisfied(required coretask.ArtifactSpec, produced []coretask.ScopedArtifact) bool {
	for _, scoped := range produced {
		if artifactSpecSatisfied(required, []coretask.ArtifactSpec{scoped.Artifact}) {
			return true
		}
	}
	return false
}

func artifactSpecSatisfied(required coretask.ArtifactSpec, produced []coretask.ArtifactSpec) bool {
	for _, artifact := range produced {
		if required.ID != "" && artifact.ID == required.ID {
			return true
		}
		if required.Name != "" && strings.EqualFold(artifact.Name, required.Name) {
			return true
		}
	}
	return false
}

func completionBlockedReason(validation coretask.TaskValidationResult) string {
	for _, check := range validation.Checks {
		if check.OK {
			continue
		}
		target := firstNonEmpty(check.Target, check.Message, check.Code)
		return fmt.Sprintf("task completion blocked: %s", target)
	}
	return "task completion blocked: validation did not pass"
}

func (s *Scheduler) resolveProfile(task coretask.Task, step coretask.Step) (string, bool) {
	if strings.TrimSpace(step.Profile) != "" {
		return strings.TrimSpace(step.Profile), false
	}
	role := firstRole(step.Assignee, task.Assignee)
	if profile := strings.TrimSpace(s.roleProfiles[role]); profile != "" {
		return profile, false
	}
	switch role {
	case coretask.RoleHuman:
		return "", true
	case coretask.RoleExplorer:
		return defaultExplorer, false
	case coretask.RoleReviewer:
		return defaultReviewer, false
	case coretask.RoleDeveloper, coretask.RoleTester:
		return defaultWorker, false
	default:
		return defaultWorker, false
	}
}

func firstRole(values ...coretask.Role) coretask.Role {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneRoleProfiles(in map[coretask.Role]string) map[coretask.Role]string {
	out := map[coretask.Role]string{
		coretask.RoleDeveloper: defaultWorker,
		coretask.RoleTester:    defaultWorker,
		coretask.RoleExplorer:  defaultExplorer,
		coretask.RoleReviewer:  defaultReviewer,
	}
	for role, profile := range in {
		if strings.TrimSpace(profile) != "" {
			out[role] = strings.TrimSpace(profile)
		}
	}
	return out
}

func summary(task coretask.Task) coretask.TaskSummary {
	return coretask.TaskSummary{
		ID: task.ID, Title: task.Title, Objective: task.Objective, Description: task.Description,
		Status: task.Status, Priority: task.Priority, Assignee: task.Assignee, Owner: task.Owner,
		WorkspaceID: task.WorkspaceID, ProjectID: task.ProjectID,
		Labels: append([]string(nil), task.Labels...), Metadata: cloneStringMap(task.Metadata),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneArtifacts(in []coretask.ArtifactSpec) []coretask.ArtifactSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]coretask.ArtifactSpec, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ChannelWorker runs task steps by opening configured session profiles.
type ChannelWorker struct {
	Client clientapi.ChannelClient
}

// RunStep opens the requested worker profile, submits the step prompt, and
// waits for the worker's final response.
func (w ChannelWorker) RunStep(ctx context.Context, req StepRunRequest) (StepRunResult, error) {
	if w.Client == nil {
		return StepRunResult{}, fmt.Errorf("taskexecutor: channel client is nil")
	}
	profile := firstNonEmpty(req.Profile, defaultWorker)
	session, err := w.open(ctx, profile, req)
	if err != nil && profile == defaultReviewer {
		session, err = w.open(ctx, defaultWorker, req)
	}
	if err != nil {
		return StepRunResult{}, err
	}
	defer func() { _ = session.Close(context.WithoutCancel(ctx)) }()
	run, err := session.Submit(ctx, clientapi.NewSubmission().
		WithText(stepPrompt(req)).
		WithMetadata(map[string]any{
			"task_id":      string(req.Task.ID),
			"execution_id": string(req.ExecutionID),
			"step_id":      string(req.Step.ID),
			"profile":      profile,
		}))
	if err != nil {
		return StepRunResult{}, err
	}
	result, err := run.Wait(ctx)
	if err != nil {
		return StepRunResult{}, err
	}
	if result.Input != nil && result.Input.Error != nil {
		return StepRunResult{}, errors.New(result.Input.Error.Message)
	}
	output := resultText(result)
	return StepRunResult{
		Output: output,
		Artifacts: []coretask.ArtifactSpec{{
			ID:          "worker-output-" + string(req.ExecutionID) + "-" + string(req.Step.ID),
			Name:        "Worker output",
			Kind:        coretask.ArtifactReport,
			Description: "Final worker response for " + firstNonEmpty(req.Step.Title, string(req.Step.ID)),
			Value:       output,
		}},
	}, nil
}

// DeferredWorker is a WorkerClient whose concrete implementation can be
// supplied after app composition has created the channel client.
type DeferredWorker struct {
	mu     sync.RWMutex
	worker WorkerClient
}

// Set configures the concrete worker implementation.
func (w *DeferredWorker) Set(worker WorkerClient) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.worker = worker
}

// RunStep delegates to the configured worker.
func (w *DeferredWorker) RunStep(ctx context.Context, req StepRunRequest) (StepRunResult, error) {
	w.mu.RLock()
	worker := w.worker
	w.mu.RUnlock()
	if worker == nil {
		return StepRunResult{}, fmt.Errorf("taskexecutor: worker is not configured")
	}
	return worker.RunStep(ctx, req)
}

func (w ChannelWorker) open(ctx context.Context, profile string, req StepRunRequest) (clientapi.SessionHandle, error) {
	return w.Client.Open(ctx, clientapi.OpenRequest{
		Session: coresession.Ref{Name: coresession.Name(profile)},
		Metadata: map[string]string{
			"task_id":      string(req.Task.ID),
			"execution_id": string(req.ExecutionID),
			"step_id":      string(req.Step.ID),
			"external_id":  req.ExternalID,
			"role":         "task_worker",
		},
	})
}

func resultText(result clientapi.Result) string {
	if result.Outbound != nil && result.Outbound.Message != nil && result.Outbound.Message.Content != nil {
		return fmt.Sprint(result.Outbound.Message.Content)
	}
	if result.Input != nil && result.Input.Outbound != nil && result.Input.Outbound.Message != nil && result.Input.Outbound.Message.Content != nil {
		return fmt.Sprint(result.Input.Outbound.Message.Content)
	}
	return ""
}

func stepPrompt(req StepRunRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n\nObjective:\n%s\n", firstNonEmpty(req.Task.Title, string(req.Task.ID)), firstNonEmpty(req.Step.Objective, req.Task.Objective, req.Step.Description, req.Task.Description))
	if req.Step.Title != "" {
		fmt.Fprintf(&b, "\nStep: %s\n", req.Step.Title)
	}
	if req.Step.Description != "" {
		fmt.Fprintf(&b, "\nStep details:\n%s\n", req.Step.Description)
	}
	if len(req.Step.Scope) > 0 || len(req.Task.Scope) > 0 {
		b.WriteString("\nScope:\n")
		for _, item := range append(append([]string(nil), req.Task.Scope...), req.Step.Scope...) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if len(req.Step.Inputs) > 0 || len(req.Task.Inputs) > 0 {
		b.WriteString("\nInputs:\n")
		writeArtifacts(&b, append(append([]coretask.ArtifactSpec(nil), req.Task.Inputs...), req.Step.Inputs...))
	}
	if len(req.Step.Outputs) > 0 || len(req.Task.Outputs) > 0 {
		b.WriteString("\nExpected outputs:\n")
		writeArtifacts(&b, append(append([]coretask.ArtifactSpec(nil), req.Task.Outputs...), req.Step.Outputs...))
	}
	criteria := append(append([]string(nil), req.Task.AcceptanceCriteria...), req.Step.AcceptanceCriteria...)
	if len(criteria) > 0 {
		b.WriteString("\nAcceptance criteria:\n")
		for _, item := range criteria {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	b.WriteString("\nReturn a concise final response with the evidence, outputs, and any blockers.")
	return strings.TrimSpace(b.String())
}

func writeArtifacts(b *strings.Builder, artifacts []coretask.ArtifactSpec) {
	for _, artifact := range artifacts {
		label := firstNonEmpty(artifact.ID, artifact.Name, string(artifact.Kind), "artifact")
		fmt.Fprintf(b, "- %s", label)
		if artifact.Kind != "" {
			fmt.Fprintf(b, " [%s]", artifact.Kind)
		}
		if artifact.Required {
			b.WriteString(" required")
		}
		if artifact.Description != "" {
			fmt.Fprintf(b, ": %s", artifact.Description)
		}
		if artifact.Ref != "" {
			fmt.Fprintf(b, " (%s)", artifact.Ref)
		}
		b.WriteByte('\n')
	}
}

func newID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(buf[:])
}
