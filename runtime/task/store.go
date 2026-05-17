package task

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/event"
	coretask "github.com/fluxplane/agentruntime/core/task"
)

// Store persists task event streams and projects task state from them.
type Store interface {
	Create(context.Context, coretask.ID, ...event.Event) error
	Append(context.Context, coretask.ID, ...event.Event) error
	AppendExpected(context.Context, coretask.ID, event.Sequence, ...event.Event) error
	Index(context.Context, coretask.TaskSummary) error
	List(context.Context) ([]coretask.TaskSummary, error)
	Load(context.Context, coretask.ID) ([]event.Record, error)
	Project(context.Context, coretask.ID) (State, error)
	ProjectWithSequence(context.Context, coretask.ID) (SequencedState, error)
	RegisterWorker(context.Context, coretask.WorkerStatus) error
	ListWorkers(context.Context) ([]coretask.WorkerStatus, error)
}

// SequencedState is a projected task state plus the current task stream tail.
type SequencedState struct {
	State    State
	Sequence event.Sequence
}

// EventStore implements Store on top of core/event.Store.
type EventStore struct {
	events event.Store
}

var _ Store = (*EventStore)(nil)

// NewStore returns a task store backed by an event store.
func NewStore(events event.Store) (*EventStore, error) {
	if events == nil {
		return nil, fmt.Errorf("task: event store is nil")
	}
	return &EventStore{events: events}, nil
}

// StreamID returns the event stream for a task.
func StreamID(taskID coretask.ID) event.StreamID {
	if taskID == "" {
		return ""
	}
	return event.StreamID("task:" + string(taskID))
}

// IndexStreamID returns the stream used for compact task list/search state.
func IndexStreamID() event.StreamID {
	return event.StreamID("task.index")
}

// WorkerStreamID returns the stream used for scheduler worker registrations.
func WorkerStreamID() event.StreamID {
	return event.StreamID("task.workers")
}

// Append appends task-scoped events to a task stream.
func (s *EventStore) Append(ctx context.Context, taskID coretask.ID, payloads ...event.Event) error {
	return s.append(ctx, taskID, false, payloads...)
}

// AppendExpected appends task-scoped events when the task stream still ends at
// the supplied sequence.
func (s *EventStore) AppendExpected(ctx context.Context, taskID coretask.ID, expected event.Sequence, payloads ...event.Event) error {
	if s == nil || s.events == nil {
		return fmt.Errorf("task: store is nil")
	}
	stream := StreamID(taskID)
	if stream == "" {
		return fmt.Errorf("task: id is empty")
	}
	records := taskRecords(taskID, payloads...)
	if len(records) == 0 {
		return nil
	}
	_, err := s.events.Append(ctx, stream, event.ExpectSequence(expected), records...)
	return err
}

// Create appends initial task-scoped events and requires the task stream to be
// empty.
func (s *EventStore) Create(ctx context.Context, taskID coretask.ID, payloads ...event.Event) error {
	return s.append(ctx, taskID, true, payloads...)
}

func (s *EventStore) append(ctx context.Context, taskID coretask.ID, create bool, payloads ...event.Event) error {
	if s == nil || s.events == nil {
		return fmt.Errorf("task: store is nil")
	}
	stream := StreamID(taskID)
	if stream == "" {
		return fmt.Errorf("task: id is empty")
	}
	if len(payloads) == 0 {
		return nil
	}
	var sequence event.Sequence
	if !create {
		current, err := s.events.Load(ctx, stream, event.LoadOptions{Direction: event.DirectionBackward, Limit: 1})
		if err != nil {
			return err
		}
		if len(current) > 0 {
			sequence = current[0].Sequence
		}
	}
	records := taskRecords(taskID, payloads...)
	if len(records) == 0 {
		return nil
	}
	_, err := s.events.Append(ctx, stream, event.ExpectSequence(sequence), records...)
	return err
}

func taskRecords(taskID coretask.ID, payloads ...event.Event) []event.Record {
	records := make([]event.Record, 0, len(payloads))
	for _, payload := range payloads {
		if payload == nil {
			continue
		}
		records = append(records, event.Record{
			Name:    payload.EventName(),
			Payload: payload,
			Attributes: map[string]string{
				"task.id": string(taskID),
			},
		})
	}
	return records
}

// Index appends the latest task summary to the task index stream.
func (s *EventStore) Index(ctx context.Context, summary coretask.TaskSummary) error {
	if s == nil || s.events == nil {
		return fmt.Errorf("task: store is nil")
	}
	if summary.ID == "" {
		return fmt.Errorf("task: id is empty")
	}
	payload := coretask.Indexed{TaskID: summary.ID, Summary: summary}
	_, err := s.events.Append(ctx, IndexStreamID(), event.AppendOptions{}, event.Record{
		Name:    payload.EventName(),
		Payload: payload,
		Attributes: map[string]string{
			"task.id": string(summary.ID),
		},
	})
	return err
}

// List loads the latest task summaries from the task index stream.
func (s *EventStore) List(ctx context.Context) ([]coretask.TaskSummary, error) {
	if s == nil || s.events == nil {
		return nil, fmt.Errorf("task: store is nil")
	}
	stored, err := s.events.Load(ctx, IndexStreamID(), event.LoadOptions{})
	if err != nil {
		return nil, err
	}
	latest := map[coretask.ID]coretask.TaskSummary{}
	order := []coretask.ID{}
	for _, record := range stored {
		payload, ok := record.Record.Payload.(coretask.Indexed)
		if !ok {
			continue
		}
		id := payload.TaskID
		if id == "" {
			id = payload.Summary.ID
		}
		if id == "" {
			continue
		}
		if _, exists := latest[id]; !exists {
			order = append(order, id)
		}
		summary := payload.Summary
		if summary.ID == "" {
			summary.ID = id
		}
		latest[id] = summary
	}
	out := make([]coretask.TaskSummary, 0, len(order))
	for _, id := range order {
		out = append(out, latest[id])
	}
	return out, nil
}

// RegisterWorker appends a scheduler worker registration snapshot.
func (s *EventStore) RegisterWorker(ctx context.Context, worker coretask.WorkerStatus) error {
	if s == nil || s.events == nil {
		return fmt.Errorf("task: store is nil")
	}
	if worker.WorkerID == "" {
		return fmt.Errorf("task: worker id is empty")
	}
	payload := coretask.WorkerRegistered{Worker: worker}
	_, err := s.events.Append(ctx, WorkerStreamID(), event.AppendOptions{}, event.Record{
		Name:    payload.EventName(),
		Payload: payload,
		Attributes: map[string]string{
			"task.worker_id": worker.WorkerID,
		},
	})
	return err
}

// ListWorkers returns the latest registration snapshot for each scheduler worker.
func (s *EventStore) ListWorkers(ctx context.Context) ([]coretask.WorkerStatus, error) {
	if s == nil || s.events == nil {
		return nil, fmt.Errorf("task: store is nil")
	}
	stored, err := s.events.Load(ctx, WorkerStreamID(), event.LoadOptions{})
	if err != nil {
		return nil, err
	}
	latest := map[string]coretask.WorkerStatus{}
	order := []string{}
	for _, record := range stored {
		payload, ok := record.Record.Payload.(coretask.WorkerRegistered)
		if !ok || payload.Worker.WorkerID == "" {
			continue
		}
		if _, exists := latest[payload.Worker.WorkerID]; !exists {
			order = append(order, payload.Worker.WorkerID)
		}
		latest[payload.Worker.WorkerID] = payload.Worker
	}
	out := make([]coretask.WorkerStatus, 0, len(order))
	for _, id := range order {
		out = append(out, latest[id])
	}
	return out, nil
}

// Load loads task event records from a task stream.
func (s *EventStore) Load(ctx context.Context, taskID coretask.ID) ([]event.Record, error) {
	if s == nil || s.events == nil {
		return nil, fmt.Errorf("task: store is nil")
	}
	stream := StreamID(taskID)
	if stream == "" {
		return nil, fmt.Errorf("task: id is empty")
	}
	stored, err := s.events.Load(ctx, stream, event.LoadOptions{})
	if err != nil {
		return nil, err
	}
	records := make([]event.Record, len(stored))
	for i, record := range stored {
		records[i] = record.Record
	}
	return records, nil
}

// Project loads and projects task state from a task stream.
func (s *EventStore) Project(ctx context.Context, taskID coretask.ID) (State, error) {
	records, err := s.Load(ctx, taskID)
	if err != nil {
		return State{}, err
	}
	return Project(records), nil
}

// ProjectWithSequence loads and projects task state while preserving the last
// stream sequence for optimistic follow-up appends.
func (s *EventStore) ProjectWithSequence(ctx context.Context, taskID coretask.ID) (SequencedState, error) {
	if s == nil || s.events == nil {
		return SequencedState{}, fmt.Errorf("task: store is nil")
	}
	stream := StreamID(taskID)
	if stream == "" {
		return SequencedState{}, fmt.Errorf("task: id is empty")
	}
	stored, err := s.events.Load(ctx, stream, event.LoadOptions{})
	if err != nil {
		return SequencedState{}, err
	}
	records := make([]event.Record, len(stored))
	var sequence event.Sequence
	for i, record := range stored {
		records[i] = record.Record
		sequence = record.Sequence
	}
	return SequencedState{State: Project(records), Sequence: sequence}, nil
}
