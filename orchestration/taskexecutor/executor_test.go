package taskexecutor

import (
	"context"
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

func TestSchedulerBlocksCompletionWhenRequiredOutputMissing(t *testing.T) {
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
	exec := state.Executions[state.CurrentExecution]
	if exec.Status != coretask.StatusInterrupted {
		t.Fatalf("execution status = %s, want interrupted", exec.Status)
	}
	if exec.Steps["run"].Status != coretask.StepStatusCompleted {
		t.Fatalf("step status = %s, want completed", exec.Steps["run"].Status)
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

type recordingReadyNotifier struct {
	tasks []coretask.ID
}

func (n *recordingReadyNotifier) NotifyTaskReady(_ context.Context, taskID coretask.ID) error {
	n.tasks = append(n.tasks, taskID)
	return nil
}

type recordingRuntimePublisher struct {
	thread corethread.Ref
	runID  clientapi.RunID
	events []event.Event
}

func (p *recordingRuntimePublisher) PublishRuntimeEvent(_ context.Context, thread corethread.Ref, runID clientapi.RunID, payload event.Event) error {
	if p.thread.ID == "" {
		p.thread = thread
	}
	if p.runID == "" {
		p.runID = runID
	}
	p.events = append(p.events, payload)
	return nil
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
