package projection

import (
	"context"
	"fmt"
	"sync"

	coreprojection "github.com/fluxplane/agentruntime/core/projection"
)

// CheckpointStore stores projector progress.
type CheckpointStore interface {
	Load(context.Context, string) (coreprojection.Checkpoint, error)
	Save(context.Context, string, coreprojection.Checkpoint) error
}

// MemoryCheckpointStore stores checkpoints in memory.
type MemoryCheckpointStore struct {
	mu          sync.Mutex
	checkpoints map[string]coreprojection.Checkpoint
}

// NewMemoryCheckpointStore returns an empty checkpoint store.
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{checkpoints: map[string]coreprojection.Checkpoint{}}
}

// Load returns the checkpoint for name, or the zero checkpoint when missing.
func (s *MemoryCheckpointStore) Load(ctx context.Context, name string) (coreprojection.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return coreprojection.Checkpoint{}, err
	}
	if name == "" {
		return coreprojection.Checkpoint{}, fmt.Errorf("projection: checkpoint name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpoints[name], nil
}

// Save stores checkpoint for name.
func (s *MemoryCheckpointStore) Save(ctx context.Context, name string, checkpoint coreprojection.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("projection: checkpoint name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkpoints == nil {
		s.checkpoints = map[string]coreprojection.Checkpoint{}
	}
	s.checkpoints[name] = checkpoint
	return nil
}
