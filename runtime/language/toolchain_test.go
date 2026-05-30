package language

import (
	"context"
	"testing"
	"time"

	corelanguage "github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-core/runtime/systemtest"
)

func TestResolveToolchainStatusUnavailableWithoutProcessManager(t *testing.T) {
	status := ResolveToolchainStatus(context.Background(), systemtest.NewMemory(), corelanguage.ToolchainSpec{
		ID: "go",
		RequiredBinaries: []corelanguage.ToolchainBinarySpec{{
			Name: "go",
		}},
	})
	if status.Available {
		t.Fatalf("status = %#v, want unavailable without process manager", status)
	}
}

func TestResolveToolchainStatusAvailableWithoutBinariesOrProcessManager(t *testing.T) {
	status := ResolveToolchainStatus(context.Background(), systemtest.NewMemory(), corelanguage.ToolchainSpec{
		ID: "parser-only",
	})
	if !status.Available || len(status.Diagnostics) != 0 {
		t.Fatalf("status = %#v, want available parser-only toolchain", status)
	}
}

func TestResolveToolchainStatusUsesSystemProcess(t *testing.T) {
	proc := &fakeProcess{result: system.ProcessResult{ExitCode: 0, Stdout: "go version go1.26 linux/amd64\n"}}
	sys := fakeSystem{process: proc}
	status := ResolveToolchainStatus(context.Background(), sys, corelanguage.ToolchainSpec{
		ID: "go",
		RequiredBinaries: []corelanguage.ToolchainBinarySpec{{
			Name:        "go",
			VersionArgs: []string{"version"},
		}},
	})
	if !status.Available || status.Version == "" || status.Binaries[0].Path != "go" {
		t.Fatalf("status = %#v, want available go status", status)
	}
	if proc.request.Command != "go" || len(proc.request.Args) != 1 || proc.request.Args[0] != "version" {
		t.Fatalf("request = %#v, want go version", proc.request)
	}
}

type fakeSystem struct {
	process *fakeProcess
}

func (s fakeSystem) Workspace() system.Workspace     { return nil }
func (s fakeSystem) Network() system.Network         { return nil }
func (s fakeSystem) Process() system.ProcessManager  { return s.process }
func (s fakeSystem) Environment() system.Environment { return nil }

type fakeProcess struct {
	request system.ProcessRequest
	result  system.ProcessResult
}

func (p *fakeProcess) Run(_ context.Context, req system.ProcessRequest) (system.ProcessResult, error) {
	p.request = req
	return p.result, nil
}

func (p *fakeProcess) Start(context.Context, system.ProcessRequest) (system.ProcessHandle, error) {
	return nil, nil
}

func (p *fakeProcess) Ensure(ctx context.Context, req system.ProcessRequest) (system.ProcessHandle, bool, error) {
	handle, err := p.Start(ctx, req)
	return handle, true, err
}

func (p *fakeProcess) List(context.Context) ([]system.ProcessInfo, error) { return nil, nil }

func (p *fakeProcess) Status(context.Context, string) (system.ProcessInfo, error) {
	return system.ProcessInfo{}, nil
}

func (p *fakeProcess) Output(context.Context, string) (system.ProcessOutput, error) {
	return system.ProcessOutput{}, nil
}

func (p *fakeProcess) Wait(context.Context, string, time.Duration) (system.ProcessResult, error) {
	return p.result, nil
}

func (p *fakeProcess) Stop(context.Context, string) error { return nil }

func (p *fakeProcess) Kill(context.Context, string) error { return nil }
