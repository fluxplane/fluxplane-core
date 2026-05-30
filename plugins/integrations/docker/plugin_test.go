package docker

import (
	"context"
	"errors"
	"testing"
	"time"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-core/runtime/systemtest"
)

func TestPluginContributesObserverAndAssertionDeriver(t *testing.T) {
	bundle, err := Plugin{}.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Observers) != 1 || bundle.Observers[0].Name != ObserverName {
		t.Fatalf("observers = %#v, want Docker observer spec", bundle.Observers)
	}
	if len(bundle.AssertionDerivers) != 1 || bundle.AssertionDerivers[0].Name != AssertionDeriverName {
		t.Fatalf("assertion derivers = %#v, want Docker assertion deriver spec", bundle.AssertionDerivers)
	}
}

func TestDockerObserverReportsUnavailableWithoutProcessManager(t *testing.T) {
	observers, err := New(systemtest.NewMemory()).EnvironmentObservers(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	observations, err := observers[0].Observe(context.Background(), runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	status, ok := observations[0].Content.(Status)
	if !ok {
		t.Fatalf("content = %#v, want Status", observations[0].Content)
	}
	if status.BinaryAvailable || status.DaemonAvailable || status.Diagnostic == "" {
		t.Fatalf("status = %#v, want unavailable process diagnostic", status)
	}
}

func TestDockerAssertionsFollowBinaryAndDaemonAvailability(t *testing.T) {
	tests := []struct {
		name       string
		process    *fakeProcess
		configured bool
		available  bool
	}{
		{
			name: "binary unavailable",
			process: &fakeProcess{runs: []fakeRun{{
				err: errors.New("executable file not found"),
			}}},
		},
		{
			name: "daemon unavailable",
			process: &fakeProcess{runs: []fakeRun{{
				result: system.ProcessResult{Stdout: "Docker version 27.5.1, build test"},
			}, {
				result: system.ProcessResult{Stderr: "Cannot connect to the Docker daemon", ExitCode: 1},
				err:    errors.New("exit status 1"),
			}}},
			configured: true,
		},
		{
			name: "daemon available",
			process: &fakeProcess{runs: []fakeRun{{
				result: system.ProcessResult{Stdout: "Docker version 27.5.1, build test"},
			}, {
				result: system.ProcessResult{Stdout: `{"Client":{"Version":"27.5.1"},"Server":{"Version":"27.5.0"}}`},
			}}},
			configured: true,
			available:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sys := fakeSystem{MemorySystem: systemtest.NewMemory(), process: tc.process}
			observers, err := New(sys).EnvironmentObservers(context.Background(), pluginhost.Context{})
			if err != nil {
				t.Fatalf("EnvironmentObservers: %v", err)
			}
			observations, err := observers[0].Observe(context.Background(), runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseTurn})
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			derivers, err := Plugin{}.AssertionDerivers(context.Background(), pluginhost.Context{})
			if err != nil {
				t.Fatalf("AssertionDerivers: %v", err)
			}
			assertions, err := derivers[0].Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{Observations: observations})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if hasAssertion(assertions, AssertionConfigured) != tc.configured {
				t.Fatalf("configured assertion present=%v want %v assertions=%#v", hasAssertion(assertions, AssertionConfigured), tc.configured, assertions)
			}
			if hasAssertion(assertions, AssertionAvailable) != tc.available {
				t.Fatalf("available assertion present=%v want %v assertions=%#v", hasAssertion(assertions, AssertionAvailable), tc.available, assertions)
			}
		})
	}
}

func hasAssertion(assertions []coreevidence.Assertion, kind string) bool {
	for _, assertion := range assertions {
		if assertion.Kind == kind && assertion.Target == Name {
			return true
		}
	}
	return false
}

type fakeSystem struct {
	*systemtest.MemorySystem
	process system.ProcessManager
}

func (s fakeSystem) Process() system.ProcessManager { return s.process }

type fakeRun struct {
	result system.ProcessResult
	err    error
}

type fakeProcess struct {
	runs []fakeRun
}

func (p *fakeProcess) Run(context.Context, system.ProcessRequest) (system.ProcessResult, error) {
	if len(p.runs) == 0 {
		return system.ProcessResult{}, errors.New("unexpected process run")
	}
	run := p.runs[0]
	p.runs = p.runs[1:]
	return run.result, run.err
}

func (p *fakeProcess) Start(context.Context, system.ProcessRequest) (system.ProcessHandle, error) {
	return nil, errors.New("not implemented")
}

func (p *fakeProcess) Ensure(context.Context, system.ProcessRequest) (system.ProcessHandle, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (p *fakeProcess) Group(string) system.ProcessGroup { return nil }

func (p *fakeProcess) List(context.Context) ([]system.ProcessInfo, error) {
	return nil, errors.New("not implemented")
}

func (p *fakeProcess) Status(context.Context, string) (system.ProcessInfo, error) {
	return system.ProcessInfo{}, errors.New("not implemented")
}

func (p *fakeProcess) Output(context.Context, string) (system.ProcessOutput, error) {
	return system.ProcessOutput{}, errors.New("not implemented")
}

func (p *fakeProcess) Wait(context.Context, string, time.Duration) (system.ProcessResult, error) {
	return system.ProcessResult{}, errors.New("not implemented")
}

func (p *fakeProcess) Stop(context.Context, string) error {
	return errors.New("not implemented")
}

func (p *fakeProcess) Kill(context.Context, string) error {
	return errors.New("not implemented")
}
