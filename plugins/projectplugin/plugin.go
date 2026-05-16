package projectplugin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimeproject "github.com/fluxplane/agentruntime/runtime/project"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name            = "project"
	InventoryOp     = "project_inventory"
	FilesOp         = "project_files"
	TasksOp         = "project_tasks"
	DocsOp          = "project_docs"
	SummaryProvider = "project.summary"
	defaultMaxFiles = 500
)

// Plugin contributes Workspace project inventory operations.
type Plugin struct {
	system  system.System
	manager *runtimeproject.Manager
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

// New returns a project inventory plugin.
func New(sys system.System) Plugin {
	var manager *runtimeproject.Manager
	if sys != nil && sys.Workspace() != nil {
		manager = runtimeproject.NewManager(sys.Workspace())
	}
	return Plugin{system: sys, manager: manager}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Workspace project inventory operations."}
}

// Contributions returns project operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	return resource.ContributionBundle{
		ContextProviders: []corecontext.ProviderSpec{summaryContextSpec()},
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Workspace project inventory and outline operations.",
			Operations:  refs(specs),
		}},
		Operations: specs,
		EventTypes: []coreevent.Event{coreproject.SignalsObserved{}},
	}, nil
}

// ContextProviders returns executable project context providers.
func (p Plugin) ContextProviders(context.Context, pluginhost.Context) ([]corecontext.Provider, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, nil
	}
	manager := p.manager
	if manager == nil {
		manager = runtimeproject.NewManager(p.system.Workspace())
	}
	return []corecontext.Provider{summaryProvider{manager: manager}}, nil
}

// Operations returns executable project operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, fmt.Errorf("projectplugin: system workspace is nil")
	}
	manager := p.manager
	if manager == nil {
		manager = runtimeproject.NewManager(p.system.Workspace())
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[coreproject.InventoryQuery, operation.Rendered](specByName(InventoryOp), p.inventory(manager)),
		operationruntime.NewTypedResult[coreproject.FilesQuery, operation.Rendered](specByName(FilesOp), p.files(manager)),
		operationruntime.NewTypedResult[coreproject.TasksQuery, operation.Rendered](specByName(TasksOp), p.tasks(manager)),
		operationruntime.NewTypedResult[coreproject.DocsQuery, operation.Rendered](specByName(DocsOp), p.docs(manager)),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec[coreproject.InventoryQuery](InventoryOp, "Discover Workspace projects and facets such as go.mod, go.work, package.json, Makefile, Taskfile.yaml, and markdown docs. The inventory is memory-only; refresh rebuilds it for this plugin instance."),
		spec[coreproject.FilesQuery](FilesOp, "List a bounded project file tree scoped to a detected project or path. This is read-only and uses Workspace-relative paths."),
		spec[coreproject.TasksQuery](TasksOp, "List cheap project task entry points discovered from Makefiles, Taskfiles, and package.json scripts."),
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
		if rebuilt || req.Refresh {
			ctx.Events().Emit(coreproject.SignalsObserved{
				WorkspaceRoot: p.system.Workspace().Root(),
				Scope:         ".",
				Signals:       inventory.Signals,
				Truncated:     inventory.Truncated,
			})
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
	Root      string               `json:"root,omitempty"`
	Projects  []projectSummary     `json:"projects,omitempty"`
	Signals   []coreproject.Signal `json:"signals,omitempty"`
	Truncated bool                 `json:"truncated,omitempty"`
}

func compactInventory(inventory coreproject.Inventory) inventorySummary {
	out := inventorySummary{Root: inventory.Root, Signals: inventory.Signals, Truncated: inventory.Truncated}
	for _, project := range inventory.Projects {
		out.Projects = append(out.Projects, compactProject(project))
	}
	return out
}

func (p Plugin) files(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.FilesQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.FilesQuery) operation.Result {
		project, rebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{ProjectID: req.ProjectID, Path: req.Path, Refresh: req.Refresh})
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
		entries, _, truncated, err := p.system.Workspace().Walk(ctx, root, system.WalkOptions{Depth: depth, ShowHidden: true, MaxEntries: max, SkipDirs: noisyDirs()})
		if err != nil {
			return operation.Failed("project_files_failed", err.Error(), nil)
		}
		files := make([]coreproject.FileRef, 0, len(entries))
		lines := []string{fmt.Sprintf("Project files: %s", displayRoot(project.Root))}
		for _, entry := range entries {
			if entry.Kind == "dir" {
				continue
			}
			if req.FacetKind != "" && !fileMatchesFacet(entry.Path.Rel, req.FacetKind) {
				continue
			}
			files = append(files, coreproject.FileRef{Path: entry.Path.Rel, Kind: entry.Kind, Size: entry.Size})
			lines = append(lines, "- "+entry.Path.Rel)
		}
		data := map[string]any{"project": compactProject(project), "files": files, "rebuilt": rebuilt, "truncated": truncated}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: data})
	}
}

func (p Plugin) tasks(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.TasksQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.TasksQuery) operation.Result {
		project, rebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{ProjectID: req.ProjectID, Path: req.Path, Refresh: req.Refresh})
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

func (p Plugin) docs(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[coreproject.DocsQuery, operation.Rendered] {
	return func(ctx operation.Context, req coreproject.DocsQuery) operation.Result {
		project, rebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{ProjectID: req.ProjectID, Path: req.Path, Refresh: req.Refresh})
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
	ID     coreproject.ID `json:"id"`
	Root   string         `json:"root,omitempty"`
	Name   string         `json:"name,omitempty"`
	Kind   string         `json:"kind,omitempty"`
	Facets []facetSummary `json:"facets,omitempty"`
}

type facetSummary struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
}

func compactProject(project coreproject.Project) projectSummary {
	out := projectSummary{ID: project.ID, Root: project.Root, Name: project.Name, Kind: project.Kind}
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
	case string(coreproject.FacetAgentsDir):
		return rel == ".agents" || strings.HasPrefix(rel, ".agents/")
	case string(coreproject.FacetClaudeDir):
		return rel == ".claude" || strings.HasPrefix(rel, ".claude/")
	default:
		return true
	}
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
