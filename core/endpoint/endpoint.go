package endpoint

import shared "github.com/fluxplane/fluxplane-endpoint"

// Ref identifies an endpoint stored or resolved by runtime code.
type Ref = shared.Ref

// Spec describes an explicitly configured endpoint.
type Spec = shared.Spec

// SourceRef describes where an endpoint came from without importing the source
// system into core.
type SourceRef = shared.SourceRef

// Resolved is the runtime-ready view of an endpoint.
type Resolved = shared.Resolved

// NewRef returns the canonical endpoint ref for id.
func NewRef(id string) Ref { return shared.NewRef(id) }

// ParseRef parses a canonical endpoint ref or bare id.
func ParseRef(value string) Ref { return shared.ParseRef(value) }
