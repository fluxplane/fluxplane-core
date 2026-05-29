package projection

import (
	"context"

	"github.com/fluxplane/fluxplane-event"
)

// Checkpoint records how far a projector has processed one event stream.
type Checkpoint struct {
	Stream   event.StreamID `json:"stream"`
	Sequence event.Sequence `json:"sequence"`
}

// Projector consumes stored event records and updates a read model.
type Projector interface {
	Project(context.Context, []event.StoredRecord) error
}

// ProjectorFunc adapts a function into a Projector.
type ProjectorFunc func(context.Context, []event.StoredRecord) error

// Project calls f when f is non-nil.
func (f ProjectorFunc) Project(ctx context.Context, records []event.StoredRecord) error {
	if f == nil {
		return nil
	}
	return f(ctx, records)
}
