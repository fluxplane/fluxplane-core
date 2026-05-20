package launch

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/adapters/storage/event/sqlite"
	coretask "github.com/fluxplane/agentruntime/core/task"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/taskexecutor"
	runtimetask "github.com/fluxplane/agentruntime/runtime/task"
)

func TestSQLiteBackedSchedulersClaimTaskOnce(t *testing.T) {
	ctx := context.Background()
	registry, err := eventregistry.New(eventregistry.Config{})
	if err != nil {
		t.Fatalf("event registry: %v", err)
	}
	path := filepath.Join(t.TempDir(), "events.sqlite")
	eventsA, err := sqlite.Open(path, registry)
	if err != nil {
		t.Fatalf("open events A: %v", err)
	}
	defer func() { _ = eventsA.Close() }()
	eventsB, err := sqlite.Open(path, registry)
	if err != nil {
		t.Fatalf("open events B: %v", err)
	}
	defer func() { _ = eventsB.Close() }()
	storeA, err := runtimetask.NewStore(eventsA)
	if err != nil {
		t.Fatalf("task store A: %v", err)
	}
	storeB, err := runtimetask.NewStore(eventsB)
	if err != nil {
		t.Fatalf("task store B: %v", err)
	}
	task := coretask.Task{
		ID:     "task_sqlite_claim",
		Title:  "SQLite shared claim",
		Status: coretask.StatusReady,
		Steps:  []coretask.Step{{ID: "run", Title: "Run"}},
	}
	if err := storeA.Create(ctx, task.ID, coretask.Created{TaskID: task.ID, Task: task}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := storeA.Index(ctx, coretask.TaskSummary{ID: task.ID, Title: task.Title, Status: task.Status}); err != nil {
		t.Fatalf("index task: %v", err)
	}
	worker := &sqliteClaimWorker{delay: 20 * time.Millisecond}
	first, err := taskexecutor.New(taskexecutor.Config{
		Store:       storeA,
		Worker:      worker,
		WorkerID:    "scheduler-a",
		MaxParallel: 1,
	})
	if err != nil {
		t.Fatalf("first scheduler: %v", err)
	}
	second, err := taskexecutor.New(taskexecutor.Config{
		Store:       storeB,
		Worker:      worker,
		WorkerID:    "scheduler-b",
		MaxParallel: 1,
	})
	if err != nil {
		t.Fatalf("second scheduler: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, scheduler := range []*taskexecutor.Scheduler{first, second} {
		wg.Add(1)
		go func(scheduler *taskexecutor.Scheduler) {
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
	state, err := storeA.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("project task: %v", err)
	}
	if state.Task.Status != coretask.StatusCompleted {
		t.Fatalf("task status = %s, want completed", state.Task.Status)
	}
	if len(state.Executions) != 1 {
		t.Fatalf("executions = %#v, want one claimed execution", state.Executions)
	}
	if got := worker.calls(); got != 1 {
		t.Fatalf("worker calls = %d, want one", got)
	}
}

type sqliteClaimWorker struct {
	mu    sync.Mutex
	total int
	delay time.Duration
}

func (w *sqliteClaimWorker) RunStep(ctx context.Context, req taskexecutor.StepRunRequest) (taskexecutor.StepRunResult, error) {
	w.mu.Lock()
	w.total++
	w.mu.Unlock()
	timer := time.NewTimer(w.delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return taskexecutor.StepRunResult{}, ctx.Err()
	case <-timer.C:
	}
	return taskexecutor.StepRunResult{
		Output: "done " + string(req.Step.ID),
		Artifacts: []coretask.ArtifactSpec{{
			ID:    "artifact-" + string(req.Step.ID),
			Kind:  coretask.ArtifactReport,
			Value: "done",
		}},
	}, nil
}

func (w *sqliteClaimWorker) calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}
