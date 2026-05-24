package context

import (
	stdcontext "context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/event"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

// ProviderName identifies a context provider.
type ProviderName string

// AnnotationAutoContext marks providers that should remain visible even when an
// agent declares an explicit context-provider allowlist.
const AnnotationAutoContext = "fluxplane.auto_context"

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

// Placement describes where a context block should be rendered in the
// provider-visible request.
type Placement string

const (
	PlacementUser      Placement = "user_context"
	PlacementSystem    Placement = "system_context"
	PlacementDeveloper Placement = "developer_context"
)

// NormalizePlacement returns the default provider-visible placement.
func NormalizePlacement(placement Placement) Placement {
	switch placement {
	case PlacementSystem, PlacementDeveloper:
		return placement
	default:
		return PlacementUser
	}
}

// RenderReason describes why context is being materialized.
type RenderReason string

const (
	RenderInitial      RenderReason = "initial"
	RenderTurn         RenderReason = "turn"
	RenderToolFollowup RenderReason = "tool_followup"
	RenderContinuation RenderReason = "continuation"
	RenderResume       RenderReason = "resume"
)

// ProviderSpec describes a context provider without binding it to an
// implementation.
type ProviderSpec struct {
	Name             ProviderName      `json:"name"`
	Description      string            `json:"description,omitempty"`
	Kinds            []BlockKind       `json:"kinds,omitempty"`
	DefaultPlacement Placement         `json:"default_placement,omitempty"`
	Annotations      map[string]string `json:"annotations,omitempty"`
}

// Validate checks that the context provider spec has a stable identity.
func (s ProviderSpec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("context: provider spec name is empty")
	}
	return nil
}

// Request describes one context-building request.
type Request struct {
	ThreadID      string                     `json:"thread_id,omitempty"`
	BranchID      string                     `json:"branch_id,omitempty"`
	TurnID        string                     `json:"turn_id,omitempty"`
	Reason        RenderReason               `json:"reason,omitempty"`
	InputText     string                     `json:"input_text,omitempty"`
	RecentContext string                     `json:"recent_context,omitempty"`
	Scope         map[string]string          `json:"scope,omitempty"`
	Observations  []coreevidence.Observation `json:"observations,omitempty"`
	BudgetTokens  int                        `json:"budget_tokens,omitempty"`
	Previous      *ProviderRenderRecord      `json:"previous,omitempty"`
}

// Block is one structured context contribution.
type Block struct {
	ID          string             `json:"id,omitempty"`
	Provider    ProviderName       `json:"provider,omitempty"`
	Kind        BlockKind          `json:"kind,omitempty"`
	Placement   Placement          `json:"placement,omitempty"`
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

// FingerprintingProvider can cheaply report whether its provider state has
// changed since the previous committed render.
type FingerprintingProvider interface {
	Provider
	StateFingerprint(stdcontext.Context, Request) (fingerprint string, ok bool, err error)
}

// ProviderRenderRecord is the committed render state for one provider.
type ProviderRenderRecord struct {
	Provider    ProviderName                   `json:"provider,omitempty"`
	Fingerprint string                         `json:"fingerprint,omitempty"`
	Blocks      map[string]RenderedBlockRecord `json:"blocks,omitempty"`
}

// RenderedBlockRecord is the committed state for one context block.
type RenderedBlockRecord struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Block       Block  `json:"block,omitempty"`
	Removed     bool   `json:"removed,omitempty"`
}

// BlockRemoved records that a previously active block was removed.
type BlockRemoved struct {
	Provider            ProviderName `json:"provider,omitempty"`
	ID                  string       `json:"id"`
	Placement           Placement    `json:"placement,omitempty"`
	PreviousFingerprint string       `json:"previous_fingerprint,omitempty"`
}

// ProviderDiff describes one provider's render delta.
type ProviderDiff struct {
	Provider ProviderName         `json:"provider,omitempty"`
	Added    []Block              `json:"added,omitempty"`
	Updated  []Block              `json:"updated,omitempty"`
	Removed  []BlockRemoved       `json:"removed,omitempty"`
	Record   ProviderRenderRecord `json:"record,omitempty"`
	Skipped  bool                 `json:"skipped,omitempty"`
}

// BuildRequest describes one materialization request.
type BuildRequest struct {
	ThreadID      string                                `json:"thread_id,omitempty"`
	BranchID      string                                `json:"branch_id,omitempty"`
	TurnID        string                                `json:"turn_id,omitempty"`
	Reason        RenderReason                          `json:"reason,omitempty"`
	InputText     string                                `json:"input_text,omitempty"`
	RecentContext string                                `json:"recent_context,omitempty"`
	Scope         map[string]string                     `json:"scope,omitempty"`
	Observations  []coreevidence.Observation            `json:"observations,omitempty"`
	BudgetTokens  int                                   `json:"budget_tokens,omitempty"`
	Previous      map[ProviderName]ProviderRenderRecord `json:"previous,omitempty"`
}

// BuildResult is the context delta and next committed render state.
type BuildResult struct {
	TurnID    string                                `json:"turn_id,omitempty"`
	Reason    RenderReason                          `json:"reason,omitempty"`
	Providers []ProviderDiff                        `json:"providers,omitempty"`
	Added     []Block                               `json:"added,omitempty"`
	Updated   []Block                               `json:"updated,omitempty"`
	Removed   []BlockRemoved                        `json:"removed,omitempty"`
	Active    []Block                               `json:"active,omitempty"`
	Records   map[ProviderName]ProviderRenderRecord `json:"records,omitempty"`
}

// EmptyDiff reports whether the render produced no provider-visible changes.
func (r BuildResult) EmptyDiff() bool {
	return len(r.Added) == 0 && len(r.Updated) == 0 && len(r.Removed) == 0
}

const (
	EventBlockRecorded   event.Name = "context.block.recorded"
	EventBlockRemoved    event.Name = "context.block.removed"
	EventRenderCommitted event.Name = "context.render.committed"
)

// BlockRecorded records an added or updated context block.
type BlockRecorded struct {
	TurnID      string       `json:"turn_id,omitempty"`
	Provider    ProviderName `json:"provider,omitempty"`
	Block       Block        `json:"block"`
	Fingerprint string       `json:"fingerprint,omitempty"`
}

func (BlockRecorded) EventName() event.Name { return EventBlockRecorded }

// BlockRemovedRecorded records a context block removal.
type BlockRemovedRecorded struct {
	TurnID  string       `json:"turn_id,omitempty"`
	Removed BlockRemoved `json:"removed"`
}

func (BlockRemovedRecorded) EventName() event.Name { return EventBlockRemoved }

// RenderCommitted records the complete committed context render state.
type RenderCommitted struct {
	TurnID  string                                `json:"turn_id,omitempty"`
	Records map[ProviderName]ProviderRenderRecord `json:"records,omitempty"`
}

func (RenderCommitted) EventName() event.Name { return EventRenderCommitted }

// RegisterEvents registers context render event payloads with registry.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("context: event registry is nil")
	}
	for _, sample := range []event.Event{
		BlockRecorded{},
		BlockRemovedRecorded{},
		RenderCommitted{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
