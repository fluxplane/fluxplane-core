package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
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

// ObservationPhase describes when an observer may run.
type ObservationPhase string

const (
	PhaseStartup      ObservationPhase = "startup"
	PhaseSessionOpen  ObservationPhase = "session_open"
	PhaseTurn         ObservationPhase = "turn"
	PhaseToolFollowup ObservationPhase = "tool_followup"
	PhaseLazy         ObservationPhase = "lazy"
)

// ObserverSpec describes an inert observation source contributed by runtime or
// plugins. Executable observer implementations live outside core.
type ObserverSpec struct {
	Name            string            `json:"name" yaml:"name"`
	Description     string            `json:"description,omitempty" yaml:"description,omitempty"`
	Environment     Ref               `json:"environment,omitempty" yaml:"environment,omitempty"`
	Phase           ObservationPhase  `json:"phase,omitempty" yaml:"phase,omitempty"`
	ObservableKinds []string          `json:"observable_kinds,omitempty" yaml:"observable_kinds,omitempty"`
	Dynamic         bool              `json:"dynamic,omitempty" yaml:"dynamic,omitempty"`
	Disabled        bool              `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// SignalDeriverSpec describes an inert derivation from observation kinds to
// signal kinds. Executable derivation logic lives outside core.
type SignalDeriverSpec struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	ObservationKinds []string          `json:"observation_kinds,omitempty"`
	Signals          []SignalTemplate  `json:"signals,omitempty"`
	Annotations      map[string]string `json:"annotations,omitempty"`
}

// SignalTemplate describes a signal shape produced by a deriver.
type SignalTemplate struct {
	Kind    string  `json:"kind"`
	Target  string  `json:"target,omitempty"`
	Subject Subject `json:"subject,omitempty"`
	Scope   string  `json:"scope,omitempty"`
	Source  string  `json:"source,omitempty"`
}

// SubjectKind identifies the kind of entity a signal/assertion is about.
type SubjectKind string

const (
	SubjectLanguage    SubjectKind = "language"
	SubjectToolchain   SubjectKind = "toolchain"
	SubjectIntegration SubjectKind = "integration"
	SubjectEndpoint    SubjectKind = "endpoint"
	SubjectCapability  SubjectKind = "capability"
	SubjectProvider    SubjectKind = "provider"
)

// Subject gives signals/assertions a structured target vocabulary. Target is
// retained during migration for existing matchers and serialized configs.
type Subject struct {
	Kind SubjectKind `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name string      `json:"name,omitempty" yaml:"name,omitempty"`
	ID   string      `json:"id,omitempty" yaml:"id,omitempty"`
}

// IsZero reports whether the subject has no matching identity.
func (s Subject) IsZero() bool {
	return strings.TrimSpace(string(s.Kind)) == "" &&
		strings.TrimSpace(s.Name) == "" &&
		strings.TrimSpace(s.ID) == ""
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
	Scope       string         `json:"scope,omitempty"`
	Content     any            `json:"content,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	At          time.Time      `json:"at,omitempty"`
}

// Signal is a normalized activation hint derived from one or more
// observations. Signals are intentionally smaller than observations so
// activation and reaction matching can stay stable.
type Signal struct {
	Kind           string            `json:"kind"`
	Target         string            `json:"target,omitempty"`
	Subject        Subject           `json:"subject,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Source         string            `json:"source,omitempty"`
	Environment    Ref               `json:"environment,omitempty"`
	Confidence     float64           `json:"confidence,omitempty"`
	ObservationIDs []string          `json:"observation_ids,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// ActivationKey returns the stable identity used for de-duplication and
// appeared/changed/disappeared comparisons.
func (s Signal) ActivationKey() string {
	parts := []string{
		strings.TrimSpace(s.Kind),
		strings.TrimSpace(s.Target),
		strings.TrimSpace(string(s.Subject.Kind)),
		strings.TrimSpace(s.Subject.Name),
		strings.TrimSpace(s.Subject.ID),
		strings.TrimSpace(s.Scope),
		strings.TrimSpace(s.Source),
	}
	return strings.Join(parts, "\x1f")
}

// IsZero reports whether the signal has no matching identity.
func (s Signal) IsZero() bool {
	return strings.TrimSpace(s.Kind) == "" &&
		strings.TrimSpace(s.Target) == "" &&
		s.Subject.IsZero() &&
		strings.TrimSpace(s.Scope) == "" &&
		strings.TrimSpace(s.Source) == ""
}

// Fingerprint returns a stable hash of the signal fields that should cause an
// on-change reaction to fire. It intentionally excludes observation time.
func (s Signal) Fingerprint() string {
	payload := struct {
		Kind           string            `json:"kind,omitempty"`
		Target         string            `json:"target,omitempty"`
		Subject        Subject           `json:"subject,omitempty"`
		Scope          string            `json:"scope,omitempty"`
		Source         string            `json:"source,omitempty"`
		Environment    Ref               `json:"environment,omitempty"`
		Confidence     float64           `json:"confidence,omitempty"`
		ObservationIDs []string          `json:"observation_ids,omitempty"`
		Metadata       map[string]string `json:"metadata,omitempty"`
	}{
		Kind:           strings.TrimSpace(s.Kind),
		Target:         strings.TrimSpace(s.Target),
		Subject:        normalizedSubject(s.Subject),
		Scope:          strings.TrimSpace(s.Scope),
		Source:         strings.TrimSpace(s.Source),
		Environment:    s.Environment,
		Confidence:     s.Confidence,
		ObservationIDs: append([]string(nil), s.ObservationIDs...),
		Metadata:       cloneSignalMetadata(s.Metadata),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		sum := sha256.Sum256([]byte(s.ActivationKey()))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizedSubject(subject Subject) Subject {
	return Subject{
		Kind: SubjectKind(strings.TrimSpace(string(subject.Kind))),
		Name: strings.TrimSpace(subject.Name),
		ID:   strings.TrimSpace(subject.ID),
	}
}

func cloneSignalMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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
