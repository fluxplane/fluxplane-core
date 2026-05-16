package task

import (
	"context"
	"fmt"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
	coretask "github.com/fluxplane/agentruntime/core/task"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
)

func TestStoreAppendsAndProjectsTaskStream(t *testing.T) {
	store, err := NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	task := coretask.Task{ID: "task_1", Title: "Feature", Status: coretask.StatusReady}
	if err := store.Append(ctx, task.ID, coretask.Created{TaskID: task.ID, Task: task}); err != nil {
		t.Fatalf("Append created: %v", err)
	}
	if err := store.Append(ctx, task.ID, coretask.ArtifactAdded{TaskID: task.ID, Artifact: coretask.ArtifactSpec{Name: "summary", Kind: coretask.ArtifactText}}); err != nil {
		t.Fatalf("Append artifact: %v", err)
	}
	records, err := store.Load(ctx, task.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2", len(records))
	}
	state, err := store.Project(ctx, task.ID)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if state.Task.ID != "task_1" || state.Task.Status != coretask.StatusReady {
		t.Fatalf("task = %#v, want projected ready task", state.Task)
	}
	if len(state.Task.Artifacts) != 1 || state.Task.Artifacts[0].Name != "summary" {
		t.Fatalf("artifacts = %#v, want summary", state.Task.Artifacts)
	}
}

func TestStoreCreateRejectsExistingTaskStream(t *testing.T) {
	store, err := NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	task := coretask.Task{ID: "task_1", Title: "Feature", Status: coretask.StatusReady}
	if err := store.Create(ctx, task.ID, coretask.Created{TaskID: task.ID, Task: task}); err != nil {
		t.Fatalf("Create created: %v", err)
	}
	err = store.Create(ctx, task.ID, coretask.Created{TaskID: task.ID, Task: coretask.Task{ID: task.ID, Title: "Replacement"}})
	if err == nil {
		t.Fatal("Create duplicate error = nil, want conflict")
	}
	state, projectErr := store.Project(ctx, task.ID)
	if projectErr != nil {
		t.Fatalf("Project: %v", projectErr)
	}
	if state.Task.Title != "Feature" {
		t.Fatalf("task title = %q, want original Feature", state.Task.Title)
	}
}

func TestStoreIndexesLatestTaskSummaries(t *testing.T) {
	store, err := NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	if err := store.Index(ctx, coretask.TaskSummary{ID: "task_1", Title: "Original", Status: coretask.StatusReady}); err != nil {
		t.Fatalf("Index original: %v", err)
	}
	if err := store.Index(ctx, coretask.TaskSummary{ID: "task_1", Title: "Updated", Status: coretask.StatusCompleted}); err != nil {
		t.Fatalf("Index updated: %v", err)
	}
	if err := store.Index(ctx, coretask.TaskSummary{ID: "task_2", Title: "Other", Status: coretask.StatusReady}); err != nil {
		t.Fatalf("Index other: %v", err)
	}
	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries len = %d, want 2", len(summaries))
	}
	if summaries[0].ID != "task_1" || summaries[0].Title != "Updated" || summaries[0].Status != coretask.StatusCompleted {
		t.Fatalf("first summary = %#v, want latest task_1", summaries[0])
	}
	if summaries[1].ID != "task_2" {
		t.Fatalf("second summary = %#v, want task_2", summaries[1])
	}
}

func TestStoreIndexDoesNotRequireExpectedSequence(t *testing.T) {
	backing := eventstore.NewMemoryStore()
	store, err := NewStore(noIndexExpectationStore{Store: backing})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	if err := store.Index(ctx, coretask.TaskSummary{ID: "task_1", Title: "One"}); err != nil {
		t.Fatalf("Index task_1: %v", err)
	}
	if err := store.Index(ctx, coretask.TaskSummary{ID: "task_2", Title: "Two"}); err != nil {
		t.Fatalf("Index task_2: %v", err)
	}
	summaries, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries len = %d, want 2", len(summaries))
	}
}

type noIndexExpectationStore struct {
	event.Store
}

func (s noIndexExpectationStore) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
	if stream == IndexStreamID() && opts.CheckExpectedSequence {
		return nil, fmt.Errorf("index append used expected sequence")
	}
	return s.Store.Append(ctx, stream, opts, records...)
}

func TestStoreStreamID(t *testing.T) {
	if got, want := StreamID("task_1"), "task:task_1"; string(got) != want {
		t.Fatalf("StreamID = %q, want %q", got, want)
	}
}
