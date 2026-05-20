// Package evidence names the observation and assertion contracts used to turn
// rich runtime knowledge into session-local activation decisions.
package evidence

import coreenvironment "github.com/fluxplane/agentruntime/core/environment"

// SubjectKind identifies the kind of entity an assertion is about.
type SubjectKind string

const (
	SubjectLanguage    SubjectKind = "language"
	SubjectToolchain   SubjectKind = "toolchain"
	SubjectIntegration SubjectKind = "integration"
	SubjectEndpoint    SubjectKind = "endpoint"
	SubjectCapability  SubjectKind = "capability"
	SubjectProvider    SubjectKind = "provider"
)

// Subject gives assertions a structured target vocabulary. The existing
// environment.Signal.Target field remains the transport shape during migration.
type Subject struct {
	Kind SubjectKind `json:"kind,omitempty"`
	Name string      `json:"name,omitempty"`
	ID   string      `json:"id,omitempty"`
}

// Ref identifies an evidence environment.
type Ref = coreenvironment.Ref

// ObservationPhase describes when an observer may run.
type ObservationPhase = coreenvironment.ObservationPhase

const (
	PhaseStartup      = coreenvironment.PhaseStartup
	PhaseSessionOpen  = coreenvironment.PhaseSessionOpen
	PhaseTurn         = coreenvironment.PhaseTurn
	PhaseToolFollowup = coreenvironment.PhaseToolFollowup
	PhaseLazy         = coreenvironment.PhaseLazy
)

// ObserverSpec describes an inert observation source.
type ObserverSpec = coreenvironment.ObserverSpec

// Observation is rich, inspectable runtime knowledge.
type Observation = coreenvironment.Observation

// Assertion is a normalized claim derived from observations.
type Assertion = coreenvironment.Signal

// AssertionTemplate declares an assertion shape produced by a deriver.
type AssertionTemplate = coreenvironment.SignalTemplate

// AssertionDeriverSpec describes inert derivation from observation kinds to
// assertion kinds.
type AssertionDeriverSpec = coreenvironment.SignalDeriverSpec
