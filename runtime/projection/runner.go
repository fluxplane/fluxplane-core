package projection

import (
	"context"
	"fmt"

	coreprojection "github.com/fluxplane/fluxplane-core/core/projection"
	"github.com/fluxplane/fluxplane-event"
)

// Runner loads event batches, projects them, and advances checkpoints.
type Runner struct {
	Events      event.Store
	Checkpoints CheckpointStore
	BatchSize   int
}

// RunOnce processes at most one batch for stream.
func (r Runner) RunOnce(ctx context.Context, name string, stream event.StreamID, projector coreprojection.Projector) (coreprojection.Checkpoint, error) {
	if r.Events == nil {
		return coreprojection.Checkpoint{}, fmt.Errorf("projection: event store is nil")
	}
	if r.Checkpoints == nil {
		return coreprojection.Checkpoint{}, fmt.Errorf("projection: checkpoint store is nil")
	}
	if projector == nil {
		return coreprojection.Checkpoint{}, fmt.Errorf("projection: projector is nil")
	}
	if stream == "" {
		return coreprojection.Checkpoint{}, fmt.Errorf("projection: stream is empty")
	}
	checkpoint, err := r.Checkpoints.Load(ctx, name)
	if err != nil {
		return coreprojection.Checkpoint{}, err
	}
	if checkpoint.Stream != "" && checkpoint.Stream != stream {
		return coreprojection.Checkpoint{}, fmt.Errorf("projection: checkpoint stream %q does not match %q", checkpoint.Stream, stream)
	}
	opts := event.LoadOptions{After: checkpoint.Sequence, Limit: r.BatchSize}
	records, err := r.Events.Load(ctx, stream, opts)
	if err != nil {
		return coreprojection.Checkpoint{}, err
	}
	if len(records) == 0 {
		if checkpoint.Stream == "" {
			checkpoint.Stream = stream
		}
		return checkpoint, nil
	}
	if err := projector.Project(ctx, records); err != nil {
		return coreprojection.Checkpoint{}, err
	}
	next := coreprojection.Checkpoint{
		Stream:   stream,
		Sequence: records[len(records)-1].Sequence,
	}
	if err := r.Checkpoints.Save(ctx, name, next); err != nil {
		return coreprojection.Checkpoint{}, err
	}
	return next, nil
}
