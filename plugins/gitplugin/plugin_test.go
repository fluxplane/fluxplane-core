package gitplugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestContributionsIncludeGitOperations(t *testing.T) {
	bundle, err := Plugin{}.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	for _, name := range []string{StatusOp, DiffOp, AddOp, CommitOp} {
		if !hasOperation(bundle.Operations, name) {
			t.Fatalf("operation %q missing from contributions", name)
		}
	}
	for _, path := range []command.Path{{Name, "status"}, {Name, "diff"}, {Name, "add"}, {Name, "commit"}} {
		if !hasCommand(bundle.Commands, path) {
			t.Fatalf("command %q missing from contributions", path.String())
		}
	}
	if len(bundle.OperationSets) != 1 {
		t.Fatalf("operation sets len = %d, want 1", len(bundle.OperationSets))
	}
	if len(bundle.OperationSets[0].Operations) != 4 {
		t.Fatalf("git operation refs len = %d, want 4", len(bundle.OperationSets[0].Operations))
	}
}

func TestAddRejectsEmptyRequest(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[AddOp].Run(operation.NewContext(context.Background(), event.Discard()), addInput{})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_add_input" {
		t.Fatalf("error = %#v, want invalid_git_add_input", result.Error)
	}
}

func TestAddStagesExplicitPath(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")

	result := ops[AddOp].Run(operation.NewContext(context.Background(), event.Discard()), addInput{Paths: []string{"file.txt"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	staged := gitOutput(t, dir, "diff", "--staged", "--name-only")
	if strings.TrimSpace(staged) != "file.txt" {
		t.Fatalf("staged files = %q, want file.txt", staged)
	}
}

func TestCommitRejectsEmptyMessage(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_commit_input" {
		t.Fatalf("error = %#v, want invalid_git_commit_input", result.Error)
	}
}

func TestCommitRejectsPathsWithoutStage(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add file",
		Paths:   []string{"file.txt"},
	})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_commit_input" {
		t.Fatalf("error = %#v, want invalid_git_commit_input", result.Error)
	}
}

func TestCommitRejectsAllWithoutStage(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add file",
		All:     true,
	})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_commit_input" {
		t.Fatalf("error = %#v, want invalid_git_commit_input", result.Error)
	}
}

func TestCommitCommitsStagedChangesAndReturnsHash(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")
	runGit(t, dir, "add", "file.txt")

	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{Message: "test: add file"})
	commit := requireCommitResult(t, result)
	head := strings.TrimSpace(gitOutput(t, dir, "rev-parse", "HEAD"))
	if commit != head {
		t.Fatalf("commit = %q, HEAD = %q", commit, head)
	}
}

func TestCommitAutoStagesPaths(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")

	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add file",
		Stage:   true,
		Paths:   []string{"file.txt"},
	})
	commit := requireCommitResult(t, result)
	head := strings.TrimSpace(gitOutput(t, dir, "rev-parse", "HEAD"))
	if commit != head {
		t.Fatalf("commit = %q, HEAD = %q", commit, head)
	}
}

func TestCommitDoesNotRunPostCommitHook(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")
	hookPath := filepath.Join(dir, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nprintf hook > hook-ran\n"), 0o700); err != nil {
		t.Fatalf("write post-commit hook: %v", err)
	}

	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add file",
		Stage:   true,
		Paths:   []string{"file.txt"},
	})
	requireCommitResult(t, result)
	if _, err := os.Stat(filepath.Join(dir, "hook-ran")); err == nil {
		t.Fatal("post-commit hook marker exists, want hook disabled")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat hook marker: %v", err)
	}
}

func TestCommitAutoStagesAll(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "one.txt", "one\n")
	writeFile(t, dir, "two.txt", "two\n")

	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add files",
		Stage:   true,
		All:     true,
	})
	commit := requireCommitResult(t, result)
	head := strings.TrimSpace(gitOutput(t, dir, "rev-parse", "HEAD"))
	if commit != head {
		t.Fatalf("commit = %q, HEAD = %q", commit, head)
	}
}

func hasOperation(specs []operation.Spec, name string) bool {
	for _, spec := range specs {
		if string(spec.Ref.Name) == name {
			return true
		}
	}
	return false
}

func hasCommand(specs []command.Spec, path command.Path) bool {
	for _, spec := range specs {
		if spec.Path.String() == path.String() {
			return true
		}
	}
	return false
}

func testGitOperations(t *testing.T, root string) map[string]operation.Operation {
	t.Helper()
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	out := map[string]operation.Operation{}
	for _, op := range ops {
		out[string(op.Spec().Ref.Name)] = op
	}
	return out
}

func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func requireCommitResult(t *testing.T, result operation.Result) string {
	t.Helper()
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want operation.Rendered", result.Output)
	}
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want map", rendered.Data)
	}
	commit, _ := data["commit"].(string)
	if strings.TrimSpace(commit) == "" {
		t.Fatalf("commit missing from data %#v", data)
	}
	return commit
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
