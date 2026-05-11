package environment

import (
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

// Name identifies an environment.
type Name string

// Ref identifies an environment by name.
type Ref struct {
	Name Name `json:"name"`
}

// Scope describes the environment boundary.
type Scope string

const (
	ScopeLocal     Scope = "local"
	ScopeWorkspace Scope = "workspace"
	ScopeSession   Scope = "session"
	ScopeRemote    Scope = "remote"
	ScopeExternal  Scope = "external"
)

// Persistence describes whether environment state is durable.
type Persistence string

const (
	PersistenceEphemeral Persistence = "ephemeral"
	PersistenceSession   Persistence = "session"
	PersistenceDurable   Persistence = "durable"
)

// Boundary describes the scoped world an environment represents.
type Boundary struct {
	Scope       Scope        `json:"scope,omitempty"`
	Trust       policy.Trust `json:"trust,omitempty"`
	Persistence Persistence  `json:"persistence,omitempty"`
}

// Observable declares one kind of observation the environment may produce.
type Observable struct {
	Kind        string            `json:"kind"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Effect declares one operation that is meaningful at this environment
// boundary. Operation runtime still owns actual execution.
type Effect struct {
	Operation   operation.Ref       `json:"operation"`
	Description string              `json:"description,omitempty"`
	Semantics   operation.Semantics `json:"semantics,omitempty"`
	Metadata    map[string]string   `json:"metadata,omitempty"`
}

// Spec is an inert environment definition.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Boundary    Boundary          `json:"boundary,omitempty"`
	Observables []Observable      `json:"observables,omitempty"`
	Effects     []Effect          `json:"effects,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Observation is a perceived or produced fact from an environment.
type Observation struct {
	ID          string         `json:"id,omitempty"`
	Environment Ref            `json:"environment,omitempty"`
	Source      string         `json:"source,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	Content     any            `json:"content,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	At          time.Time      `json:"at,omitempty"`
}

// EffectRequest asks orchestration/runtime to apply one operation within an
// environment boundary.
type EffectRequest struct {
	Environment Ref             `json:"environment,omitempty"`
	Operation   operation.Ref   `json:"operation"`
	Input       operation.Value `json:"input,omitempty"`
}

// EffectResult reports the result of applying an effect and the observation it
// produced, if any.
type EffectResult struct {
	Result      operation.Result `json:"result"`
	Observation Observation      `json:"observation,omitempty"`
}
