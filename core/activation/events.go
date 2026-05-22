package activation

import (
	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/event"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/skill"
)

const (
	EventFocusDetected           event.Name = "focus.detected"
	EventSurfacePrepareRequested event.Name = "surface.prepare_requested"
	EventSurfaceResolved         event.Name = "surface.resolved"
	EventSurfacePrepared         event.Name = "surface.prepared"
	EventSurfacePrepareSkipped   event.Name = "surface.prepare_skipped"
	EventSurfaceExpired          event.Name = "surface.expired"
)

// Source describes who or what requested focus/surface preparation.
type Source string

const (
	SourceReaction            Source = "reaction"
	SourceUserCommand         Source = "user_command"
	SourceUserDirective       Source = "user_directive"
	SourceModelFocus          Source = "model_focus"
	SourceModelPrepare        Source = "model_prepare"
	SourceWorkflow            Source = "workflow"
	SourceReflectionCandidate Source = "reflection_candidate"
)

// Lifetime describes how long a prepared surface remains active.
type Lifetime string

const (
	LifetimeTurn    Lifetime = "turn"
	LifetimeRun     Lifetime = "run"
	LifetimeSession Lifetime = "session"
)

// FocusSource records a source that shaped the current focus.
type FocusSource struct {
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value,omitempty"`
}

// FocusDetected records a structured declaration of current work focus.
type FocusDetected struct {
	Objective      string                 `json:"objective,omitempty"`
	Intents        []string               `json:"intents,omitempty"`
	Subjects       []coreevidence.Subject `json:"subjects,omitempty"`
	Sources        []FocusSource          `json:"sources,omitempty"`
	Source         Source                 `json:"source,omitempty"`
	Summary        string                 `json:"summary,omitempty"`
	Rationale      string                 `json:"rationale,omitempty"`
	Confidence     float64                `json:"confidence,omitempty"`
	ObservationIDs []string               `json:"observation_ids,omitempty"`
	AssertionIDs   []string               `json:"assertion_ids,omitempty"`
}

func (FocusDetected) EventName() event.Name { return EventFocusDetected }

// SurfacePrepareRequested records a request to prepare matching resources.
type SurfacePrepareRequested struct {
	Terms          []string          `json:"terms,omitempty"`
	ActivationSets []string          `json:"activation_sets,omitempty"`
	Objective      string            `json:"objective,omitempty"`
	Lifetime       Lifetime          `json:"lifetime,omitempty"`
	Source         Source            `json:"source,omitempty"`
	Provenance     map[string]string `json:"provenance,omitempty"`
}

func (SurfacePrepareRequested) EventName() event.Name { return EventSurfacePrepareRequested }

// ResolvedResource is a compact matched resource reference.
type ResolvedResource struct {
	Kind    TargetKind `json:"kind,omitempty"`
	Name    string     `json:"name,omitempty"`
	Address string     `json:"address,omitempty"`
	Alias   string     `json:"alias,omitempty"`
}

// ResolutionCandidate records an ambiguous possible match for a requested term.
type ResolutionCandidate struct {
	Term      string             `json:"term,omitempty"`
	Matches   []ResolvedResource `json:"matches,omitempty"`
	Message   string             `json:"message,omitempty"`
	Reason    string             `json:"reason,omitempty"`
	ScoreHint float64            `json:"score_hint,omitempty"`
}

// Diagnostic records a compact preparation diagnostic safe for trace output.
type Diagnostic struct {
	Term    string `json:"term,omitempty"`
	Target  string `json:"target,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// SurfaceResolved records deterministic resolution from terms to resources.
type SurfaceResolved struct {
	ActivationSets  []string              `json:"activation_sets,omitempty"`
	Resources       []ResolvedResource    `json:"resources,omitempty"`
	Ambiguous       []ResolutionCandidate `json:"ambiguous,omitempty"`
	UnmatchedTerms  []string              `json:"unmatched_terms,omitempty"`
	Skipped         []Diagnostic          `json:"skipped,omitempty"`
	Diagnostics     []Diagnostic          `json:"diagnostics,omitempty"`
	ResolutionNotes []string              `json:"resolution_notes,omitempty"`
}

func (SurfaceResolved) EventName() event.Name { return EventSurfaceResolved }

// SurfacePrepared records the resources that became active.
type SurfacePrepared struct {
	ActivationSets   []string                  `json:"activation_sets,omitempty"`
	Operations       []operation.Ref           `json:"operations,omitempty"`
	OperationSets    []string                  `json:"operation_sets,omitempty"`
	ContextProviders []corecontext.ProviderRef `json:"context_providers,omitempty"`
	Datasources      []datasource.Ref          `json:"datasources,omitempty"`
	Skills           []skill.Ref               `json:"skills,omitempty"`
	References       []ReferenceTarget         `json:"references,omitempty"`
	InlineContexts   []string                  `json:"inline_contexts,omitempty"`
	Lifetime         Lifetime                  `json:"lifetime,omitempty"`
	Source           Source                    `json:"source,omitempty"`
	Diagnostics      []Diagnostic              `json:"diagnostics,omitempty"`
}

func (SurfacePrepared) EventName() event.Name { return EventSurfacePrepared }

// SurfacePrepareSkipped records one candidate that was not activated.
type SurfacePrepareSkipped struct {
	Term          string     `json:"term,omitempty"`
	ActivationSet string     `json:"activation_set,omitempty"`
	Resource      string     `json:"resource,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Source        Source     `json:"source,omitempty"`
	Diagnostic    Diagnostic `json:"diagnostic,omitempty"`
}

func (SurfacePrepareSkipped) EventName() event.Name { return EventSurfacePrepareSkipped }

// SurfaceExpired records removal of short-lived prepared resources.
type SurfaceExpired struct {
	ActivationSets   []string                  `json:"activation_sets,omitempty"`
	Operations       []operation.Ref           `json:"operations,omitempty"`
	OperationSets    []string                  `json:"operation_sets,omitempty"`
	ContextProviders []corecontext.ProviderRef `json:"context_providers,omitempty"`
	Datasources      []datasource.Ref          `json:"datasources,omitempty"`
	Skills           []skill.Ref               `json:"skills,omitempty"`
	References       []ReferenceTarget         `json:"references,omitempty"`
	InlineContexts   []string                  `json:"inline_contexts,omitempty"`
	Lifetime         Lifetime                  `json:"lifetime,omitempty"`
	Reason           string                    `json:"reason,omitempty"`
}

func (SurfaceExpired) EventName() event.Name { return EventSurfaceExpired }
