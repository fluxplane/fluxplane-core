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

	coreproject "github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/runtime/system"
	"golang.org/x/mod/modfile"
)

const (
	defaultMaxEntries = 10000
	defaultMaxBytes   = 128 * 1024
)

// Manager keeps a process-local, memory-only project inventory.
type Manager struct {
	workspace system.Workspace
	mu        sync.Mutex
	inventory coreproject.Inventory
	built     bool
}

// NewManager returns a Workspace-backed project inventory manager.
func NewManager(workspace system.Workspace) *Manager {
	return &Manager{workspace: workspace}
}

// Inventory returns a detected project inventory, rebuilding when requested.
func (m *Manager) Inventory(ctx context.Context, req coreproject.InventoryQuery) (coreproject.Inventory, bool, error) {
	if m == nil || m.workspace == nil {
		return coreproject.Inventory{}, false, fmt.Errorf("project: workspace is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.built && !req.Refresh {
		return m.inventory, false, nil
	}
	inventory, err := scan(ctx, m.workspace, req)
	if err != nil {
		return coreproject.Inventory{}, false, err
	}
	m.inventory = inventory
	m.built = true
	return inventory, true, nil
}

// Project selects a project by id or nearest path.
func (m *Manager) Project(ctx context.Context, req coreproject.ProjectQuery) (coreproject.Project, bool, error) {
	inv, rebuilt, err := m.Inventory(ctx, coreproject.InventoryQuery{Refresh: req.Refresh})
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

func scan(ctx context.Context, ws system.Workspace, req coreproject.InventoryQuery) (coreproject.Inventory, error) {
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > defaultMaxBytes {
		maxBytes = defaultMaxBytes
	}
	entries, _, truncated, err := ws.Walk(ctx, ".", system.WalkOptions{
		Depth:      50,
		ShowHidden: true,
		MaxEntries: defaultMaxEntries,
		SkipDirs:   noisyDirs(),
	})
	if err != nil {
		return coreproject.Inventory{}, err
	}
	builders := map[string]*projectBuilder{}
	var markdown []markdownFacet
	var warnings []coreproject.Warning
	probeRoot(ctx, ws, builders, maxBytes)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return coreproject.Inventory{}, err
		}
		rel := cleanRel(entry.Path.Rel)
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
		case name == "Taskfile.yaml" || name == "Taskfile.yml":
			addTaskfile(ctx, ws, builders, dir, rel, maxBytes)
		case name == "Makefile" || name == "makefile":
			addMakefile(ctx, ws, builders, dir, rel, maxBytes)
		case name == ".git" || strings.HasSuffix(rel, "/.git"):
			builderFor(builders, dir).addFacet(coreproject.Facet{Kind: coreproject.FacetGitRepo, Manifest: manifest(rel, "git_repo", coreproject.ParseStatusParsed, nil, "")})
		case strings.HasPrefix(rel, ".github/workflows/"):
			builderFor(builders, ".").addFacet(coreproject.Facet{Kind: coreproject.FacetCI, Manifest: manifest(rel, "github_workflow", coreproject.ParseStatusUnsupported, nil, "")})
		case strings.HasSuffix(strings.ToLower(name), ".md"):
			markdown = append(markdown, readMarkdown(ctx, ws, dir, rel, maxBytes))
		}
	}
	attachMarkdown(builders, markdown)
	projects := finalize(builders)
	setParents(projects)
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Root < projects[j].Root })
	limit := req.MaxResults
	if limit > 0 && len(projects) > limit {
		projects = projects[:limit]
		truncated = true
	}
	return coreproject.Inventory{
		Root:      ".",
		Projects:  projects,
		Truncated: truncated,
		Summary: coreproject.Summary{
			ProjectCount: len(projects),
			FacetCounts:  facetCounts(projects),
		},
		Warnings: warnings,
	}, nil
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

func probeRoot(ctx context.Context, ws system.Workspace, builders map[string]*projectBuilder, maxBytes int) {
	probes := []struct {
		path string
		add  func(context.Context, system.Workspace, map[string]*projectBuilder, string, string, int)
	}{
		{path: "go.mod", add: addGoMod},
		{path: "go.work", add: addGoWork},
		{path: "package.json", add: addPackageJSON},
		{path: "Taskfile.yaml", add: addTaskfile},
		{path: "Taskfile.yml", add: addTaskfile},
		{path: "Makefile", add: addMakefile},
		{path: "makefile", add: addMakefile},
	}
	for _, probe := range probes {
		if _, _, err := ws.Stat(ctx, probe.path); err == nil {
			probe.add(ctx, ws, builders, "", probe.path, maxBytes)
		}
	}
	if _, _, err := ws.Stat(ctx, ".git"); err == nil {
		builderFor(builders, "").addFacet(coreproject.Facet{Kind: coreproject.FacetGitRepo, Manifest: manifest(".git", "git_repo", coreproject.ParseStatusParsed, nil, "")})
	}
	if _, _, err := ws.Stat(ctx, ".agents"); err == nil {
		addResourceDir(builders, "", ".agents", coreproject.FacetAgentsDir, "agents_dir")
	}
	if _, _, err := ws.Stat(ctx, ".claude"); err == nil {
		addResourceDir(builders, "", ".claude", coreproject.FacetClaudeDir, "claude_dir")
	}
	if _, _, err := ws.Stat(ctx, ".github/workflows"); err == nil {
		builderFor(builders, "").addFacet(coreproject.Facet{Kind: coreproject.FacetCI, Manifest: manifest(".github/workflows", "github_workflow", coreproject.ParseStatusUnsupported, nil, "")})
	}
}

func addResourceDir(builders map[string]*projectBuilder, dir, rel string, kind coreproject.FacetKind, manifestKind string) {
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     kind,
		Manifest: manifest(rel, manifestKind, coreproject.ParseStatusParsed, nil, ""),
	})
}

func addGoMod(ctx context.Context, ws system.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, truncated, _, err := ws.ReadFile(ctx, rel, int64(maxBytes))
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

func addGoWork(ctx context.Context, ws system.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := ws.ReadFile(ctx, rel, int64(maxBytes))
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

func addPackageJSON(ctx context.Context, ws system.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := ws.ReadFile(ctx, rel, int64(maxBytes))
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
				tasks = append(tasks, coreproject.Task{Name: key, Kind: "package_script", Command: raw.Scripts[key], Path: rel})
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

func addTaskfile(ctx context.Context, ws system.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := ws.ReadFile(ctx, rel, int64(maxBytes))
	status := coreproject.ParseStatusParsed
	var msg string
	var tasks []coreproject.Task
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		tasks = parseTaskfileTasks(string(data), rel)
	}
	builderFor(builders, dir).addFacet(coreproject.Facet{
		Kind:     coreproject.FacetTaskfile,
		Manifest: manifest(rel, "taskfile", status, map[string]string{"tasks": fmt.Sprint(len(tasks))}, msg),
		Tasks:    tasks,
	})
}

func addMakefile(ctx context.Context, ws system.Workspace, builders map[string]*projectBuilder, dir, rel string, maxBytes int) {
	data, _, _, err := ws.ReadFile(ctx, rel, int64(maxBytes))
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

func readMarkdown(ctx context.Context, ws system.Workspace, dir, rel string, maxBytes int) markdownFacet {
	data, truncated, _, err := ws.ReadFile(ctx, rel, int64(maxBytes))
	doc := coreproject.DocumentOutline{Path: rel, Truncated: truncated}
	status := coreproject.ParseStatusParsed
	var msg string
	if err != nil {
		status, msg = coreproject.ParseStatusFailed, err.Error()
	} else {
		doc.Headings = parseMarkdownHeadings(string(data))
		if len(doc.Headings) > 0 {
			doc.Title = doc.Headings[0].Title
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

func finalize(builders map[string]*projectBuilder) []coreproject.Project {
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
			ID:     coreproject.ID(projectID(root)),
			Root:   root,
			Name:   projectName(root, builder.facets),
			Kind:   projectKind(builder.facets),
			Facets: builder.facets,
		}
		projects = append(projects, project)
	}
	return projects
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

func noisyDirs() []string {
	return []string{".git", ".cache", "node_modules", "vendor", "dist", "build", "target", "tmp"}
}

var makeTargetRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:(?:\s|$)`)

func parseMakeTargets(content, rel string) []coreproject.Task {
	var out []coreproject.Task
	seen := map[string]bool{}
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
		out = append(out, coreproject.Task{Name: match[1], Kind: "makefile", Command: "make " + match[1], Path: rel})
	}
	return out
}

func parseTaskfileTasks(content, rel string) []coreproject.Task {
	var out []coreproject.Task
	inTasks := false
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "tasks:" {
			inTasks = true
			continue
		}
		if !inTasks {
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if name == "" || strings.Contains(name, " ") {
			continue
		}
		out = append(out, coreproject.Task{Name: name, Kind: "taskfile", Command: "task " + name, Path: rel})
	}
	return out
}

func parseMarkdownHeadings(content string) []coreproject.Heading {
	var out []coreproject.Heading
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		title := strings.TrimSpace(trimmed[level:])
		if title == "" {
			continue
		}
		out = append(out, coreproject.Heading{Level: level, Title: title, Line: i + 1})
	}
	return out
}
