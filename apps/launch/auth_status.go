package launch

import (
	"context"
	"strings"
	"time"

	sharedauthstatus "github.com/fluxplane/fluxplane-auth/authstatus"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	coreevidence "github.com/fluxplane/fluxplane-evidence"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
)

const (
	authStatusObservationKind        = "auth.status"
	authStatusAssertionAuthenticated = "integration.authenticated"
	authStatusObserverName           = "auth.status"
	authStatusAssertionDeriverName   = "auth.assertions"
	authStatusObservationSource      = "auth"
)

func newAuthStatusObserver(targets []sharedauthstatus.Target, resolver sharedsecret.Resolver) runtimeevidence.Observer {
	return authStatusObserver{targets: append([]sharedauthstatus.Target(nil), targets...), resolver: resolver}
}

func newAuthStatusAssertionDeriver() runtimeevidence.AssertionDeriver {
	return authStatusAssertionDeriver{}
}

type authStatusObserver struct {
	targets  []sharedauthstatus.Target
	resolver sharedsecret.Resolver
}

func (authStatusObserver) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            authStatusObserverName,
		Description:     "Reports non-secret plugin auth readiness.",
		Phase:           coreevidence.PhaseStartup,
		ObservableKinds: []string{authStatusObservationKind},
		Annotations:     map[string]string{"source": authStatusObservationSource},
	}
}

func (o authStatusObserver) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	out := make([]coreevidence.Observation, 0, len(o.targets))
	now := time.Now().UTC()
	for _, target := range o.targets {
		status := sharedauthstatus.Evaluate(ctx, o.resolver, target)
		out = append(out, coreevidence.Observation{
			ID:      "auth:" + target.Plugin + ":" + target.Instance,
			Source:  authStatusObservationSource,
			Kind:    authStatusObservationKind,
			Scope:   target.Instance,
			Content: status,
			Metadata: map[string]any{
				"plugin":   status.Plugin,
				"instance": status.Instance,
				"method":   status.Method,
				"status":   status.Status,
				"fields":   authStatusFieldMetadata(status.Fields),
			},
			At: now,
		})
	}
	return out, nil
}

type authStatusAssertionDeriver struct{}

func (authStatusAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             authStatusAssertionDeriverName,
		Description:      "Derives integration authenticated assertions from auth status observations.",
		ObservationKinds: []string{authStatusObservationKind},
		Assertions: []coreevidence.AssertionTemplate{{
			Kind:    authStatusAssertionAuthenticated,
			Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration},
		}},
	}
}

func (authStatusAssertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != authStatusObservationKind {
			continue
		}
		status, ok := authStatusFromObservation(observation.Content)
		if !ok || !status.Connected {
			continue
		}
		target := status.Plugin
		subjectID := status.Plugin + "/" + status.Instance
		out = append(out, coreevidence.Assertion{
			Kind:           authStatusAssertionAuthenticated,
			Target:         target,
			Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: status.Plugin, ID: subjectID},
			Scope:          status.Instance,
			Source:         authStatusObservationSource,
			Confidence:     1,
			ObservationIDs: authStatusObservationIDs(observation.ID),
			Metadata: map[string]string{
				"plugin":   status.Plugin,
				"instance": status.Instance,
				"method":   status.Method,
				"status":   status.Status,
				"fields":   strings.Join(authStatusSetFieldNames(status.Fields), ","),
			},
		})
	}
	return out, nil
}

func authStatusFieldMetadata(fields []sharedauthstatus.FieldStatus) string {
	return strings.Join(authStatusSetFieldNames(fields), ",")
}

func authStatusSetFieldNames(fields []sharedauthstatus.FieldStatus) []string {
	var out []string
	for _, field := range fields {
		if field.Set && strings.TrimSpace(field.Name) != "" {
			out = append(out, strings.TrimSpace(field.Name))
		}
	}
	return out
}

func authStatusFromObservation(content any) (sharedauthstatus.Status, bool) {
	switch typed := content.(type) {
	case sharedauthstatus.Status:
		return typed, true
	case *sharedauthstatus.Status:
		if typed == nil {
			return sharedauthstatus.Status{}, false
		}
		return *typed, true
	default:
		return sharedauthstatus.Status{}, false
	}
}

func authStatusObservationIDs(id string) []string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return []string{id}
}
