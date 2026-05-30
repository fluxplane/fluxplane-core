package project

import (
	"context"
	"strings"
	"testing"
	"time"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coreproject "github.com/fluxplane/fluxplane-core/core/project"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-core/runtime/systemtest"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	coreevent "github.com/fluxplane/fluxplane-event"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

func TestProjectOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runProjectPluginBackends(t, func(t *testing.T, sys system.System) {
		writeProjectFile(t, sys.Workspace(), "go.mod", "module example.com/app\n\ngo 1.26\n")
		writeProjectFile(t, sys.Workspace(), "package.json", `{"name":"app","scripts":{"test":"node test.js"}}`)
		writeProjectFile(t, sys.Workspace(), "Dockerfile", "FROM scratch\n")
		writeProjectFile(t, sys.Workspace(), "api.Dockerfile", "FROM alpine\n")
		writeProjectFile(t, sys.Workspace(), "docker-compose.yaml", "services:\n  app:\n    image: example/app\n")
		writeProjectFile(t, sys.Workspace(), ".agents/plans/example.md", "# Plan\n")
		writeProjectFile(t, sys.Workspace(), ".claude/commands/check.md", "# Check\n")
		writeProjectFile(t, sys.Workspace(), "README.md", "# App\n\n## Usage\n\n### CLI\n")

		inventory := runProjectOp(t, sys, InventoryOp, map[string]any{"refresh": true})
		if !strings.Contains(inventory.Text, ". [project:.]") || !strings.Contains(inventory.Text, "go_module go.mod") || !strings.Contains(inventory.Text, "node_package package.json") || !strings.Contains(inventory.Text, "dockerfile Dockerfile") || !strings.Contains(inventory.Text, "docker_compose docker-compose.yaml") || !strings.Contains(inventory.Text, "agents_dir .agents") || !strings.Contains(inventory.Text, "claude_dir .claude") {
			t.Fatalf("inventory text = %q", inventory.Text)
		}
		data, ok := inventory.Data.(map[string]any)
		if !ok {
			t.Fatalf("inventory data = %#v, want map", inventory.Data)
		}
		summary, ok := data["inventory"].(inventorySummary)
		if !ok {
			t.Fatalf("inventory summary = %#v, want inventorySummary", data["inventory"])
		}
		if summary.WorkspaceID == "" || len(summary.Hints) == 0 || summary.Hints[0].WorkspaceID == "" {
			t.Fatalf("summary = %#v, want workspace ids", summary)
		}

		tasks := runProjectOp(t, sys, TasksOp, map[string]any{})
		if !strings.Contains(tasks.Text, "test (package_script)") {
			t.Fatalf("tasks text = %q", tasks.Text)
		}

		docs := runProjectOp(t, sys, DocsOp, map[string]any{})
		if !strings.Contains(docs.Text, "# App") || !strings.Contains(docs.Text, "## Usage") || !strings.Contains(docs.Text, "### CLI") {
			t.Fatalf("docs text = %q", docs.Text)
		}
		bareIDFiles := runProjectOp(t, sys, FilesOp, map[string]any{"project_id": ".", "max_results": 20})
		if !strings.Contains(bareIDFiles.Text, "go.mod") {
			t.Fatalf("bare id files text = %q", bareIDFiles.Text)
		}
		bareIDDocs := runProjectOp(t, sys, DocsOp, map[string]any{"project_id": "."})
		if !strings.Contains(bareIDDocs.Text, "# App") {
			t.Fatalf("bare id docs text = %q", bareIDDocs.Text)
		}
		agentFiles := runProjectOp(t, sys, FilesOp, map[string]any{"path": ".", "facet_kind": "agents_dir", "max_results": 10})
		if !strings.Contains(agentFiles.Text, ".agents/plans/example.md") || strings.Contains(agentFiles.Text, ".claude/commands/check.md") {
			t.Fatalf("agent facet files text = %q", agentFiles.Text)
		}
		dockerfileFiles := runProjectOp(t, sys, FilesOp, map[string]any{"path": ".", "facet_kind": "dockerfile", "max_results": 20})
		if !strings.Contains(dockerfileFiles.Text, "Dockerfile") || !strings.Contains(dockerfileFiles.Text, "api.Dockerfile") || strings.Contains(dockerfileFiles.Text, "docker-compose.yaml") {
			t.Fatalf("dockerfile facet files text = %q", dockerfileFiles.Text)
		}
		composeFiles := runProjectOp(t, sys, FilesOp, map[string]any{"path": ".", "facet_kind": "docker_compose", "max_results": 20})
		if !strings.Contains(composeFiles.Text, "docker-compose.yaml") || strings.Contains(composeFiles.Text, "Dockerfile") {
			t.Fatalf("docker compose facet files text = %q", composeFiles.Text)
		}
		providers, err := New(sys).ContextProviders(context.Background(), pluginhost.Context{})
		if err != nil {
			t.Fatalf("ContextProviders: %v", err)
		}
		if len(providers) != 1 {
			t.Fatalf("context providers len = %d, want 1", len(providers))
		}
		if providers[0].Spec().Annotations[corecontext.AnnotationAutoContext] != "true" {
			t.Fatalf("provider spec = %#v, want auto context", providers[0].Spec())
		}
		blocks, err := providers[0].Build(context.Background(), corecontext.Request{})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "Workspace project summary:") || !strings.Contains(blocks[0].Content, "project_inventory") {
			t.Fatalf("blocks = %#v", blocks)
		}
	})
}

func TestProjectObserverAndAssertionDeriver(t *testing.T) {
	sys := systemtest.NewMemory()
	writeProjectFile(t, sys.Workspace(), "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeProjectFile(t, sys.Workspace(), "README.md", "# App\n")
	plugin := New(sys)

	observers, err := plugin.EnvironmentObservers(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	if len(observers) != 1 || observers[0].Spec().Name != ObserverName {
		t.Fatalf("observers = %#v, want project inventory observer", observers)
	}
	observations, err := observers[0].Observe(context.Background(), runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseSessionOpen})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationProjectInventory {
		t.Fatalf("observations = %#v, want project inventory observation", observations)
	}

	derivers, err := plugin.AssertionDerivers(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("AssertionDerivers: %v", err)
	}
	hints, err := derivers[0].Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{Observations: observations})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !hasEnvironmentHint(hints, AssertionLanguageDetected, "go") {
		t.Fatalf("hints = %#v, want Go language hint", hints)
	}
	if !hasEnvironmentHint(hints, AssertionLanguageDetected, "markdown") {
		t.Fatalf("hints = %#v, want markdown language hint", hints)
	}
	if !hasEnvironmentHint(hints, AssertionProjectToolchainHint, "go") {
		t.Fatalf("hints = %#v, want Go toolchain hint", hints)
	}
	if !hasEnvironmentHint(hints, AssertionProjectManifest, "go.mod") {
		t.Fatalf("hints = %#v, want go.mod manifest hint", hints)
	}
}

func TestProjectPluginResolvesWorkspaceDeclarationsLazily(t *testing.T) {
	sys := systemtest.NewMemory()
	plugin := New(sys)
	writeProjectFile(t, sys.Workspace(), "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeProjectFile(t, sys.Workspace(), ".agents/workspaces.json", `{"workspaces":[{"id":"workspace:configured:test","roots":[{"path":"/memory-workspace"}]}]}`)

	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var inventory operation.Rendered
	for _, op := range ops {
		if string(op.Spec().Ref.Name) != InventoryOp {
			continue
		}
		result := op.Run(operation.NewContext(context.Background(), nil), map[string]any{"refresh": true})
		if result.Status != operation.StatusOK {
			t.Fatalf("inventory status = %s error = %#v", result.Status, result.Error)
		}
		var ok bool
		inventory, ok = result.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("inventory output = %#v, want rendered", result.Output)
		}
	}
	data, ok := inventory.Data.(map[string]any)
	if !ok {
		t.Fatalf("inventory data = %#v, want map", inventory.Data)
	}
	summary, ok := data["inventory"].(inventorySummary)
	if !ok {
		t.Fatalf("summary = %#v, want inventorySummary", data["inventory"])
	}
	if summary.WorkspaceID != "workspace:configured:test" {
		t.Fatalf("workspace id = %q, want declared workspace", summary.WorkspaceID)
	}
}

func hasEnvironmentHint(hints []coreevidence.Assertion, kind, target string) bool {
	for _, hint := range hints {
		if hint.Kind == kind && hint.Target == target {
			return true
		}
	}
	return false
}

func TestProjectTaskRunDryRunAndExecution(t *testing.T) {
	base := systemtest.NewMemory()
	proc := &fakeTaskProcess{result: system.ProcessResult{
		Command:  "task",
		Args:     []string{"--taskfile", "Taskfile.yaml", "lint"},
		Stdout:   "ok\n",
		ExitCode: 0,
		Duration: 25 * time.Millisecond,
	}}
	sys := taskRunSystem{MemorySystem: base, process: proc}
	writeProjectFile(t, sys.Workspace(), "Taskfile.yaml", "version: '3'\ntasks:\n  lint:\n    desc: Run lint\n    cmds:\n      - go vet ./...\n")

	dryRun := runProjectTaskOp(t, sys, map[string]any{"name": "lint", "kind": "taskfile", "dry_run": true})
	if !dryRun.DryRun || dryRun.Executable != "task" || !sameStrings(dryRun.Args, []string{"--taskfile", "Taskfile.yaml", "lint"}) {
		t.Fatalf("dry run = %#v", dryRun)
	}
	dryRunOp := findProjectOp(t, sys, TaskRunOp)
	intents, ok, err := operation.IntentFor(operation.NewContext(context.Background(), nil), dryRunOp, map[string]any{"name": "lint", "kind": "taskfile", "dry_run": true})
	if err != nil {
		t.Fatalf("IntentFor dry run: %v", err)
	}
	if !ok || len(intents.Operations) != 1 {
		t.Fatalf("dry run intents = %#v ok=%v, want one process intent", intents, ok)
	}
	if proc.startCount != 0 {
		t.Fatalf("process start count = %d, want 0 for dry run", proc.startCount)
	}

	var events []coreevent.Event
	result := runProjectTaskOpWithEvents(t, sys, map[string]any{"task_id": "taskfile:Taskfile.yaml:lint"}, &events)
	if result.Stdout != "ok\n" || result.ExitCode != 0 {
		t.Fatalf("result = %#v, want process output", result)
	}
	if proc.request.Command != "task" || !sameStrings(proc.request.Args, []string{"--taskfile", "Taskfile.yaml", "lint"}) {
		t.Fatalf("process request = %#v", proc.request)
	}
	if len(events) == 0 {
		t.Fatalf("events = %#v, want forwarded process and usage events", events)
	}
}

func runProjectPluginBackends(t *testing.T, fn func(*testing.T, system.System)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, systemtest.NewMemory())
	})
	t.Run("host", func(t *testing.T) {
		sys, err := system.NewHost(system.Config{Root: t.TempDir()})
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		fn(t, sys)
	})
}

func runProjectOp(t *testing.T, sys system.System, name string, input map[string]any) operation.Rendered {
	t.Helper()
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			result := op.Run(operation.NewContext(context.Background(), nil), input)
			if result.Status != operation.StatusOK {
				t.Fatalf("%s status = %s error = %#v", name, result.Status, result.Error)
			}
			rendered, ok := result.Output.(operation.Rendered)
			if !ok {
				t.Fatalf("%s output = %#v, want Rendered", name, result.Output)
			}
			return rendered
		}
	}
	t.Fatalf("operation %s not found", name)
	return operation.Rendered{}
}

func runProjectTaskOp(t *testing.T, sys system.System, input map[string]any) coreproject.TaskRunResult {
	t.Helper()
	var events []coreevent.Event
	return runProjectTaskOpWithEvents(t, sys, input, &events)
}

func runProjectTaskOpWithEvents(t *testing.T, sys system.System, input map[string]any, events *[]coreevent.Event) coreproject.TaskRunResult {
	t.Helper()
	op := findProjectOp(t, sys, TaskRunOp)
	ctx := operation.NewContext(context.Background(), coreevent.SinkFunc(func(event coreevent.Event) {
		*events = append(*events, event)
	}))
	result := op.Run(ctx, input)
	if result.Status != operation.StatusOK {
		t.Fatalf("%s status = %s error = %#v output = %#v", TaskRunOp, result.Status, result.Error, result.Output)
	}
	out, ok := result.Output.(coreproject.TaskRunResult)
	if !ok {
		t.Fatalf("%s output = %#v, want TaskRunResult", TaskRunOp, result.Output)
	}
	return out
}

func findProjectOp(t *testing.T, sys system.System, name string) operation.Operation {
	t.Helper()
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	t.Fatalf("operation %s not found", name)
	return nil
}

type taskRunSystem struct {
	*systemtest.MemorySystem
	process *fakeTaskProcess
}

func (s taskRunSystem) Process() system.ProcessManager { return s.process }

type fakeTaskProcess struct {
	request    system.ProcessRequest
	result     system.ProcessResult
	startCount int
}

func (p *fakeTaskProcess) Run(_ context.Context, req system.ProcessRequest) (system.ProcessResult, error) {
	p.request = req
	return p.result, nil
}

func (p *fakeTaskProcess) Start(_ context.Context, req system.ProcessRequest) (system.ProcessHandle, error) {
	p.startCount++
	p.request = req
	events := make(chan system.ProcessEvent, 2)
	events <- system.ProcessEvent{ProcessID: "test-process", Kind: "output", Stream: "stdout", Data: p.result.Stdout, Time: time.Now()}
	close(events)
	result := p.result
	result.Command = req.Command
	result.Args = append([]string(nil), req.Args...)
	result.Workdir = req.Workdir
	return fakeTaskHandle{request: req, result: result, events: events}, nil
}

func (p *fakeTaskProcess) Ensure(ctx context.Context, req system.ProcessRequest) (system.ProcessHandle, bool, error) {
	handle, err := p.Start(ctx, req)
	return handle, true, err
}

func (p *fakeTaskProcess) List(context.Context) ([]system.ProcessInfo, error) { return nil, nil }

func (p *fakeTaskProcess) Status(context.Context, string) (system.ProcessInfo, error) {
	return system.ProcessInfo{}, nil
}

func (p *fakeTaskProcess) Output(context.Context, string) (system.ProcessOutput, error) {
	return system.ProcessOutput{}, nil
}

func (p *fakeTaskProcess) Wait(context.Context, string, time.Duration) (system.ProcessResult, error) {
	return p.result, nil
}

func (p *fakeTaskProcess) Stop(context.Context, string) error { return nil }

func (p *fakeTaskProcess) Kill(context.Context, string) error { return nil }

type fakeTaskHandle struct {
	request system.ProcessRequest
	result  system.ProcessResult
	events  <-chan system.ProcessEvent
}

func (h fakeTaskHandle) ID() string { return "test-process" }

func (h fakeTaskHandle) Info() system.ProcessInfo {
	return system.ProcessInfo{ID: h.ID(), Command: h.request.Command, Args: h.request.Args, Workdir: h.request.Workdir, Running: true}
}

func (h fakeTaskHandle) Events() <-chan system.ProcessEvent { return h.events }

func (h fakeTaskHandle) Wait(context.Context) (system.ProcessResult, error) { return h.result, nil }

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func writeProjectFile(t *testing.T, ws runtimeworkspace.Workspace, rel, content string) {
	t.Helper()
	resolved, err := ws.ResolveCreate(context.Background(), rel)
	if err != nil {
		t.Fatalf("ResolveCreate(%s): %v", rel, err)
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		t.Fatalf("WorkspaceFileSystem(%s): %v", rel, err)
	}
	if err := fsys.WriteFile(context.Background(), runtimeworkspace.PathName(resolved), []byte(content), fpsystem.WriteFileOptions{Perm: 0644, Overwrite: true}); err != nil {
		t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}
