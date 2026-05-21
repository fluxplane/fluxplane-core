package projections

import (
	"context"
	"fmt"

	"github.com/fluxplane/engine/core/event"
	coreprojection "github.com/fluxplane/engine/core/projection"
	runtimeprojection "github.com/fluxplane/engine/runtime/projection"
)

// Manager owns projection freshness policy for use cases.
type Manager struct {
	Runner     runtimeprojection.Runner
	MaxBatches int
}

// EnsureFresh runs projector until stream has no more records after its
// checkpoint, or until MaxBatches is reached when configured.
func (m Manager) EnsureFresh(ctx context.Context, name string, stream event.StreamID, projector coreprojection.Projector) (coreprojection.Checkpoint, error) {
	if name == "" {
		return coreprojection.Checkpoint{}, fmt.Errorf("projections: name is empty")
	}
	var last coreprojection.Checkpoint
	for batches := 0; ; batches++ {
		if m.MaxBatches > 0 && batches >= m.MaxBatches {
			return last, fmt.Errorf("projections: projection %q did not become fresh after %d batches", name, m.MaxBatches)
		}
		next, err := m.Runner.RunOnce(ctx, name, stream, projector)
		if err != nil {
			return coreprojection.Checkpoint{}, err
		}
		if next == last {
			return next, nil
		}
		last = next
	}
}
