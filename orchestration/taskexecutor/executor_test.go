package taskexecutor

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	coretask "github.com/fluxplane/agentruntime/core/task"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

func TestSchedulerRunsReadyTaskDAG(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:       "task_1",
		Title:    "Review core",
		Status:   coretask.StatusReady,
		Assignee: coretask.RoleDeveloper,
		Outputs:  []coretask.ArtifactSpec{{ID: "report", Kind: coretask.ArtifactReport, Required: true}},
		Steps: []coretask.Step{
			{ID: "inspect", Title: "Inspect"},
			{ID: "report", Title: "Report", DependsOn: []coretask.StepID{"inspect"}, Outputs: []coretask.ArtifactSpec{{ID: "report", Kind: coretask.ArtifactReport, Required: true}}},
		},
		CreatedAt: testTime(),
	}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusCompleted {
		t.Fatalf("task status = %s, want completed", state.Task.Status)
	}
	exec := state.Executions[state.CurrentExecution]
	if exec.Steps["inspect"].Status != coretask.StepStatusCompleted || exec.Steps["report"].Status != coretask.StepStatusCompleted {
		t.Fatalf("steps = %#v, want completed", exec.Steps)
	}
	if got := len(worker.steps); got != 2 {
		t.Fatalf("worker calls = %d, want 2", got)
	}
	if worker.steps[0] != "inspect" || worker.steps[1] != "report" {
		t.Fatalf("worker order = %#v, want dependency order", worker.steps)
	}
}

func TestCompletionBlockedReasonListsMissingRequiredOutputs(t *testing.T) {
	validation := coretask.TaskValidationResult{
		Checks: []coretask.TaskCheck{
			{Code: "required_output", Target: "summary", Message: "required output summary"},
			{Code: "required_output", Target: "verification", Message: "required output verification"},
		},
	}

	got := completionBlockedReason(validation)
	for _, want := range []string{"missing required outputs", "summary", "verification"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reason = %q, missing %q", got, want)
		}
	}
}

func TestSchedulerPublishesTaskEventsToOriginThread(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Review core",
		Status: coretask.StatusReady,
		Metadata: map[string]string{
			coretask.MetadataOriginThreadID: "thread_1",
			coretask.MetadataOriginRunID:    "run_1",
		},
		Steps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	}
	createTask(t, store, task)
	publisher := &recordingRuntimePublisher{}
	scheduler := newScheduler(t, store, &recordingWorker{})
	scheduler.SetRuntimeEventPublisher(publisher)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(publisher.events) == 0 {
		t.Fatal("published events = 0, want scheduler task events")
	}
	if publisher.thread.ID != "thread_1" || publisher.runID != "run_1" {
		t.Fatalf("target = %s/%s, want origin thread/run", publisher.thread.ID, publisher.runID)
	}
	if !publisher.seen(coretask.EventExecutionStartedName) || !publisher.seen(coretask.EventStepDispatchedName) || !publisher.seen(coretask.EventStepCompletedName) {
		t.Fatalf("published names = %#v, want execution/step progress", publisher.names())
	}
}

func TestSchedulerPublishesTaskEventsToRunWatcher(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Review core",
		Status: coretask.StatusReady,
		Metadata: map[string]string{
			coretask.MetadataOriginThreadID: "thread_origin",
			coretask.MetadataOriginRunID:    "run_origin",
			coretask.MetadataWatchThreadID:  "thread_watch",
			coretask.MetadataWatchRunID:     "run_watch",
		},
		Steps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	}
	createTask(t, store, task)
	publisher := &recordingRuntimePublisher{}
	scheduler := newScheduler(t, store, &recordingWorker{})
	scheduler.SetRuntimeEventPublisher(publisher)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !publisher.targetSeen("thread_origin", "run_origin") {
		t.Fatalf("targets = %#v, want origin target", publisher.targets)
	}
	if !publisher.targetSeen("thread_watch", "run_watch") {
		t.Fatalf("targets = %#v, want watch target", publisher.targets)
	}
}

func TestSchedulerContinuesPublishingAfterDestinationFailure(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Review core",
		Status: coretask.StatusReady,
		Metadata: map[string]string{
			coretask.MetadataOriginThreadID: "thread_origin",
			coretask.MetadataOriginRunID:    "run_origin",
			coretask.MetadataWatchThreadID:  "thread_watch",
			coretask.MetadataWatchRunID:     "run_watch",
		},
		Steps: []coretask.Step{{ID: "inspect", Title: "Inspect"}},
	}
	createTask(t, store, task)
	publisher := &failingRuntimePublisher{failThread: "thread_origin"}
	scheduler := newScheduler(t, store, &recordingWorker{})
	scheduler.SetRuntimeEventPublisher(publisher)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if publisher.targetSeen("thread_origin", "run_origin") {
		t.Fatalf("targets = %#v, want origin publish failures not recorded as success", publisher.targets)
	}
	if !publisher.targetSeen("thread_watch", "run_watch") {
		t.Fatalf("targets = %#v, want watcher publish after origin failure", publisher.targets)
	}
}

func TestSchedulerDoesNotPublishTaskEventsWithoutOriginThread(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Review core", Status: coretask.StatusReady, Steps: []coretask.Step{{ID: "inspect", Title: "Inspect"}}}
	createTask(t, store, task)
	publisher := &recordingRuntimePublisher{}
	scheduler := newScheduler(t, store, &recordingWorker{})
	scheduler.SetRuntimeEventPublisher(publisher)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published events = %#v, want none without origin thread", publisher.names())
	}
}

func TestSchedulerClaimsTaskWithDurableLease(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Lease", Status: coretask.StatusReady}
	createTask(t, store, task)
	scheduler, err := New(Config{
		Store:         store,
		Worker:        &recordingWorker{},
		WorkerID:      "worker-a",
		LeaseDuration: 10 * time.Minute,
		Now:           testTime,
		NewID:         func(prefix string) string { return prefix + "test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	exec := state.Executions[state.CurrentExecution]
	if exec.WorkerID != "worker-a" || exec.LeaseID != "lease_test" || !exec.LeaseExpiresAt.Equal(testTime().Add(10*time.Minute)) {
		t.Fatalf("execution lease = worker=%q lease=%q expires=%s", exec.WorkerID, exec.LeaseID, exec.LeaseExpiresAt)
	}
}

func TestSchedulerRenewsLeaseWhileWorkerRuns(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Lease renewal", Status: coretask.StatusReady}
	createTask(t, store, task)
	base := time.Unix(1700000000, 0).UTC()
	var nowMu sync.Mutex
	now := base
	nowFunc := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		now = now.Add(10 * time.Millisecond)
		return now
	}
	block := make(chan struct{})
	worker := &blockingWorker{started: make(chan struct{}), block: block}
	scheduler, err := New(Config{
		Store:          store,
		Worker:         worker,
		WorkerID:       "worker-a",
		LeaseDuration:  30 * time.Millisecond,
		LeaseHeartbeat: time.Millisecond,
		Now:            nowFunc,
		NewID:          func(prefix string) string { return prefix + "test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	errs := make(chan error, 1)
	go func() { errs <- scheduler.RunTask(ctx, task.ID) }()
	<-worker.started

	waitForLeaseAfter(t, store, task.ID, base.Add(40*time.Millisecond))
	close(block)
	if err := <-errs; err != nil {
		t.Fatalf("RunTask: %v", err)
	}
}

func TestSchedulerInterruptsExpiredExecutionLease(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Expired", Status: coretask.StatusRunning}
	createTask(t, store, task)
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_old",
		Execution: coretask.Execution{
			ID:             "exec_old",
			TaskID:         task.ID,
			Status:         coretask.StatusRunning,
			WorkerID:       "worker-old",
			LeaseID:        "lease_old",
			LeaseExpiresAt: testTime().Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("Append execution: %v", err)
	}
	if err := store.Index(ctx, summary(task)); err != nil {
		t.Fatalf("Index running task: %v", err)
	}
	scheduler, err := New(Config{
		Store:  store,
		Worker: &recordingWorker{},
		Now:    testTime,
		NewID:  func(prefix string) string { return prefix + "test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := scheduler.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusInterrupted {
		t.Fatalf("task status = %s, want interrupted", state.Task.Status)
	}
	exec := state.Executions["exec_old"]
	if exec.Status != coretask.StatusInterrupted || exec.Error == nil || !strings.Contains(exec.Error.Message, "lease expired") {
		t.Fatalf("execution = %#v, want interrupted expired lease", exec)
	}
	if got := len(exec.Diagnostics); got != 1 || exec.Diagnostics[0].Code != "task_execution_lease_expired" {
		t.Fatalf("diagnostics = %#v, want lease expired diagnostic", exec.Diagnostics)
	}
}

func TestSchedulerRequeuesExpiredLeaseWhenAttemptRemains(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Retry",
		Status: coretask.StatusRunning,
		Steps:  []coretask.Step{{ID: "step_1", Title: "Step"}},
	}
	createTask(t, store, task)
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_old",
		Execution: coretask.Execution{
			ID:             "exec_old",
			TaskID:         task.ID,
			Status:         coretask.StatusRunning,
			Attempt:        1,
			WorkerID:       "worker-old",
			LeaseID:        "lease_old",
			LeaseExpiresAt: testTime().Add(-time.Minute),
			Steps: map[coretask.StepID]coretask.StepExecution{
				"step_1": {StepID: "step_1", Status: coretask.StepStatusRunning},
			},
		},
	}); err != nil {
		t.Fatalf("Append execution: %v", err)
	}
	if err := store.Index(ctx, summary(task)); err != nil {
		t.Fatalf("Index running task: %v", err)
	}
	scheduler, err := New(Config{
		Store:       store,
		Worker:      &recordingWorker{},
		MaxAttempts: 2,
		Now:         testTime,
		NewID:       func(prefix string) string { return prefix + "test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := scheduler.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusReady {
		t.Fatalf("task status = %s, want ready retry", state.Task.Status)
	}
	exec := state.Executions["exec_old"]
	if exec.Status != coretask.StatusInterrupted || exec.Steps["step_1"].Status != coretask.StepStatusWaiting {
		t.Fatalf("execution = %#v, want interrupted with waiting step", exec)
	}
	if got := len(exec.Diagnostics); got != 1 || exec.Diagnostics[0].Code != "task_execution_lease_expired_requeued" {
		t.Fatalf("diagnostics = %#v, want requeued diagnostic", exec.Diagnostics)
	}

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask retry: %v", err)
	}
	state, err = store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project retry: %v", err)
	}
	exec = state.Executions["exec_old"]
	if exec.Attempt != 2 || state.Task.Status != coretask.StatusCompleted {
		t.Fatalf("retry state = status %s exec %#v, want attempt 2 completed", state.Task.Status, exec)
	}
}

func TestSchedulerRestartRequeuesExpiredRunningLease(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Restart retry", Status: coretask.StatusRunning}
	createTask(t, store, task)
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_old",
		Execution: coretask.Execution{
			ID:             "exec_old",
			TaskID:         task.ID,
			Status:         coretask.StatusRunning,
			Attempt:        1,
			WorkerID:       "dead-worker",
			LeaseID:        "lease_dead",
			LeaseExpiresAt: testTime().Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("Append execution: %v", err)
	}
	if err := store.Index(ctx, summary(task)); err != nil {
		t.Fatalf("Index running task: %v", err)
	}

	// New scheduler instance simulates a local runtime restart: it has no
	// in-memory running task reservation but can recover from the task stream.
	restarted, err := New(Config{
		Store:       store,
		Worker:      &recordingWorker{},
		WorkerID:    "worker-after-restart",
		MaxAttempts: 2,
		Now:         testTime,
		NewID:       func(prefix string) string { return prefix + "restart" },
	})
	if err != nil {
		t.Fatalf("New restarted scheduler: %v", err)
	}

	if err := restarted.Tick(ctx); err != nil {
		t.Fatalf("Tick restarted scheduler: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project after restart tick: %v", err)
	}
	if state.Task.Status != coretask.StatusReady {
		t.Fatalf("task status after restart tick = %s, want ready retry", state.Task.Status)
	}

	if err := restarted.Tick(ctx); err != nil {
		t.Fatalf("Tick retry: %v", err)
	}
	waitForTaskStatus(t, store, task.ID, coretask.StatusCompleted)
	state, err = store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project completed retry: %v", err)
	}
	exec := state.Executions["exec_old"]
	if exec.Attempt != 2 || exec.WorkerID != "worker-after-restart" {
		t.Fatalf("execution after retry = %#v, want attempt 2 on restarted worker", exec)
	}
}

func TestSchedulerRestartRequeuesExpiredWorkerRegistrationBeforeExecutionLeaseExpiry(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Worker expired retry", Status: coretask.StatusRunning}
	createTask(t, store, task)
	if err := store.RegisterWorker(ctx, coretask.WorkerStatus{
		WorkerID:       "dead-worker",
		RegisteredAt:   testTime().Add(-time.Hour),
		LeaseExpiresAt: testTime().Add(-time.Minute),
		Capacity:       1,
		MaxParallel:    1,
	}); err != nil {
		t.Fatalf("RegisterWorker dead: %v", err)
	}
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_old",
		Execution: coretask.Execution{
			ID:             "exec_old",
			TaskID:         task.ID,
			Status:         coretask.StatusRunning,
			Attempt:        1,
			WorkerID:       "dead-worker",
			LeaseID:        "lease_dead",
			LeaseExpiresAt: testTime().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("Append execution: %v", err)
	}
	if err := store.Index(ctx, summary(task)); err != nil {
		t.Fatalf("Index running task: %v", err)
	}
	restarted, err := New(Config{
		Store:       store,
		Worker:      &recordingWorker{},
		WorkerID:    "worker-after-restart",
		MaxAttempts: 2,
		Now:         testTime,
		NewID:       func(prefix string) string { return prefix + "restart" },
	})
	if err != nil {
		t.Fatalf("New restarted scheduler: %v", err)
	}

	if err := restarted.Tick(ctx); err != nil {
		t.Fatalf("Tick restarted scheduler: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project after worker expiry: %v", err)
	}
	if state.Task.Status != coretask.StatusReady {
		t.Fatalf("task status after worker expiry = %s, want ready retry", state.Task.Status)
	}
	exec := state.Executions["exec_old"]
	if exec.Status != coretask.StatusInterrupted {
		t.Fatalf("execution status = %s, want interrupted retry base", exec.Status)
	}
	if got := len(exec.Diagnostics); got != 1 || exec.Diagnostics[0].Code != "task_execution_worker_expired_requeued" {
		t.Fatalf("diagnostics = %#v, want worker-expired requeue diagnostic", exec.Diagnostics)
	}

	if err := restarted.Tick(ctx); err != nil {
		t.Fatalf("Tick retry: %v", err)
	}
	waitForTaskStatus(t, store, task.ID, coretask.StatusCompleted)
	state, err = store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project completed retry: %v", err)
	}
	exec = state.Executions["exec_old"]
	if exec.Attempt != 2 || exec.WorkerID != "worker-after-restart" {
		t.Fatalf("execution after retry = %#v, want attempt 2 on restarted worker", exec)
	}
}

func TestSchedulerDoesNotRecoverActiveWorkerRegistrationBeforeExecutionLeaseExpiry(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Active worker", Status: coretask.StatusRunning}
	createTask(t, store, task)
	if err := store.RegisterWorker(ctx, coretask.WorkerStatus{
		WorkerID:       "active-worker",
		RegisteredAt:   testTime().Add(-time.Minute),
		LeaseExpiresAt: testTime().Add(time.Minute),
		Capacity:       1,
		MaxParallel:    1,
	}); err != nil {
		t.Fatalf("RegisterWorker active: %v", err)
	}
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_active",
		Execution: coretask.Execution{
			ID:             "exec_active",
			TaskID:         task.ID,
			Status:         coretask.StatusRunning,
			Attempt:        1,
			WorkerID:       "active-worker",
			LeaseID:        "lease_active",
			LeaseExpiresAt: testTime().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("Append execution: %v", err)
	}
	if err := store.Index(ctx, summary(task)); err != nil {
		t.Fatalf("Index running task: %v", err)
	}
	restarted, err := New(Config{
		Store:       store,
		Worker:      &recordingWorker{},
		WorkerID:    "worker-after-restart",
		MaxAttempts: 2,
		Now:         testTime,
		NewID:       func(prefix string) string { return prefix + "restart" },
	})
	if err != nil {
		t.Fatalf("New restarted scheduler: %v", err)
	}

	if err := restarted.Tick(ctx); err != nil {
		t.Fatalf("Tick restarted scheduler: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusRunning {
		t.Fatalf("task status = %s, want still running", state.Task.Status)
	}
	exec := state.Executions["exec_active"]
	if exec.Status != coretask.StatusRunning || exec.Attempt != 1 {
		t.Fatalf("execution = %#v, want untouched active worker execution", exec)
	}
}

func TestSchedulerStatusReportsDurableLeases(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Leased", Status: coretask.StatusRunning}
	createTask(t, store, task)
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_1",
		Execution: coretask.Execution{
			ID:             "exec_1",
			TaskID:         task.ID,
			Status:         coretask.StatusRunning,
			WorkerID:       "worker-a",
			LeaseID:        "lease_1",
			LeaseExpiresAt: testTime().Add(time.Minute),
		},
	}); err != nil {
		t.Fatalf("Append execution: %v", err)
	}
	if err := store.Index(ctx, summary(task)); err != nil {
		t.Fatalf("Index running task: %v", err)
	}
	scheduler := newScheduler(t, store, &recordingWorker{})

	status := scheduler.Status()
	if len(status.Leases) != 1 {
		t.Fatalf("leases = %#v, want one durable lease", status.Leases)
	}
	lease := status.Leases[0]
	if lease.TaskID != task.ID || lease.ExecutionID != "exec_1" || lease.WorkerID != "worker-a" || lease.LeaseID != "lease_1" || lease.Expired {
		t.Fatalf("lease = %#v, want active durable lease", lease)
	}
	if text := status.ModelText(); !strings.Contains(text, "lease task_1/exec_1") {
		t.Fatalf("model text = %q, want lease summary", text)
	}
}

func TestSchedulerStatusReportsDurableQueuedTasks(t *testing.T) {
	store := newTaskStore(t)
	createTask(t, store, coretask.Task{ID: "task_b", Title: "Queued B", Status: coretask.StatusReady})
	createTask(t, store, coretask.Task{ID: "task_a", Title: "Queued A", Status: coretask.StatusReady})
	scheduler := newScheduler(t, store, &recordingWorker{})

	status := scheduler.Status()
	if got := status.Queued; len(got) != 2 || got[0] != "task_a" || got[1] != "task_b" {
		t.Fatalf("queued = %#v, want sorted durable ready tasks", got)
	}
	if text := status.ModelText(); !strings.Contains(text, "queued: task_a") || !strings.Contains(text, "queued: task_b") {
		t.Fatalf("model text = %q, want queued task summaries", text)
	}
}

func TestSchedulerStatusReportsRegisteredWorkers(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	scheduler, err := New(Config{
		Store:       store,
		Worker:      &recordingWorker{},
		WorkerID:    "worker-local",
		MaxParallel: 3,
		WorkerPools: map[coretask.Role]WorkerPoolConfig{
			coretask.RoleReviewer: {Profiles: []string{"reviewer-a", "reviewer-b"}, MaxParallel: 1},
		},
		ReconcileInterval: time.Minute,
		Now:               testTime,
		NewID:             func(prefix string) string { return prefix + "test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := scheduler.registerWorker(ctx); err != nil {
		t.Fatalf("registerWorker: %v", err)
	}
	status := scheduler.Status()
	if len(status.Workers) != 1 {
		t.Fatalf("workers = %#v, want one registered worker", status.Workers)
	}
	worker := status.Workers[0]
	if worker.WorkerID != "worker-local" || !worker.Active || worker.Capacity != 3 || worker.MaxParallel != 3 {
		t.Fatalf("worker = %#v, want active local capacity", worker)
	}
	if got := worker.MaxParallelByRole[coretask.RoleReviewer]; got != 1 {
		t.Fatalf("reviewer max parallel = %d, want 1", got)
	}
	if got := worker.ProfilesByRole[coretask.RoleReviewer]; len(got) != 2 || got[0] != "reviewer-a" || got[1] != "reviewer-b" {
		t.Fatalf("reviewer profiles = %#v, want configured pool", got)
	}
	if text := status.ModelText(); !strings.Contains(text, "worker worker-local: active") {
		t.Fatalf("model text = %q, want worker summary", text)
	}
}

func TestSchedulerStatusMarksExpiredRegisteredWorkersInactive(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	if err := store.RegisterWorker(ctx, coretask.WorkerStatus{
		WorkerID:       "worker-old",
		RegisteredAt:   testTime().Add(-2 * time.Hour),
		LeaseExpiresAt: testTime().Add(-time.Hour),
		Capacity:       2,
		MaxParallel:    2,
	}); err != nil {
		t.Fatalf("RegisterWorker old: %v", err)
	}
	scheduler, err := New(Config{
		Store:    store,
		Worker:   &recordingWorker{},
		WorkerID: "worker-local",
		Now:      testTime,
		NewID:    func(prefix string) string { return prefix + "test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	status := scheduler.Status()
	if len(status.Workers) != 2 {
		t.Fatalf("workers = %#v, want old plus local worker", status.Workers)
	}
	if status.Workers[0].WorkerID != "worker-local" || !status.Workers[0].Active {
		t.Fatalf("first worker = %#v, want active local worker sorted first", status.Workers[0])
	}
	if status.Workers[1].WorkerID != "worker-old" || status.Workers[1].Active {
		t.Fatalf("second worker = %#v, want inactive old worker", status.Workers[1])
	}
}

func TestSchedulerBlocksHumanAssignedStep(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Human review",
		Status: coretask.StatusReady,
		Steps:  []coretask.Step{{ID: "approve", Title: "Approve", Assignee: coretask.RoleHuman}},
	}
	createTask(t, store, task)
	scheduler := newScheduler(t, store, &recordingWorker{})

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusBlocked {
		t.Fatalf("task status = %s, want blocked", state.Task.Status)
	}
	step := state.Executions[state.CurrentExecution].Steps["approve"]
	if step.Status != coretask.StepStatusBlocked {
		t.Fatalf("step status = %s, want blocked", step.Status)
	}
}

func TestSchedulerClaimSkipsAlreadyRunningTask(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Already running", Status: coretask.StatusReady, Steps: []coretask.Step{{ID: "run"}}}
	createTask(t, store, task)
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_existing",
		Execution:   coretask.Execution{ID: "exec_existing", TaskID: task.ID, Status: coretask.StatusRunning},
	}); err != nil {
		t.Fatalf("Append running execution: %v", err)
	}
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(worker.steps) != 0 {
		t.Fatalf("worker steps = %#v, want no dispatch", worker.steps)
	}
}

func TestSchedulerConcurrentClaimsCreateOneExecution(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Concurrent claim",
		Status: coretask.StatusReady,
		Steps:  []coretask.Step{{ID: "approve", Title: "Approve", Assignee: coretask.RoleHuman}},
	}
	createTask(t, store, task)
	schedulerA := newScheduler(t, store, &recordingWorker{})
	schedulerB := newScheduler(t, store, &recordingWorker{})
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, scheduler := range []*Scheduler{schedulerA, schedulerB} {
		wg.Add(1)
		go func(scheduler *Scheduler) {
			defer wg.Done()
			<-start
			errs <- scheduler.RunTask(ctx, task.ID)
		}(scheduler)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RunTask concurrent: %v", err)
		}
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(state.Executions) != 1 {
		t.Fatalf("executions = %#v, want exactly one claimed execution", state.Executions)
	}
	if state.Task.Status != coretask.StatusBlocked {
		t.Fatalf("task status = %s, want blocked", state.Task.Status)
	}
}

func TestSchedulerDoesNotOverwriteWholeTaskCancellation(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Whole task", Status: coretask.StatusReady}
	createTask(t, store, task)
	worker := &recordingWorker{
		onRun: func(req StepRunRequest) {
			if err := store.Append(ctx, req.Task.ID, coretask.StatusChanged{
				TaskID: req.Task.ID, Previous: coretask.StatusRunning, Current: coretask.StatusCancelled, Reason: "user cancelled",
			}); err != nil {
				t.Fatalf("Append cancellation: %v", err)
			}
		},
	}
	scheduler := newScheduler(t, store, worker)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusCancelled {
		t.Fatalf("task status = %s, want cancelled", state.Task.Status)
	}
	if state.Executions[state.CurrentExecution].Status == coretask.StatusCompleted {
		t.Fatalf("execution = %#v, want not completed after cancellation", state.Executions[state.CurrentExecution])
	}
	if diagnostics := state.Executions[state.CurrentExecution].Diagnostics; len(diagnostics) != 1 || diagnostics[0].Code != "task_stale_artifacts_ignored" {
		t.Fatalf("execution diagnostics = %#v, want stale artifact diagnostic", diagnostics)
	}
}

func TestSchedulerDoesNotOverwriteStepCancellation(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Step task", Status: coretask.StatusReady, Steps: []coretask.Step{{ID: "run", Title: "Run"}}}
	createTask(t, store, task)
	worker := &recordingWorker{
		onRun: func(req StepRunRequest) {
			if err := store.Append(ctx, req.Task.ID, coretask.StatusChanged{
				TaskID: req.Task.ID, Previous: coretask.StatusRunning, Current: coretask.StatusCancelled, Reason: "user cancelled",
			}); err != nil {
				t.Fatalf("Append cancellation: %v", err)
			}
		},
	}
	scheduler := newScheduler(t, store, worker)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	step := state.Executions[state.CurrentExecution].Steps["run"]
	if state.Task.Status != coretask.StatusCancelled {
		t.Fatalf("task status = %s, want cancelled", state.Task.Status)
	}
	if step.Status == coretask.StepStatusCompleted {
		t.Fatalf("step status = completed, want stale worker output ignored")
	}
	if diagnostics := step.Diagnostics; len(diagnostics) != 1 || diagnostics[0].Code != "task_stale_step_result_ignored" {
		t.Fatalf("step diagnostics = %#v, want stale result diagnostic", diagnostics)
	}
}

func TestSchedulerResumesInterruptedExecution(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Resume",
		Status: coretask.StatusReady,
		Steps: []coretask.Step{
			{ID: "approve", Title: "Approve", Assignee: coretask.RoleHuman},
			{ID: "run", Title: "Run", DependsOn: []coretask.StepID{"approve"}},
		},
	}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask block: %v", err)
	}
	blocked, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project blocked: %v", err)
	}
	if blocked.Task.Status != coretask.StatusBlocked {
		t.Fatalf("blocked task status = %s, want blocked", blocked.Task.Status)
	}
	if err := store.Append(ctx, task.ID,
		coretask.StepStatusChanged{TaskID: task.ID, ExecutionID: blocked.CurrentExecution, StepID: "approve", Previous: coretask.StepStatusBlocked, Current: coretask.StepStatusCompleted, Reason: "approved"},
		coretask.StatusChanged{TaskID: task.ID, Previous: coretask.StatusBlocked, Current: coretask.StatusReady, Reason: "approved"},
	); err != nil {
		t.Fatalf("Append approval: %v", err)
	}
	if err := store.Index(ctx, summary(coretask.Task{ID: task.ID, Title: task.Title, Status: coretask.StatusReady})); err != nil {
		t.Fatalf("Index ready: %v", err)
	}

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask resume: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project final: %v", err)
	}
	if state.CurrentExecution != blocked.CurrentExecution {
		t.Fatalf("execution = %s, want resumed %s", state.CurrentExecution, blocked.CurrentExecution)
	}
	exec := state.Executions[state.CurrentExecution]
	if state.Task.Status != coretask.StatusCompleted || exec.Steps["run"].Status != coretask.StepStatusCompleted {
		t.Fatalf("state = %#v exec = %#v, want resumed completion", state.Task, exec)
	}
}

func TestSchedulerCancellationPropagatesToWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Cancel", Status: coretask.StatusReady}
	createTask(t, store, task)
	worker := &cancelAwareWorker{started: make(chan struct{}), done: make(chan struct{})}
	scheduler := newScheduler(t, store, worker)
	scheduler.reconcileInterval = time.Hour

	go scheduler.Start(ctx)
	<-worker.started
	cancel()
	select {
	case <-worker.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not observe scheduler cancellation")
	}
}

func TestSchedulerSubmitTaskAndStatus(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Submit", Status: coretask.StatusReady}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)

	result, err := scheduler.SubmitTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if !result.Started || !result.Running {
		t.Fatalf("submit result = %#v, want started running", result)
	}
	waitForTaskStatus(t, store, task.ID, coretask.StatusCompleted)
	status := scheduler.Status()
	if !status.Enabled || status.Capacity != status.MaxParallel || len(status.Running) != 0 {
		t.Fatalf("status = %#v, want enabled idle scheduler", status)
	}
}

func TestSchedulerSubmitTaskReportsAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Running", Status: coretask.StatusReady}
	createTask(t, store, task)
	if err := store.Append(ctx, task.ID, coretask.ExecutionStarted{
		TaskID:      task.ID,
		ExecutionID: "exec_existing",
		Execution:   coretask.Execution{ID: "exec_existing", TaskID: task.ID, Status: coretask.StatusRunning},
	}); err != nil {
		t.Fatalf("Append running execution: %v", err)
	}
	scheduler := newScheduler(t, store, &recordingWorker{})

	result, err := scheduler.SubmitTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if !result.Running || result.Started || result.Status != coretask.StatusRunning || !strings.Contains(result.Summary, "already running") {
		t.Fatalf("submit result = %#v, want already-running feedback", result)
	}
}

func TestSchedulerDisableStopsAutomaticTickButAllowsSubmit(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Manual", Status: coretask.StatusReady}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)
	scheduler.SetEnabled(false)

	if err := scheduler.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(worker.steps) != 0 {
		t.Fatalf("worker steps after disabled tick = %#v, want none", worker.steps)
	}
	if _, err := scheduler.SubmitTask(ctx, task.ID); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	waitForTaskStatus(t, store, task.ID, coretask.StatusCompleted)
}

func TestSchedulerNotifyTaskReadyRespectsEnabled(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Reactive", Status: coretask.StatusReady}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)
	scheduler.SetEnabled(false)

	if err := scheduler.NotifyTaskReady(ctx, task.ID); err != nil {
		t.Fatalf("NotifyTaskReady disabled: %v", err)
	}
	if len(worker.steps) != 0 {
		t.Fatalf("worker steps after disabled notify = %#v, want none", worker.steps)
	}
	scheduler.SetEnabled(true)
	if err := scheduler.NotifyTaskReady(ctx, task.ID); err != nil {
		t.Fatalf("NotifyTaskReady enabled: %v", err)
	}
	waitForTaskStatus(t, store, task.ID, coretask.StatusCompleted)
}

func TestSchedulerFinalizesMissingRequiredTaskOutput(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:      "task_1",
		Title:   "Missing output",
		Status:  coretask.StatusReady,
		Outputs: []coretask.ArtifactSpec{{ID: "summary", Kind: coretask.ArtifactReport, Required: true}},
		Steps:   []coretask.Step{{ID: "run", Title: "Run"}},
	}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler := newScheduler(t, store, worker)

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusCompleted {
		t.Fatalf("task status = %s, want completed", state.Task.Status)
	}
	exec := state.Executions[state.CurrentExecution]
	if exec.Status != coretask.StatusCompleted {
		t.Fatalf("execution status = %s, want completed", exec.Status)
	}
	if exec.Steps["run"].Status != coretask.StepStatusCompleted {
		t.Fatalf("step status = %s, want completed", exec.Steps["run"].Status)
	}
	if got, want := worker.steps, []coretask.StepID{"run", finalizerStepID}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("worker steps = %#v, want %#v", got, want)
	}
	if !artifactSpecSatisfied(coretask.ArtifactSpec{ID: "summary"}, exec.Artifacts) {
		t.Fatalf("execution artifacts = %#v, want finalized summary artifact", exec.Artifacts)
	}
}

func TestSchedulerBindsDeclaredStepOutputs(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:      "task_1",
		Title:   "Declared output",
		Status:  coretask.StatusReady,
		Outputs: []coretask.ArtifactSpec{{ID: "summary", Kind: coretask.ArtifactReport, Required: true}},
		Steps: []coretask.Step{{
			ID:      "run",
			Title:   "Run",
			Outputs: []coretask.ArtifactSpec{{ID: "summary", Kind: coretask.ArtifactReport, Required: true}},
		}},
	}
	createTask(t, store, task)
	scheduler := newScheduler(t, store, &recordingWorker{})

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusCompleted {
		t.Fatalf("task status = %s, want completed", state.Task.Status)
	}
	step := state.Executions[state.CurrentExecution].Steps["run"]
	if !artifactSpecSatisfied(coretask.ArtifactSpec{ID: "summary"}, step.Artifacts) {
		t.Fatalf("step artifacts = %#v, want declared summary artifact", step.Artifacts)
	}
}

func TestSchedulerStoresLargeWorkerOutputAsReferenceArtifact(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:      "task_1",
		Title:   "Large output",
		Status:  coretask.StatusReady,
		Outputs: []coretask.ArtifactSpec{{ID: "summary", Kind: coretask.ArtifactReport, Required: true}},
		Steps: []coretask.Step{{
			ID:      "run",
			Title:   "Run",
			Outputs: []coretask.ArtifactSpec{{ID: "summary", Kind: coretask.ArtifactReport, Required: true}},
		}},
	}
	createTask(t, store, task)
	scheduler := newScheduler(t, store, largeOutputWorker{output: strings.Repeat("large-output ", 2048)})

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	step := state.Executions[state.CurrentExecution].Steps["run"]
	var summary coretask.ArtifactSpec
	for _, artifact := range step.Artifacts {
		if artifact.ID == "summary" {
			summary = artifact
			break
		}
	}
	if summary.ID == "" {
		t.Fatalf("step artifacts = %#v, want summary artifact", step.Artifacts)
	}
	if summary.Ref == "" || summary.Metadata["replaced"] != "true" || summary.Metadata["replacement_preview"] == "" {
		t.Fatalf("summary artifact = %#v, want replacement ref and preview metadata", summary)
	}
	if _, err := os.Stat(summary.Ref); err != nil {
		t.Fatalf("replacement ref %q: %v", summary.Ref, err)
	}
	if step.Output == nil || len(step.Output.(string)) >= len(strings.Repeat("large-output ", 2048)) {
		t.Fatalf("step output = %#v, want compact replacement output", step.Output)
	}
}

func TestSchedulerEventNotifyAndReconciliationCompleteReadyTaskBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inner := eventstore.NewMemoryStore()
	schedulerStore, err := runtimetask.NewStore(inner)
	if err != nil {
		t.Fatalf("NewStore scheduler: %v", err)
	}
	worker := &concurrencyRecordingWorker{releaseDelay: 2 * time.Millisecond}
	scheduler, err := New(Config{
		Store:             schedulerStore,
		Worker:            worker,
		ReconcileInterval: 5 * time.Millisecond,
		MaxParallel:       4,
		NewID:             func(prefix string) string { return prefix + "test" },
		Now:               testTime,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	notifyingStore, err := runtimetask.NewStore(NewNotifyingEventStore(inner, scheduler))
	if err != nil {
		t.Fatalf("NewStore notifying: %v", err)
	}

	go scheduler.Start(ctx)

	const tasks = 32
	for i := 0; i < tasks; i++ {
		task := coretask.Task{
			ID:     coretask.ID("task_burst_" + stringID(i)),
			Title:  "Burst task",
			Status: coretask.StatusReady,
			Steps:  []coretask.Step{{ID: "run", Title: "Run"}},
		}
		createTask(t, notifyingStore, task)
	}
	for i := 0; i < tasks; i++ {
		waitForTaskStatus(t, schedulerStore, coretask.ID("task_burst_"+stringID(i)), coretask.StatusCompleted)
	}
	if got := worker.calls(); got != tasks {
		t.Fatalf("worker calls = %d, want %d", got, tasks)
	}
	if got := worker.maxSeen(); got > 4 {
		t.Fatalf("max concurrent workers = %d, want <= 4", got)
	}
}

func TestConcurrentSchedulersClaimTaskOnce(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{
		ID:     "task_1",
		Title:  "Shared claim",
		Status: coretask.StatusReady,
		Steps:  []coretask.Step{{ID: "run", Title: "Run"}},
	}
	createTask(t, store, task)
	worker := &concurrencyRecordingWorker{releaseDelay: 10 * time.Millisecond}
	first, err := New(Config{
		Store:       store,
		Worker:      worker,
		WorkerID:    "scheduler-a",
		MaxParallel: 1,
	})
	if err != nil {
		t.Fatalf("New first scheduler: %v", err)
	}
	second, err := New(Config{
		Store:       store,
		Worker:      worker,
		WorkerID:    "scheduler-b",
		MaxParallel: 1,
	})
	if err != nil {
		t.Fatalf("New second scheduler: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, scheduler := range []*Scheduler{first, second} {
		wg.Add(1)
		go func(scheduler *Scheduler) {
			defer wg.Done()
			errs <- scheduler.RunTask(ctx, task.ID)
		}(scheduler)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RunTask: %v", err)
		}
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.Status != coretask.StatusCompleted {
		t.Fatalf("task status = %s, want completed", state.Task.Status)
	}
	if len(state.Executions) != 1 {
		t.Fatalf("executions = %#v, want one claimed execution", state.Executions)
	}
	worker.mu.Lock()
	total := worker.total
	worker.mu.Unlock()
	if total != 1 {
		t.Fatalf("worker total = %d, want exactly one execution", total)
	}
}

func TestNotifyingEventStoreNotifiesReadyTaskIndexEvents(t *testing.T) {
	ctx := context.Background()
	inner := eventstore.NewMemoryStore()
	notifier := &recordingReadyNotifier{}
	store := NewNotifyingEventStore(inner, notifier)

	if _, err := store.Append(ctx, "task:draft", event.ExpectSequence(0), event.Record{
		Payload: coretask.Created{TaskID: "draft", Task: coretask.Task{ID: "draft", Title: "Draft", Status: coretask.StatusDraft}},
	}); err != nil {
		t.Fatalf("Append draft: %v", err)
	}
	if len(notifier.tasks) != 0 {
		t.Fatalf("notified task stream events = %#v, want none before index", notifier.tasks)
	}
	if _, err := store.Append(ctx, runtimetask.IndexStreamID(), event.AppendOptions{}, event.Record{
		Payload: coretask.Indexed{TaskID: "ready", Summary: coretask.TaskSummary{ID: "ready", Status: coretask.StatusReady}},
	}); err != nil {
		t.Fatalf("Append ready index: %v", err)
	}
	if _, err := store.Append(ctx, runtimetask.IndexStreamID(), event.AppendOptions{}, event.Record{
		Payload: coretask.Indexed{TaskID: "draft", Summary: coretask.TaskSummary{ID: "draft", Status: coretask.StatusReady}},
	}); err != nil {
		t.Fatalf("Append draft ready index: %v", err)
	}
	if got := notifier.tasks; len(got) != 2 || got[0] != "ready" || got[1] != "draft" {
		t.Fatalf("notified tasks = %#v, want ready then draft", got)
	}
}

func TestNotifyTaskReadyRecordsDiagnosticWhenDisabled(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Ready", Status: coretask.StatusReady}
	createTask(t, store, task)
	scheduler := newScheduler(t, store, &recordingWorker{})
	scheduler.SetEnabled(false)

	if err := scheduler.NotifyTaskReady(ctx, task.ID); err != nil {
		t.Fatalf("NotifyTaskReady: %v", err)
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(state.Task.Diagnostics) != 1 || state.Task.Diagnostics[0].Code != "task_auto_schedule_disabled" {
		t.Fatalf("diagnostics = %#v, want auto-schedule disabled diagnostic", state.Task.Diagnostics)
	}
}

func TestNotifyTaskReadyRecordsDiagnosticWhenCapacityUnavailable(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	block := make(chan struct{})
	worker := &blockingWorker{started: make(chan struct{}), block: block}
	task1 := coretask.Task{ID: "task_1", Title: "First", Status: coretask.StatusReady}
	task2 := coretask.Task{ID: "task_2", Title: "Second", Status: coretask.StatusReady}
	createTask(t, store, task1)
	createTask(t, store, task2)
	scheduler, err := New(Config{Store: store, Worker: worker, MaxParallel: 1, Now: testTime, NewID: func(prefix string) string { return prefix + "test" }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer close(block)

	if err := scheduler.NotifyTaskReady(ctx, task1.ID); err != nil {
		t.Fatalf("NotifyTaskReady task1: %v", err)
	}
	<-worker.started
	if err := scheduler.NotifyTaskReady(ctx, task2.ID); err != nil {
		t.Fatalf("NotifyTaskReady task2: %v", err)
	}
	state, err := store.Project(ctx, task2.ID)
	if err != nil {
		t.Fatalf("Project task2: %v", err)
	}
	if len(state.Task.Diagnostics) != 1 || state.Task.Diagnostics[0].Code != "task_auto_schedule_deferred" {
		t.Fatalf("diagnostics = %#v, want auto-schedule deferred diagnostic", state.Task.Diagnostics)
	}
}

func TestSchedulerUsesConfiguredRoleProfiles(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	task := coretask.Task{ID: "task_1", Title: "Review", Status: coretask.StatusReady, Assignee: coretask.RoleReviewer}
	createTask(t, store, task)
	worker := &recordingWorker{}
	scheduler, err := New(Config{
		Store: store, Worker: worker, Now: testTime,
		NewID:        func(prefix string) string { return prefix + "test" },
		RoleProfiles: map[coretask.Role]string{coretask.RoleReviewer: "strict-reviewer"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := scheduler.RunTask(ctx, task.ID); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(worker.profiles) != 1 || worker.profiles[0] != "strict-reviewer" {
		t.Fatalf("profiles = %#v, want configured reviewer profile", worker.profiles)
	}
}

func TestSchedulerRotatesWorkerPoolProfiles(t *testing.T) {
	ctx := context.Background()
	store := newTaskStore(t)
	createTask(t, store, coretask.Task{ID: "task_1", Title: "Review 1", Status: coretask.StatusReady, Assignee: coretask.RoleReviewer})
	createTask(t, store, coretask.Task{ID: "task_2", Title: "Review 2", Status: coretask.StatusReady, Assignee: coretask.RoleReviewer})
	worker := &recordingWorker{}
	scheduler, err := New(Config{
		Store: store, Worker: worker, Now: testTime,
		NewID: func(prefix string) string { return prefix + stringID(len(worker.steps)+1) },
		WorkerPools: map[coretask.Role]WorkerPoolConfig{
			coretask.RoleReviewer: {Profiles: []string{"reviewer-a", "reviewer-b"}, MaxParallel: 2},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := scheduler.RunTask(ctx, "task_1"); err != nil {
		t.Fatalf("RunTask task_1: %v", err)
	}
	if err := scheduler.RunTask(ctx, "task_2"); err != nil {
		t.Fatalf("RunTask task_2: %v", err)
	}
	if got := worker.profiles; len(got) != 2 || got[0] != "reviewer-a" || got[1] != "reviewer-b" {
		t.Fatalf("profiles = %#v, want reviewer-a then reviewer-b", got)
	}
	status := scheduler.Status()
	if status.MaxParallelByRole[coretask.RoleReviewer] != 2 || len(status.ProfilesByRole[coretask.RoleReviewer]) != 2 {
		t.Fatalf("status = %#v, want reviewer pool metadata", status)
	}
}

type recordingReadyNotifier struct {
	tasks []coretask.ID
}

func (n *recordingReadyNotifier) NotifyTaskReady(_ context.Context, taskID coretask.ID) error {
	n.tasks = append(n.tasks, taskID)
	return nil
}

type recordingRuntimePublisher struct {
	thread  corethread.Ref
	runID   clientapi.RunID
	events  []event.Event
	targets []string
}

func (p *recordingRuntimePublisher) PublishRuntimeEvent(_ context.Context, thread corethread.Ref, runID clientapi.RunID, payload event.Event) error {
	if p.thread.ID == "" {
		p.thread = thread
	}
	if p.runID == "" {
		p.runID = runID
	}
	p.targets = append(p.targets, string(thread.ID)+"\x00"+string(runID))
	p.events = append(p.events, payload)
	return nil
}

func (p *recordingRuntimePublisher) targetSeen(threadID, runID string) bool {
	target := threadID + "\x00" + runID
	for _, got := range p.targets {
		if got == target {
			return true
		}
	}
	return false
}

func (p *recordingRuntimePublisher) seen(name event.Name) bool {
	for _, payload := range p.events {
		if payload != nil && payload.EventName() == name {
			return true
		}
	}
	return false
}

func (p *recordingRuntimePublisher) names() []event.Name {
	names := make([]event.Name, 0, len(p.events))
	for _, payload := range p.events {
		if payload != nil {
			names = append(names, payload.EventName())
		}
	}
	return names
}

type failingRuntimePublisher struct {
	failThread corethread.ID
	targets    []string
}

func (p *failingRuntimePublisher) PublishRuntimeEvent(_ context.Context, thread corethread.Ref, runID clientapi.RunID, _ event.Event) error {
	if thread.ID == p.failThread {
		return context.Canceled
	}
	p.targets = append(p.targets, string(thread.ID)+"\x00"+string(runID))
	return nil
}

func (p *failingRuntimePublisher) targetSeen(threadID, runID string) bool {
	target := threadID + "\x00" + runID
	for _, got := range p.targets {
		if got == target {
			return true
		}
	}
	return false
}

type recordingWorker struct {
	steps    []coretask.StepID
	profiles []string
	onRun    func(StepRunRequest)
}

func (w *recordingWorker) RunStep(_ context.Context, req StepRunRequest) (StepRunResult, error) {
	w.steps = append(w.steps, req.Step.ID)
	w.profiles = append(w.profiles, req.Profile)
	if w.onRun != nil {
		w.onRun(req)
	}
	return StepRunResult{
		Output: "done " + string(req.Step.ID),
		Artifacts: []coretask.ArtifactSpec{{
			ID:    "artifact-" + string(req.Step.ID),
			Name:  "Artifact " + string(req.Step.ID),
			Kind:  coretask.ArtifactReport,
			Value: "done",
		}},
	}, nil
}

type largeOutputWorker struct {
	output string
}

func (w largeOutputWorker) RunStep(_ context.Context, _ StepRunRequest) (StepRunResult, error) {
	return StepRunResult{Output: w.output}, nil
}

type blockingWorker struct {
	started chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (w *blockingWorker) RunStep(ctx context.Context, _ StepRunRequest) (StepRunResult, error) {
	w.once.Do(func() { close(w.started) })
	select {
	case <-ctx.Done():
		return StepRunResult{}, ctx.Err()
	case <-w.block:
		return StepRunResult{Output: "done"}, nil
	}
}

type concurrencyRecordingWorker struct {
	mu           sync.Mutex
	current      int
	max          int
	total        int
	releaseDelay time.Duration
}

func (w *concurrencyRecordingWorker) RunStep(ctx context.Context, req StepRunRequest) (StepRunResult, error) {
	w.mu.Lock()
	w.current++
	w.total++
	if w.current > w.max {
		w.max = w.current
	}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.current--
		w.mu.Unlock()
	}()
	if w.releaseDelay > 0 {
		timer := time.NewTimer(w.releaseDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return StepRunResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return StepRunResult{Output: "done " + string(req.Step.ID)}, nil
}

func (w *concurrencyRecordingWorker) calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}

func (w *concurrencyRecordingWorker) maxSeen() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.max
}

func stringID(i int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i == 0 {
		return "0"
	}
	out := make([]byte, 0, 4)
	for i > 0 {
		out = append([]byte{digits[i%len(digits)]}, out...)
		i /= len(digits)
	}
	return string(out)
}

func newTaskStore(t *testing.T) *runtimetask.EventStore {
	t.Helper()
	store, err := runtimetask.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func newScheduler(t *testing.T, store runtimetask.Store, worker WorkerClient) *Scheduler {
	t.Helper()
	scheduler, err := New(Config{
		Store:  store,
		Worker: worker,
		NewID: func(prefix string) string {
			return prefix + "test"
		},
		Now: testTime,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return scheduler
}

func createTask(t *testing.T, store runtimetask.Store, task coretask.Task) {
	t.Helper()
	if err := store.Create(context.Background(), task.ID, coretask.Created{TaskID: task.ID, Task: task}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Index(context.Background(), summary(task)); err != nil {
		t.Fatalf("Index: %v", err)
	}
}

func waitForTaskStatus(t *testing.T, store runtimetask.Store, taskID coretask.ID, want coretask.Status) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := store.Project(context.Background(), taskID)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if state.Task.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, err := store.Project(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Project final: %v", err)
	}
	t.Fatalf("task status = %s, want %s", state.Task.Status, want)
}

func waitForLeaseAfter(t *testing.T, store runtimetask.Store, taskID coretask.ID, after time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := store.Project(context.Background(), taskID)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		exec := state.Executions[state.CurrentExecution]
		if exec.LeaseExpiresAt.After(after) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, err := store.Project(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Project final: %v", err)
	}
	exec := state.Executions[state.CurrentExecution]
	t.Fatalf("lease expires at %s, want after %s", exec.LeaseExpiresAt, after)
}

type cancelAwareWorker struct {
	started chan struct{}
	done    chan struct{}
}

func (w *cancelAwareWorker) RunStep(ctx context.Context, req StepRunRequest) (StepRunResult, error) {
	close(w.started)
	<-ctx.Done()
	close(w.done)
	return StepRunResult{}, ctx.Err()
}

func testTime() time.Time {
	return time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
}
