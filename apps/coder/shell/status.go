package codershell

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	runtimeevidence "github.com/fluxplane/agentruntime/runtime/evidence"
	projectruntime "github.com/fluxplane/agentruntime/runtime/project"
	"github.com/fluxplane/agentruntime/runtime/system"
)

type shellStatus struct {
	cwd          string
	workspace    string
	projectName  string
	projectKind  string
	goModule     string
	goVersion    string
	locale       string
	user         string
	provider     string
	model        string
	loading      bool
	facets       []string
	taskCount    int
	projectCount int
	warningCount int
}

type shellStatusLoadedMsg struct {
	status shellStatus
	err    error
}

func initialStatus(sys system.System, path string, opts Options) shellStatus {
	status := shellStatus{
		cwd:      strings.TrimSpace(path),
		provider: strings.TrimSpace(opts.Provider),
		model:    strings.TrimSpace(opts.Model),
		loading:  true,
	}
	if sys != nil && sys.Workspace() != nil {
		status.workspace = sys.Workspace().Root()
		if status.cwd == "" {
			status.cwd = status.workspace
		}
	}
	if status.cwd == "" {
		status.cwd = "."
	}
	return status
}

func loadStatusCmd(ctx context.Context, sys system.System, base shellStatus) tea.Cmd {
	return func() tea.Msg {
		status := loadStatus(ctx, sys)
		if status.cwd == "" {
			status.cwd = base.cwd
		}
		if status.workspace == "" {
			status.workspace = base.workspace
		}
		status.provider = base.provider
		status.model = base.model
		status.loading = false
		return shellStatusLoadedMsg{status: status}
	}
}

func loadStatus(ctx context.Context, sys system.System) shellStatus {
	status := shellStatus{}
	if sys == nil || sys.Workspace() == nil {
		return status
	}
	workspace := sys.Workspace()
	status.workspace = workspace.Root()
	status.cwd = workspace.Root()

	manager := projectruntime.NewManager(workspace)
	inventory, _, err := manager.Inventory(ctx, coreproject.InventoryQuery{MaxResults: 25, MaxBytes: 64 * 1024})
	if err == nil {
		status.projectCount = len(inventory.Projects)
		status.warningCount = len(inventory.Warnings)
		if len(inventory.Projects) > 0 {
			project := chooseStatusProject(inventory.Projects, workspace.Root())
			status.projectName = project.Name
			status.projectKind = project.Kind
			status.facets = compactFacetNames(project.Facets)
			status.taskCount = countTasks(project.Facets)
			status.goModule = goModuleName(project.Facets)
			status.warningCount += len(project.Warnings)
		}
	}
	status.applyBaselineObservations(ctx)
	status.goVersion = detectGoVersion(ctx, sys)
	return status
}

func (s *shellStatus) applyBaselineObservations(ctx context.Context) {
	if s == nil {
		return
	}
	observations, err := runtimeevidence.BaselineObserver().Observe(ctx, runtimeevidence.ObservationRequest{
		Phase: coreevidence.PhaseTurn,
	})
	if err != nil {
		return
	}
	for _, observation := range observations {
		content, ok := observation.Content.(map[string]any)
		if !ok {
			continue
		}
		switch observation.Kind {
		case runtimeevidence.ObservationSystemLocale:
			s.locale = firstStringValue(content, "LC_ALL", "LC_CTYPE", "LANG")
		case runtimeevidence.ObservationSystemUser:
			s.user = firstStringValue(content, "username")
		}
	}
}

func firstStringValue(content map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := content[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(typed.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func chooseStatusProject(projects []coreproject.Project, root string) coreproject.Project {
	if len(projects) == 0 {
		return coreproject.Project{}
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		cleanRoot = root
	}
	for _, project := range projects {
		projectRoot := project.Root
		abs, err := filepath.Abs(projectRoot)
		if err == nil {
			projectRoot = abs
		}
		if projectRoot == cleanRoot || project.Root == "." {
			return project
		}
	}
	return projects[0]
}

func compactFacetNames(facets []coreproject.Facet) []string {
	labels := make([]string, 0, len(facets))
	for _, facet := range facets {
		switch facet.Kind {
		case coreproject.FacetGoModule:
			labels = append(labels, "go.mod")
		case coreproject.FacetGoWorkspace:
			labels = append(labels, "go.work")
		case coreproject.FacetGitRepo:
			labels = append(labels, "git")
		case coreproject.FacetTaskfile:
			labels = append(labels, "task")
		case coreproject.FacetMakefile:
			labels = append(labels, "make")
		case coreproject.FacetMarkdownDocs:
			labels = append(labels, "docs")
		case coreproject.FacetAgentsDir:
			labels = append(labels, "agents")
		case coreproject.FacetClaudeDir:
			labels = append(labels, "claude")
		case coreproject.FacetNodePackage:
			labels = append(labels, "node")
		default:
			labels = append(labels, strings.TrimSpace(string(facet.Kind)))
		}
	}
	labels = uniqueStrings(labels)
	if len(labels) > 8 {
		labels = append(labels[:8], fmt.Sprintf("+%d", len(labels)-8))
	}
	return labels
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func countTasks(facets []coreproject.Facet) int {
	total := 0
	for _, facet := range facets {
		total += len(facet.Tasks)
	}
	return total
}

func goModuleName(facets []coreproject.Facet) string {
	for _, facet := range facets {
		if facet.Kind != coreproject.FacetGoModule {
			continue
		}
		if module := strings.TrimSpace(facet.Summary["module"]); module != "" {
			return module
		}
		if module := strings.TrimSpace(facet.Manifest.Summary["module"]); module != "" {
			return module
		}
		if name := strings.TrimSpace(facet.Name); name != "" {
			return name
		}
	}
	return ""
}

func detectGoVersion(ctx context.Context, sys system.System) string {
	if sys == nil || sys.Process() == nil {
		return ""
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := sys.Process().Run(runCtx, system.ProcessRequest{
		Command:   "go",
		Args:      []string{"version"},
		Timeout:   2 * time.Second,
		MaxStdout: 256,
		MaxStderr: 256,
	})
	if err != nil {
		return ""
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) >= 3 && fields[0] == "go" && fields[1] == "version" {
		return fields[2]
	}
	return strings.TrimSpace(result.Stdout)
}
