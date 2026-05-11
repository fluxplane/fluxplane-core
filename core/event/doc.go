// Package event defines the pure core model for typed domain events.
//
// Event payloads are typed facts emitted by operations, workflows, sessions,
// capabilities, or other runtime components. Records are delivery/persistence
// envelopes created by runtime or orchestration layers when an event is
// streamed, stored, correlated, replayed, filtered, or rendered.
package event
