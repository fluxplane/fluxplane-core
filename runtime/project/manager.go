// Package project contains Workspace-backed project inventory helpers.
package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	coreproject "github.com/fluxplane/fluxplane-core/core/project"
	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/yuin/goldmark"
	goldast "github.com/yuin/goldmark/ast"
	goldtext "github.com/yuin/goldmark/text"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

const (
	defaultMaxEntries = 10000
	defaultMaxBytes   = 128 * 1024
)

// Manager keeps a process-local, memory-only project inventory.
type Manager struct {
	workspace   runtimeworkspace.Workspace
	workspaceID coreworkspace.ID
	mu          sync.Mutex
	inventory   coreproject.Inventory
	built       bool
}

// NewManager returns a Workspace-backed project inventory manager.
func NewManager(workspace runtimeworkspace.Workspace) *Manager {
	return &Manager{workspace: workspace}
}

// NewManagerForWorkspace returns a Workspace-backed project inventory manager
// associated with a resolved core workspace id.
func NewManagerForWorkspace(workspace runtimeworkspace.Workspace, workspaceID coreworkspace.ID) *Manager {
	return &Manager{workspace: workspace, workspaceID: workspaceID}
}

// Inventory returns a detected project inventory, rebuilding when requested.
func (m *Manager) Inventory(ctx context.Context, req coreproject.InventoryQuery) (coreproject.Inventory, bool, error) {
	if m == nil || m.workspace == nil {
		return coreproject.Inventory{}, false, fmt.Errorf("project: workspace is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.WorkspaceID != "" && m.workspaceID == "" {
		return coreproject.Inventory{}, false, fmt.Errorf("project: workspace %q was requested from an unscoped project manager", req.WorkspaceID)
	}
	if req.WorkspaceID != "" && req.WorkspaceID != m.workspaceID {
		return coreproject.Inventory{}, false, fmt.Errorf("project: workspace %q is not managed by this project manager", req.WorkspaceID)
	}
	if m.built && !req.Refresh {
		return m.inventory, false, nil
	}
	inventory, err := scan(ctx, m.workspace, req, m.workspaceID)
	if err != nil {
		return coreproject.Inventory{}, false, err
	}
	m.inventory = inventory
	m.built = true
	return inventory, true, nil
}

// Project selects a project by id or nearest path.
func (m *Manager) Project(ctx context.Context, req coreproject.ProjectQuery) (coreproject.Project, bool, error) {
	if m == nil || m.workspace == nil {
		return coreproject.Project{}, false, fmt.Errorf("project: workspace is nil")
	}
	if req.WorkspaceID != "" && m.workspaceID == "" {
		return coreproject.Project{}, false, fmt.Errorf("project: workspace %q was requested from an unscoped project manager", req.WorkspaceID)
	}
	if req.WorkspaceID != "" && req.WorkspaceID != m.workspaceID {
		return coreproject.Project{}, false, fmt.Errorf("project: workspace %q is not managed by this project manager", req.WorkspaceID)
	}
	inv, rebuilt, err := m.Inventory(ctx, coreproject.InventoryQuery{WorkspaceID: req.WorkspaceID, Refresh: req.Refresh})
	if err != nil {
		return coreproject.Project{}, rebuilt, err
	}
	if req.ProjectID != "" {
		projectID := normalizeProjectID(req.ProjectID)
		for _, project := range inv.Projects {
			if project.ID == projectID {
				return project, rebuilt, nil
			}
		}
		return coreproject.Project{}, rebuilt, fs.ErrNotExist
	}
	if strings.TrimSpace(req.Path) == "" {
		if len(inv.Projects) == 1 {
			return inv.Projects[0], rebuilt, nil
		}
		return coreproject.Project{}, rebuilt, fs.ErrNotExist
	}
	project, ok := nearestProject(inv.Projects, cleanRel(req.Path))
	if !ok {
		return coreproject.Project{}, rebuilt, fs.ErrNotExist
	}
	return project, rebuilt, nil
}

// TaskRunSelection records a selected task and the direct process invocation
// needed to run it.
type TaskRunSelection struct {
	Project    coreproject.Project
	Task       coreproject.Task
	Executable string
	Args       []string
	Workdir    string
}

// ResolveTaskRun selects a discovered task and prepares a no-shell process
// invocation for it.
func (m *Manager) ResolveTaskRun(ctx context.Context, req coreproject.TaskRunRequest) (TaskRunSelection, bool, error) {
	project, rebuilt, err := m.Project(ctx, coreproject.ProjectQuery{WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Path: req.Path})
	if err != nil {
		return TaskRunSelection{}, rebuilt, err
	}
	task, err := selectTask(project, req)
	if err != nil {
		return TaskRunSelection{}, rebuilt, err
	}
	executable, args, err := taskCommand(task, req.Args)
	if err != nil {
		return TaskRunSelection{}, rebuilt, err
	}
	return TaskRunSelection{
		Project:    project,
		Task:       task,
		Executable: executable,
		Args:       args,
		Workdir:    task.Workdir,
	}, rebuilt, nil
}

func scan(ctx context.Context, ws runtimeworkspace.Workspace, req coreproject.InventoryQuery, workspaceID coreworkspace.ID) (coreproject.Inventory, error) {
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > defaultMaxBytes {
		maxBytes = defaultMaxBytes
	}
	entries, truncated, err := walkWorkspace(ctx, ws, ".", fpsystem.WalkOptions{
		Depth:      50,
		ShowHidden: true,
		MaxEntries: defaultMaxEntries,
		SkipDirs:   noisyDirs(),
	})
	if err != nil {
		return coreproject.Inventory{}, err
	}
	entries = filterNamedRootEntries(entries)
	roots := ws.Roots()
	seenRoots := map[string]struct{}{}
	rootKey, err := workspaceRootKey(ctx, ws, ".")
	if err != nil {
		return coreproject.Inventory{}, err
	}
	if rootKey != "" {
		seenRoots[rootKey] = struct{}{}
	}
	for _, root := range roots[1:] {
		if strings.TrimSpace(root.Rel) == "" || !root.Read {
			continue
		}
		if root.Scratch {
			continue
		}
		rootKey, err := workspaceRootKey(ctx, ws, root.Rel)
		if err != nil {
			return coreproject.Inventory{}, err
		}
		if rootKey != "" {
			if _, ok := seenRoots[rootKey]; ok {
				continue
			}
			seenRoots[rootKey] = struct{}{}
		}
		rootEntries, rootTruncated, err := walkWorkspace(ctx, ws, root.Rel, fpsystem.WalkOptions{
			Depth:      50,
			ShowHidden: true,
			MaxEntries: defaultMaxEntries,
			SkipDirs:   noisyDirs(),
		})
		if err != nil {
			return coreproject.Inventory{}, err
		}
		entries = append(entries, rootEntries...)
		truncated = truncated || rootTruncated
	}
	builders := map[string]*projectBuilder{}
	var markdown []markdownFacet
	var warnings []coreproject.Warning
	probeRoot(ctx, ws, builders, maxBytes)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return coreproject.Inventory{}, err
		}
		rel := cleanRel(entry.Path)
		if rel == "" {
			continue
		}
		name := path.Base(rel)
		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		if entry.Kind == "dir" {
			switch name {
			case ".agents":
				addResourceDir(builders, dir, rel, coreproject.FacetAgentsDir, "agents_dir")
			case ".claude":
				addResourceDir(builders, dir, rel, coreproject.FacetClaudeDir, "claude_dir")
				addAIConfig(builders, dir, rel, "claude", "bundle", "")
			case ".git":
				builderFor(builders, dir).addFacet(coreproject.Facet{Kind: coreproject.FacetGitRepo, Manifest: manifest(rel, "git_repo", coreproject.ParseStatusParsed, nil, "")})
			}
			continue
		}
		switch {
		case name == "go.mod":
			addGoMod(ctx, ws, builders, dir, rel, maxBytes)
		case name == "go.work":
			addGoWork(ctx, ws, builders, dir, rel, maxBytes)
		case name == "package.json":
			addPackageJSON(ctx, ws, builders, dir, rel, maxBytes)
		case name == "package-lock.json" || name == "pnpm-lock.yaml" || name == "yarn.lock":
			addSimpleFacet(builders, dir, rel, coreproject.FacetNodeLockfile, name)
		case name == "Cargo.toml":
			addSimpleFacet(builders, dir, rel, coreproject.FacetCargoManifest, "cargo.toml")
		case name == "Cargo.lock":
			addSimpleFacet(builders, dir, rel, coreproject.FacetCargoLockfile, "cargo.lock")
		case isAppManifestName(name):
			addAppManifest(ctx, ws, builders, dir, rel, maxBytes)
		case isCoderConfigName(name):
			addCoderConfig(ctx, ws, builders, dir, rel, maxBytes)
		case name == "Taskfile.yaml" || name == "Taskfile.yml":
			addTaskfile(ctx, ws, builders, dir, rel, maxBytes)
		case name == "Makefile" || name == "makefile":
			addMakefile(ctx, ws, builders, dir, rel, maxBytes)
		case isDockerfileName(name):
			addSimpleFacet(builders, dir, rel, coreproject.FacetDockerfile, "dockerfile")
		case name == "docker-compose.yaml":
			addSimpleFacet(builders, dir, rel, coreproject.FacetDockerCompose, "docker_compose")
		case name == ".git" || strings.HasSuffix(rel, "/.git"):
			builderFor(builders, dir).addFacet(coreproject.Facet{Kind: coreproject.FacetGitRepo, Manifest: manifest(rel, "git_repo", coreproject.ParseStatusParsed, nil, "")})
		case strings.HasPrefix(rel, ".github/workflows/"):
			builderFor(builders, ".").addFacet(coreproject.Facet{Kind: coreproject.FacetCI, Manifest: manifest(rel, "github_workflow", coreproject.ParseStatusUnsupported, nil, "")})
		case isInstructionAIConfig(name):
			addInstructionAIConfig(builders, dir, rel, name)
			markdown = append(markdown, readMarkdown(ctx, ws, dir, rel, maxBytes))
		case isClaudeAgentConfig(rel):
			addAIConfig(builders, aiConfigProjectRoot(rel), rel, "claude", "agent", parentAIConfig(rel))
			markdown = append(markdown, readMarkdown(ctx, ws, dir, rel, maxBytes))
		case strings.HasSuffix(strings.ToLower(name), ".md"):
			markdown = append(markdown, readMarkdown(ctx, ws, dir, rel, maxBytes))
		}
	}
	attachMarkdown(builders, markdown)
	projects := finalize(builders, workspaceID)
	setParents(projects)
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Root < projects[j].Root })
	limit := req.MaxResults
	if limit > 0 && len(projects) > limit {
		projects = projects[:limit]
		truncated = true
	}
	hints := attachHints(projects, workspaceID)
	return coreproject.Inventory{
		WorkspaceID: workspaceID,
		Root:        ".",
		Projects:    projects,
		Hints:       hints,
		Truncated:   truncated,
		Summary: coreproject.Summary{
			ProjectCount: len(projects),
			FacetCounts:  facetCounts(projects),
		},
		Warnings: warnings,
	}, nil
}

func filterNamedRootEntries(entries []fpsystem.WalkEntry) []fpsystem.WalkEntry {
	if len(entries) == 0 {
		return nil
	}
	out := entries[:0]
	for _, entry := range entries {
		if strings.HasPrefix(entry.Path, "@") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func workspaceRootKey(ctx context.Context, ws runtimeworkspace.Workspace, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		rel = "."
	}
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return "", err
	}
	if resolved.Abs != "" {
		return resolved.Abs, nil
	}
	return resolved.Rel, nil
}

func workspaceName(resolved runtimeworkspace.ResolvedPath) string {
	if strings.TrimSpace(resolved.Rel) == "" {
		return "."
	}
	return resolved.Rel
}

func workspaceFileSystem(ws runtimeworkspace.Workspace) (fpsystem.FileSystem, error) {
	if ws == nil || ws.System() == nil || ws.System().FileSystem() == nil {
		return nil, fmt.Errorf("project: workspace filesystem is nil")
	}
	return ws.System().FileSystem(), nil
}

func statWorkspacePath(ctx context.Context, ws runtimeworkspace.Workspace, rel string) (fs.FileInfo, runtimeworkspace.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	fsys, err := workspaceFileSystem(ws)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	info, err := fsys.Stat(workspaceName(resolved))
	return info, resolved, err
}

func readWorkspaceFile(ctx context.Context, ws runtimeworkspace.Workspace, rel string, maxBytes int64) ([]byte, bool, runtimeworkspace.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	fsys, err := workspaceFileSystem(ws)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, workspaceName(resolved), maxBytes)
	return data, truncated, resolved, err
}

func walkWorkspace(ctx context.Context, ws runtimeworkspace.Workspace, rel string, opts fpsystem.WalkOptions) ([]fpsystem.WalkEntry, bool, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, false, err
	}
	fsys, err := workspaceFileSystem(ws)
	if err != nil {
		return nil, false, err
	}
	return fpsystem.Walk(ctx, fsys, workspaceName(resolved), opts)
}

type projectBuilder struct {
	root   string
	facets []coreproject.Facet
}

func builderFor(builders map[string]*projectBuilder, root string) *projectBuilder {
	root = cleanRel(root)
	builder := builders[root]
	if builder == nil {
		builder = &projectBuilder{root: root}
		builders[root] = builder
	}
	return builder
}

func (b *projectBuilder) addFacet(facet coreproject.Facet) {
	key := string(facet.Kind) + "\x00" + facet.Manifest.Path
	for _, existing := range b.facets {
		if string(existing.Kind)+"\x00"+existing.Manifest.Path == key {
			return
		}
	}
	b.facets = append(b.facets, facet)
}

func probeRoot(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, maxBytes int) {
	probes := []struct {
		path string
		add  func(context.Context, runtimeworkspace.Workspace, map[string]*projectBuilder, string, string, int)
	}{
		{path: "go.mod", add: addGoMod},
		{path: "go.work", add: addGoWork},
		{path: "package.json", add: addPackageJSON},
		{path: "Taskfile.yaml", add: addTaskfile},
		{path: "Taskfile.yml", add: addTaskfile},
		{path: "Makefile", add: addMakefile},
		{path: "makefile", add: addMakefile},
		{path: "Dockerfile", add: addDockerfile},
		{path: "docker-compose.yaml", add: addDockerCompose},
		{path: "fluxplane.yaml", add: addAppManifest},
		{path: ".coder.yaml", add: addCoderConfig},
		{path: ".coder.yml", add: addCoderConfig},
	}
	for _, probe := range probes {
		if _, _, err := statWorkspacePath(ctx, ws, probe.path); err == nil {
			probe.add(ctx, ws, builders, "", probe.path, maxBytes)
		}
	}
	if _, _, err := statWorkspacePath(ctx, ws, ".git"); err == nil {
		builderFor(builders, "").addFacet(coreproject.Facet{Kind: coreproject.FacetGitRepo, Manifest: manifest(".git", "git_repo", coreproject.ParseStatusParsed, nil, "")})
	}
	if _, _, err := statWorkspacePath(ctx, ws, ".agents"); err == nil {
		addResourceDir(builders, "", ".agents", coreproject.FacetAgentsDir, "agents_dir")
	}
	if _, _, err := statWorkspacePath(ctx, ws, ".claude"); err == nil {
		addResourceDir(builders, "", ".claude", coreproject.FacetClaudeDir, "claude_dir")
		addAIConfig(builders, "", ".claude", "claude", "bundle", "")
	}
	if _, _, err := statWorkspacePath(ctx, ws, ".github/workflows"); err == nil {
		builderFor(builders, "").addFacet(coreproject.Facet{Kind: coreproject.FacetCI, Manifest: manifest(".github/workflows", "github_workflow", coreproject.ParseStatusUnsupported, nil, "")})
	}
}

func addSimpleFacet(builders map[string]*projectBuilder, dir, rel string, kind coreproject.FacetKind, manifestKind string) {
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     kind,
		Manifest: manifest(rel, manifestKind, coreproject.ParseStatusParsed, nil, ""),
	})
}

func addResourceDir(builders map[string]*projectBuilder, dir, rel string, kind coreproject.FacetKind, manifestKind string) {
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     kind,
		Manifest: manifest(rel, manifestKind, coreproject.ParseStatusParsed, nil, ""),
	})
}

func addDockerfile(_ context.Context, _ runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, _ int) {
	addSimpleFacet(builders, dir, rel, coreproject.FacetDockerfile, "dockerfile")
}

func addDockerCompose(_ context.Context, _ runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, _ int) {
	addSimpleFacet(builders, dir, rel, coreproject.FacetDockerCompose, "docker_compose")
}

func addAppManifest(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, truncated, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	status := coreproject.ParseStatusParsed
	summary := map[string]string{}
	var msg string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		var raw struct {
			Kind string `yaml:"kind" json:"kind"`
			Name string `yaml:"name" json:"name"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			status, msg = coreproject.ParseStatusFailed, err.Error()
		} else {
			summary["kind"] = strings.TrimSpace(raw.Kind)
			summary["name"] = strings.TrimSpace(raw.Name)
			if truncated {
				summary["truncated"] = "true"
			}
		}
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetAppManifest,
		Name:     summary["name"],
		Manifest: manifest(rel, "fluxplane_app_manifest", status, summary, msg),
		Summary:  summary,
	})
}

func addCoderConfig(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, truncated, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	status := coreproject.ParseStatusParsed
	summary := map[string]string{}
	var msg string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		var raw struct {
			Version int `yaml:"version" json:"version"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			status, msg = coreproject.ParseStatusFailed, err.Error()
		} else {
			if raw.Version > 0 {
				summary["version"] = fmt.Sprint(raw.Version)
			}
			if truncated {
				summary["truncated"] = "true"
			}
		}
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetCoderConfig,
		Manifest: manifest(rel, "coder_config", status, summary, msg),
		Summary:  summary,
	})
}

func addInstructionAIConfig(builders map[string]*projectBuilder, dir, rel, name string) {
	switch name {
	case "AGENTS.md":
		addAIConfig(builders, dir, rel, "generic", "instruction", "")
	case "CLAUDE.md":
		addAIConfig(builders, dir, rel, "claude", "instruction", "")
	case "MEMORY.md":
		addAIConfig(builders, dir, rel, "generic", "context:memory", "")
	}
}

func addAIConfig(builders map[string]*projectBuilder, dir, rel, vendor, kind, parent string) {
	summary := map[string]string{"vendor": vendor, "kind": kind}
	if strings.TrimSpace(parent) != "" {
		summary["parent"] = cleanRel(parent)
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetAIConfig,
		Name:     aiConfigName(rel, kind),
		Manifest: manifest(rel, "ai_config", coreproject.ParseStatusParsed, summary, ""),
		Summary:  summary,
	})
}

func addGoMod(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, truncated, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	summary := map[string]string{}
	status := coreproject.ParseStatusParsed
	var msg string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else if file, err := modfile.Parse(rel, data, nil); err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		if file.Module != nil {
			summary["module"] = file.Module.Mod.Path
		}
		if file.Go != nil {
			summary["go"] = file.Go.Version
		}
		if truncated {
			summary["truncated"] = "true"
		}
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetGoModule,
		Name:     summary["module"],
		Manifest: manifest(rel, "go.mod", status, summary, msg),
		Summary:  summary,
	})
}

func addGoWork(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	summary := map[string]string{}
	status := coreproject.ParseStatusParsed
	var msg string
	var related []string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else if file, err := modfile.ParseWork(rel, data, nil); err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		if file.Go != nil {
			summary["go"] = file.Go.Version
		}
		for _, use := range file.Use {
			related = append(related, cleanRel(path.Join(dir, use.Path)))
		}
		sort.Strings(related)
		if len(related) > 0 {
			summary["modules"] = strings.Join(related, ",")
		}
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:        coreproject.FacetGoWorkspace,
		Manifest:    manifest(rel, "go.work", status, summary, msg),
		Summary:     summary,
		RelatedDirs: related,
	})
}

func addPackageJSON(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	summary := map[string]string{}
	var tasks []coreproject.Task
	status := coreproject.ParseStatusParsed
	var msg string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		var raw struct {
			Name    string            `json:"name"`
			Version string            `json:"version"`
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			status, msg = coreproject.ParseStatusFailed, err.Error()
		} else {
			summary["name"] = raw.Name
			summary["version"] = raw.Version
			keys := make([]string, 0, len(raw.Scripts))
			for key := range raw.Scripts {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				manager := nodePackageManager(ctx, ws, dir)
				args := []string{"run", key}
				tasks = append(tasks, coreproject.Task{
					ID:          taskID("package_script", rel, key),
					Name:        key,
					Kind:        "package_script",
					Command:     manager + " run " + key,
					Path:        rel,
					Workdir:     dir,
					Executable:  manager,
					Args:        args,
					Description: raw.Scripts[key],
					Metadata:    map[string]string{"runner": "package_script", "package_manager": manager},
				})
			}
		}
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetNodePackage,
		Name:     summary["name"],
		Manifest: manifest(rel, "package.json", status, summary, msg),
		Summary:  summary,
		Tasks:    tasks,
	})
}

func addTaskfile(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	status := coreproject.ParseStatusParsed
	var msg string
	var tasks []coreproject.Task
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		tasks, err = parseTaskfileTasks(data, rel)
		if err != nil {
			status, msg = coreproject.ParseStatusFailed, err.Error()
		}
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetTaskfile,
		Manifest: manifest(rel, "taskfile", status, map[string]string{"tasks": fmt.Sprint(len(tasks))}, msg),
		Tasks:    tasks,
	})
}

func addMakefile(ctx context.Context, ws runtimeworkspace.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	status := coreproject.ParseStatusParsed
	var msg string
	var tasks []coreproject.Task
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		tasks = parseMakeTargets(string(data), rel)
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetMakefile,
		Manifest: manifest(rel, "makefile", status, map[string]string{"tasks": fmt.Sprint(len(tasks))}, msg),
		Tasks:    tasks,
	})
}

type markdownFacet struct {
	dir   string
	facet coreproject.Facet
}

func readMarkdown(ctx context.Context, ws runtimeworkspace.Workspace, dir, rel string, maxBytes int) markdownFacet {
	data, truncated, _, err := readWorkspaceFile(ctx, ws, rel, int64(maxBytes))
	doc := coreproject.DocumentOutline{Path: rel, Truncated: truncated}
	status := coreproject.ParseStatusParsed
	var msg string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		doc.Headings = parseMarkdownOutline(data)
		if heading, ok := documentTitle(doc.Headings); ok {
			doc.Title = heading.Title
		}
	}
	return markdownFacet{dir: dir, facet: coreproject.Facet{
		Kind:      coreproject.FacetMarkdownDocs,
		Manifest:  manifest(rel, "markdown", status, nil, msg),
		Documents: []coreproject.DocumentOutline{doc},
	}}
}

func attachMarkdown(builders map[string]*projectBuilder, markdown []markdownFacet) {
	roots := make([]string, 0, len(builders))
	for root := range builders {
		roots = append(roots, root)
	}
	for _, item := range markdown {
		owner := nearestRoot(roots, item.dir)
		if owner == "" {
			if _, ok := builders[""]; !ok {
				builderFor(builders, item.dir).addFacet(item.facet)
				continue
			}
		}
		builderFor(builders, owner).addFacet(item.facet)
	}
}

func manifest(path, kind string, status coreproject.ParseStatus, summary map[string]string, msg string) coreproject.Manifest {
	return coreproject.Manifest{Path: cleanRel(path), Kind: kind, Status: status, Summary: summary, Error: msg}
}

func finalize(builders map[string]*projectBuilder, workspaceID coreworkspace.ID) []coreproject.Project {
	roots := make([]string, 0, len(builders))
	for root := range builders {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	projects := make([]coreproject.Project, 0, len(roots))
	for _, root := range roots {
		builder := builders[root]
		sort.SliceStable(builder.facets, func(i, j int) bool {
			if builder.facets[i].Kind == builder.facets[j].Kind {
				return builder.facets[i].Manifest.Path < builder.facets[j].Manifest.Path
			}
			return builder.facets[i].Kind < builder.facets[j].Kind
		})
		project := coreproject.Project{
			WorkspaceID: workspaceID,
			ID:          coreproject.ID(projectID(root)),
			Root:        root,
			Name:        projectName(root, builder.facets),
			Kind:        projectKind(builder.facets),
			Facets:      builder.facets,
		}
		projects = append(projects, project)
	}
	return projects
}

func attachHints(projects []coreproject.Project, workspaceID coreworkspace.ID) []coreproject.Hint {
	var out []coreproject.Hint
	for i := range projects {
		project := &projects[i]
		for _, facet := range project.Facets {
			for _, hint := range hintsForFacet(project.ID, facet, workspaceID) {
				project.Hints = append(project.Hints, hint)
				out = append(out, hint)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ProjectID == out[j].ProjectID {
			if out[i].Kind == out[j].Kind {
				return out[i].Path < out[j].Path
			}
			return out[i].Kind < out[j].Kind
		}
		return out[i].ProjectID < out[j].ProjectID
	})
	return out
}

func hintsForFacet(projectID coreproject.ID, facet coreproject.Facet, workspaceID coreworkspace.ID) []coreproject.Hint {
	base := coreproject.Hint{
		WorkspaceID: workspaceID,
		Kind:        "manifest",
		Path:        facet.Manifest.Path,
		ProjectID:   projectID,
		Confidence:  1,
		Metadata:    map[string]string{"facet": string(facet.Kind), "manifest_kind": facet.Manifest.Kind},
	}
	switch facet.Kind {
	case coreproject.FacetGoModule:
		base.Language = "go"
		base.Toolchain = "go"
		base.Metadata["name"] = "go.mod"
	case coreproject.FacetGoWorkspace:
		base.Language = "go"
		base.Toolchain = "go"
		base.Metadata["name"] = "go.work"
	case coreproject.FacetNodePackage:
		base.Language = "javascript"
		base.Toolchain = "node"
		base.Metadata["name"] = "package.json"
	case coreproject.FacetNodeLockfile:
		base.Language = "javascript"
		base.Toolchain = nodeLockToolchain(facet.Manifest.Path)
		base.Metadata["name"] = path.Base(facet.Manifest.Path)
	case coreproject.FacetCargoManifest:
		base.Language = "rust"
		base.Toolchain = "cargo"
		base.Metadata["name"] = "Cargo.toml"
	case coreproject.FacetCargoLockfile:
		base.Language = "rust"
		base.Toolchain = "cargo"
		base.Metadata["name"] = "Cargo.lock"
	case coreproject.FacetMarkdownDocs:
		base.Kind = "documentation"
		base.Language = "markdown"
		base.Metadata["name"] = "markdown"
	case coreproject.FacetTaskfile, coreproject.FacetMakefile:
		base.Kind = "task"
	case coreproject.FacetCI:
		base.Kind = "ci"
	case coreproject.FacetDockerfile:
		base.Toolchain = "docker"
		base.Metadata["name"] = path.Base(facet.Manifest.Path)
	case coreproject.FacetDockerCompose:
		base.Toolchain = "docker-compose"
		base.Metadata["name"] = "docker-compose.yaml"
	default:
		return nil
	}
	return []coreproject.Hint{base}
}

func isDockerfileName(name string) bool {
	return name == "Dockerfile" || strings.HasSuffix(name, ".Dockerfile")
}

func nodeLockToolchain(rel string) string {
	switch path.Base(rel) {
	case "pnpm-lock.yaml":
		return "pnpm"
	case "yarn.lock":
		return "yarn"
	default:
		return "npm"
	}
}

func setParents(projects []coreproject.Project) {
	for i := range projects {
		var parent coreproject.ID
		longest := -1
		for _, candidate := range projects {
			if candidate.Root == projects[i].Root {
				continue
			}
			if isWithin(projects[i].Root, candidate.Root) && len(candidate.Root) > longest {
				parent = candidate.ID
				longest = len(candidate.Root)
			}
		}
		projects[i].ParentID = parent
	}
}

func projectID(root string) string {
	if root == "" {
		return "project:."
	}
	return "project:" + root
}

func normalizeProjectID(id coreproject.ID) coreproject.ID {
	raw := cleanRel(strings.TrimPrefix(string(id), "project:"))
	if raw == "" {
		return coreproject.ID("project:.")
	}
	return coreproject.ID("project:" + raw)
}

func projectName(root string, facets []coreproject.Facet) string {
	for _, facet := range facets {
		if facet.Name != "" {
			return facet.Name
		}
		if facet.Summary["module"] != "" {
			return facet.Summary["module"]
		}
		if facet.Summary["name"] != "" {
			return facet.Summary["name"]
		}
	}
	if root == "" {
		return "."
	}
	return path.Base(root)
}

func projectKind(facets []coreproject.Facet) string {
	if len(facets) == 1 {
		return string(facets[0].Kind)
	}
	return "multi"
}

func facetCounts(projects []coreproject.Project) map[string]int {
	out := map[string]int{}
	for _, project := range projects {
		for _, facet := range project.Facets {
			out[string(facet.Kind)]++
		}
	}
	return out
}

func nearestProject(projects []coreproject.Project, rel string) (coreproject.Project, bool) {
	var best coreproject.Project
	bestLen := -1
	for _, project := range projects {
		if rel == project.Root || isWithin(rel, project.Root) {
			if len(project.Root) > bestLen {
				best = project
				bestLen = len(project.Root)
			}
		}
	}
	return best, bestLen >= 0
}

func nearestRoot(roots []string, rel string) string {
	best := ""
	bestLen := -1
	for _, root := range roots {
		if rel == root || isWithin(rel, root) {
			if len(root) > bestLen {
				best = root
				bestLen = len(root)
			}
		}
	}
	return best
}

func isWithin(rel, root string) bool {
	if root == "" {
		return true
	}
	return rel == root || strings.HasPrefix(rel, root+"/")
}

func cleanRel(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || raw == "." {
		return ""
	}
	clean := path.Clean(raw)
	if clean == "." {
		return ""
	}
	return strings.TrimPrefix(clean, "./")
}

func isAppManifestName(name string) bool {
	switch name {
	case "fluxplane.yaml":
		return true
	default:
		return false
	}
}

func isCoderConfigName(name string) bool {
	switch name {
	case ".coder.yaml", ".coder.yml":
		return true
	default:
		return false
	}
}

func isInstructionAIConfig(name string) bool {
	switch name {
	case "AGENTS.md", "CLAUDE.md", "MEMORY.md":
		return true
	default:
		return false
	}
}

func isClaudeAgentConfig(rel string) bool {
	parts := strings.Split(cleanRel(rel), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == ".claude" && parts[i+1] == "agents" && strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), ".md") {
			return true
		}
	}
	return false
}

func aiConfigProjectRoot(rel string) string {
	parts := strings.Split(cleanRel(rel), "/")
	for i, part := range parts {
		if part == ".claude" {
			return strings.Join(parts[:i], "/")
		}
	}
	return path.Dir(cleanRel(rel))
}

func parentAIConfig(rel string) string {
	parts := strings.Split(cleanRel(rel), "/")
	for i, part := range parts {
		if part == ".claude" {
			return strings.Join(append(parts[:i:i], ".claude"), "/")
		}
	}
	return ""
}

func aiConfigName(rel, kind string) string {
	if kind == "agent" {
		base := path.Base(rel)
		return strings.TrimSuffix(base, path.Ext(base))
	}
	return path.Base(rel)
}

func taskID(kind, rel, name string) coreproject.TaskID {
	return coreproject.TaskID(strings.Join([]string{kind, cleanRel(rel), strings.TrimSpace(name)}, ":"))
}

func selectTask(project coreproject.Project, req coreproject.TaskRunRequest) (coreproject.Task, error) {
	var matches []coreproject.Task
	for _, facet := range project.Facets {
		for _, task := range facet.Tasks {
			if req.TaskID != "" {
				if task.ID == req.TaskID {
					return task, nil
				}
				continue
			}
			if strings.TrimSpace(req.Name) == "" {
				continue
			}
			if task.Name != strings.TrimSpace(req.Name) {
				continue
			}
			if req.Kind != "" && task.Kind != strings.TrimSpace(req.Kind) {
				continue
			}
			matches = append(matches, task)
		}
	}
	if req.TaskID != "" {
		return coreproject.Task{}, fmt.Errorf("project: task_id %q not found in %s", req.TaskID, project.ID)
	}
	if strings.TrimSpace(req.Name) == "" {
		return coreproject.Task{}, fmt.Errorf("project: task_id or name is required")
	}
	if len(matches) == 0 {
		if req.Kind != "" {
			return coreproject.Task{}, fmt.Errorf("project: task %q of kind %q not found in %s", req.Name, req.Kind, project.ID)
		}
		return coreproject.Task{}, fmt.Errorf("project: task %q not found in %s", req.Name, project.ID)
	}
	if len(matches) > 1 {
		var ids []string
		for _, task := range matches {
			ids = append(ids, string(task.ID))
		}
		sort.Strings(ids)
		return coreproject.Task{}, fmt.Errorf("project: task %q is ambiguous; select task_id or kind (%s)", req.Name, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func taskCommand(task coreproject.Task, extraArgs []string) (string, []string, error) {
	executable := strings.TrimSpace(task.Executable)
	if executable == "" {
		return "", nil, fmt.Errorf("project: task %q has no executable", task.ID)
	}
	args := append([]string(nil), task.Args...)
	if len(extraArgs) > 0 {
		switch task.Kind {
		case "taskfile", "package_script":
			args = append(args, "--")
		}
		args = append(args, extraArgs...)
	}
	return executable, args, nil
}

func nodePackageManager(ctx context.Context, ws runtimeworkspace.Workspace, dir string) string {
	for _, candidate := range ancestorDirs(dir) {
		if hasWorkspaceFile(ctx, ws, path.Join(candidate, "pnpm-lock.yaml")) {
			return "pnpm"
		}
		if hasWorkspaceFile(ctx, ws, path.Join(candidate, "yarn.lock")) {
			return "yarn"
		}
		if hasWorkspaceFile(ctx, ws, path.Join(candidate, "package-lock.json")) {
			return "npm"
		}
	}
	return "npm"
}

func ancestorDirs(dir string) []string {
	current := cleanRel(dir)
	var out []string
	for {
		out = append(out, current)
		if current == "" {
			return out
		}
		parent := path.Dir(current)
		if parent == "." {
			parent = ""
		}
		current = parent
	}
}

func hasWorkspaceFile(ctx context.Context, ws runtimeworkspace.Workspace, rel string) bool {
	info, _, err := statWorkspacePath(ctx, ws, cleanRel(rel))
	return err == nil && info != nil && !info.IsDir()
}

func noisyDirs() []string {
	return []string{".git", ".cache", "node_modules", "vendor", "dist", "build", "target", "tmp"}
}

var makeTargetRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:(?:\s|$)`)

func parseMakeTargets(content, rel string) []coreproject.Task {
	var out []coreproject.Task
	seen := map[string]bool{}
	base := path.Base(rel)
	workdir := path.Dir(rel)
	if workdir == "." {
		workdir = ""
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") {
			continue
		}
		match := makeTargetRE.FindStringSubmatch(line)
		if len(match) != 2 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		out = append(out, coreproject.Task{
			ID:         taskID("makefile", rel, match[1]),
			Name:       match[1],
			Kind:       "makefile",
			Command:    "make " + match[1],
			Path:       rel,
			Workdir:    workdir,
			Executable: "make",
			Args:       []string{"-f", base, match[1]},
			Metadata:   map[string]string{"runner": "makefile"},
		})
	}
	return out
}

func parseTaskfileTasks(content []byte, rel string) ([]coreproject.Task, error) {
	var raw struct {
		Tasks map[string]yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return nil, err
	}
	var out []coreproject.Task
	names := make([]string, 0, len(raw.Tasks))
	for name := range raw.Tasks {
		if strings.TrimSpace(name) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	base := path.Base(rel)
	workdir := path.Dir(rel)
	if workdir == "." {
		workdir = ""
	}
	for _, name := range names {
		description := taskfileDescription(raw.Tasks[name])
		out = append(out, coreproject.Task{
			ID:          taskID("taskfile", rel, name),
			Name:        name,
			Kind:        "taskfile",
			Command:     "task " + name,
			Path:        rel,
			Workdir:     workdir,
			Executable:  "task",
			Args:        []string{"--taskfile", base, name},
			Description: description,
			Metadata:    map[string]string{"runner": "taskfile"},
		})
	}
	return out, nil
}

func taskfileDescription(node yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key == nil || value == nil || value.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "desc":
			return strings.TrimSpace(value.Value)
		case "summary":
			if strings.TrimSpace(value.Value) != "" {
				return strings.TrimSpace(value.Value)
			}
		}
	}
	return ""
}

func parseMarkdownOutline(source []byte) []coreproject.Heading {
	doc := goldmark.New().Parser().Parse(goldtext.NewReader(source))
	var flat []coreproject.Heading
	_ = goldast.Walk(doc, func(node goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering {
			return goldast.WalkContinue, nil
		}
		heading, ok := node.(*goldast.Heading)
		if !ok {
			return goldast.WalkContinue, nil
		}
		title := markdownNodeText(heading, source)
		if title == "" {
			return goldast.WalkContinue, nil
		}
		flat = append(flat, coreproject.Heading{Level: heading.Level, Title: title, Line: markdownNodeLine(heading, source)})
		return goldast.WalkSkipChildren, nil
	})
	return nestHeadings(flat)
}

func markdownNodeText(node goldast.Node, source []byte) string {
	var parts []string
	_ = goldast.Walk(node, func(child goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering || child == node {
			return goldast.WalkContinue, nil
		}
		switch n := child.(type) {
		case *goldast.Text:
			parts = append(parts, string(n.Value(source)))
		case *goldast.String:
			parts = append(parts, string(n.Value))
		}
		return goldast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(strings.Join(parts, "")), " ")
}

func markdownNodeLine(node goldast.Node, source []byte) int {
	lines := node.Lines()
	if lines == nil || lines.Len() == 0 {
		return 0
	}
	segment := lines.At(0)
	if segment.Start < 0 || segment.Start > len(source) {
		return 0
	}
	return 1 + strings.Count(string(source[:segment.Start]), "\n")
}

func nestHeadings(flat []coreproject.Heading) []coreproject.Heading {
	var roots []coreproject.Heading
	type stackEntry struct {
		level    int
		children *[]coreproject.Heading
	}
	stack := []stackEntry{{level: 0, children: &roots}}
	for _, heading := range flat {
		heading.Children = nil
		for len(stack) > 1 && stack[len(stack)-1].level >= heading.Level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		*parent.children = append(*parent.children, heading)
		idx := len(*parent.children) - 1
		childPtr := &(*parent.children)[idx].Children
		stack = append(stack, stackEntry{level: heading.Level, children: childPtr})
	}
	return roots
}

func documentTitle(headings []coreproject.Heading) (coreproject.Heading, bool) {
	var first *coreproject.Heading
	var walk func([]coreproject.Heading) (coreproject.Heading, bool)
	walk = func(items []coreproject.Heading) (coreproject.Heading, bool) {
		for i := range items {
			if first == nil {
				first = &items[i]
			}
			if items[i].Level == 1 {
				return items[i], true
			}
			if heading, ok := walk(items[i].Children); ok {
				return heading, true
			}
		}
		return coreproject.Heading{}, false
	}
	if heading, ok := walk(headings); ok {
		return heading, true
	}
	if first != nil {
		return *first, true
	}
	return coreproject.Heading{}, false
}
