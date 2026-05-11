package projection

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
	coreprojection "github.com/fluxplane/agentruntime/core/projection"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
)

type testEvent struct{}

func (testEvent) EventName() event.Name { return "test.event" }

func TestRunnerRunOnceAdvancesCheckpoint(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	if _, err := events.Append(ctx, "stream", event.ExpectSequence(0),
		event.Record{Payload: testEvent{}},
		event.Record{Payload: testEvent{}},
	); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	var projected int
	runner := Runner{
		Events:      events,
		Checkpoints: NewMemoryCheckpointStore(),
		BatchSize:   1,
	}
	checkpoint, err := runner.RunOnce(ctx, "test", "stream", coreprojection.ProjectorFunc(func(context.Context, []event.StoredRecord) error {
		projected++
		return nil
	}))
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if projected != 1 {
		t.Fatalf("projected = %d, want 1", projected)
	}
	if checkpoint.Sequence != 1 {
		t.Fatalf("checkpoint sequence = %d, want 1", checkpoint.Sequence)
	}
}
