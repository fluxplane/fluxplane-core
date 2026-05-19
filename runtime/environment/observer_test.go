package environment

import (
	"context"
	"errors"
	"testing"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
)

func TestRunObserversFiltersByPhaseAndDefaultsSource(t *testing.T) {
	observers := []Observer{
		testObserver{name: "startup", phase: coreenvironment.PhaseStartup},
		testObserver{name: "turn", phase: coreenvironment.PhaseTurn},
	}
	observations, diagnostics := RunObservers(context.Background(), observers, ObservationRequest{Phase: coreenvironment.PhaseTurn})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if len(observations) != 1 {
		t.Fatalf("observations len = %d, want 1", len(observations))
	}
	if observations[0].Source != "turn" {
		t.Fatalf("source = %q, want turn", observations[0].Source)
	}
}

func TestRunObserversReportsDiagnostics(t *testing.T) {
	_, diagnostics := RunObservers(context.Background(), []Observer{testObserver{name: "bad", err: errors.New("failed")}}, ObservationRequest{})
	if len(diagnostics) != 1 || diagnostics[0].Name != "bad" {
		t.Fatalf("diagnostics = %#v, want bad diagnostic", diagnostics)
	}
}

func TestRunObserversFiltersByObservableKinds(t *testing.T) {
	observations, diagnostics := RunObservers(context.Background(), []Observer{
		testObserver{
			name:  "narrow",
			kinds: []string{"selected"},
			observations: []coreenvironment.Observation{{
				Kind: "selected",
			}, {
				Kind: "ignored",
			}},
		},
	}, ObservationRequest{})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if len(observations) != 1 || observations[0].Kind != "selected" {
		t.Fatalf("observations = %#v, want only selected kind", observations)
	}
}

func TestDeriveSignalsDefaultsSource(t *testing.T) {
	signals, diagnostics := DeriveSignals(context.Background(), []SignalDeriver{testDeriver{name: "go.signals"}}, SignalDeriveRequest{})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if len(signals) != 1 {
		t.Fatalf("signals len = %d, want 1", len(signals))
	}
	if signals[0].Source != "go.signals" {
		t.Fatalf("source = %q, want go.signals", signals[0].Source)
	}
}

func TestTemplateSignalDeriverDerivesSignalsFromMatchingObservations(t *testing.T) {
	deriver := TemplateSignalDeriver(coreenvironment.SignalDeriverSpec{
		Name:             "taskfile.signals",
		ObservationKinds: []string{"project.task_runner"},
		Signals: []coreenvironment.SignalTemplate{{
			Kind:   "project.task_runner.detected",
			Target: "taskfile",
		}},
	})
	signals, diagnostics := DeriveSignals(context.Background(), []SignalDeriver{deriver}, SignalDeriveRequest{
		Observations: []coreenvironment.Observation{{
			ID:    "taskfile:Taskfile.yaml",
			Kind:  "project.task_runner",
			Scope: "workspace:/repo",
			Environment: coreenvironment.Ref{
				Name: "workspace",
			},
		}, {
			Kind: "unrelated",
		}},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	if len(signals) != 1 {
		t.Fatalf("signals len = %d, want 1", len(signals))
	}
	signal := signals[0]
	if signal.Kind != "project.task_runner.detected" || signal.Target != "taskfile" || signal.Source != "taskfile.signals" {
		t.Fatalf("signal = %#v, want taskfile signal with default source", signal)
	}
	if signal.Scope != "workspace:/repo" || signal.Environment.Name != "workspace" {
		t.Fatalf("signal scope/environment = %#v, want observation scope/environment", signal)
	}
	if len(signal.ObservationIDs) != 1 || signal.ObservationIDs[0] != "taskfile:Taskfile.yaml" {
		t.Fatalf("observation ids = %#v, want originating observation", signal.ObservationIDs)
	}
}

type testObserver struct {
	name         string
	phase        coreenvironment.ObservationPhase
	kinds        []string
	observations []coreenvironment.Observation
	err          error
}

func (o testObserver) Spec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{Name: o.name, Phase: o.phase, ObservableKinds: o.kinds}
}

func (o testObserver) Observe(context.Context, ObservationRequest) ([]coreenvironment.Observation, error) {
	if o.err != nil {
		return nil, o.err
	}
	if o.observations != nil {
		return o.observations, nil
	}
	return []coreenvironment.Observation{{Kind: "test"}}, nil
}

type testDeriver struct {
	name string
}

func (d testDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{Name: d.name}
}

func (d testDeriver) Derive(context.Context, SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	return []coreenvironment.Signal{{Kind: "language.detected", Target: "go"}}, nil
}
