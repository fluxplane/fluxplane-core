// Package evidence owns runtime execution contracts for evidence observers and
// assertion derivers.
package evidence

import (
	"context"
	"os"
	osuser "os/user"
	"time"

	coreenvironment "github.com/fluxplane/fluxplane-core/core/environment"
	coreevidence "github.com/fluxplane/fluxplane-evidence"
)

const (
	BaselineObserverName = "runtime.baseline"

	ObservationSystemTime   = "system.time"
	ObservationSystemLocale = "system.locale"
	ObservationSystemUser   = "system.user"
)

// ObservationRequest is passed to evidence observers.
type ObservationRequest struct {
	Phase        coreevidence.ObservationPhase
	Observations []coreevidence.Observation
}

// Observer produces rich evidence observations.
type Observer interface {
	Spec() coreevidence.ObserverSpec
	Observe(context.Context, ObservationRequest) ([]coreevidence.Observation, error)
}

// AssertionDeriveRequest is passed to assertion derivers.
type AssertionDeriveRequest struct {
	Observations []coreevidence.Observation
}

// AssertionDeriver converts observations into normalized assertions.
type AssertionDeriver interface {
	Spec() coreevidence.AssertionDeriverSpec
	Derive(context.Context, AssertionDeriveRequest) ([]coreevidence.Assertion, error)
}

type templateAssertionDeriver struct {
	spec coreevidence.AssertionDeriverSpec
}

// Diagnostic describes observer or deriver failures.
type Diagnostic struct {
	Name    string
	Message string
}

// TemplateAssertionDeriver returns a pure deriver backed by inert assertion
// templates. It is intended for user/app-authored declarative derivation rules.
func TemplateAssertionDeriver(spec coreevidence.AssertionDeriverSpec) AssertionDeriver {
	return templateAssertionDeriver{spec: spec}
}

// TemplateAssertionDerivers adapts inert assertion deriver specs into
// executable pure derivers.
func TemplateAssertionDerivers(specs []coreevidence.AssertionDeriverSpec) []AssertionDeriver {
	if len(specs) == 0 {
		return nil
	}
	out := make([]AssertionDeriver, 0, len(specs))
	for _, spec := range specs {
		out = append(out, TemplateAssertionDeriver(spec))
	}
	return out
}

func (d templateAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return d.spec
}

func (d templateAssertionDeriver) Derive(_ context.Context, req AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	spec := d.spec
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if !observationKindSelected(spec.ObservationKinds, observation.Kind) {
			continue
		}
		for _, template := range spec.Assertions {
			if template.Kind == "" {
				continue
			}
			assertion := coreevidence.Assertion{
				Kind:        template.Kind,
				Target:      template.Target,
				Subject:     template.Subject,
				Scope:       firstNonEmpty(template.Scope, observation.Scope),
				Source:      template.Source,
				Environment: observation.Environment,
				Confidence:  1,
			}
			if observation.ID != "" {
				assertion.ObservationIDs = []string{observation.ID}
			}
			out = append(out, assertion)
		}
	}
	return out, nil
}

// RunObservers executes observers whose spec phase matches the requested phase.
func RunObservers(ctx context.Context, observers []Observer, req ObservationRequest) ([]coreevidence.Observation, []Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	var out []coreevidence.Observation
	var diagnostics []Diagnostic
	for _, observer := range observers {
		if observer == nil {
			continue
		}
		spec := observer.Spec()
		if spec.Phase != "" && req.Phase != "" && spec.Phase != req.Phase {
			continue
		}
		observations, err := observer.Observe(ctx, req)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Name: spec.Name, Message: err.Error()})
			continue
		}
		for _, observation := range observations {
			if !observationKindAllowed(spec.ObservableKinds, observation.Kind) {
				continue
			}
			if observation.Source == "" {
				observation.Source = spec.Name
			}
			if observation.Environment.Name == "" {
				observation.Environment = spec.Environment
			}
			out = append(out, observation)
		}
	}
	return out, diagnostics
}

func observationKindAllowed(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func observationKindSelected(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return false
	}
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// DeriveAssertions runs all derivers over the active observation set.
func DeriveAssertions(ctx context.Context, derivers []AssertionDeriver, req AssertionDeriveRequest) ([]coreevidence.Assertion, []Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	var out []coreevidence.Assertion
	var diagnostics []Diagnostic
	for _, deriver := range derivers {
		if deriver == nil {
			continue
		}
		spec := deriver.Spec()
		assertions, err := deriver.Derive(ctx, req)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Name: spec.Name, Message: err.Error()})
			continue
		}
		for _, assertion := range assertions {
			if assertion.Source == "" {
				assertion.Source = spec.Name
			}
			out = append(out, assertion)
		}
	}
	return out, diagnostics
}

type baselineObserver struct{}

// BaselineObserver reports cheap, non-secret local runtime facts.
func BaselineObserver() Observer {
	return baselineObserver{}
}

func (baselineObserver) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:        BaselineObserverName,
		Description: "Reports cheap non-secret local runtime facts such as time, locale, and username.",
		Environment: coreevidence.Ref{
			Name: "local",
		},
		Phase: coreevidence.PhaseTurn,
		ObservableKinds: []string{
			ObservationSystemTime,
			ObservationSystemLocale,
			ObservationSystemUser,
		},
		Dynamic: true,
	}
}

func (baselineObserver) Observe(context.Context, ObservationRequest) ([]coreevidence.Observation, error) {
	now := time.Now()
	out := []coreevidence.Observation{{
		ID:          "system:time",
		Kind:        ObservationSystemTime,
		Scope:       string(coreenvironment.ScopeSession),
		Content:     systemTimeContent(now),
		At:          now.UTC(),
		Environment: coreevidence.Ref{Name: "local"},
	}}
	if locale := systemLocaleContent(); len(locale) > 0 {
		out = append(out, coreevidence.Observation{
			ID:          "system:locale",
			Kind:        ObservationSystemLocale,
			Scope:       string(coreenvironment.ScopeSession),
			Content:     locale,
			At:          now.UTC(),
			Environment: coreevidence.Ref{Name: "local"},
		})
	}
	if current, err := osuser.Current(); err == nil && current != nil && current.Username != "" {
		out = append(out, coreevidence.Observation{
			ID:    "system:user",
			Kind:  ObservationSystemUser,
			Scope: string(coreenvironment.ScopeSession),
			Content: map[string]any{
				"username": current.Username,
				"uid":      current.Uid,
				"gid":      current.Gid,
			},
			At:          now.UTC(),
			Environment: coreevidence.Ref{Name: "local"},
		})
	}
	return out, nil
}

func systemTimeContent(now time.Time) map[string]any {
	name, offset := now.Zone()
	return map[string]any{
		"time":       now.Format(time.RFC3339),
		"utc_time":   now.UTC().Format(time.RFC3339),
		"timezone":   now.Location().String(),
		"zone":       name,
		"utc_offset": offset,
	}
}

func systemLocaleContent() map[string]any {
	out := map[string]any{}
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		if value := os.Getenv(key); value != "" {
			out[key] = value
		}
	}
	return out
}
