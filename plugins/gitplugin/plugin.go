package gitplugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name     = "git"
	StatusOp = "git_status"
	DiffOp   = "git_diff"
	AddOp    = "git_add"
	CommitOp = "git_commit"
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
	specs := []operation.Spec{statusSpec(), diffSpec(), addSpec(), commitSpec()}
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Git repository operations.", Operations: operationRefs(specs)}},
		Operations:    specs,
	}, nil
}

// Operations returns executable git operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("gitplugin: system is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[statusInput, map[string]any](statusSpec(), p.status(), operationruntime.WithIntent(statusIntent)),
		operationruntime.NewTypedResult[diffInput, map[string]any](diffSpec(), p.diff(), operationruntime.WithIntent(diffIntent)),
		operationruntime.NewTypedResult[addInput, map[string]any](addSpec(), p.add(), operationruntime.WithIntent(addIntent)),
		operationruntime.NewTypedResult[commitInput, map[string]any](commitSpec(), p.commit(), operationruntime.WithIntent(commitIntent)),
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

func addSpec() operation.Spec {
	return operationruntime.WithTypedContract[addInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: AddOp},
		Description: "Stage git workspace changes.",
		Semantics:   gitWriteSemantics(),
	})
}

func commitSpec() operation.Spec {
	return operationruntime.WithTypedContract[commitInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: CommitOp},
		Description: "Create a git commit from staged changes, optionally staging paths first.",
		Semantics:   gitWriteSemantics(),
	})
}

func gitWriteSemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Idempotency: operation.IdempotencyNonIdempotent,
		Effects: operation.EffectSet{
			operation.EffectProcess,
			operation.EffectFilesystem,
			operation.EffectWriteExternal,
			operation.EffectCreate,
			operation.EffectUpdate,
		},
		Risk: operation.RiskMedium,
	}
}

type statusInput struct{}

type addInput struct {
	All   bool     `json:"all,omitempty" jsonschema:"description=Stage all tracked and untracked workspace changes, equivalent to git add -A."`
	Paths []string `json:"paths,omitempty" jsonschema:"description=Paths to stage. Required unless all is true."`
}

type commitInput struct {
	Message    string   `json:"message" jsonschema:"description=Commit message. Prefer a concise conventional or semantic commit subject with optional body."`
	Stage      bool     `json:"stage,omitempty" jsonschema:"description=Stage paths or all changes before committing."`
	All        bool     `json:"all,omitempty" jsonschema:"description=When stage is true, stage all tracked and untracked workspace changes with git add -A."`
	Paths      []string `json:"paths,omitempty" jsonschema:"description=When stage is true, stage only these paths unless all is true."`
	AllowEmpty bool     `json:"allow_empty,omitempty" jsonschema:"description=Allow creating an empty commit."`
}

func statusIntent(operation.Context, statusInput) (operation.IntentSet, error) {
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("git", "status", "--short", "--branch"),
		pathIntent(operation.IntentFilesystemRead, operation.IntentRoleReadTarget, ".git"),
	}}, nil
}

func diffIntent(_ operation.Context, req diffInput) (operation.IntentSet, error) {
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
	ops := []operation.IntentOperation{
		processIntent("git", args...),
		pathIntent(operation.IntentFilesystemRead, operation.IntentRoleReadTarget, ".git"),
	}
	for _, path := range req.Paths {
		ops = append(ops, pathIntent(operation.IntentFilesystemRead, operation.IntentRoleReadTarget, path))
	}
	return operation.IntentSet{Operations: ops}, nil
}

func addIntent(_ operation.Context, req addInput) (operation.IntentSet, error) {
	args, result := gitAddArgs(req.All, req.Paths, "invalid_git_add_input")
	if result.IsError() {
		return operation.IntentSet{}, fmt.Errorf("%s", result.Error.Message)
	}
	ops := []operation.IntentOperation{
		processIntent("git", args...),
		pathIntent(operation.IntentPersistenceModify, operation.IntentRoleWriteTarget, ".git/index"),
	}
	if req.All {
		ops = append(ops, pathIntent(operation.IntentFilesystemRead, operation.IntentRoleReadTarget, "."))
		return operation.IntentSet{Operations: ops}, nil
	}
	for _, path := range req.Paths {
		ops = append(ops, pathIntent(operation.IntentFilesystemRead, operation.IntentRoleReadTarget, path))
	}
	return operation.IntentSet{Operations: ops}, nil
}

func commitIntent(_ operation.Context, req commitInput) (operation.IntentSet, error) {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return operation.IntentSet{}, fmt.Errorf("message is required")
	}
	if !req.Stage && (req.All || len(req.Paths) > 0) {
		return operation.IntentSet{}, fmt.Errorf("paths or all require stage to be true")
	}
	var ops []operation.IntentOperation
	if req.Stage {
		add, err := addIntent(nil, addInput{All: req.All, Paths: req.Paths})
		if err != nil {
			return operation.IntentSet{}, err
		}
		ops = append(ops, add.Operations...)
	}
	args := []string{"-c", "core.hooksPath=/dev/null", "commit", "--no-verify", "--no-gpg-sign"}
	if req.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	args = append(args, "-m", message)
	ops = append(ops,
		processIntent("git", args...),
		pathIntent(operation.IntentPersistenceModify, operation.IntentRoleWriteTarget, ".git"),
		pathIntent(operation.IntentPersistenceModify, operation.IntentRoleWriteTarget, ".git/COMMIT_EDITMSG"),
	)
	return operation.IntentSet{Operations: ops}, nil
}

func processIntent(command string, args ...string) operation.IntentOperation {
	out := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		out = append(out, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command(command), Args: out},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}
}

func pathIntent(behavior operation.IntentBehavior, role operation.IntentRole, path string) operation.IntentOperation {
	return operation.IntentOperation{
		Behavior:  behavior,
		Target:    operation.PathTarget{Path: operation.Path(path)},
		Role:      role,
		Certainty: operation.IntentCertain,
	}
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	refs := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref)
	}
	return refs
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

func (p Plugin) add() operationruntime.TypedResultHandler[addInput, map[string]any] {
	return func(ctx operation.Context, req addInput) operation.Result {
		args, result := gitAddArgs(req.All, req.Paths, "invalid_git_add_input")
		if result.IsError() {
			return result
		}
		run, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 30 * time.Second})
		data := processData(run)
		if err != nil {
			return operation.Failed("git_add_failed", err.Error(), data)
		}
		return operation.OK(operation.Rendered{Text: processText(run, "Staged changes."), Data: data})
	}
}

func (p Plugin) commit() operationruntime.TypedResultHandler[commitInput, map[string]any] {
	return func(ctx operation.Context, req commitInput) operation.Result {
		message := strings.TrimSpace(req.Message)
		if message == "" {
			return operation.Failed("invalid_git_commit_input", "message is required", nil)
		}
		if !req.Stage && (req.All || len(req.Paths) > 0) {
			return operation.Failed("invalid_git_commit_input", "paths or all require stage to be true", nil)
		}
		if req.Stage {
			args, result := gitAddArgs(req.All, req.Paths, "invalid_git_commit_input")
			if result.IsError() {
				return result
			}
			addResult, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 30 * time.Second})
			if err != nil {
				return operation.Failed("git_commit_stage_failed", err.Error(), processData(addResult))
			}
		}
		args := []string{"-c", "core.hooksPath=/dev/null", "commit", "--no-verify", "--no-gpg-sign"}
		if req.AllowEmpty {
			args = append(args, "--allow-empty")
		}
		args = append(args, "-m", message)
		commitResult, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 30 * time.Second, MaxStdout: 128 * 1024, MaxStderr: 128 * 1024})
		data := processData(commitResult)
		if err != nil {
			return operation.Failed("git_commit_failed", err.Error(), data)
		}
		headResult, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: []string{"rev-parse", "HEAD"}, Timeout: 30 * time.Second})
		if err != nil {
			data["rev_parse_stdout"] = headResult.Stdout
			data["rev_parse_stderr"] = headResult.Stderr
			data["rev_parse_exit_code"] = headResult.ExitCode
			return operation.Failed("git_commit_rev_parse_failed", err.Error(), data)
		}
		commit := strings.TrimSpace(headResult.Stdout)
		data["commit"] = commit
		return operation.OK(operation.Rendered{Text: commitText(commit, commitResult), Data: data})
	}
}

func gitAddArgs(all bool, paths []string, invalidCode string) ([]string, operation.Result) {
	if all {
		return []string{"add", "-A"}, operation.Result{}
	}
	if len(paths) == 0 {
		return nil, operation.Failed(invalidCode, "all must be true or at least one path is required", nil)
	}
	args := []string{"add", "--"}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, operation.Failed(invalidCode, "paths must not contain empty values", nil)
		}
		args = append(args, path)
	}
	return args, operation.Result{}
}

func processData(result system.ProcessResult) map[string]any {
	return map[string]any{"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode}
}

func processText(result system.ProcessResult, fallback string) string {
	var parts []string
	if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
		parts = append(parts, stdout)
	}
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		parts = append(parts, stderr)
	}
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, "\n")
}

func commitText(commit string, result system.ProcessResult) string {
	text := "Committed " + commit
	if output := processText(result, ""); output != "" {
		text += "\n" + output
	}
	return text
}
