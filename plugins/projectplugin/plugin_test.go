package projectplugin

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/fluxplane/agentruntime/runtime/systemtest"
)

func TestProjectOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runProjectPluginBackends(t, func(t *testing.T, sys system.System) {
		writeProjectFile(t, sys.Workspace(), "go.mod", "module example.com/app\n\ngo 1.26\n")
		writeProjectFile(t, sys.Workspace(), "package.json", `{"name":"app","scripts":{"test":"node test.js"}}`)
		writeProjectFile(t, sys.Workspace(), ".agents/plans/example.md", "# Plan\n")
		writeProjectFile(t, sys.Workspace(), ".claude/commands/check.md", "# Check\n")
		writeProjectFile(t, sys.Workspace(), "README.md", "# App\n\n## Usage\n\n### CLI\n")

		inventory := runProjectOp(t, sys, InventoryOp, map[string]any{"refresh": true})
		if !strings.Contains(inventory.Text, ". [project:.]") || !strings.Contains(inventory.Text, "go_module go.mod") || !strings.Contains(inventory.Text, "node_package package.json") || !strings.Contains(inventory.Text, "agents_dir .agents") || !strings.Contains(inventory.Text, "claude_dir .claude") {
			t.Fatalf("inventory text = %q", inventory.Text)
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

func writeProjectFile(t *testing.T, ws system.Workspace, rel, content string) {
	t.Helper()
	if _, err := ws.WriteFile(context.Background(), rel, []byte(content), 0644, true); err != nil {
		t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}
