package environment

import (
	"context"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
)

// ObservationRequest is passed to environment observers.
type ObservationRequest struct {
	Phase        coreenvironment.ObservationPhase
	Observations []coreenvironment.Observation
}

// Observer produces observations for one configured environment boundary.
type Observer interface {
	Spec() coreenvironment.ObserverSpec
	Observe(context.Context, ObservationRequest) ([]coreenvironment.Observation, error)
}

// SignalDeriveRequest is passed to signal derivers.
type SignalDeriveRequest struct {
	Observations []coreenvironment.Observation
}

// SignalDeriver produces normalized signals from observations.
type SignalDeriver interface {
	Spec() coreenvironment.SignalDeriverSpec
	Derive(context.Context, SignalDeriveRequest) ([]coreenvironment.Signal, error)
}

type templateSignalDeriver struct {
	spec coreenvironment.SignalDeriverSpec
}

// Diagnostic describes an observer or deriver failure.
type Diagnostic struct {
	Name    string
	Message string
}

// TemplateSignalDeriver returns a pure deriver backed by inert signal templates.
// It is intended for user/app-authored declarative derivation rules; plugins
// can still provide custom executable derivers when templates are not enough.
func TemplateSignalDeriver(spec coreenvironment.SignalDeriverSpec) SignalDeriver {
	return templateSignalDeriver{spec: spec}
}

// TemplateSignalDerivers adapts inert signal deriver specs into executable
// pure derivers.
func TemplateSignalDerivers(specs []coreenvironment.SignalDeriverSpec) []SignalDeriver {
	if len(specs) == 0 {
		return nil
	}
	out := make([]SignalDeriver, 0, len(specs))
	for _, spec := range specs {
		out = append(out, TemplateSignalDeriver(spec))
	}
	return out
}

func (d templateSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return d.spec
}

func (d templateSignalDeriver) Derive(_ context.Context, req SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	spec := d.spec
	var out []coreenvironment.Signal
	for _, observation := range req.Observations {
		if !observationKindSelected(spec.ObservationKinds, observation.Kind) {
			continue
		}
		for _, template := range spec.Signals {
			if template.Kind == "" {
				continue
			}
			signal := coreenvironment.Signal{
				Kind:        template.Kind,
				Target:      template.Target,
				Scope:       firstNonEmpty(template.Scope, observation.Scope),
				Source:      template.Source,
				Environment: observation.Environment,
				Confidence:  1,
			}
			if observation.ID != "" {
				signal.ObservationIDs = []string{observation.ID}
			}
			out = append(out, signal)
		}
	}
	return out, nil
}

// RunObservers executes observers whose spec phase matches the requested phase.
func RunObservers(ctx context.Context, observers []Observer, req ObservationRequest) ([]coreenvironment.Observation, []Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	var out []coreenvironment.Observation
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

// DeriveSignals runs all derivers over the active observation set.
func DeriveSignals(ctx context.Context, derivers []SignalDeriver, req SignalDeriveRequest) ([]coreenvironment.Signal, []Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	var out []coreenvironment.Signal
	var diagnostics []Diagnostic
	for _, deriver := range derivers {
		if deriver == nil {
			continue
		}
		spec := deriver.Spec()
		signals, err := deriver.Derive(ctx, req)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Name: spec.Name, Message: err.Error()})
			continue
		}
		for _, signal := range signals {
			if signal.Source == "" {
				signal.Source = spec.Name
			}
			out = append(out, signal)
		}
	}
	return out, diagnostics
}
