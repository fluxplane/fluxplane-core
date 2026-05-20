// Package evidence names runtime execution contracts for evidence observers and
// assertion derivers.
package evidence

import runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"

// ObservationRequest is passed to evidence observers.
type ObservationRequest = runtimeenvironment.ObservationRequest

// Observer produces rich evidence observations.
type Observer = runtimeenvironment.Observer

// AssertionDeriveRequest is passed to assertion derivers.
type AssertionDeriveRequest = runtimeenvironment.SignalDeriveRequest

// AssertionDeriver converts observations into normalized assertions.
type AssertionDeriver = runtimeenvironment.SignalDeriver

// Diagnostic describes observer or deriver failures.
type Diagnostic = runtimeenvironment.Diagnostic

// RunObservers executes observers whose spec phase matches the requested phase.
var RunObservers = runtimeenvironment.RunObservers

// DeriveAssertions runs all derivers over the active observation set.
var DeriveAssertions = runtimeenvironment.DeriveSignals
