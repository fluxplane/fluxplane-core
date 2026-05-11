// Package operationruntime executes core operations.
//
// It owns runtime concerns around the pure core operation model: middleware
// chains, validation hooks, registries, lifecycle event emission, and result
// normalization. It does not own session lifecycle, workflow orchestration,
// persistence, or rendering.
package operationruntime
