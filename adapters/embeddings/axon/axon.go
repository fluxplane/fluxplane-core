// Package axon adapts Axon's local Hugot embedding provider.
package axon

import (
	"context"
	"strings"

	axonembeddings "github.com/codewandler/axon/indexer/embeddings"
)

const (
	// ProviderName selects the local Axon/Hugot CPU embedding adapter.
	ProviderName = "axon"
)

// Embedder adapts Axon's embedding provider to the semantic index embedder interface.
type Embedder struct {
	provider axonembeddings.Provider
}

// Config configures the local Axon embedding adapter.
type Config struct {
	ModelPath string
	Model     string
}

// New returns a local CPU embedder backed by Axon's Hugot provider.
func New(cfg Config) *Embedder {
	return &Embedder{provider: axonembeddings.NewHugot(cfg.ModelPath, cfg.Model)}
}

// Model returns the provider/model identifier used for incremental index invalidation.
func (e *Embedder) Model() string {
	if e == nil || e.provider == nil {
		return ProviderName
	}
	return ProviderName + "/" + strings.TrimSpace(e.provider.Name())
}

// Embed embeds all texts in one local model inference batch.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.provider.EmbedBatch(ctx, texts)
}

// Close releases local model resources.
func (e *Embedder) Close() error {
	if e == nil || e.provider == nil {
		return nil
	}
	return e.provider.Close()
}
