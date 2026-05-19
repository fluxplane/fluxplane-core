// Package language contains runtime helpers for language support.
package language

import (
	"context"
	"strings"
	"time"

	corelanguage "github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const defaultProbeTimeout = 10 * time.Second

// ResolveToolchainStatus probes a toolchain through the runtime system
// boundary. Core toolchain specs remain inert; this package owns process IO.
func ResolveToolchainStatus(ctx context.Context, sys system.System, spec corelanguage.ToolchainSpec) corelanguage.ToolchainStatus {
	status := corelanguage.ToolchainStatus{ID: spec.ID, Available: true, Versions: map[string]string{}}
	if len(spec.RequiredBinaries) == 0 {
		status.Available = true
		return status
	}
	if sys == nil || sys.Process() == nil {
		return unavailable(status, "process manager is unavailable")
	}
	for _, binary := range spec.RequiredBinaries {
		binStatus := probeBinary(ctx, sys, binary)
		if !binStatus.Available {
			status.Available = false
			status.Diagnostics = append(status.Diagnostics, corelanguage.Diagnostic{
				Severity: "warning",
				Code:     "toolchain_binary_unavailable",
				Target:   binary.Name,
				Message:  binStatus.Error,
			})
		}
		if binStatus.Version != "" {
			status.Versions[binary.Name] = binStatus.Version
			if status.Version == "" {
				status.Version = binStatus.Version
			}
		}
		status.Binaries = append(status.Binaries, binStatus)
	}
	if len(status.Versions) == 0 {
		status.Versions = nil
	}
	return status
}

func unavailable(status corelanguage.ToolchainStatus, msg string) corelanguage.ToolchainStatus {
	status.Available = false
	status.Diagnostics = append(status.Diagnostics, corelanguage.Diagnostic{
		Severity: "warning",
		Code:     "toolchain_unavailable",
		Target:   status.ID,
		Message:  msg,
	})
	return status
}

func probeBinary(ctx context.Context, sys system.System, binary corelanguage.ToolchainBinarySpec) corelanguage.ToolchainBinaryStatus {
	out := corelanguage.ToolchainBinaryStatus{Name: binary.Name}
	args := binary.VersionArgs
	if len(args) == 0 {
		args = []string{"version"}
	}
	result, err := sys.Process().Run(ctx, system.ProcessRequest{
		Command:   binary.Name,
		Args:      args,
		Timeout:   defaultProbeTimeout,
		MaxStdout: 4096,
		MaxStderr: 4096,
	})
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Available = result.ExitCode == 0
	out.Path = binary.Name
	out.Version = firstNonEmpty(strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr))
	if !out.Available && out.Error == "" {
		out.Error = firstNonEmpty(strings.TrimSpace(result.Stderr), "version probe failed")
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
