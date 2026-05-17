package taskexecutor

import (
	"context"

	"github.com/fluxplane/agentruntime/core/event"
	coretask "github.com/fluxplane/agentruntime/core/task"
)

// ReadyNotifier receives task IDs that became ready through appended task
// events. Notification failures must not roll back the event append.
type ReadyNotifier interface {
	NotifyTaskReady(context.Context, coretask.ID) error
}

// NotifyingEventStore wraps an event store and notifies the task scheduler when
// task index updates publish ready summaries. It is a local reactive trigger;
// the scheduler's reconciliation loop remains the recovery path for missed or
// out-of-band events.
type NotifyingEventStore struct {
	inner    event.Store
	notifier ReadyNotifier
}

var _ event.Store = NotifyingEventStore{}

// NewNotifyingEventStore returns an event store wrapper that reacts to ready
// task events. A nil notifier leaves the wrapped store behavior unchanged.
func NewNotifyingEventStore(inner event.Store, notifier ReadyNotifier) event.Store {
	if inner == nil || notifier == nil {
		return inner
	}
	return NotifyingEventStore{inner: inner, notifier: notifier}
}

// Append stores records and notifies ready tasks after the append succeeds.
func (s NotifyingEventStore) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
	stored, err := s.inner.Append(ctx, stream, opts, records...)
	if err != nil {
		return stored, err
	}
	s.notifyReady(ctx, stored)
	return stored, nil
}

// AppendBatch stores records atomically and notifies ready tasks after the
// batch append succeeds.
func (s NotifyingEventStore) AppendBatch(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	results, err := s.inner.AppendBatch(ctx, requests...)
	if err != nil {
		return results, err
	}
	for _, result := range results {
		s.notifyReady(ctx, result.Records)
	}
	return results, nil
}

// Load delegates to the wrapped event store.
func (s NotifyingEventStore) Load(ctx context.Context, stream event.StreamID, opts event.LoadOptions) ([]event.StoredRecord, error) {
	return s.inner.Load(ctx, stream, opts)
}

func (s NotifyingEventStore) notifyReady(ctx context.Context, records []event.StoredRecord) {
	if s.notifier == nil || len(records) == 0 {
		return
	}
	ready := map[coretask.ID]bool{}
	for _, stored := range records {
		switch payload := stored.Record.Payload.(type) {
		case coretask.Indexed:
			if payload.Summary.Status == coretask.StatusReady {
				ready[firstTaskID(payload.TaskID, payload.Summary.ID)] = true
			}
		}
	}
	notifyCtx := context.WithoutCancel(ctx)
	for taskID := range ready {
		if taskID == "" {
			continue
		}
		_ = s.notifier.NotifyTaskReady(notifyCtx, taskID)
	}
}

func firstTaskID(values ...coretask.ID) coretask.ID {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
