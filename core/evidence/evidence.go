// Package evidence names the observation and assertion contracts used to turn
// rich runtime knowledge into session-local activation decisions.
package evidence

import coreenvironment "github.com/fluxplane/agentruntime/core/environment"

const (
	SubjectLanguage    = coreenvironment.SubjectLanguage
	SubjectToolchain   = coreenvironment.SubjectToolchain
	SubjectIntegration = coreenvironment.SubjectIntegration
	SubjectEndpoint    = coreenvironment.SubjectEndpoint
	SubjectCapability  = coreenvironment.SubjectCapability
	SubjectProvider    = coreenvironment.SubjectProvider
)

// SubjectKind identifies the kind of entity an assertion is about.
type SubjectKind = coreenvironment.SubjectKind

// Subject gives assertions a structured target vocabulary.
type Subject = coreenvironment.Subject

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
