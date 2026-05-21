package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/runtime/system"
)

func TestContributionsIncludeGitOperations(t *testing.T) {
	bundle, err := Plugin{}.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	for _, name := range []string{StatusOp, DiffOp, AddOp, CommitOp, TagOp, PushOp} {
		if !hasOperation(bundle.Operations, name) {
			t.Fatalf("operation %q missing from contributions", name)
		}
	}
	if len(bundle.Commands) != 0 {
		t.Fatalf("commands len = %d, want 0", len(bundle.Commands))
	}
	if len(bundle.OperationSets) != 1 {
		t.Fatalf("operation sets len = %d, want 1", len(bundle.OperationSets))
	}
	if len(bundle.OperationSets[0].Operations) != 6 {
		t.Fatalf("git operation refs len = %d, want 6", len(bundle.OperationSets[0].Operations))
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

func TestAddIntentIncludesIndexAndPaths(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	provider := requireIntentProvider(t, ops[AddOp])

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), addInput{Paths: []string{"README.md"}})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if !hasPathIntent(intents, operation.IntentPersistenceModify, ".git/index") {
		t.Fatalf("intents = %#v, want index write", intents)
	}
	if !hasPathIntent(intents, operation.IntentFilesystemRead, "README.md") {
		t.Fatalf("intents = %#v, want README read", intents)
	}
}

func TestStatusIntentIsProcessOnly(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	provider := requireIntentProvider(t, ops[StatusOp])

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), statusInput{})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if len(intents.Operations) != 1 {
		t.Fatalf("intents = %#v, want process-only status intent", intents)
	}
	target, ok := intents.Operations[0].Target.(operation.ProcessTarget)
	if !ok || target.Command != "git" {
		t.Fatalf("target = %#v, want git process target", intents.Operations[0].Target)
	}
}

func TestDiffIntentDoesNotForceGitDirectoryTarget(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	provider := requireIntentProvider(t, ops[DiffOp])

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), diffInput{})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if hasAnyPathIntent(intents, ".git") {
		t.Fatalf("intents = %#v, diff must not force .git sensitive path target", intents)
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

func TestDiffCompactModesAndBoundsOutput(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "test: add file")
	writeFile(t, dir, "file.txt", strings.Repeat("changed line\n", 20))
	writeFile(t, dir, "other.txt", "new\n")

	ctx := operation.NewContext(context.Background(), event.Discard())
	stat := requireRenderedResult(t, ops[DiffOp].Run(ctx, diffInput{StatOnly: true}))
	if !strings.Contains(stat.Text, "file.txt") || strings.Contains(stat.Text, "@@") {
		t.Fatalf("stat diff text = %q, want diffstat without patch hunks", stat.Text)
	}
	if got := renderedDataString(t, stat, "mode"); got != "stat" {
		t.Fatalf("stat mode = %q, want stat", got)
	}

	names := requireRenderedResult(t, ops[DiffOp].Run(ctx, diffInput{NamesOnly: true}))
	if strings.TrimSpace(names.Text) != "file.txt" || strings.Contains(names.Text, "@@") {
		t.Fatalf("names diff text = %q, want only file.txt", names.Text)
	}
	if got := renderedDataString(t, names, "mode"); got != "names" {
		t.Fatalf("names mode = %q, want names", got)
	}

	truncated := requireRenderedResult(t, ops[DiffOp].Run(ctx, diffInput{MaxBytes: 40}))
	if !strings.Contains(truncated.Text, "[git diff truncated") {
		t.Fatalf("truncated diff text = %q, want truncation note", truncated.Text)
	}
	data, ok := truncated.Data.(map[string]any)
	if !ok || data["truncated"] != true {
		t.Fatalf("truncated diff data = %#v, want truncated=true", truncated.Data)
	}
}

func TestDiffRejectsConflictingCompactModes(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[DiffOp].Run(operation.NewContext(context.Background(), event.Discard()), diffInput{StatOnly: true, NamesOnly: true})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_diff_input" {
		t.Fatalf("error = %#v, want invalid_git_diff_input", result.Error)
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

func TestCommitIntentIncludesStageAndCommitTargets(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	provider := requireIntentProvider(t, ops[CommitOp])

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), commitInput{
		Message: "test: add file",
		Stage:   true,
		Paths:   []string{"file.txt"},
	})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	for _, path := range []string{".git/index", ".git", ".git/COMMIT_EDITMSG", "file.txt"} {
		if !hasAnyPathIntent(intents, path) {
			t.Fatalf("intents = %#v, want path %s", intents, path)
		}
	}
	if !hasProcessIntent(intents, "git") {
		t.Fatalf("intents = %#v, want git process intent", intents)
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

func TestCommitPartialPathWarnsAboutRemainingDirtyFiles(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "a.go", "package main\n")
	writeFile(t, dir, "b.go", "package main\n")

	// commit only a.go, leaving b.go dirty
	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add a.go",
		Stage:   true,
		Paths:   []string{"a.go"},
	})
	commit := requireCommitResult(t, result)
	if strings.TrimSpace(commit) == "" {
		t.Fatal("commit hash is empty")
	}

	// result text must mention the warning
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want Rendered", result.Output)
	}
	if !strings.Contains(rendered.Text, "b.go") {
		t.Fatalf("warning text %q does not mention b.go", rendered.Text)
	}
	if !strings.Contains(rendered.Text, "uncommitted changes remain") {
		t.Fatalf("warning text %q missing expected prefix", rendered.Text)
	}

	// data must carry remaining_dirty listing b.go
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T, want map", rendered.Data)
	}
	dirty, ok := data["remaining_dirty"].([]string)
	if !ok || len(dirty) == 0 {
		t.Fatalf("remaining_dirty = %#v, want non-empty []string", data["remaining_dirty"])
	}
	found := false
	for _, f := range dirty {
		if strings.Contains(f, "b.go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("remaining_dirty = %v, want entry containing b.go", dirty)
	}
}

func TestCommitAllDoesNotWarnWhenNoDirtyFiles(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "a.go", "package main\n")
	writeFile(t, dir, "b.go", "package main\n")

	// commit all, so nothing should remain dirty
	result := ops[CommitOp].Run(operation.NewContext(context.Background(), event.Discard()), commitInput{
		Message: "test: add all",
		Stage:   true,
		All:     true,
	})
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want Rendered", result.Output)
	}
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T, want map", rendered.Data)
	}
	if _, exists := data["remaining_dirty"]; exists {
		t.Fatalf("remaining_dirty present in all-commit result, want absent")
	}
	if strings.Contains(rendered.Text, "uncommitted") {
		t.Fatalf("text %q must not contain warning for all-commit", rendered.Text)
	}
}

func TestTagCreatesAnnotatedTag(t *testing.T) {
	dir := testRepo(t)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "test: add file")

	result := ops[TagOp].Run(operation.NewContext(context.Background(), event.Discard()), tagInput{
		Name:    "v1.0.0",
		Message: "release v1.0.0",
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	tags := gitOutput(t, dir, "tag", "--list", "v1.0.0")
	if strings.TrimSpace(tags) != "v1.0.0" {
		t.Fatalf("tags = %q, want v1.0.0", tags)
	}
}

func TestTagRejectsUnsafeName(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[TagOp].Run(operation.NewContext(context.Background(), event.Discard()), tagInput{Name: "-bad"})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_tag_input" {
		t.Fatalf("error = %#v, want invalid_git_tag_input", result.Error)
	}
}

func TestTagIntentIncludesTagRef(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	provider := requireIntentProvider(t, ops[TagOp])

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), tagInput{Name: "v1.0.0"})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if !hasPathIntent(intents, operation.IntentPersistenceModify, ".git/refs/tags/v1.0.0") {
		t.Fatalf("intents = %#v, want tag ref write", intents)
	}
	if !hasProcessIntent(intents, "git") {
		t.Fatalf("intents = %#v, want git process intent", intents)
	}
}

func TestPushPushesExplicitRefToLocalRemote(t *testing.T) {
	dir := testRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	ops := testGitOperations(t, dir)
	writeFile(t, dir, "file.txt", "hello\n")
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "test: add file")
	runGit(t, dir, "branch", "-M", "main")

	result := ops[PushOp].Run(operation.NewContext(context.Background(), event.Discard()), pushInput{
		Remote:   remote,
		Refspecs: []string{"main"},
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	head := strings.TrimSpace(gitOutput(t, dir, "rev-parse", "main"))
	remoteHead := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "main"))
	if remoteHead != head {
		t.Fatalf("remote main = %q, want %q", remoteHead, head)
	}
}

func TestPushRequiresExplicitRefspecOrTags(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[PushOp].Run(operation.NewContext(context.Background(), event.Discard()), pushInput{})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_push_input" {
		t.Fatalf("error = %#v, want invalid_git_push_input", result.Error)
	}
}

func TestPushRejectsForceRefspec(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	result := ops[PushOp].Run(operation.NewContext(context.Background(), event.Discard()), pushInput{Refspecs: []string{"+main"}})
	if result.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "invalid_git_push_input" {
		t.Fatalf("error = %#v, want invalid_git_push_input", result.Error)
	}
}

func TestPushIntentIncludesConfigAndNetworkRemote(t *testing.T) {
	ops := testGitOperations(t, t.TempDir())
	provider := requireIntentProvider(t, ops[PushOp])

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), pushInput{
		Remote:   "https://example.com/repo.git",
		Refspecs: []string{"main"},
	})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if !hasPathIntent(intents, operation.IntentFilesystemRead, ".git/config") {
		t.Fatalf("intents = %#v, want git config read", intents)
	}
	if !hasURLIntent(intents, operation.IntentNetworkWrite, "https://example.com/repo.git") {
		t.Fatalf("intents = %#v, want network write remote", intents)
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

func requireIntentProvider(t *testing.T, op operation.Operation) operation.IntentProvider {
	t.Helper()
	provider, ok := op.(operation.IntentProvider)
	if !ok {
		t.Fatalf("%s does not implement IntentProvider", op.Spec().Ref.String())
	}
	return provider
}

func hasPathIntent(intents operation.IntentSet, behavior operation.IntentBehavior, path string) bool {
	for _, intent := range intents.Operations {
		target, ok := intent.Target.(operation.PathTarget)
		if ok && intent.Behavior == behavior && target.Path == operation.Path(path) {
			return true
		}
	}
	return false
}

func hasAnyPathIntent(intents operation.IntentSet, path string) bool {
	for _, intent := range intents.Operations {
		target, ok := intent.Target.(operation.PathTarget)
		if ok && target.Path == operation.Path(path) {
			return true
		}
	}
	return false
}

func hasProcessIntent(intents operation.IntentSet, command string) bool {
	for _, intent := range intents.Operations {
		target, ok := intent.Target.(operation.ProcessTarget)
		if ok && target.Command == operation.Command(command) {
			return true
		}
	}
	return false
}

func hasURLIntent(intents operation.IntentSet, behavior operation.IntentBehavior, url string) bool {
	for _, intent := range intents.Operations {
		target, ok := intent.Target.(operation.URLTarget)
		if ok && intent.Behavior == behavior && target.URL == operation.URL(url) {
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

func requireRenderedResult(t *testing.T, result operation.Result) operation.Rendered {
	t.Helper()
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want operation.Rendered", result.Output)
	}
	return rendered
}

func renderedDataString(t *testing.T, rendered operation.Rendered, key string) string {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want map", rendered.Data)
	}
	value, _ := data[key].(string)
	return value
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
