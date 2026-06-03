package context

import (
	stdcontext "context"
	"fmt"

	fpcontext "github.com/fluxplane/fluxplane-context"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-event"
)

type ProviderName = fpcontext.ProviderName

// AnnotationAutoContext marks providers that should remain visible even when an
// agent declares an explicit context-provider allowlist.
const AnnotationAutoContext = "fluxplane.auto_context"

type ProviderRef = fpcontext.ProviderRef

type BlockKind = fpcontext.BlockKind

const (
	BlockText      = fpcontext.BlockText
	BlockReference = fpcontext.BlockReference
	BlockData      = fpcontext.BlockData
)

type Freshness = fpcontext.Freshness

const (
	FreshnessStatic  = fpcontext.FreshnessStatic
	FreshnessDynamic = fpcontext.FreshnessDynamic
)

type Placement = fpcontext.Placement

const (
	PlacementUser      = fpcontext.PlacementUser
	PlacementSystem    = fpcontext.PlacementSystem
	PlacementDeveloper = fpcontext.PlacementDeveloper
)

// NormalizePlacement returns the default provider-visible placement.
func NormalizePlacement(placement Placement) Placement {
	return fpcontext.NormalizePlacement(placement)
}

type RenderReason = fpcontext.RenderReason

const (
	RenderInitial      = fpcontext.RenderInitial
	RenderTurn         = fpcontext.RenderTurn
	RenderToolFollowup = fpcontext.RenderToolFollowup
	RenderContinuation = fpcontext.RenderContinuation
	RenderResume       = fpcontext.RenderResume
)

type ProviderSpec = fpcontext.Spec

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

// RequestFromPortable wraps a runtime-neutral context request in Core's
// evidence-aware request shape.
func RequestFromPortable(req fpcontext.Request) Request {
	return Request{
		ThreadID:      req.ThreadID,
		BranchID:      req.BranchID,
		TurnID:        req.TurnID,
		Reason:        req.Reason,
		InputText:     req.InputText,
		RecentContext: req.RecentContext,
		Scope:         cloneStringMap(req.Scope),
		BudgetTokens:  req.BudgetTokens,
	}
}

// Portable returns the runtime-neutral portion of this Core context request.
func (r Request) Portable() fpcontext.Request {
	return fpcontext.Request{
		ThreadID:      r.ThreadID,
		BranchID:      r.BranchID,
		TurnID:        r.TurnID,
		Reason:        r.Reason,
		InputText:     r.InputText,
		RecentContext: r.RecentContext,
		Scope:         cloneStringMap(r.Scope),
		BudgetTokens:  r.BudgetTokens,
	}
}

type Block = fpcontext.Block

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

// Portable returns the runtime-neutral portion of this materialization request.
func (r BuildRequest) Portable() fpcontext.Request {
	return fpcontext.Request{
		ThreadID:      r.ThreadID,
		BranchID:      r.BranchID,
		TurnID:        r.TurnID,
		Reason:        r.Reason,
		InputText:     r.InputText,
		RecentContext: r.RecentContext,
		Scope:         cloneStringMap(r.Scope),
		BudgetTokens:  r.BudgetTokens,
	}
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
