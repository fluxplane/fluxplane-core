package gitplugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name     = "git"
	StatusOp = "git_status"
	DiffOp   = "git_diff"
)

// Plugin contributes basic git operations.
type Plugin struct {
	system system.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a git plugin.
func New(sys system.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Basic git inspection operations."}
}

// Contributions returns git specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := []operation.Spec{statusSpec(), diffSpec()}
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Git repository operations.", Operations: []operation.Ref{specs[0].Ref, specs[1].Ref}}},
		Operations:    specs,
		Commands: []command.Spec{
			commandFor(specs[0], command.Path{Name, "status"}),
			commandFor(specs[1], command.Path{Name, "diff"}),
		},
	}, nil
}

// Operations returns executable git operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("gitplugin: system is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[statusInput, map[string]any](statusSpec(), p.status()),
		operationruntime.NewTypedResult[diffInput, map[string]any](diffSpec(), p.diff()),
	}, nil
}

func statusSpec() operation.Spec {
	return operationruntime.WithTypedContract[statusInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: StatusOp},
		Description: "Show git status for the workspace.",
		Semantics:   operation.Semantics{Determinism: operation.DeterminismNonDeterministic, Effects: operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal}, Risk: operation.RiskLow},
	})
}

func diffSpec() operation.Spec {
	return operationruntime.WithTypedContract[diffInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: DiffOp},
		Description: "Show git diff for the workspace.",
		Semantics:   operation.Semantics{Determinism: operation.DeterminismNonDeterministic, Effects: operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal}, Risk: operation.RiskLow},
	})
}

type statusInput struct{}

func commandFor(spec operation.Spec, path command.Path) command.Spec {
	return command.Spec{
		Path:        path,
		Description: spec.Description,
		Target:      invocation.Target{Kind: invocation.TargetOperation, Operation: spec.Ref},
		Input:       spec.Input,
		Output:      spec.Output,
		Policy:      policy.InvocationPolicy{AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerAgent}, RequiredTrust: policy.TrustVerified},
	}
}

func (p Plugin) status() operationruntime.TypedResultHandler[statusInput, map[string]any] {
	return func(ctx operation.Context, _ statusInput) operation.Result {
		result, err := p.system.Process().Run(ctx, system.ProcessRequest{
			Command: "git",
			Args:    []string{"status", "--short", "--branch"},
			Timeout: 30 * time.Second,
		})
		data := map[string]any{"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode}
		if err != nil {
			return operation.Failed("git_status_failed", err.Error(), data)
		}
		text := strings.TrimSpace(result.Stdout)
		if text == "" {
			text = "No git status output."
		}
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

type diffInput struct {
	Staged bool     `json:"staged,omitempty" jsonschema:"description=Show staged changes instead of unstaged changes."`
	Ref    string   `json:"ref,omitempty" jsonschema:"description=Optional ref or ref range."`
	Paths  []string `json:"paths,omitempty" jsonschema:"description=Limit diff to paths."`
}

func (p Plugin) diff() operationruntime.TypedResultHandler[diffInput, map[string]any] {
	return func(ctx operation.Context, req diffInput) operation.Result {
		args := []string{"diff"}
		if req.Staged {
			args = append(args, "--staged")
		}
		if strings.TrimSpace(req.Ref) != "" {
			args = append(args, req.Ref)
		}
		if len(req.Paths) > 0 {
			args = append(args, "--")
			args = append(args, req.Paths...)
		}
		result, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 30 * time.Second, MaxStdout: 256 * 1024})
		data := map[string]any{"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode}
		if err != nil {
			return operation.Failed("git_diff_failed", err.Error(), data)
		}
		text := strings.TrimSpace(result.Stdout)
		if text == "" {
			text = "No changes."
		}
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}
