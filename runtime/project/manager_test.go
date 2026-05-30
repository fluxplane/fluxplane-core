package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	coreproject "github.com/fluxplane/fluxplane-core/core/project"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

func TestManagerDetectsProjectsWithHostWorkspace(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		writeWorkspaceFile(t, ws, "go.mod", "module example.com/root\n\ngo 1.26\n")
		writeWorkspaceFile(t, ws, "package.json", `{"name":"root-js","scripts":{"test":"node test.js"}}`)
		writeWorkspaceFile(t, ws, "Makefile", "build:\n\tgo build ./...\n")
		writeWorkspaceFile(t, ws, "Taskfile.yaml", "version: '3'\ntasks:\n  lint:\n    cmds:\n      - go vet ./...\n")
		writeWorkspaceFile(t, ws, "Dockerfile", "FROM scratch\n")
		writeWorkspaceFile(t, ws, "api.Dockerfile", "FROM alpine\n")
		writeWorkspaceFile(t, ws, "docker-compose.yaml", "services:\n  app:\n    image: example/app\n")
		writeWorkspaceFile(t, ws, ".git/config", "[core]\n")
		writeWorkspaceFile(t, ws, ".agents/plans/example.md", "# Plan\n")
		writeWorkspaceFile(t, ws, ".claude/commands/check.md", "# Check\n")
		writeWorkspaceFile(t, ws, "README.md", "# Root\n\n## Setup\n")
		writeWorkspaceFile(t, ws, "docs/guide.md", "# Guide\n\n## Install\n")
		writeWorkspaceFile(t, ws, "tools/go.mod", "module example.com/tools\n\ngo 1.26\n")
		writeWorkspaceFile(t, ws, "go.work", "go 1.26\n\nuse (\n\t.\n\t./tools\n)\n")
		for i := 0; i < 20; i++ {
			writeWorkspaceFile(t, ws, filepath.Join(".cache", "go-build", string(rune('a'+i)), "entry.txt"), "noise")
		}

		manager := NewManager(ws)
		inventory, rebuilt, err := manager.Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
		if err != nil {
			t.Fatalf("Inventory: %v", err)
		}
		if !rebuilt {
			t.Fatal("Inventory rebuilt = false, want true")
		}
		if len(inventory.Projects) != 2 {
			t.Fatalf("projects = %#v, want root and tools", inventory.Projects)
		}
		root := projectByRoot(t, inventory, "")
		if !hasFacet(root, coreproject.FacetGoModule) || !hasFacet(root, coreproject.FacetGoWorkspace) || !hasFacet(root, coreproject.FacetNodePackage) || !hasFacet(root, coreproject.FacetMakefile) || !hasFacet(root, coreproject.FacetTaskfile) || !hasFacet(root, coreproject.FacetMarkdownDocs) || !hasFacet(root, coreproject.FacetDockerfile) || !hasFacet(root, coreproject.FacetDockerCompose) || !hasFacet(root, coreproject.FacetAgentsDir) || !hasFacet(root, coreproject.FacetClaudeDir) || !hasFacet(root, coreproject.FacetGitRepo) {
			t.Fatalf("root facets = %#v", root.Facets)
		}
		facetByKindAndPath(t, root, coreproject.FacetDockerfile, "Dockerfile")
		facetByKindAndPath(t, root, coreproject.FacetDockerfile, "api.Dockerfile")
		facetByKindAndPath(t, root, coreproject.FacetDockerCompose, "docker-compose.yaml")
		if !hasDocument(root, "docs/guide.md") {
			t.Fatalf("root documents = %#v, want nested docs/guide.md attached to root", root.Facets)
		}
		if !hasHint(inventory.Hints, "go", "go", "go.mod") || !hasHint(inventory.Hints, "markdown", "", "README.md") || !hasHint(inventory.Hints, "", "docker", "Dockerfile") || !hasHint(inventory.Hints, "", "docker-compose", "docker-compose.yaml") {
			t.Fatalf("hints = %#v, want go, markdown, Docker, and docker-compose project hints", inventory.Hints)
		}
		tools := projectByRoot(t, inventory, "tools")
		if tools.ParentID != root.ID {
			t.Fatalf("tools parent = %q, want %q", tools.ParentID, root.ID)
		}
		bareIDProject, _, err := manager.Project(context.Background(), coreproject.ProjectQuery{ProjectID: "tools"})
		if err != nil {
			t.Fatalf("Project bare id: %v", err)
		}
		if bareIDProject.ID != tools.ID {
			t.Fatalf("bare id project = %q, want %q", bareIDProject.ID, tools.ID)
		}

		limited, _, err := manager.Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true, MaxResults: 1})
		if err != nil {
			t.Fatalf("Inventory limited: %v", err)
		}
		if len(limited.Projects) != 1 || !hasFacet(limited.Projects[0], coreproject.FacetGoModule) {
			t.Fatalf("limited projects = %#v, want one discovered Go project", limited.Projects)
		}
		if hasHintProject(limited.Hints, tools.ID) {
			t.Fatalf("limited hints = %#v, want no hints for omitted project %s", limited.Hints, tools.ID)
		}

		inventory, rebuilt, err = manager.Inventory(context.Background(), coreproject.InventoryQuery{})
		if err != nil {
			t.Fatalf("Inventory cached: %v", err)
		}
		if rebuilt {
			t.Fatal("Inventory rebuilt = true, want memory reuse")
		}
	})
}

func TestManagerCreatesDocsOnlyProjectWithoutOwner(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		writeWorkspaceFile(t, ws, "docs/guide.md", "# Guide\n")
		inventory, _, err := NewManager(ws).Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
		if err != nil {
			t.Fatalf("Inventory: %v", err)
		}
		if len(inventory.Projects) != 1 || inventory.Projects[0].Root != "docs" || !hasDocument(inventory.Projects[0], "docs/guide.md") {
			t.Fatalf("inventory = %#v, want docs-only project", inventory)
		}
	})
}

func TestManagerDetectsFluxplaneCoderAndAIConfigFacets(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		writeWorkspaceFile(t, ws, "fluxplane.yaml", "kind: app\nname: demo\n")
		writeWorkspaceFile(t, ws, ".coder.yaml", "version: 1\nworkspace: {}\nimports: {}\n")
		writeWorkspaceFile(t, ws, "AGENTS.md", "# Agents\n")
		writeWorkspaceFile(t, ws, "CLAUDE.md", "# Claude\n")
		writeWorkspaceFile(t, ws, "MEMORY.md", "# Memory\n")
		writeWorkspaceFile(t, ws, ".claude/agents/writer.md", "# Writer\n")

		inventory, _, err := NewManager(ws).Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
		if err != nil {
			t.Fatalf("Inventory: %v", err)
		}
		root := projectByRoot(t, inventory, "")
		appFacet := facetByKindAndPath(t, root, coreproject.FacetAppManifest, "fluxplane.yaml")
		if appFacet.Summary["name"] != "demo" || appFacet.Manifest.Kind != "fluxplane_app_manifest" {
			t.Fatalf("app facet = %#v, want demo app manifest", appFacet)
		}
		coderFacet := facetByKindAndPath(t, root, coreproject.FacetCoderConfig, ".coder.yaml")
		if coderFacet.Summary["version"] != "1" || coderFacet.Manifest.Kind != "coder_config" {
			t.Fatalf("coder facet = %#v, want versioned coder config", coderFacet)
		}
		assertAIConfig(t, root, "AGENTS.md", "generic", "instruction", "")
		assertAIConfig(t, root, "CLAUDE.md", "claude", "instruction", "")
		assertAIConfig(t, root, "MEMORY.md", "generic", "context:memory", "")
		assertAIConfig(t, root, ".claude", "claude", "bundle", "")
		assertAIConfig(t, root, ".claude/agents/writer.md", "claude", "agent", ".claude")
	})
}

func TestManagerResolvesProjectTaskRunCommands(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		writeWorkspaceFile(t, ws, "package.json", `{"name":"app","scripts":{"test":"vitest run"}}`)
		writeWorkspaceFile(t, ws, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		writeWorkspaceFile(t, ws, "Taskfile.yaml", "version: '3'\ntasks:\n  lint:\n    desc: Run lint\n    cmds:\n      - go vet ./...\n")
		writeWorkspaceFile(t, ws, "Makefile", "build:\n\tgo build ./...\n")

		manager := NewManager(ws)
		selection, _, err := manager.ResolveTaskRun(context.Background(), coreproject.TaskRunRequest{Name: "lint", Kind: "taskfile", Args: []string{"./..."}})
		if err != nil {
			t.Fatalf("ResolveTaskRun taskfile: %v", err)
		}
		if selection.Task.ID != "taskfile:Taskfile.yaml:lint" || selection.Task.Description != "Run lint" {
			t.Fatalf("task = %#v, want stable id and description", selection.Task)
		}
		if selection.Executable != "task" || !sameStrings(selection.Args, []string{"--taskfile", "Taskfile.yaml", "lint", "--", "./..."}) {
			t.Fatalf("taskfile command = %s %#v", selection.Executable, selection.Args)
		}

		selection, _, err = manager.ResolveTaskRun(context.Background(), coreproject.TaskRunRequest{Name: "test", Args: []string{"--watch=false"}})
		if err != nil {
			t.Fatalf("ResolveTaskRun package script: %v", err)
		}
		if selection.Executable != "pnpm" || !sameStrings(selection.Args, []string{"run", "test", "--", "--watch=false"}) {
			t.Fatalf("package command = %s %#v", selection.Executable, selection.Args)
		}

		selection, _, err = manager.ResolveTaskRun(context.Background(), coreproject.TaskRunRequest{TaskID: "makefile:Makefile:build", Args: []string{"VAR=1"}})
		if err != nil {
			t.Fatalf("ResolveTaskRun makefile: %v", err)
		}
		if selection.Executable != "make" || !sameStrings(selection.Args, []string{"-f", "Makefile", "build", "VAR=1"}) {
			t.Fatalf("make command = %s %#v", selection.Executable, selection.Args)
		}
	})
}

func TestManagerResolvesPackageManagerFromAncestorLockfile(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		writeWorkspaceFile(t, ws, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		writeWorkspaceFile(t, ws, "apps/web/package.json", `{"name":"web","scripts":{"test":"vitest run"}}`)

		manager := NewManager(ws)
		selection, _, err := manager.ResolveTaskRun(context.Background(), coreproject.TaskRunRequest{Path: "apps/web", Name: "test", Args: []string{"--run"}})
		if err != nil {
			t.Fatalf("ResolveTaskRun nested package script: %v", err)
		}
		if selection.Executable != "pnpm" || selection.Task.Metadata["package_manager"] != "pnpm" {
			t.Fatalf("selection = %#v, want ancestor pnpm lockfile to select pnpm", selection)
		}
		if !sameStrings(selection.Args, []string{"run", "test", "--", "--run"}) {
			t.Fatalf("package command args = %#v", selection.Args)
		}
	})
}

func TestManagerPrefersNearestPackageLockOverAncestorLockfile(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		writeWorkspaceFile(t, ws, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		writeWorkspaceFile(t, ws, "apps/web/package-lock.json", `{"lockfileVersion":3}`)
		writeWorkspaceFile(t, ws, "apps/web/package.json", `{"name":"web","scripts":{"test":"node test.js"}}`)

		manager := NewManager(ws)
		selection, _, err := manager.ResolveTaskRun(context.Background(), coreproject.TaskRunRequest{Path: "apps/web", Name: "test"})
		if err != nil {
			t.Fatalf("ResolveTaskRun nested package script: %v", err)
		}
		if selection.Executable != "npm" || selection.Task.Metadata["package_manager"] != "npm" {
			t.Fatalf("selection = %#v, want nearest package-lock to select npm", selection)
		}
	})
}

func TestParseMarkdownOutlineUsesMarkdownAST(t *testing.T) {
	outline := parseMarkdownOutline([]byte(`# Root *Title*

Intro

## Child [Link](https://example.com)

` + "```go\n# Not a heading\n```\n" + `
### Grand` + "`code`" + `

Setext
------

#### Skipped
`))
	if len(outline) != 1 {
		t.Fatalf("outline = %#v, want one root", outline)
	}
	root := outline[0]
	if root.Level != 1 || root.Title != "Root Title" || root.Line != 1 {
		t.Fatalf("root = %#v", root)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root children = %#v, want child and setext", root.Children)
	}
	child := root.Children[0]
	if child.Level != 2 || child.Title != "Child Link" || len(child.Children) != 1 {
		t.Fatalf("child = %#v", child)
	}
	if child.Children[0].Level != 3 || child.Children[0].Title != "Grandcode" {
		t.Fatalf("grandchild = %#v", child.Children[0])
	}
	setext := root.Children[1]
	if setext.Level != 2 || setext.Title != "Setext" || len(setext.Children) != 1 {
		t.Fatalf("setext = %#v", setext)
	}
	if setext.Children[0].Level != 4 || setext.Children[0].Title != "Skipped" {
		t.Fatalf("skipped child = %#v", setext.Children[0])
	}
}

func runManagerBackends(t *testing.T, fn func(*testing.T, system.Workspace)) {
	t.Helper()
	t.Run("host", func(t *testing.T) {
		sys, err := system.NewHost(system.Config{Root: t.TempDir()})
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		fn(t, sys.Workspace())
	})
}

func writeWorkspaceFile(t *testing.T, ws system.Workspace, rel, content string) {
	t.Helper()
	resolved, err := ws.ResolveCreate(context.Background(), rel)
	if err != nil {
		t.Fatalf("ResolveCreate(%s): %v", rel, err)
	}
	if ws.System() == nil || ws.System().FileSystem() == nil {
		t.Fatalf("workspace filesystem is nil")
	}
	name := resolved.Rel
	if name == "" {
		name = "."
	}
	if err := ws.System().FileSystem().WriteFile(context.Background(), name, []byte(content), fpsystem.WriteFileOptions{Perm: 0644, Overwrite: true}); err != nil {
		t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}

func projectByRoot(t *testing.T, inventory coreproject.Inventory, root string) coreproject.Project {
	t.Helper()
	for _, project := range inventory.Projects {
		if project.Root == root {
			return project
		}
	}
	t.Fatalf("project root %q not found in %#v", root, inventory.Projects)
	return coreproject.Project{}
}

func hasFacet(project coreproject.Project, kind coreproject.FacetKind) bool {
	for _, facet := range project.Facets {
		if facet.Kind == kind {
			return true
		}
	}
	return false
}

func facetByKindAndPath(t *testing.T, project coreproject.Project, kind coreproject.FacetKind, rel string) coreproject.Facet {
	t.Helper()
	for _, facet := range project.Facets {
		if facet.Kind == kind && facet.Manifest.Path == rel {
			return facet
		}
	}
	t.Fatalf("facet %s at %s not found in %#v", kind, rel, project.Facets)
	return coreproject.Facet{}
}

func assertAIConfig(t *testing.T, project coreproject.Project, rel, vendor, kind, parent string) {
	t.Helper()
	facet := facetByKindAndPath(t, project, coreproject.FacetAIConfig, rel)
	if facet.Summary["vendor"] != vendor || facet.Summary["kind"] != kind || facet.Summary["parent"] != parent {
		t.Fatalf("ai config facet = %#v, want vendor=%s kind=%s parent=%s", facet, vendor, kind, parent)
	}
}

func hasHint(hints []coreproject.Hint, language, toolchain, path string) bool {
	for _, hint := range hints {
		if string(hint.Language) == language && hint.Toolchain == toolchain && hint.Path == path {
			return true
		}
	}
	return false
}

func hasHintProject(hints []coreproject.Hint, projectID coreproject.ID) bool {
	for _, hint := range hints {
		if hint.ProjectID == projectID {
			return true
		}
	}
	return false
}

func hasDocument(project coreproject.Project, path string) bool {
	for _, facet := range project.Facets {
		for _, doc := range facet.Documents {
			if doc.Path == path {
				return true
			}
		}
	}
	return false
}

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

func TestManagerHostWorkspaceDoesNotDependOnCWD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/host\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	inventory, _, err := NewManager(sys.Workspace()).Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	project := projectByRoot(t, inventory, "")
	if project.Name != "example.com/host" {
		t.Fatalf("project name = %q, want module path", project.Name)
	}
}

func TestManagerDetectsProjectsInNamedHostRoots(t *testing.T) {
	primary := t.TempDir()
	api := t.TempDir()
	if err := os.WriteFile(filepath.Join(primary, "go.mod"), []byte("module example.com/root\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatalf("write primary go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(api, "go.mod"), []byte("module example.com/api\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatalf("write api go.mod: %v", err)
	}
	sys, err := system.NewHost(system.Config{
		Root: primary,
		Workspace: system.WorkspaceConfig{Roots: []system.WorkspaceRootConfig{{
			Name: "api",
			Path: api,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	inventory, _, err := NewManager(sys.Workspace()).Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	projectByRoot(t, inventory, "")
	apiProject := projectByRoot(t, inventory, "@api")
	if apiProject.Name != "example.com/api" {
		t.Fatalf("api project name = %q, want module path", apiProject.Name)
	}
	if !hasHint(inventory.Hints, "go", "go", "@api/go.mod") {
		t.Fatalf("hints = %#v, want named-root go hint", inventory.Hints)
	}
}

func TestManagerSkipsDuplicatePhysicalHostRoots(t *testing.T) {
	primary := t.TempDir()
	if err := os.WriteFile(filepath.Join(primary, "go.mod"), []byte("module example.com/root\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatalf("write primary go.mod: %v", err)
	}
	sys, err := system.NewHost(system.Config{
		Root: primary,
		Workspace: system.WorkspaceConfig{Roots: []system.WorkspaceRootConfig{{
			Name: "same",
			Path: primary,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	inventory, _, err := NewManager(sys.Workspace()).Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inventory.Projects) != 1 {
		t.Fatalf("projects = %#v, want one project for duplicate physical roots", inventory.Projects)
	}
	project := projectByRoot(t, inventory, "")
	if project.Name != "example.com/root" {
		t.Fatalf("project name = %q, want module path", project.Name)
	}
	if hasHint(inventory.Hints, "go", "go", "@same/go.mod") {
		t.Fatalf("hints = %#v, want duplicate named root skipped", inventory.Hints)
	}
}

func TestManagerSkipsScratchHostRoot(t *testing.T) {
	primary := t.TempDir()
	scratch := t.TempDir()
	if err := os.WriteFile(filepath.Join(primary, "go.mod"), []byte("module example.com/root\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatalf("write primary go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(scratch, "generated"), 0755); err != nil {
		t.Fatalf("MkdirAll scratch generated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "generated", "go.mod"), []byte("module example.com/generated\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatalf("write scratch go.mod: %v", err)
	}
	sys, err := system.NewHost(system.Config{
		Root: primary,
		Workspace: system.WorkspaceConfig{
			Roots: []system.WorkspaceRootConfig{{
				Name: "tmp",
				Path: scratch,
			}},
			ScratchRoot: "tmp",
		},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	inventory, _, err := NewManager(sys.Workspace()).Inventory(context.Background(), coreproject.InventoryQuery{Refresh: true})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inventory.Projects) != 1 {
		t.Fatalf("projects = %#v, want scratch root omitted", inventory.Projects)
	}
	if hasHint(inventory.Hints, "go", "go", "@tmp/generated/go.mod") {
		t.Fatalf("hints = %#v, want scratch root omitted", inventory.Hints)
	}
}

func TestManagerRejectsWorkspaceIDWhenUnscoped(t *testing.T) {
	runManagerBackends(t, func(t *testing.T, ws system.Workspace) {
		manager := NewManager(ws)
		_, _, err := manager.Inventory(context.Background(), coreproject.InventoryQuery{WorkspaceID: "workspace:configured:other"})
		if err == nil {
			t.Fatal("Inventory: want workspace mismatch error")
		}
	})
}
