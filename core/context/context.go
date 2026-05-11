package context

import (
	stdcontext "context"

	"github.com/fluxplane/agentruntime/core/policy"
)

// ProviderName identifies a context provider.
type ProviderName string

// ProviderRef identifies a provider by name.
type ProviderRef struct {
	Name ProviderName `json:"name"`
}

// BlockKind describes the shape or purpose of a context block.
type BlockKind string

const (
	BlockText      BlockKind = "text"
	BlockReference BlockKind = "reference"
	BlockData      BlockKind = "data"
)

// Freshness describes whether a block is static or time-sensitive.
type Freshness string

const (
	FreshnessStatic  Freshness = "static"
	FreshnessDynamic Freshness = "dynamic"
)

// ProviderSpec describes a context provider without binding it to an
// implementation.
type ProviderSpec struct {
	Name        ProviderName      `json:"name"`
	Description string            `json:"description,omitempty"`
	Kinds       []BlockKind       `json:"kinds,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Request describes one context-building request.
type Request struct {
	Scope        map[string]string `json:"scope,omitempty"`
	BudgetTokens int               `json:"budget_tokens,omitempty"`
}

// Block is one structured context contribution.
type Block struct {
	ID          string             `json:"id,omitempty"`
	Provider    ProviderName       `json:"provider,omitempty"`
	Kind        BlockKind          `json:"kind,omitempty"`
	Title       string             `json:"title,omitempty"`
	Content     string             `json:"content,omitempty"`
	URI         string             `json:"uri,omitempty"`
	MediaType   string             `json:"media_type,omitempty"`
	Priority    int                `json:"priority,omitempty"`
	Tokens      int                `json:"tokens,omitempty"`
	Sensitivity policy.Sensitivity `json:"sensitivity,omitempty"`
	Freshness   Freshness          `json:"freshness,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
}

// Provider is the minimal core port for producing structured context blocks.
type Provider interface {
	Spec() ProviderSpec
	Build(stdcontext.Context, Request) ([]Block, error)
}
