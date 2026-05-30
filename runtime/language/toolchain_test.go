package language

import (
	"context"
	"testing"

	corelanguage "github.com/fluxplane/fluxplane-core/core/language"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

func TestResolveToolchainStatusUnavailableWithoutProcessManager(t *testing.T) {
	status := ResolveToolchainStatus(context.Background(), nil, corelanguage.ToolchainSpec{
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
	status := ResolveToolchainStatus(context.Background(), nil, corelanguage.ToolchainSpec{
		ID: "parser-only",
	})
	if !status.Available || len(status.Diagnostics) != 0 {
		t.Fatalf("status = %#v, want available parser-only toolchain", status)
	}
}

func TestResolveToolchainStatusUsesSystemProcess(t *testing.T) {
	proc := &fakeProcess{result: fpsystem.ProcessResult{ExitCode: 0, Stdout: "go version go1.26 linux/amd64\n"}}
	status := ResolveToolchainStatus(context.Background(), proc, corelanguage.ToolchainSpec{
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

type fakeProcess struct {
	request fpsystem.ProcessRequest
	result  fpsystem.ProcessResult
}

func (p *fakeProcess) Run(_ context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	p.request = req
	return p.result, nil
}

func (p *fakeProcess) Start(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	return nil, nil
}

func (p *fakeProcess) Ensure(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	handle, err := p.Start(ctx, req)
	return handle, true, err
}

func (p *fakeProcess) Group(string) fpsystem.ProcessGroup { return nil }

func (p *fakeProcess) List(context.Context) ([]fpsystem.ProcessInfo, error) { return nil, nil }
