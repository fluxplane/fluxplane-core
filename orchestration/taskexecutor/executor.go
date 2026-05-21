// Package taskexecutor schedules event-sourced tasks onto worker sessions.
package taskexecutor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	coresession "github.com/fluxplane/engine/core/session"
	coretask "github.com/fluxplane/engine/core/task"
	corethread "github.com/fluxplane/engine/core/thread"
	clientapi "github.com/fluxplane/engine/orchestration/client"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	runtimetask "github.com/fluxplane/engine/runtime/task"
)

const (
	defaultReconcileInterval = 2 * time.Second
	defaultMaxParallel       = 2
	defaultLeaseDuration     = 30 * time.Minute
	defaultWorker            = "worker"
	defaultExplorer          = "explorer"
	defaultReviewer          = "reviewer"
	finalizerStepID          = coretask.StepID("task-finalize")
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
	Profiles    []string
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
	WorkerPools       map[coretask.Role]WorkerPoolConfig
	WorkerID          string
	LeaseDuration     time.Duration
	LeaseHeartbeat    time.Duration
	MaxAttempts       int
	RuntimeEvents     RuntimeEventPublisher
	OnError           func(error)
	Now               func() time.Time
	NewID             func(string) string
}

// WorkerPoolConfig configures profile selection and capacity for a role.
type WorkerPoolConfig struct {
	Profiles    []string
	MaxParallel int
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
	workerPools       map[coretask.Role]workerPool
	runtimeEvents     RuntimeEventPublisher
	onError           func(error)
	now               func() time.Time
	newID             func(string) string
	workerID          string
	leaseDuration     time.Duration
	leaseHeartbeat    time.Duration
	maxAttempts       int

	mu          sync.Mutex
	enabled     bool
	active      bool
	ctx         context.Context
	running     map[coretask.ID]runningTask
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
	if cfg.WorkerID == "" {
		cfg.WorkerID = cfg.NewID("scheduler_")
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = defaultLeaseDuration
	}
	if cfg.LeaseHeartbeat <= 0 {
		cfg.LeaseHeartbeat = cfg.LeaseDuration / 3
	}
	if cfg.LeaseHeartbeat <= 0 {
		cfg.LeaseHeartbeat = time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	return &Scheduler{
		store:             cfg.Store,
		worker:            cfg.Worker,
		reconcileInterval: cfg.ReconcileInterval,
		maxParallel:       cfg.MaxParallel,
		roleProfiles:      cloneRoleProfiles(cfg.RoleProfiles),
		workerPools:       cloneWorkerPools(cfg.RoleProfiles, cfg.WorkerPools, cfg.MaxParallel),
		runtimeEvents:     cfg.RuntimeEvents,
		onError:           cfg.OnError,
		now:               cfg.Now,
		newID:             cfg.NewID,
		workerID:          cfg.WorkerID,
		leaseDuration:     cfg.LeaseDuration,
		leaseHeartbeat:    cfg.LeaseHeartbeat,
		maxAttempts:       cfg.MaxAttempts,
		enabled:           true,
		running:           map[coretask.ID]runningTask{},
	}, nil
}

type runningTask struct {
	Role    coretask.Role
	Profile string
}

type workerPool struct {
	Profiles    []string
	MaxParallel int
	Next        int
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
		if err := s.registerWorker(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.recordError("task_worker_register_failed", err)
		}
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
			if summary.ID != "" && summary.Status == coretask.StatusRunning {
				if err := s.reconcileRecoverableExecution(ctx, summary.ID); err != nil {
					s.recordError("task_lease_reconcile_failed", err)
				}
			}
			continue
		}
		if !s.reserve(summary.ID, firstRole(summary.Assignee, coretask.RoleDeveloper)) {
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
		s.appendSchedulerDiagnostic(ctx, taskID, "", "", "task_auto_schedule_disabled", "ready task was not auto-scheduled because automatic task scheduling is disabled; use task_run to start it manually")
		return nil
	}
	submitted, err := s.submitTask(ctx, taskID)
	if err == nil && !submitted.Started && !submitted.Running && submitted.Status == coretask.StatusReady {
		message := submitted.Summary
		if strings.TrimSpace(message) == "" {
			message = "ready task was not auto-scheduled"
		}
		s.appendSchedulerDiagnostic(ctx, taskID, "", "", "task_auto_schedule_deferred", message)
	}
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
		if state.Task.Status == coretask.StatusRunning {
			result.Running = true
			result.Summary = fmt.Sprintf("Task %s is already running in background.", taskID)
			return result, nil
		}
		result.Summary = fmt.Sprintf("Task %s is %s, not ready.", taskID, state.Task.Status)
		return result, nil
	}
	role := firstRole(state.Task.Assignee, coretask.RoleDeveloper)
	if !s.reserve(taskID, role) {
		result.Running = s.isRunning(taskID)
		if result.Running {
			result.Summary = fmt.Sprintf("Task %s is already running.", taskID)
		} else {
			result.Summary = fmt.Sprintf("Task %s is ready but waiting for scheduler capacity.", taskID)
		}
		return result, nil
	}
	runCtx := s.runContext(ctx)
	if err := s.registerWorker(runCtx); err != nil {
		s.release(taskID)
		return SubmitResult{}, err
	}
	go func() {
		defer s.release(taskID)
		if err := s.RunTask(runCtx, taskID); err != nil && !errors.Is(err, context.Canceled) {
			s.recordError("task_run_failed", err)
		}
	}()
	result.Started = true
	result.Running = true
	result.Summary = fmt.Sprintf("Task %s scheduled and running in background.", taskID)
	return result, nil
}

// Status reports scheduler runtime state.
func (s *Scheduler) Status() coretask.SchedulerStatusResult {
	s.mu.Lock()
	running := make([]coretask.ID, 0, len(s.running))
	runningByRole := map[coretask.Role][]coretask.ID{}
	for id, task := range s.running {
		running = append(running, id)
		runningByRole[task.Role] = append(runningByRole[task.Role], id)
	}
	sort.Slice(running, func(i, j int) bool { return running[i] < running[j] })
	for role := range runningByRole {
		sort.Slice(runningByRole[role], func(i, j int) bool { return runningByRole[role][i] < runningByRole[role][j] })
	}
	capacityByRole := map[coretask.Role]int{}
	maxByRole := map[coretask.Role]int{}
	profilesByRole := map[coretask.Role][]string{}
	for role, pool := range s.workerPools {
		maxByRole[role] = pool.MaxParallel
		capacityByRole[role] = pool.MaxParallel - s.runningForRoleLocked(role)
		profilesByRole[role] = append([]string(nil), pool.Profiles...)
	}
	result := coretask.SchedulerStatusResult{
		Enabled:           s.enabled,
		Active:            s.active,
		Running:           running,
		RunningByRole:     runningByRole,
		Capacity:          s.maxParallel - len(s.running),
		MaxParallel:       s.maxParallel,
		CapacityByRole:    capacityByRole,
		MaxParallelByRole: maxByRole,
		ProfilesByRole:    profilesByRole,
		ReconcileInterval: s.reconcileInterval.String(),
		Diagnostics:       append([]coretask.Diagnostic(nil), s.diagnostics...),
	}
	now := s.now
	s.mu.Unlock()
	queued, leases, workers, err := s.durableSchedulerState(context.Background(), now())
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, coretask.Diagnostic{Code: "task_scheduler_status_state_failed", Message: err.Error()})
	} else {
		result.Queued = queued
		result.Leases = leases
		result.Workers = mergeWorkerStatuses(workers, s.localWorkerStatus(now()))
	}
	return result
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

func (s *Scheduler) reserve(taskID coretask.ID, role coretask.Role) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.running) >= s.maxParallel {
		return false
	}
	if _, exists := s.running[taskID]; exists {
		return false
	}
	role = firstRole(role, coretask.RoleDeveloper)
	pool := s.workerPools[role]
	if pool.MaxParallel > 0 && s.runningForRoleLocked(role) >= pool.MaxParallel {
		return false
	}
	profile := firstNonEmpty(firstProfile(pool.Profiles), s.roleProfiles[role], defaultWorker)
	s.running[taskID] = runningTask{Role: role, Profile: profile}
	return true
}

func (s *Scheduler) runningForRoleLocked(role coretask.Role) int {
	var count int
	for _, task := range s.running {
		if task.Role == role {
			count++
		}
	}
	return count
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

func (s *Scheduler) applyLease(exec *coretask.Execution) {
	if exec == nil {
		return
	}
	now := s.now()
	exec.WorkerID = s.workerID
	exec.LeaseID = s.newID("lease_")
	exec.LeaseExpiresAt = now.Add(s.leaseDuration)
}

func (s *Scheduler) reconcileRecoverableExecution(ctx context.Context, taskID coretask.ID) error {
	if s.isRunning(taskID) {
		return nil
	}
	workers, err := s.store.ListWorkers(ctx)
	if err != nil {
		return err
	}
	appended, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[state.CurrentExecution]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution == "" || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		reason, diagnosticCode, recoverable := executionRecoveryReason(exec, workers, s.now())
		if !recoverable {
			return false, nil
		}
		if executionAttempt(exec) < s.maxAttempts {
			events := retryStepEvents(taskID, exec.ID, exec)
			events = append(events,
				coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: exec.ID, Reason: reason},
				coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusReady, Reason: reason + "; retry scheduled"},
				coretask.SchedulerDiagnostic{TaskID: taskID, ExecutionID: exec.ID, Diagnostic: coretask.Diagnostic{Code: diagnosticCode + "_requeued", Message: reason + "; retry scheduled", Target: string(exec.ID)}},
			)
			return true, events
		}
		return true, []event.Event{
			coretask.ExecutionInterrupted{TaskID: taskID, ExecutionID: exec.ID, Reason: reason},
			coretask.StatusChanged{TaskID: taskID, Previous: coretask.StatusRunning, Current: coretask.StatusInterrupted, Reason: reason},
			coretask.SchedulerDiagnostic{TaskID: taskID, ExecutionID: exec.ID, Diagnostic: coretask.Diagnostic{Code: diagnosticCode, Message: reason, Target: string(exec.ID)}},
		}
	})
	if err != nil || !appended {
		return err
	}
	return nil
}

func executionRecoveryReason(exec coretask.Execution, workers []coretask.WorkerStatus, now time.Time) (string, string, bool) {
	workerID := firstNonEmpty(exec.WorkerID, "unknown")
	if !exec.LeaseExpiresAt.IsZero() && !exec.LeaseExpiresAt.After(now) {
		return fmt.Sprintf("task execution lease expired for worker %s", workerID), "task_execution_lease_expired", true
	}
	if workerRegistrationExpired(exec.WorkerID, workers, now) {
		return fmt.Sprintf("task execution worker registration expired for worker %s", workerID), "task_execution_worker_expired", true
	}
	return "", "", false
}

func workerRegistrationExpired(workerID string, workers []coretask.WorkerStatus, now time.Time) bool {
	if strings.TrimSpace(workerID) == "" {
		return false
	}
	for _, worker := range workers {
		if worker.WorkerID != workerID {
			continue
		}
		return !worker.LeaseExpiresAt.IsZero() && !worker.LeaseExpiresAt.After(now)
	}
	return false
}

func (s *Scheduler) runWorkerStep(ctx context.Context, req StepRunRequest) (StepRunResult, error) {
	if req.Task.ID == "" || req.ExecutionID == "" {
		return s.worker.RunStep(ctx, req)
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		workerInterval := s.reconcileInterval
		if workerInterval <= 0 {
			workerInterval = defaultReconcileInterval
		}
		workerTicker := time.NewTicker(workerInterval)
		defer workerTicker.Stop()
		var leaseTicker *time.Ticker
		var leaseC <-chan time.Time
		if s.leaseHeartbeat > 0 {
			leaseTicker = time.NewTicker(s.leaseHeartbeat)
			leaseC = leaseTicker.C
			defer leaseTicker.Stop()
		}
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-workerTicker.C:
				if err := s.registerWorker(heartbeatCtx); err != nil && !errors.Is(err, context.Canceled) {
					s.recordError("task_worker_register_failed", err)
				}
			case <-leaseC:
				if err := s.renewExecutionLease(heartbeatCtx, req.Task.ID, req.ExecutionID); err != nil && !errors.Is(err, context.Canceled) {
					s.recordError("task_lease_renew_failed", err)
				}
			}
		}
	}()
	result, err := s.worker.RunStep(ctx, req)
	cancel()
	<-done
	return result, err
}

func (s *Scheduler) renewExecutionLease(ctx context.Context, taskID coretask.ID, execID coretask.ExecutionID) error {
	_, err := s.withExpectedRetry(ctx, taskID, func(state runtimetask.State) (bool, []event.Event) {
		exec, ok := state.Executions[execID]
		if state.Task.Status != coretask.StatusRunning || state.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		if exec.WorkerID != "" && exec.WorkerID != s.workerID {
			return false, nil
		}
		if exec.LeaseID == "" {
			return false, nil
		}
		return true, []event.Event{coretask.ExecutionLeaseRenewed{
			TaskID:         taskID,
			ExecutionID:    execID,
			WorkerID:       s.workerID,
			LeaseID:        exec.LeaseID,
			LeaseExpiresAt: s.now().Add(s.leaseDuration),
		}}
	})
	return err
}

func (s *Scheduler) registerWorker(ctx context.Context) error {
	return s.store.RegisterWorker(ctx, s.localWorkerStatus(s.now()))
}

func (s *Scheduler) localWorkerStatus(now time.Time) coretask.WorkerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	capacityByRole := map[coretask.Role]int{}
	maxByRole := map[coretask.Role]int{}
	profilesByRole := map[coretask.Role][]string{}
	for role, pool := range s.workerPools {
		maxByRole[role] = pool.MaxParallel
		capacityByRole[role] = pool.MaxParallel - s.runningForRoleLocked(role)
		profilesByRole[role] = append([]string(nil), pool.Profiles...)
	}
	return coretask.WorkerStatus{
		WorkerID:          s.workerID,
		RegisteredAt:      now,
		LeaseExpiresAt:    now.Add(2 * s.reconcileInterval),
		Active:            true,
		Capacity:          s.maxParallel - len(s.running),
		MaxParallel:       s.maxParallel,
		CapacityByRole:    capacityByRole,
		MaxParallelByRole: maxByRole,
		ProfilesByRole:    profilesByRole,
	}
}

func mergeWorkerStatuses(stored []coretask.WorkerStatus, local coretask.WorkerStatus) []coretask.WorkerStatus {
	latest := make(map[string]coretask.WorkerStatus, len(stored)+1)
	for _, worker := range stored {
		if strings.TrimSpace(worker.WorkerID) == "" {
			continue
		}
		latest[worker.WorkerID] = cloneWorkerStatus(worker)
	}
	if strings.TrimSpace(local.WorkerID) != "" {
		latest[local.WorkerID] = cloneWorkerStatus(local)
	}
	out := make([]coretask.WorkerStatus, 0, len(latest))
	for _, worker := range latest {
		out = append(out, worker)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkerID < out[j].WorkerID })
	return out
}

func (s *Scheduler) durableSchedulerState(ctx context.Context, now time.Time) ([]coretask.ID, []coretask.ExecutionLease, []coretask.WorkerStatus, error) {
	summaries, err := s.store.List(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var queued []coretask.ID
	var leases []coretask.ExecutionLease
	for _, summary := range summaries {
		if summary.ID == "" {
			continue
		}
		if summary.Status == coretask.StatusReady {
			queued = append(queued, summary.ID)
			continue
		}
		if summary.Status != coretask.StatusRunning {
			continue
		}
		state, err := s.store.Project(ctx, summary.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		exec := state.Executions[state.CurrentExecution]
		if exec.ID == "" || exec.LeaseID == "" {
			continue
		}
		leases = append(leases, coretask.ExecutionLease{
			TaskID:         state.Task.ID,
			ExecutionID:    exec.ID,
			Status:         exec.Status,
			WorkerID:       exec.WorkerID,
			LeaseID:        exec.LeaseID,
			LeaseExpiresAt: exec.LeaseExpiresAt,
			Expired:        !exec.LeaseExpiresAt.IsZero() && !exec.LeaseExpiresAt.After(now),
		})
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].TaskID == leases[j].TaskID {
			return leases[i].ExecutionID < leases[j].ExecutionID
		}
		return leases[i].TaskID < leases[j].TaskID
	})
	sort.Slice(queued, func(i, j int) bool { return queued[i] < queued[j] })
	workers, err := s.store.ListWorkers(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	for i := range workers {
		workers[i].Active = workers[i].LeaseExpiresAt.IsZero() || workers[i].LeaseExpiresAt.After(now)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].WorkerID < workers[j].WorkerID })
	return queued, leases, workers, nil
}

func executionAttempt(exec coretask.Execution) int {
	if exec.Attempt <= 0 {
		return 1
	}
	return exec.Attempt
}

func nextAttempt(attempt int) int {
	if attempt <= 0 {
		return 2
	}
	return attempt + 1
}

func retryStepEvents(taskID coretask.ID, execID coretask.ExecutionID, exec coretask.Execution) []event.Event {
	events := make([]event.Event, 0, len(exec.Steps))
	for stepID, step := range exec.Steps {
		if step.Status != coretask.StepStatusRunning && step.Status != coretask.StepStatusBlocked {
			continue
		}
		events = append(events, coretask.StepStatusChanged{
			TaskID:      taskID,
			ExecutionID: execID,
			StepID:      stepID,
			Previous:    step.Status,
			Current:     coretask.StepStatusWaiting,
			Reason:      "execution lease expired; step will be retried",
		})
	}
	return events
}

// RunTask claims and runs one ready task synchronously.
func (s *Scheduler) RunTask(ctx context.Context, taskID coretask.ID) error {
	if err := s.registerWorker(ctx); err != nil {
		return err
	}
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
		exec.Attempt = nextAttempt(exec.Attempt)
		s.applyLease(&exec)
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
	now := s.now()
	exec := coretask.Execution{
		ID:             execID,
		TaskID:         state.Task.ID,
		Status:         coretask.StatusRunning,
		WorkflowRef:    state.Task.WorkflowRef,
		Workflow:       state.Task.Workflow,
		StartedAt:      now,
		Attempt:        1,
		WorkerID:       s.workerID,
		LeaseID:        s.newID("lease_"),
		LeaseExpiresAt: now.Add(s.leaseDuration),
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
	profiles, blocked := s.resolveProfiles(state.Task, step)
	if blocked {
		return s.blockTask(ctx, state.Task.ID, execID, "task is assigned to a human")
	}
	result, err := s.runWorkerStep(ctx, StepRunRequest{
		Task:        state.Task,
		ExecutionID: execID,
		Step:        step,
		Profile:     firstProfile(profiles),
		Profiles:    profiles,
		ExternalID:  string(execID) + ":task",
	})
	if err != nil {
		opErr := &operation.Error{Code: "task_worker_failed", Message: err.Error()}
		if appendErr := s.appendExecutionTerminal(ctx, state.Task.ID, execID, coretask.ExecutionFailed{TaskID: state.Task.ID, ExecutionID: execID, Error: opErr}); appendErr != nil {
			return appendErr
		}
		return err
	}
	result, artifacts := normalizeWorkerResult(ctx, result, step.Outputs, execID, "")
	events := artifactEvents(state.Task.ID, execID, "", artifacts)
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
				if missing := missingRequiredOutputs(state, validation); len(missing) > 0 {
					finalized, err := s.finalizeMissingOutputs(ctx, state, missing)
					if err != nil {
						return err
					}
					if finalized {
						continue
					}
				}
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
	profiles, blocked := s.resolveProfiles(task, step)
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
		Profile:  firstProfile(profiles), ExternalID: externalID,
	})
	if err != nil {
		return err
	}
	if !dispatched {
		return nil
	}
	result, err := s.runWorkerStep(ctx, StepRunRequest{Task: task, ExecutionID: execID, Step: step, Profile: firstProfile(profiles), Profiles: profiles, ExternalID: externalID})
	if err != nil {
		return s.appendStepTerminal(ctx, task.ID, execID, step.ID, coretask.StepFailed{
			TaskID: task.ID, ExecutionID: execID, StepID: step.ID,
			Error: &operation.Error{Code: "task_worker_failed", Message: err.Error()},
		})
	}
	result, artifacts := normalizeWorkerResult(ctx, result, step.Outputs, execID, step.ID)
	events := artifactEvents(task.ID, execID, step.ID, artifacts)
	events = append(events, coretask.StepCompleted{TaskID: task.ID, ExecutionID: execID, StepID: step.ID, Output: result.Output})
	return s.appendStepTerminal(ctx, task.ID, execID, step.ID, events...)
}

func (s *Scheduler) finalizeMissingOutputs(ctx context.Context, state runtimetask.State, missing []coretask.ArtifactSpec) (bool, error) {
	execID := state.CurrentExecution
	step := coretask.Step{
		ID:                 finalizerStepID,
		Title:              "Finalize task outputs",
		Objective:          "Produce missing required task-level outputs from completed step evidence.",
		Description:        finalizerDescription(state, missing),
		AcceptanceCriteria: append([]string(nil), state.Task.AcceptanceCriteria...),
		Inputs:             finalizerInputs(state),
		Outputs:            cloneArtifacts(missing),
		Assignee:           state.Task.Assignee,
		Scope:              append([]string(nil), state.Task.Scope...),
	}
	profiles, blocked := s.resolveProfiles(state.Task, step)
	if blocked {
		return false, nil
	}
	task := state.Task
	task.Outputs = nil
	s.appendSchedulerDiagnostic(ctx, state.Task.ID, execID, "", "task_finalizing_outputs", fmt.Sprintf("all steps are terminal; producing %d missing required task output(s)", len(missing)))
	result, err := s.runWorkerStep(ctx, StepRunRequest{
		Task:        task,
		ExecutionID: execID,
		Step:        step,
		Profile:     firstProfile(profiles),
		Profiles:    profiles,
		ExternalID:  string(execID) + ":" + string(finalizerStepID),
	})
	if err != nil {
		return false, s.appendExecutionTerminal(ctx, state.Task.ID, execID, coretask.ExecutionFailed{
			TaskID:      state.Task.ID,
			ExecutionID: execID,
			Error:       &operation.Error{Code: "task_finalizer_failed", Message: err.Error()},
		})
	}
	_, artifacts := normalizeWorkerResult(ctx, result, missing, execID, "")
	events := artifactEvents(state.Task.ID, execID, "", artifacts)
	if len(events) == 0 {
		return false, nil
	}
	appended, err := s.withExpectedRetry(ctx, state.Task.ID, func(current runtimetask.State) (bool, []event.Event) {
		exec, ok := current.Executions[execID]
		if current.Task.Status != coretask.StatusRunning || current.CurrentExecution != execID || !ok || exec.Status != coretask.StatusRunning {
			return false, nil
		}
		if !runtimetask.AllStepsTerminal(current) {
			return false, nil
		}
		return true, events
	})
	if err == nil && !appended {
		s.appendSchedulerDiagnostic(ctx, state.Task.ID, execID, "", "task_stale_finalizer_result_ignored", "finalizer output ignored because the task execution is no longer running")
	}
	return appended, err
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
	destinations := taskRuntimeDestinations(task)
	if len(destinations) == 0 {
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
		for _, destination := range destinations {
			if err := publisher.PublishRuntimeEvent(publishCtx, destination.thread, destination.runID, payload); err != nil {
				s.recordError("task_runtime_event_publish_failed", err)
				continue
			}
		}
	}
}

type taskRuntimeDestination struct {
	thread corethread.Ref
	runID  clientapi.RunID
}

func taskRuntimeDestinations(task coretask.Task) []taskRuntimeDestination {
	destinations := make([]taskRuntimeDestination, 0, 2)
	seen := map[string]bool{}
	add := func(threadKey, branchKey, runKey string) {
		thread, runID, ok := taskRuntimeDestinationFromMetadata(task, threadKey, branchKey, runKey)
		if !ok {
			return
		}
		key := string(thread.ID) + "\x00" + string(thread.BranchID) + "\x00" + string(runID)
		if seen[key] {
			return
		}
		seen[key] = true
		destinations = append(destinations, taskRuntimeDestination{thread: thread, runID: runID})
	}
	add(coretask.MetadataOriginThreadID, coretask.MetadataOriginBranchID, coretask.MetadataOriginRunID)
	add(coretask.MetadataWatchThreadID, coretask.MetadataWatchBranchID, coretask.MetadataWatchRunID)
	return destinations
}

func taskRuntimeDestinationFromMetadata(task coretask.Task, threadKey, branchKey, runKey string) (corethread.Ref, clientapi.RunID, bool) {
	threadID := strings.TrimSpace(task.Metadata[threadKey])
	if threadID == "" {
		return corethread.Ref{}, "", false
	}
	thread := corethread.Ref{
		ID:       corethread.ID(threadID),
		BranchID: corethread.BranchID(strings.TrimSpace(task.Metadata[branchKey])),
	}
	if thread.BranchID == "" {
		thread.BranchID = corethread.MainBranch
	}
	return thread, clientapi.RunID(strings.TrimSpace(task.Metadata[runKey])), true
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

func normalizeWorkerResult(ctx context.Context, result StepRunResult, outputs []coretask.ArtifactSpec, execID coretask.ExecutionID, stepID coretask.StepID) (StepRunResult, []coretask.ArtifactSpec) {
	original := result
	result.Output = normalizeWorkerOutput(ctx, result.Output, execID, stepID)
	artifacts := bindDeclaredOutputs(original, outputs)
	for i := range artifacts {
		artifacts[i] = normalizeWorkerArtifact(ctx, artifacts[i], execID, stepID, i)
	}
	return result, artifacts
}

func normalizeWorkerOutput(ctx context.Context, value operation.Value, execID coretask.ExecutionID, stepID coretask.StepID) operation.Value {
	if value == nil {
		return nil
	}
	_, replacement := replaceLargeWorkerValue(ctx, value, execID, stepID, "output")
	if replacement == nil {
		return value
	}
	return replacement.ModelText()
}

func normalizeWorkerArtifact(ctx context.Context, artifact coretask.ArtifactSpec, execID coretask.ExecutionID, stepID coretask.StepID, index int) coretask.ArtifactSpec {
	if artifact.Value == nil {
		return artifact
	}
	_, replacement := replaceLargeWorkerValue(ctx, artifact.Value, execID, stepID, fmt.Sprintf("artifact-%d", index))
	if replacement == nil {
		return artifact
	}
	if artifact.Kind == "" {
		artifact.Kind = coretask.ArtifactReference
	}
	artifact.Ref = replacement.Path
	artifact.Value = replacement.Preview
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]string{}
	}
	artifact.Metadata["replaced"] = "true"
	artifact.Metadata["replacement_kind"] = replacement.Kind
	artifact.Metadata["replacement_path"] = replacement.Path
	artifact.Metadata["replacement_size_bytes"] = fmt.Sprintf("%d", replacement.SizeBytes)
	artifact.Metadata["replacement_threshold_bytes"] = fmt.Sprintf("%d", replacement.ThresholdBytes)
	artifact.Metadata["replacement_digest"] = replacement.Digest
	artifact.Metadata["replacement_media_type"] = replacement.MediaType
	artifact.Metadata["replacement_preview"] = replacement.Preview
	if replacement.Tail != "" {
		artifact.Metadata["replacement_tail"] = replacement.Tail
	}
	if replacement.OmittedBytes > 0 {
		artifact.Metadata["replacement_omitted_bytes"] = fmt.Sprintf("%d", replacement.OmittedBytes)
	}
	return artifact
}

func replaceLargeWorkerValue(ctx context.Context, value operation.Value, execID coretask.ExecutionID, stepID coretask.StepID, suffix string) (operation.Value, *operationruntime.ResultReplacement) {
	text := workerValueText(value)
	result := operation.OK(operation.Rendered{Text: text, Model: text, Data: value})
	_, replacement, err := operationruntime.ReplaceLargeResult(ctx, result, operationruntime.ReplacementOptions{
		Operation: operation.Ref{Name: "task_worker_output"},
		CallID:    operation.CallID(workerReplacementCallID(execID, stepID, suffix)),
	})
	if err != nil || replacement == nil {
		return value, nil
	}
	return replacement.ModelText(), replacement
}

func workerReplacementCallID(execID coretask.ExecutionID, stepID coretask.StepID, suffix string) string {
	parts := []string{string(execID)}
	if stepID != "" {
		parts = append(parts, string(stepID))
	}
	if suffix != "" {
		parts = append(parts, suffix)
	}
	return strings.Join(parts, ":")
}

func workerValueText(value operation.Value) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
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

func missingRequiredOutputs(state runtimetask.State, validation coretask.TaskValidationResult) []coretask.ArtifactSpec {
	if validation.Completable {
		return nil
	}
	produced := scopedArtifacts(state)
	var missing []coretask.ArtifactSpec
	for _, output := range state.Task.Outputs {
		if !output.Required || scopedArtifactSatisfied(output, produced) {
			continue
		}
		missing = append(missing, output)
	}
	return missing
}

func finalizerInputs(state runtimetask.State) []coretask.ArtifactSpec {
	var inputs []coretask.ArtifactSpec
	exec, ok := state.Executions[state.CurrentExecution]
	if !ok {
		return nil
	}
	for _, step := range state.Task.Steps {
		stepExec := exec.Steps[step.ID]
		for _, artifact := range stepExec.Artifacts {
			input := artifact
			if input.ID == "" {
				input.ID = string(step.ID)
			}
			if input.Name == "" {
				input.Name = firstNonEmpty(step.Title, string(step.ID))
			}
			if input.Description == "" {
				input.Description = "Output from completed step " + firstNonEmpty(step.Title, string(step.ID))
			}
			input.Required = false
			inputs = append(inputs, input)
		}
		if stepExec.Output != nil {
			inputs = append(inputs, coretask.ArtifactSpec{
				ID:          string(step.ID) + "-output",
				Name:        firstNonEmpty(step.Title, string(step.ID)) + " output",
				Kind:        coretask.ArtifactText,
				Description: "Final response from completed step " + firstNonEmpty(step.Title, string(step.ID)),
				Value:       stepExec.Output,
			})
		}
	}
	return inputs
}

func finalizerDescription(state runtimetask.State, missing []coretask.ArtifactSpec) string {
	var b strings.Builder
	b.WriteString("All declared task steps are terminal. Create the missing required task-level output artifacts from the completed step evidence. Do not rerun completed steps.\n\nMissing required outputs:\n")
	writeArtifacts(&b, missing)
	if exec, ok := state.Executions[state.CurrentExecution]; ok {
		b.WriteString("\nCompleted step evidence:\n")
		for _, step := range state.Task.Steps {
			stepExec := exec.Steps[step.ID]
			fmt.Fprintf(&b, "- %s: %s", firstNonEmpty(step.Title, string(step.ID)), stepExec.Status)
			if stepExec.Output != nil {
				fmt.Fprintf(&b, "; output=%s", compactWorkerEvidence(workerValueText(stepExec.Output), 240))
			}
			if len(stepExec.Artifacts) > 0 {
				b.WriteString("; artifacts=")
				for i, artifact := range stepExec.Artifacts {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(firstNonEmpty(artifact.ID, artifact.Name, string(artifact.Kind), "artifact"))
				}
			}
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func compactWorkerEvidence(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
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
	var missing []string
	for _, check := range validation.Checks {
		if check.OK {
			continue
		}
		target := firstNonEmpty(check.Target, check.Message, check.Code)
		if check.Code == "required_output" {
			missing = append(missing, target)
			continue
		}
		if len(missing) > 0 {
			return fmt.Sprintf("task completion blocked: missing required outputs %s; also failed %s", strings.Join(missing, ", "), target)
		}
		return fmt.Sprintf("task completion blocked: %s", target)
	}
	if len(missing) > 0 {
		return fmt.Sprintf("task completion blocked: missing required outputs %s", strings.Join(missing, ", "))
	}
	return "task completion blocked: validation did not pass"
}

func (s *Scheduler) resolveProfiles(task coretask.Task, step coretask.Step) ([]string, bool) {
	if strings.TrimSpace(step.Profile) != "" {
		return []string{strings.TrimSpace(step.Profile)}, false
	}
	role := firstRole(step.Assignee, task.Assignee)
	switch role {
	case coretask.RoleHuman:
		return nil, true
	}
	profiles := s.nextProfiles(role)
	if len(profiles) == 0 {
		profiles = []string{defaultWorker}
	}
	return profiles, false
}

func (s *Scheduler) nextProfiles(role coretask.Role) []string {
	role = firstRole(role, coretask.RoleDeveloper)
	s.mu.Lock()
	defer s.mu.Unlock()
	pool := s.workerPools[role]
	if len(pool.Profiles) == 0 {
		if profile := strings.TrimSpace(s.roleProfiles[role]); profile != "" {
			return []string{profile}
		}
		return []string{defaultWorker}
	}
	start := pool.Next % len(pool.Profiles)
	pool.Next = (pool.Next + 1) % len(pool.Profiles)
	s.workerPools[role] = pool
	out := make([]string, 0, len(pool.Profiles))
	for i := range pool.Profiles {
		out = append(out, pool.Profiles[(start+i)%len(pool.Profiles)])
	}
	return out
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

func cloneWorkerPools(roleProfiles map[coretask.Role]string, in map[coretask.Role]WorkerPoolConfig, globalMax int) map[coretask.Role]workerPool {
	out := map[coretask.Role]workerPool{}
	roles := []coretask.Role{coretask.RoleDeveloper, coretask.RoleTester, coretask.RoleExplorer, coretask.RoleReviewer}
	defaults := cloneRoleProfiles(roleProfiles)
	for _, role := range roles {
		profile := firstNonEmpty(defaults[role], defaultWorker)
		out[role] = workerPool{Profiles: []string{profile}, MaxParallel: globalMax}
	}
	for role, cfg := range in {
		profiles := compactProfiles(cfg.Profiles)
		if len(profiles) == 0 {
			profiles = out[role].Profiles
		}
		maxParallel := cfg.MaxParallel
		if maxParallel <= 0 {
			maxParallel = globalMax
		}
		out[role] = workerPool{Profiles: profiles, MaxParallel: maxParallel}
	}
	return out
}

func compactProfiles(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, profile := range in {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			continue
		}
		if _, exists := seen[profile]; exists {
			continue
		}
		seen[profile] = struct{}{}
		out = append(out, profile)
	}
	return out
}

func firstProfile(profiles []string) string {
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0]
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

func cloneRoleIntMap(in map[coretask.Role]int) map[coretask.Role]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[coretask.Role]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRoleProfilesMap(in map[coretask.Role][]string) map[coretask.Role][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[coretask.Role][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func cloneWorkerStatus(in coretask.WorkerStatus) coretask.WorkerStatus {
	out := in
	out.CapacityByRole = cloneRoleIntMap(in.CapacityByRole)
	out.MaxParallelByRole = cloneRoleIntMap(in.MaxParallelByRole)
	out.ProfilesByRole = cloneRoleProfilesMap(in.ProfilesByRole)
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
	profiles := compactProfiles(req.Profiles)
	if len(profiles) == 0 {
		profiles = []string{firstNonEmpty(req.Profile, defaultWorker)}
	}
	var (
		session clientapi.SessionHandle
		profile string
		err     error
	)
	for _, candidate := range profiles {
		session, err = w.open(ctx, candidate, req)
		if err == nil {
			profile = candidate
			break
		}
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
