package project

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coreproject "github.com/fluxplane/fluxplane-core/core/project"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/usage"
	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimeproject "github.com/fluxplane/fluxplane-core/runtime/project"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

const (
	Name             = "project"
	InventoryOp      = "project_inventory"
	FilesOp          = "project_files"
	TasksOp          = "project_tasks"
	TaskRunOp        = "project_task_run"
	DocsOp           = "project_docs"
	SummaryProvider  = "project.summary"
	ObserverName     = "project.inventory"
	AssertionDeriver = "project.assertions"
	defaultMaxFiles  = 500
)

const (
	ObservationProjectInventory   = "project.inventory"
	AssertionLanguageDetected     = "language.detected"
	AssertionProjectToolchainHint = "project.toolchain.hinted"
	AssertionProjectManifest      = "project.manifest.detected"
)

const (
	defaultTaskTimeout     = 30 * time.Second
	defaultTaskOutputBytes = 64 * 1024
	maxTaskTimeout         = 10 * time.Minute
)

// Plugin contributes Workspace project inventory operations.
type Plugin struct {
	system           system.System
	manager          *runtimeproject.Manager
	workspaceManager *runtimeworkspace.Manager
	workspaceID      coreworkspace.ID
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}

func New(sys system.System) Plugin {
	return NewWithWorkspaceManager(sys, runtimeworkspace.NewManager(), "")
}

// NewWithWorkspaceManager returns a project inventory plugin using workspace
// resolution from the supplied manager.
func NewWithWorkspaceManager(sys system.System, workspaceManager *runtimeworkspace.Manager, explicitWorkspaceID coreworkspace.ID) Plugin {
	return Plugin{system: sys, workspaceManager: workspaceManager, workspaceID: explicitWorkspaceID}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Workspace project inventory operations."}
}

// Contributions returns project operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	return resource.ContributionBundle{
		ContextProviders:  []corecontext.ProviderSpec{summaryContextSpec()},
		Observers:         []coreevidence.ObserverSpec{projectObserverSpec()},
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{projectAssertionDeriverSpec()},
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Workspace project inventory and outline operations.",
			Operations:  refs(specs),
		}},
		Operations: specs,
	}, nil
}

// EnvironmentObservers returns executable project inventory observers.
func (p Plugin) EnvironmentObservers(ctx context.Context, _ pluginhost.Context) ([]runtimeevidence.Observer, error) {
	manager := p.projectManager(ctx)
	if manager == nil {
		return nil, nil
	}
	return []runtimeevidence.Observer{projectObserver{manager: manager}}, nil
}

// AssertionDerivers returns executable project inventory assertion derivation.
func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{projectAssertionDeriver{}}, nil
}

func (p Plugin) ContextProviders(ctx context.Context, _ pluginhost.Context) ([]corecontext.Provider, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, nil
	}
	manager := p.projectManager(ctx)
	if manager == nil {
		return nil, nil
	}
	return []corecontext.Provider{summaryProvider{manager: manager}}, nil
}

func (p Plugin) Operations(ctx context.Context, _ pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, fmt.Errorf("projectplugin: system workspace is nil")
	}
	manager := p.projectManager(ctx)
	if manager == nil {
		return nil, fmt.Errorf("projectplugin: project manager is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[coreproject.InventoryQuery, operation.Rendered](specByName(InventoryOp), p.inventory(manager)),
		operationruntime.NewTypedResult[coreproject.FilesQuery, operation.Rendered](specByName(FilesOp), p.files(manager)),
		operationruntime.NewTypedResult[coreproject.TasksQuery, operation.Rendered](specByName(TasksOp), p.tasks(manager)),
		operationruntime.NewTypedResult[coreproject.TaskRunRequest, coreproject.TaskRunResult](specByName(TaskRunOp), p.taskRun(manager), operationruntime.WithIntent(p.taskRunIntent(manager))),
		operationruntime.NewTypedResult[coreproject.DocsQuery, operation.Rendered](specByName(DocsOp), p.docs(manager)),
	}, nil
}

func (p Plugin) projectManager(ctx context.Context) *runtimeproject.Manager {
	if p.manager != nil {
		return p.manager
	}
	if p.system == nil || p.system.Workspace() == nil {
		return nil
	}
	workspaceManager := p.workspaceManager
	if workspaceManager == nil {
		workspaceManager = runtimeworkspace.NewManager()
	}
	result, err := workspaceManager.ResolveSystemWorkspace(ctx, p.system.Workspace(), p.workspaceID)
	if err != nil || result.Selection.Active == "" {
		return runtimeproject.NewManager(p.system.Workspace())
	}
	return runtimeproject.NewManagerForWorkspace(p.system.Workspace(), result.Selection.Active)
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec[coreproject.InventoryQuery](InventoryOp, "Discover Workspace projects and facets such as go.mod, go.work, package.json, Makefile, Taskfile.yaml, Dockerfile, docker-compose.yaml, and markdown docs. The inventory is memory-only; refresh rebuilds it for this plugin instance."),
		spec[coreproject.FilesQuery](FilesOp, "List a bounded project file tree scoped to a detected project or path. This is read-only and uses Workspace-relative paths."),
		spec[coreproject.TasksQuery](TasksOp, "List cheap project task entry points discovered from Makefiles, Taskfiles, and package.json scripts."),
		taskRunSpec(),
		spec[coreproject.DocsQuery](DocsOp, "Return markdown document heading outlines discovered in the Workspace project inventory."),
	}
}

func spec[I any](name, description string) operation.Spec {
	return operationruntime.WithTypedContract[I, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskRunSpec() operation.Spec {
	return operationruntime.WithTypedContract[coreproject.TaskRunRequest, coreproject.TaskRunResult](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(TaskRunOp)},
		Description: "Run one discovered project task from a Makefile, Taskfile, or package.json script through the managed process boundary. Use dry_run to resolve the command without executing it.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
	})
}

func specByName(name string) operation.Spec {
	for _, spec := range specs() {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}
}

func refs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

type projectObserver struct {
	manager *runtimeproject.Manager
}

func (projectObserver) Spec() coreevidence.ObserverSpec {
	return projectObserverSpec()
}

func (o projectObserver) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	if o.manager == nil {
		return nil, nil
	}
	inventory, _, err := o.manager.Inventory(ctx, coreproject.InventoryQuery{})
	if err != nil {
		return nil, err
	}
	if len(inventory.Projects) == 0 && len(inventory.Hints) == 0 {
		return nil, nil
	}
	scope := string(inventory.WorkspaceID)
	if scope == "" {
		scope = "workspace"
	}
	return []coreevidence.Observation{{
		ID:      "project:inventory:" + scope,
		Kind:    ObservationProjectInventory,
		Scope:   scope,
		Content: compactInventory(inventory),
		Environment: coreevidence.Ref{
			Name: "workspace",
		},
		At: time.Now().UTC(),
	}}, nil
}

func projectObserverSpec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:        ObserverName,
		Description: "Observes bounded Workspace project inventory and project capability hints.",
		Environment: coreevidence.Ref{
			Name: "workspace",
		},
		Phase:           coreevidence.PhaseSessionOpen,
		ObservableKinds: []string{ObservationProjectInventory},
		Dynamic:         true,
	}
}

type projectAssertionDeriver struct{}

func (projectAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return projectAssertionDeriverSpec()
}

func (projectAssertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != ObservationProjectInventory {
			continue
		}
		for _, hint := range projectHintsFromContent(observation.Content) {
			confidence := hint.Confidence
			if confidence == 0 {
				confidence = 1
			}
			if hint.Language != "" {
				out = append(out, coreevidence.Assertion{
					Kind:           AssertionLanguageDetected,
					Target:         string(hint.Language),
					Subject:        coreevidence.Subject{Kind: coreevidence.SubjectLanguage, Name: string(hint.Language)},
					Scope:          observation.Scope,
					Environment:    observation.Environment,
					Confidence:     confidence,
					ObservationIDs: observationIDs(observation.ID),
				})
			}
			if hint.Toolchain != "" {
				out = append(out, coreevidence.Assertion{
					Kind:           AssertionProjectToolchainHint,
					Target:         hint.Toolchain,
					Subject:        coreevidence.Subject{Kind: coreevidence.SubjectToolchain, Name: hint.Toolchain},
					Scope:          observation.Scope,
					Environment:    observation.Environment,
					Confidence:     confidence,
					ObservationIDs: observationIDs(observation.ID),
				})
			}
			if hint.Path != "" {
				out = append(out, coreevidence.Assertion{
					Kind:           AssertionProjectManifest,
					Target:         hint.Path,
					Subject:        coreevidence.Subject{Kind: "manifest", Name: hint.Path},
					Scope:          observation.Scope,
					Environment:    observation.Environment,
					Confidence:     confidence,
					ObservationIDs: observationIDs(observation.ID),
				})
			}
		}
	}
	return out, nil
}

func projectAssertionDeriverSpec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             AssertionDeriver,
		Description:      "Derives language, toolchain, and manifest hints from project inventory observations.",
		ObservationKinds: []string{ObservationProjectInventory},
	}
}

func projectHintsFromContent(content any) []coreproject.Hint {
	switch typed := content.(type) {
	case inventorySummary:
		return append([]coreproject.Hint(nil), typed.Hints...)
	case *inventorySummary:
		if typed == nil {
			return nil
		}
		return append([]coreproject.Hint(nil), typed.Hints...)
	case coreproject.Inventory:
		return append([]coreproject.Hint(nil), typed.Hints...)
	case *coreproject.Inventory:
		if typed == nil {
			return nil
		}
		return append([]coreproject.Hint(nil), typed.Hints...)
	default:
		return nil
	}
}

func observationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

func summaryContextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             SummaryProvider,
		Description:      "Compact Workspace project orientation.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
		Annotations:      map[string]string{corecontext.AnnotationAutoContext: "true"},
	}
}

type summaryProvider struct {
	manager *runtimeproject.Manager
}

func (p summaryProvider) Spec() corecontext.ProviderSpec { return summaryContextSpec() }

func (p summaryProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	if p.manager == nil {
		return nil, nil
	}
	inventory, _, err := p.manager.Inventory(ctx, coreproject.InventoryQuery{})
	if err != nil || len(inventory.Projects) == 0 {
		return nil, nil
	}
	content := renderProjectSummary(inventory)
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	return []corecontext.Block{{
		ID:        SummaryProvider,
		Provider:  SummaryProvider,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "Project Summary",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func renderProjectSummary(inventory coreproject.Inventory) string {
	var lines []string
	lines = append(lines, "Workspace project summary:")
	for i, project := range inventory.Projects {
		if i >= 5 {
			lines = append(lines, fmt.Sprintf("- other projects: %d more", len(inventory.Projects)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", displayRoot(project.Root), project.ID, project.Name))
		facets := facetLabels(project.Facets)
		if len(facets) > 0 {
			lines = append(lines, "  facets: "+strings.Join(facets, ", "))
		}
		if docs := firstDocuments(project, 4); len(docs) > 0 {
			lines = append(lines, "  docs: "+strings.Join(docs, ", "))
		}
		if tasks := taskSources(project, 4); len(tasks) > 0 {
			lines = append(lines, "  tasks: "+strings.Join(tasks, ", "))
		}
	}
	lines = append(lines, "Use project_inventory, project_docs, project_tasks, and project_files for details.")
	return strings.Join(lines, "\n")
}

func facetLabels(facets []coreproject.Facet) []string {
	seen := map[string]bool{}
	var out []string
	for _, facet := range facets {
		label := string(facet.Kind)
		if facet.Manifest.Path != "" {
			label += " " + facet.Manifest.Path
		}
		if !seen[label] {
			out = append(out, label)
			seen[label] = true
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func firstDocuments(project coreproject.Project, limit int) []string {
	var out []string
	for _, facet := range project.Facets {
		for _, doc := range facet.Documents {
			out = append(out, doc.Path)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func taskSources(project coreproject.Project, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, facet := range project.Facets {
		for _, task := range facet.Tasks {
			label := task.Kind
			if label == "" {
				label = task.Path
			}
			if label != "" && !seen[label] {
				out = append(out, label)
				seen[label] = true
				if len(out) >= limit {
					return out
				}
			}
		}
	}
	return out
}

func (p Plugin) inventory(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.InventoryQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.InventoryQuery) operation.Result {
		inventory, rebuilt, err := manager.Inventory(ctx, req)
		if err != nil {
			return operation.Failed("project_inventory_failed", err.Error(), nil)
		}
		lines := []string{fmt.Sprintf("Projects: %d", len(inventory.Projects))}
		for _, project := range inventory.Projects {
			lines = append(lines, fmt.Sprintf("- %s [%s] (%s): %s", displayRoot(project.Root), project.ID, project.Kind, project.Name))
			for _, facet := range project.Facets {
				lines = append(lines, fmt.Sprintf("  - %s %s", facet.Kind, facet.Manifest.Path))
			}
		}
		data := map[string]any{"inventory": compactInventory(inventory), "rebuilt": rebuilt}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: data})
	}
}

type inventorySummary struct {
	WorkspaceID coreworkspace.ID   `json:"workspace_id,omitempty"`
	Root        string             `json:"root,omitempty"`
	Projects    []projectSummary   `json:"projects,omitempty"`
	Hints       []coreproject.Hint `json:"hints,omitempty"`
	Truncated   bool               `json:"truncated,omitempty"`
}

func compactInventory(inventory coreproject.Inventory) inventorySummary {
	out := inventorySummary{WorkspaceID: inventory.WorkspaceID, Root: inventory.Root, Hints: inventory.Hints, Truncated: inventory.Truncated}
	for _, project := range inventory.Projects {
		out.Projects = append(out.Projects, compactProject(project))
	}
	return out
}

func (p Plugin) files(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.FilesQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.FilesQuery) operation.Result {
		project, rebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Path: req.Path, Refresh: req.Refresh})
		if err != nil {
			return operation.Failed("project_files_failed", err.Error(), nil)
		}
		root := project.Root
		if root == "" {
			root = "."
		}
		depth := req.Depth
		if depth <= 0 {
			depth = 4
		}
		max := req.MaxResults
		if max <= 0 || max > defaultMaxFiles {
			max = defaultMaxFiles
		}
		resolved, err := p.system.Workspace().ResolveExisting(ctx, root)
		if err != nil {
			return operation.Failed("project_files_failed", err.Error(), nil)
		}
		fsys, err := runtimeworkspace.FileSystem(p.system.Workspace())
		if err != nil {
			return operation.Failed("project_files_failed", err.Error(), nil)
		}
		entries, truncated, err := fpsystem.Walk(ctx, fsys, runtimeworkspace.PathName(resolved), fpsystem.WalkOptions{Depth: depth, ShowHidden: true, MaxEntries: max, SkipDirs: noisyDirs()})
		if err != nil {
			return operation.Failed("project_files_failed", err.Error(), nil)
		}
		files := make([]coreproject.FileRef, 0, len(entries))
		lines := []string{fmt.Sprintf("Project files: %s", displayRoot(project.Root))}
		for _, entry := range entries {
			if entry.Kind == "dir" {
				continue
			}
			if req.FacetKind != "" && !fileMatchesFacet(entry.Path, req.FacetKind) {
				continue
			}
			files = append(files, coreproject.FileRef{Path: entry.Path, Kind: entry.Kind, Size: entry.Size})
			lines = append(lines, "- "+entry.Path)
		}
		data := map[string]any{"project": compactProject(project), "files": files, "rebuilt": rebuilt, "truncated": truncated}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: data})
	}
}

func (p Plugin) tasks(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.TasksQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.TasksQuery) operation.Result {
		project, rebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Path: req.Path, Refresh: req.Refresh})
		if err != nil {
			return operation.Failed("project_tasks_failed", err.Error(), nil)
		}
		var tasks []coreproject.Task
		for _, facet := range project.Facets {
			for _, task := range facet.Tasks {
				if req.Kind == "" || req.Kind == task.Kind {
					tasks = append(tasks, task)
				}
			}
		}
		sort.SliceStable(tasks, func(i, j int) bool {
			if tasks[i].Kind == tasks[j].Kind {
				return tasks[i].Name < tasks[j].Name
			}
			return tasks[i].Kind < tasks[j].Kind
		})
		lines := []string{fmt.Sprintf("Project tasks: %s", displayRoot(project.Root))}
		for _, task := range tasks {
			lines = append(lines, fmt.Sprintf("- %s (%s): %s", task.Name, task.Kind, task.Command))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"project": compactProject(project), "tasks": tasks, "rebuilt": rebuilt}})
	}
}

func (p Plugin) taskRun(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.TaskRunRequest, coreproject.TaskRunResult] {
	return func(ctx operation.Context, req coreproject.TaskRunRequest) operation.Result {
		selection, _, err := manager.ResolveTaskRun(ctx, req)
		if err != nil {
			return operation.Failed("project_task_run_failed", err.Error(), nil)
		}
		result := taskRunResult(selection, req)
		if req.DryRun {
			result.DryRun = true
			return operation.OK(result)
		}
		if p.system == nil || p.system.Process() == nil {
			result.Diagnostics = append(result.Diagnostics, coreproject.Warning{Code: "process_unavailable", Message: "project task execution requires a process manager"})
			return taskRunFailed("process manager is unavailable", result)
		}
		processReq := fpsystem.ProcessRequest{
			Command:   selection.Executable,
			Args:      selection.Args,
			Workdir:   selection.Workdir,
			Env:       system.DefaultProcessEnv(),
			Timeout:   taskTimeout(req.TimeoutMS),
			MaxStdout: int64(taskOutputBytes(req.MaxOutputBytes)),
			MaxStderr: int64(taskOutputBytes(req.MaxOutputBytes)),
		}
		handle, err := p.system.Process().Start(ctx, processReq)
		if err != nil {
			return taskRunFailed(err.Error(), result)
		}
		eventCtx, cancelEvents := context.WithCancel(ctx)
		events := handle.Subscribe(eventCtx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for event := range events {
				ctx.Events().Emit(event)
			}
		}()
		processResult, waitErr := handle.Wait(ctx)
		cancelEvents()
		<-done
		emitTaskProcessUsage(ctx, processResult)
		result.Executable = processResult.Command
		result.Args = processResult.Args
		result.Workdir = processResult.Workdir
		result.Stdout = processResult.Stdout
		result.Stderr = processResult.Stderr
		result.ExitCode = processResult.ExitCode
		result.TimedOut = processResult.TimedOut
		result.StdoutTruncated = processResult.StdoutTruncated
		result.StderrTruncated = processResult.StderrTruncated
		result.DurationMS = processResult.Duration.Milliseconds()
		if waitErr != nil {
			return taskRunFailed(waitErr.Error(), result)
		}
		return operation.OK(result)
	}
}

func taskRunFailed(message string, result coreproject.TaskRunResult) operation.Result {
	return operation.Result{
		Status: operation.StatusFailed,
		Output: result,
		Error:  &operation.Error{Code: "project_task_run_failed", Message: message},
	}
}

func (p Plugin) taskRunIntent(manager *runtimeproject.Manager) operationruntime.TypedIntentHandler[coreproject.TaskRunRequest] {
	return func(ctx operation.Context, req coreproject.TaskRunRequest) (operation.IntentSet, error) {
		selection, _, err := manager.ResolveTaskRun(ctx, req)
		if err != nil {
			return operation.IntentSet{}, err
		}
		return operation.IntentSet{Operations: []operation.IntentOperation{processIntent(selection.Executable, selection.Args, selection.Workdir)}}, nil
	}
}

func taskRunResult(selection runtimeproject.TaskRunSelection, req coreproject.TaskRunRequest) coreproject.TaskRunResult {
	return coreproject.TaskRunResult{
		WorkspaceID: selection.Project.WorkspaceID,
		ProjectID:   selection.Project.ID,
		ProjectRoot: selection.Project.Root,
		Task:        selection.Task,
		Executable:  selection.Executable,
		Args:        append([]string(nil), selection.Args...),
		Workdir:     selection.Workdir,
		DryRun:      req.DryRun,
	}
}

func taskTimeout(timeoutMS int) time.Duration {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultTaskTimeout
	}
	if timeout > maxTaskTimeout {
		timeout = maxTaskTimeout
	}
	return timeout
}

func taskOutputBytes(max int) int {
	if max <= 0 || max > defaultTaskOutputBytes {
		return defaultTaskOutputBytes
	}
	return max
}

func processIntent(command string, args []string, workdir string) operation.IntentOperation {
	arguments := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		arguments = append(arguments, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command(command), Args: arguments, Workdir: operation.Workdir(workdir)},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}
}

func emitTaskProcessUsage(ctx operation.Context, result fpsystem.ProcessResult) {
	ctx.Events().Emit(usage.Recorded{
		Source: TaskRunOp,
		Subject: usage.Subject{
			Kind: usage.SubjectProcess,
			Name: result.Command,
		},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricWallTime, Quantity: float64(result.Duration.Milliseconds()), Unit: usage.UnitMillisecond},
			{Metric: usage.MetricFileBytes, Quantity: float64(len(result.Stdout)), Unit: usage.UnitByte, Direction: usage.DirectionOutput, Dimensions: map[string]string{"stream": "stdout"}},
			{Metric: usage.MetricFileBytes, Quantity: float64(len(result.Stderr)), Unit: usage.UnitByte, Direction: usage.DirectionOutput, Dimensions: map[string]string{"stream": "stderr"}},
		},
	})
}

func (p Plugin) docs(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.DocsQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.DocsQuery) operation.Result {
		project, rebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Path: req.Path, Refresh: req.Refresh})
		if err != nil {
			return operation.Failed("project_docs_failed", err.Error(), nil)
		}
		max := req.MaxResults
		if max <= 0 || max > 100 {
			max = 100
		}
		filterPath := cleanRel(req.Path)
		if filterPath == cleanRel(project.Root) {
			filterPath = ""
		}
		var docs []coreproject.DocumentOutline
		for _, facet := range project.Facets {
			for _, doc := range facet.Documents {
				if filterPath != "" && doc.Path != filterPath && !strings.HasPrefix(doc.Path, strings.TrimSuffix(filterPath, "/")+"/") {
					continue
				}
				docs = append(docs, doc)
				if len(docs) >= max {
					break
				}
			}
			if len(docs) >= max {
				break
			}
		}
		lines := []string{fmt.Sprintf("Project docs: %s", displayRoot(project.Root))}
		for _, doc := range docs {
			lines = append(lines, "- "+doc.Path)
			lines = append(lines, renderHeadings(doc.Headings, 1, 20)...)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"project": compactProject(project), "documents": compactDocuments(docs), "rebuilt": rebuilt}})
	}
}

type projectSummary struct {
	WorkspaceID coreworkspace.ID `json:"workspace_id,omitempty"`
	ID          coreproject.ID   `json:"id"`
	Root        string           `json:"root,omitempty"`
	Name        string           `json:"name,omitempty"`
	Kind        string           `json:"kind,omitempty"`
	Facets      []facetSummary   `json:"facets,omitempty"`
}

type facetSummary struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
}

func compactProject(project coreproject.Project) projectSummary {
	out := projectSummary{WorkspaceID: project.WorkspaceID, ID: project.ID, Root: project.Root, Name: project.Name, Kind: project.Kind}
	for _, facet := range project.Facets {
		out.Facets = append(out.Facets, facetSummary{Kind: string(facet.Kind), Path: facet.Manifest.Path})
	}
	return out
}

func compactDocuments(docs []coreproject.DocumentOutline) []coreproject.DocumentOutline {
	out := make([]coreproject.DocumentOutline, 0, len(docs))
	for _, doc := range docs {
		doc.Headings = boundedHeadingTree(doc.Headings, 20)
		out = append(out, doc)
	}
	return out
}

func renderHeadings(headings []coreproject.Heading, depth, limit int) []string {
	var lines []string
	var walk func([]coreproject.Heading, int)
	walk = func(items []coreproject.Heading, currentDepth int) {
		for _, heading := range items {
			if limit > 0 && len(lines) >= limit {
				return
			}
			lines = append(lines, fmt.Sprintf("%s%s %s", strings.Repeat("  ", currentDepth), strings.Repeat("#", heading.Level), heading.Title))
			walk(heading.Children, currentDepth+1)
		}
	}
	walk(headings, depth)
	return lines
}

func boundedHeadingTree(headings []coreproject.Heading, limit int) []coreproject.Heading {
	if limit <= 0 {
		return headings
	}
	remaining := limit
	var copyTree func([]coreproject.Heading) []coreproject.Heading
	copyTree = func(items []coreproject.Heading) []coreproject.Heading {
		out := make([]coreproject.Heading, 0, len(items))
		for _, heading := range items {
			if remaining <= 0 {
				break
			}
			remaining--
			heading.Children = copyTree(heading.Children)
			out = append(out, heading)
		}
		return out
	}
	return copyTree(headings)
}

func fileMatchesFacet(rel, facet string) bool {
	switch facet {
	case string(coreproject.FacetGoModule), string(coreproject.FacetGoWorkspace):
		return strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "go.mod") || strings.HasSuffix(rel, "go.work")
	case string(coreproject.FacetNodePackage):
		return strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, "package.json")
	case string(coreproject.FacetMarkdownDocs):
		return strings.HasSuffix(strings.ToLower(rel), ".md")
	case string(coreproject.FacetDockerfile):
		return dockerfileName(path.Base(rel))
	case string(coreproject.FacetDockerCompose):
		return path.Base(rel) == "docker-compose.yaml"
	case string(coreproject.FacetAgentsDir):
		return rel == ".agents" || strings.HasPrefix(rel, ".agents/")
	case string(coreproject.FacetClaudeDir):
		return rel == ".claude" || strings.HasPrefix(rel, ".claude/")
	default:
		return true
	}
}

func dockerfileName(name string) bool {
	return name == "Dockerfile" || strings.HasSuffix(name, ".Dockerfile")
}

func displayRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

func cleanRel(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || raw == "." {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(raw, "./"), "/")
}

func noisyDirs() []string {
	return []string{".git", ".cache", "node_modules", "vendor", "dist", "build", "target", "tmp"}
}
