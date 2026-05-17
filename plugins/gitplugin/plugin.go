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
	TagOp    = "git_tag"
	PushOp   = "git_push"
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
	specs := []operation.Spec{statusSpec(), diffSpec(), addSpec(), commitSpec(), tagSpec(), pushSpec()}
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
		operationruntime.NewTypedResult[tagInput, map[string]any](tagSpec(), p.tag(), operationruntime.WithIntent(tagIntent)),
		operationruntime.NewTypedResult[pushInput, map[string]any](pushSpec(), p.push(), operationruntime.WithIntent(pushIntent)),
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
		Description: "Show git diff for the workspace, with optional compact stat/name views and bounded output.",
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

func tagSpec() operation.Spec {
	return operationruntime.WithTypedContract[tagInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: TagOp},
		Description: "Create a lightweight or annotated git tag.",
		Semantics:   gitWriteSemantics(),
	})
}

func pushSpec() operation.Spec {
	return operationruntime.WithTypedContract[pushInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: PushOp},
		Description: "Push explicit git refspecs or tags to a configured remote.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Idempotency: operation.IdempotencyNonIdempotent,
			Effects: operation.EffectSet{
				operation.EffectProcess,
				operation.EffectFilesystem,
				operation.EffectNetwork,
				operation.EffectReadExternal,
				operation.EffectWriteExternal,
				operation.EffectUpdate,
			},
			Risk: operation.RiskMedium,
		},
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

type tagInput struct {
	Name    string `json:"name" jsonschema:"description=Tag name to create.,required"`
	Ref     string `json:"ref,omitempty" jsonschema:"description=Optional commit-ish to tag. Defaults to HEAD."`
	Message string `json:"message,omitempty" jsonschema:"description=Annotated tag message. When set, creates an annotated tag."`
}

type pushInput struct {
	Remote         string   `json:"remote,omitempty" jsonschema:"description=Remote name or URL. Defaults to origin."`
	Refspecs       []string `json:"refspecs,omitempty" jsonschema:"description=Explicit refspecs to push, for example main or HEAD:refs/heads/main."`
	Tags           bool     `json:"tags,omitempty" jsonschema:"description=Push tags with --tags."`
	SetUpstream    bool     `json:"set_upstream,omitempty" jsonschema:"description=Set upstream tracking with -u."`
	ForceWithLease bool     `json:"force_with_lease,omitempty" jsonschema:"description=Use --force-with-lease. Raw force refspecs are rejected."`
	DryRun         bool     `json:"dry_run,omitempty" jsonschema:"description=Show what would be pushed without updating the remote."`
}

func statusIntent(operation.Context, statusInput) (operation.IntentSet, error) {
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("git", "status", "--short", "--branch"),
	}}, nil
}

func diffIntent(_ operation.Context, req diffInput) (operation.IntentSet, error) {
	args, err := gitDiffArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	ops := []operation.IntentOperation{
		processIntent("git", args...),
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

func tagIntent(_ operation.Context, req tagInput) (operation.IntentSet, error) {
	args, result := gitTagArgs(req)
	if result.IsError() {
		return operation.IntentSet{}, fmt.Errorf("%s", result.Error.Message)
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("git", args...),
		pathIntent(operation.IntentPersistenceModify, operation.IntentRoleWriteTarget, ".git/refs/tags/"+strings.TrimSpace(req.Name)),
		pathIntent(operation.IntentPersistenceModify, operation.IntentRoleWriteTarget, ".git"),
	}}, nil
}

func pushIntent(_ operation.Context, req pushInput) (operation.IntentSet, error) {
	args, result := gitPushArgs(req)
	if result.IsError() {
		return operation.IntentSet{}, fmt.Errorf("%s", result.Error.Message)
	}
	ops := []operation.IntentOperation{
		processIntent("git", args...),
		pathIntent(operation.IntentFilesystemRead, operation.IntentRoleReadTarget, ".git/config"),
	}
	if remote := strings.TrimSpace(req.Remote); remote != "" && looksLikeURL(remote) {
		ops = append(ops, operation.IntentOperation{
			Behavior:  operation.IntentNetworkWrite,
			Target:    operation.URLTarget{URL: operation.URL(remote)},
			Role:      operation.IntentRoleNetworkTarget,
			Certainty: operation.IntentCertain,
		})
	}
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
	Staged    bool     `json:"staged,omitempty" jsonschema:"description=Show staged changes instead of unstaged changes."`
	Ref       string   `json:"ref,omitempty" jsonschema:"description=Optional ref or ref range."`
	Paths     []string `json:"paths,omitempty" jsonschema:"description=Limit diff to paths."`
	StatOnly  bool     `json:"stat_only,omitempty" jsonschema:"description=Show only diffstat instead of full patch."`
	NamesOnly bool     `json:"names_only,omitempty" jsonschema:"description=Show only changed file names instead of full patch."`
	MaxBytes  int      `json:"max_bytes,omitempty" jsonschema:"description=Maximum diff text bytes returned. Defaults to a compact provider-safe limit."`
}

func (p Plugin) diff() operationruntime.TypedResultHandler[diffInput, map[string]any] {
	return func(ctx operation.Context, req diffInput) operation.Result {
		args, err := gitDiffArgs(req)
		if err != nil {
			return operation.Failed("invalid_git_diff_input", err.Error(), nil)
		}
		maxBytes := gitDiffMaxBytes(req)
		result, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 30 * time.Second, MaxStdout: 256 * 1024})
		text, truncated := capGitDiffText(strings.TrimSpace(result.Stdout), maxBytes)
		mode := gitDiffMode(req)
		data := map[string]any{"stdout": text, "stderr": result.Stderr, "exit_code": result.ExitCode, "mode": mode, "truncated": truncated, "max_bytes": maxBytes}
		if err != nil {
			return operation.Failed("git_diff_failed", err.Error(), data)
		}
		if text == "" {
			text = "No changes."
		}
		if truncated {
			text += "\n\n[git diff truncated; narrow paths or use stat_only, names_only, or a larger max_bytes.]"
		}
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

const (
	defaultGitDiffMaxBytes = 32 * 1024
	maximumGitDiffMaxBytes = 128 * 1024
)

func gitDiffArgs(req diffInput) ([]string, error) {
	if req.StatOnly && req.NamesOnly {
		return nil, fmt.Errorf("stat_only and names_only cannot be combined")
	}
	args := []string{"diff"}
	if req.Staged {
		args = append(args, "--staged")
	}
	switch {
	case req.StatOnly:
		args = append(args, "--stat")
	case req.NamesOnly:
		args = append(args, "--name-only")
	}
	if strings.TrimSpace(req.Ref) != "" {
		args = append(args, req.Ref)
	}
	if len(req.Paths) > 0 {
		args = append(args, "--")
		args = append(args, req.Paths...)
	}
	return args, nil
}

func gitDiffMode(req diffInput) string {
	switch {
	case req.StatOnly:
		return "stat"
	case req.NamesOnly:
		return "names"
	default:
		return "patch"
	}
}

func gitDiffMaxBytes(req diffInput) int {
	if req.MaxBytes <= 0 {
		return defaultGitDiffMaxBytes
	}
	if req.MaxBytes > maximumGitDiffMaxBytes {
		return maximumGitDiffMaxBytes
	}
	return req.MaxBytes
}

func capGitDiffText(text string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text, false
	}
	if maxBytes < 4 {
		return text[:maxBytes], true
	}
	return text[:maxBytes-3] + "...", true
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
		// When staging only explicit paths (not all), warn about any remaining dirty files
		// so partial commits are visible to the caller.
		if req.Stage && !req.All && len(req.Paths) > 0 {
			if dirty := remainingDirtyFiles(ctx, p.system); len(dirty) > 0 {
				data["remaining_dirty"] = dirty
				text := commitText(commit, commitResult) + "\n\n⚠ uncommitted changes remain in: " + strings.Join(dirty, ", ")
				return operation.OK(operation.Rendered{Text: text, Data: data})
			}
		}
		return operation.OK(operation.Rendered{Text: commitText(commit, commitResult), Data: data})
	}
}

func (p Plugin) tag() operationruntime.TypedResultHandler[tagInput, map[string]any] {
	return func(ctx operation.Context, req tagInput) operation.Result {
		args, result := gitTagArgs(req)
		if result.IsError() {
			return result
		}
		run, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 30 * time.Second, MaxStdout: 128 * 1024, MaxStderr: 128 * 1024})
		data := processData(run)
		data["tag"] = strings.TrimSpace(req.Name)
		if err != nil {
			return operation.Failed("git_tag_failed", err.Error(), data)
		}
		return operation.OK(operation.Rendered{Text: processText(run, "Created tag "+strings.TrimSpace(req.Name)), Data: data})
	}
}

func (p Plugin) push() operationruntime.TypedResultHandler[pushInput, map[string]any] {
	return func(ctx operation.Context, req pushInput) operation.Result {
		args, result := gitPushArgs(req)
		if result.IsError() {
			return result
		}
		run, err := p.system.Process().Run(ctx, system.ProcessRequest{Command: "git", Args: args, Timeout: 2 * time.Minute, MaxStdout: 128 * 1024, MaxStderr: 128 * 1024})
		data := processData(run)
		data["remote"] = gitPushRemote(req)
		data["refspecs"] = append([]string(nil), req.Refspecs...)
		data["tags"] = req.Tags
		data["dry_run"] = req.DryRun
		if err != nil {
			return operation.Failed("git_push_failed", err.Error(), data)
		}
		return operation.OK(operation.Rendered{Text: processText(run, "Pushed to "+gitPushRemote(req)), Data: data})
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

func gitTagArgs(req tagInput) ([]string, operation.Result) {
	name := strings.TrimSpace(req.Name)
	if err := validateGitToken(name, "tag name"); err != nil {
		return nil, operation.Failed("invalid_git_tag_input", err.Error(), nil)
	}
	args := []string{"tag"}
	message := strings.TrimSpace(req.Message)
	if message != "" {
		args = append(args, "-a", name, "-m", message)
	} else {
		args = append(args, name)
	}
	ref := strings.TrimSpace(req.Ref)
	if ref != "" {
		if err := validateGitToken(ref, "ref"); err != nil {
			return nil, operation.Failed("invalid_git_tag_input", err.Error(), nil)
		}
		args = append(args, ref)
	}
	return args, operation.Result{}
}

func gitPushArgs(req pushInput) ([]string, operation.Result) {
	remote := gitPushRemote(req)
	if err := validateGitToken(remote, "remote"); err != nil {
		return nil, operation.Failed("invalid_git_push_input", err.Error(), nil)
	}
	if !req.Tags && len(req.Refspecs) == 0 {
		return nil, operation.Failed("invalid_git_push_input", "refspecs or tags are required", nil)
	}
	args := []string{"push"}
	if req.DryRun {
		args = append(args, "--dry-run")
	}
	if req.SetUpstream {
		args = append(args, "-u")
	}
	if req.ForceWithLease {
		args = append(args, "--force-with-lease")
	}
	if req.Tags {
		args = append(args, "--tags")
	}
	args = append(args, remote)
	for _, refspec := range req.Refspecs {
		refspec = strings.TrimSpace(refspec)
		if err := validateGitRefspec(refspec); err != nil {
			return nil, operation.Failed("invalid_git_push_input", err.Error(), nil)
		}
		args = append(args, refspec)
	}
	return args, operation.Result{}
}

func gitPushRemote(req pushInput) string {
	remote := strings.TrimSpace(req.Remote)
	if remote == "" {
		return "origin"
	}
	return remote
}

func validateGitToken(value, label string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s must not start with '-'", label)
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%s must not contain whitespace", label)
	}
	if strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "\\") {
		return fmt.Errorf("%s is not a safe git ref token", label)
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") {
		return fmt.Errorf("%s is not a safe git ref token", label)
	}
	return nil
}

func validateGitRefspec(value string) error {
	if err := validateGitToken(value, "refspec"); err != nil {
		return err
	}
	if strings.HasPrefix(value, "+") {
		return fmt.Errorf("force refspecs are rejected; use force_with_lease")
	}
	return nil
}

func looksLikeURL(value string) bool {
	return strings.Contains(value, "://")
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

// remainingDirtyFiles returns paths that have unstaged changes or are untracked
// after a partial commit. It runs git status --porcelain and collects XY codes
// where the worktree column (Y) is non-space, or the file is untracked (??).
func remainingDirtyFiles(ctx operation.Context, sys system.System) []string {
	result, err := sys.Process().Run(ctx, system.ProcessRequest{
		Command: "git",
		Args:    []string{"status", "--porcelain"},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil
	}
	var dirty []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if len(line) < 4 {
			continue
		}
		// porcelain format: XY<space>path, where X=index, Y=worktree.
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if xy == "??" || xy[1] != ' ' {
			dirty = append(dirty, path)
		}
	}
	return dirty
}
