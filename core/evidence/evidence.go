// Package evidence names the observation and assertion contracts used to turn
// rich runtime knowledge into session-local activation decisions.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Name identifies an evidence environment name.
type Name string

// Ref identifies an evidence environment.
type Ref struct {
	Name Name `json:"name"`
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

// ObserverSpec describes an inert observation source.
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

// AssertionDeriverSpec describes inert derivation from observation kinds to
// assertion kinds.
type AssertionDeriverSpec struct {
	Name             string              `json:"name"`
	Description      string              `json:"description,omitempty"`
	ObservationKinds []string            `json:"observation_kinds,omitempty"`
	Assertions       []AssertionTemplate `json:"assertions,omitempty"`
	Annotations      map[string]string   `json:"annotations,omitempty"`
}

// AssertionTemplate declares an assertion shape produced by a deriver.
type AssertionTemplate struct {
	Kind    string  `json:"kind"`
	Target  string  `json:"target,omitempty"`
	Subject Subject `json:"subject,omitempty"`
	Scope   string  `json:"scope,omitempty"`
	Source  string  `json:"source,omitempty"`
}

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

// Subject gives assertions a structured target vocabulary. Target is retained
// during migration for existing matchers and serialized configs.
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

// Observation is rich, inspectable runtime knowledge.
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

// Assertion is a normalized claim derived from one or more observations.
// Assertions are intentionally smaller than observations so activation and
// reaction matching can stay stable.
type Assertion struct {
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
func (a Assertion) ActivationKey() string {
	parts := []string{
		strings.TrimSpace(a.Kind),
		strings.TrimSpace(a.Target),
		strings.TrimSpace(string(a.Subject.Kind)),
		strings.TrimSpace(a.Subject.Name),
		strings.TrimSpace(a.Subject.ID),
		strings.TrimSpace(a.Scope),
		strings.TrimSpace(a.Source),
	}
	return strings.Join(parts, "\x1f")
}

// IsZero reports whether the assertion has no matching identity.
func (a Assertion) IsZero() bool {
	return strings.TrimSpace(a.Kind) == "" &&
		strings.TrimSpace(a.Target) == "" &&
		a.Subject.IsZero() &&
		strings.TrimSpace(a.Scope) == "" &&
		strings.TrimSpace(a.Source) == ""
}

// Fingerprint returns a stable hash of the assertion fields that should cause
// an on-change reaction to fire. It intentionally excludes observation time.
func (a Assertion) Fingerprint() string {
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
		Kind:           strings.TrimSpace(a.Kind),
		Target:         strings.TrimSpace(a.Target),
		Subject:        normalizedSubject(a.Subject),
		Scope:          strings.TrimSpace(a.Scope),
		Source:         strings.TrimSpace(a.Source),
		Environment:    a.Environment,
		Confidence:     a.Confidence,
		ObservationIDs: append([]string(nil), a.ObservationIDs...),
		Metadata:       cloneAssertionMetadata(a.Metadata),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		sum := sha256.Sum256([]byte(a.ActivationKey()))
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

func cloneAssertionMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
