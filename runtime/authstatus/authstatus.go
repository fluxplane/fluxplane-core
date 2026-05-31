// Package authstatus adapts shared auth status evaluation into runtime evidence.
package authstatus

import (
	"context"
	"strings"
	"time"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
	sharedauthstatus "github.com/fluxplane/fluxplane-auth/authstatus"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/resource"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
)

const (
	ObservationKind          = "auth.status"
	AssertionAuthenticated   = "integration.authenticated"
	ObserverName             = "auth.status"
	AssertionDeriverName     = "auth.assertions"
	StatusConnected          = sharedauthstatus.StatusConnected
	StatusNotConnected       = sharedauthstatus.StatusNotConnected
	defaultObservationSource = "auth"
)

type Status = sharedauthstatus.Status
type FieldStatus = sharedauthstatus.FieldStatus

// Target is one plugin instance whose declared auth methods should be observed.
type Target struct {
	Ref     resource.PluginRef
	Methods []coresecret.AuthMethodSpec
}

// NewObserver returns a startup observer for auth readiness.
func NewObserver(targets []Target, resolver coresecret.Resolver) runtimeevidence.Observer {
	return observer{targets: append([]Target(nil), targets...), resolver: resolver}
}

// NewAssertionDeriver returns a deriver for auth readiness observations.
func NewAssertionDeriver() runtimeevidence.AssertionDeriver {
	return assertionDeriver{}
}

type observer struct {
	targets  []Target
	resolver coresecret.Resolver
}

func (observer) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            ObserverName,
		Description:     "Reports non-secret plugin auth readiness.",
		Phase:           coreevidence.PhaseStartup,
		ObservableKinds: []string{ObservationKind},
		Annotations:     map[string]string{"source": defaultObservationSource},
	}
}

func (o observer) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	out := make([]coreevidence.Observation, 0, len(o.targets))
	now := time.Now().UTC()
	for _, target := range o.targets {
		ref := target.Ref
		status := sharedauthstatus.Evaluate(ctx, o.resolver, sharedauthstatus.Target{
			Plugin:   ref.Name,
			Instance: ref.InstanceName(),
			Methods:  target.Methods,
		})
		out = append(out, coreevidence.Observation{
			ID:      "auth:" + ref.Name + ":" + ref.InstanceName(),
			Source:  defaultObservationSource,
			Kind:    ObservationKind,
			Scope:   ref.InstanceName(),
			Content: status,
			Metadata: map[string]any{
				"plugin":   status.Plugin,
				"instance": status.Instance,
				"method":   status.Method,
				"status":   status.Status,
				"fields":   fieldMetadata(status.Fields),
			},
			At: now,
		})
	}
	return out, nil
}

type assertionDeriver struct{}

func (assertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             AssertionDeriverName,
		Description:      "Derives integration authenticated assertions from auth status observations.",
		ObservationKinds: []string{ObservationKind},
		Assertions: []coreevidence.AssertionTemplate{{
			Kind:    AssertionAuthenticated,
			Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration},
		}},
	}
}

func (assertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != ObservationKind {
			continue
		}
		status, ok := statusFromObservation(observation.Content)
		if !ok || !status.Connected {
			continue
		}
		target := status.Plugin
		subjectID := status.Plugin + "/" + status.Instance
		out = append(out, coreevidence.Assertion{
			Kind:           AssertionAuthenticated,
			Target:         target,
			Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: status.Plugin, ID: subjectID},
			Scope:          status.Instance,
			Source:         defaultObservationSource,
			Confidence:     1,
			ObservationIDs: observationIDs(observation.ID),
			Metadata: map[string]string{
				"plugin":   status.Plugin,
				"instance": status.Instance,
				"method":   status.Method,
				"status":   status.Status,
				"fields":   strings.Join(setFieldNames(status.Fields), ","),
			},
		})
	}
	return out, nil
}

func fieldMetadata(fields []FieldStatus) string {
	return strings.Join(setFieldNames(fields), ",")
}

func setFieldNames(fields []FieldStatus) []string {
	var out []string
	for _, field := range fields {
		if field.Set && strings.TrimSpace(field.Name) != "" {
			out = append(out, strings.TrimSpace(field.Name))
		}
	}
	return out
}

func statusFromObservation(content any) (Status, bool) {
	switch typed := content.(type) {
	case Status:
		return typed, true
	case *Status:
		if typed == nil {
			return Status{}, false
		}
		return *typed, true
	default:
		return Status{}, false
	}
}

func observationIDs(id string) []string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return []string{id}
}
