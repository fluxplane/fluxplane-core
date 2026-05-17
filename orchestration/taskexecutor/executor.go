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
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

const (
	defaultPollInterval = 2 * time.Second
	defaultMaxParallel  = 2
	defaultWorker       = "worker"
	defaultExplorer     = "explorer"
	defaultReviewer     = "reviewer"
)

var errTaskBlocked = errors.New("task blocked")

// WorkerClient runs one task step through a concrete execution backend.
type WorkerClient interface {
	RunStep(context.Context, StepRunRequest) (StepRunResult, error)
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
	Store        runtimetask.Store
	Worker       WorkerClient
	PollInterval time.Duration
	MaxParallel  int
	RoleProfiles map[coretask.Role]string
	Now          func() time.Time
	NewID        func(string) string
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
	store        runtimetask.Store
	worker       WorkerClient
	pollInterval time.Duration
	maxParallel  int
	roleProfiles map[coretask.Role]string
	now          func() time.Time
	newID        func(string) string

	mu      sync.Mutex
	enabled bool
	active  bool
	ctx     context.Context
	running map[coretask.ID]struct{}
}

// New returns a task scheduler.
func New(cfg Config) (*Scheduler, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("taskexecutor: store is nil")
	}
	if cfg.Worker == nil {
		return nil, fmt.Errorf("taskexecutor: worker is nil")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
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
		store:        cfg.Store,
		worker:       cfg.Worker,
		pollInterval: cfg.PollInterval,
		maxParallel:  cfg.MaxParallel,
		roleProfiles: cloneRoleProfiles(cfg.RoleProfiles),
		now:          cfg.Now,
		newID:        cfg.NewID,
		enabled:      true,
		running:      map[coretask.ID]struct{}{},
	}, nil
}

// Start runs the scheduler loop until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.setActive(ctx, true)
	defer s.clearActive()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		_ = s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick starts work for ready tasks until local capacity is full.
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
			_ = s.RunTask(ctx, taskID)
		}(summary.ID)
	}
	return nil
}

// SubmitTask starts one ready task asynchronously when local capacity is
// available. It bypasses automatic polling enablement so operators can run a
// specific task while the scheduler loop is paused.
func (s *Scheduler) SubmitTask(ctx context.Context, taskID coretask.ID) (SubmitResult, error) {
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
		_ = s.RunTask(runCtx, taskID)
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
		Enabled:      s.enabled,
		Active:       s.active,
		Running:      running,
		Capacity:     s.maxParallel - len(s.running),
		MaxParallel:  s.maxParallel,
		PollInterval: s.pollInterval.String(),
	}
}

// SetEnabled controls automatic ready-task polling.
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
		err = s.store.AppendExpected(ctx, taskID, projected.Sequence,
			coretask.ExecutionStarted{TaskID: taskID, ExecutionID: exec.ID, Execution: exec},
		)
		if errors.Is(err, event.ErrAppendConflict) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, s.index(ctx, taskID)
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
	err = s.store.AppendExpected(ctx, taskID, projected.Sequence,
		coretask.ExecutionStarted{TaskID: taskID, ExecutionID: execID, Execution: exec},
	)
	if errors.Is(err, event.ErrAppendConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.index(ctx, taskID)
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
	events := artifactEvents(state.Task.ID, execID, "", result.Artifacts)
	events = append(events, coretask.ExecutionCompleted{TaskID: state.Task.ID, ExecutionID: execID, Output: result.Output})
	if err := s.appendExecutionTerminal(ctx, state.Task.ID, execID, events...); err != nil {
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
	if err := s.store.Append(ctx, task.ID, coretask.StepDispatched{
		TaskID: task.ID, ExecutionID: execID, StepID: step.ID,
		Title:    firstNonEmpty(step.Title, step.Objective, string(step.ID)),
		Assignee: firstRole(step.Assignee, task.Assignee),
		Profile:  profile, ExternalID: externalID,
	}); err != nil {
		return err
	}
	result, err := s.worker.RunStep(ctx, StepRunRequest{Task: task, ExecutionID: execID, Step: step, Profile: profile, ExternalID: externalID})
	if err != nil {
		return s.appendStepTerminal(ctx, task.ID, execID, step.ID, coretask.StepFailed{
			TaskID: task.ID, ExecutionID: execID, StepID: step.ID,
			Error: &operation.Error{Code: "task_worker_failed", Message: err.Error()},
		})
	}
	events := artifactEvents(task.ID, execID, step.ID, result.Artifacts)
	events = append(events, coretask.StepCompleted{TaskID: task.ID, ExecutionID: execID, StepID: step.ID, Output: result.Output})
	return s.appendStepTerminal(ctx, task.ID, execID, step.ID, events...)
}

func (s *Scheduler) appendStepTerminal(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, events ...event.Event) error {
	for {
		projected, err := s.store.ProjectWithSequence(ctx, taskID)
		if err != nil {
			return err
		}
		state := projected.State
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return nil
		}
		if exec.Steps[stepID].Status != coretask.StepStatusRunning {
			return nil
		}
		err = s.store.AppendExpected(ctx, taskID, projected.Sequence, events...)
		if errors.Is(err, event.ErrAppendConflict) {
			continue
		}
		if err != nil {
			return err
		}
		return s.index(ctx, taskID)
	}
}

func (s *Scheduler) appendExecutionTerminal(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, events ...event.Event) error {
	for {
		projected, err := s.store.ProjectWithSequence(ctx, taskID)
		if err != nil {
			return err
		}
		state := projected.State
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return nil
		}
		err = s.store.AppendExpected(ctx, taskID, projected.Sequence, events...)
		if errors.Is(err, event.ErrAppendConflict) {
			continue
		}
		if err != nil {
			return err
		}
		return s.index(ctx, taskID)
	}
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
	return s.store.Append(ctx, after.Task.ID, events...)
}

func (s *Scheduler) blockStep(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, stepID coretask.StepID, reason string) error {
	err := s.store.Append(ctx, taskID,
		coretask.StepStatusChanged{TaskID: taskID, ExecutionID: execID, StepID: stepID, Current: coretask.StepStatusBlocked, Reason: reason},
		coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: execID, Reason: reason},
		coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusBlocked, Reason: reason},
	)
	if err != nil {
		return err
	}
	return s.index(ctx, taskID)
}

func (s *Scheduler) blockTask(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID, reason string) error {
	err := s.store.Append(ctx, taskID,
		coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: execID, Reason: reason},
		coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusBlocked, Reason: reason},
	)
	if err != nil {
		return err
	}
	return s.index(ctx, taskID)
}

func (s *Scheduler) index(ctx context.Context, taskID coretask.ID) error {
	state, err := s.store.Project(ctx, taskID)
	if err != nil {
		return err
	}
	return s.store.Index(ctx, summary(state.Task))
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
