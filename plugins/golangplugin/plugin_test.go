package golangplugin

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

func TestGoOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runGoPluginBackends(t, func(t *testing.T, sys system.System) {
		writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/app\n\ngo 1.26\n")
		writeGoFile(t, sys.Workspace(), "root.go", `package app

func RootOnly() {}
`)
		writeGoFile(t, sys.Workspace(), "pkg/service/service.go", `package service

import "context"

// DefaultName is the fallback name.
const DefaultName = "world"

var Enabled = true

type Runner interface {
	Run(context.Context) error
}

// Service runs work.
type Service struct {
	Name string
}

// NewService creates a service.
func NewService(name string) *Service {
	return &Service{Name: name}
}

// Run executes the service.
func (s *Service) Run(ctx context.Context) error {
	return nil
}
`)
		writeGoFile(t, sys.Workspace(), "pkg/service/service_test.go", `package service

func TestRun() {}
`)
		writeGoFile(t, sys.Workspace(), "tools/go.mod", "module example.com/tools\n\ngo 1.26\n")
		writeGoFile(t, sys.Workspace(), "tools/tool.go", `package tools

func ToolOnly() {}
`)
		writeGoFile(t, sys.Workspace(), "vendor/example.com/lib/lib.go", `package lib

func VendoredRoot() {}
`)
		writeGoFile(t, sys.Workspace(), "tools/vendor/example.com/lib/lib.go", `package lib

func VendoredNested() {}
`)
		writeGoFile(t, sys.Workspace(), "pkg/bad/bad.go", `package bad

func Broken(
`)
		writeGoFile(t, sys.Workspace(), "pkg/bad/good.go", `package bad

func Good() {}
`)

		project := runGoOp(t, sys, ProjectOp, map[string]any{"refresh": true})
		if !strings.Contains(project.Text, "go_module go.mod") {
			t.Fatalf("project text = %q", project.Text)
		}
		scopedProject := runGoOp(t, sys, ProjectOp, map[string]any{"path": "tools"})
		if !strings.Contains(scopedProject.Text, "tools [project:tools]: example.com/tools") || strings.Contains(scopedProject.Text, ". [project:.]: example.com/app") {
			t.Fatalf("scoped project text = %q, want only tools module", scopedProject.Text)
		}

		packages := runGoOp(t, sys, PackagesOp, map[string]any{"path": "pkg/service"})
		if !strings.Contains(packages.Text, "pkg/service service") {
			t.Fatalf("packages text = %q", packages.Text)
		}
		scopedPackages := runGoOp(t, sys, PackagesOp, map[string]any{"project_id": "project:tools"})
		if !strings.Contains(scopedPackages.Text, "tools tools") || strings.Contains(scopedPackages.Text, "pkg/service") || strings.Contains(scopedPackages.Text, "vendor") {
			t.Fatalf("scoped packages text = %q, want only tools non-vendor packages", scopedPackages.Text)
		}

		outline := runGoOp(t, sys, OutlineOp, map[string]any{"path": "pkg/service/service.go", "include_docs": true})
		for _, want := range []string{"struct Service", "interface Runner", "function NewService", "method Service.Run", "const DefaultName", "var Enabled"} {
			if !strings.Contains(outline.Text, want) {
				t.Fatalf("outline text = %q, want %q", outline.Text, want)
			}
		}
		if !strings.Contains(outline.Text, "doc: Service runs work.") {
			t.Fatalf("outline text = %q, want visible docs", outline.Text)
		}
		smallMaxBytesOutline := runGoOp(t, sys, OutlineOp, map[string]any{"path": "pkg/service/service.go", "max_bytes": 20})
		if !strings.Contains(smallMaxBytesOutline.Text, "struct Service") {
			t.Fatalf("small max_bytes outline text = %q, want complete parse", smallMaxBytesOutline.Text)
		}
		partialOutline := runGoOp(t, sys, OutlineOp, map[string]any{"path": "pkg/bad", "max_results": 10})
		if !strings.Contains(partialOutline.Text, "function Good") || !strings.Contains(partialOutline.Text, "Diagnostics: 1 file(s) skipped") {
			t.Fatalf("partial outline text = %q, want good symbol and diagnostic", partialOutline.Text)
		}

		symbols := runGoOp(t, sys, SymbolOp, map[string]any{"query": "Service", "path": "pkg/service", "max_results": 10})
		if !strings.Contains(symbols.Text, "Service") || !strings.Contains(symbols.Text, "NewService") {
			t.Fatalf("symbols text = %q", symbols.Text)
		}
		methodByCaseKind := runGoOp(t, sys, SymbolOp, map[string]any{"kind": "Method", "query": "Run", "path": "pkg/service", "max_results": 10})
		if !strings.Contains(methodByCaseKind.Text, "method Service.Run") {
			t.Fatalf("method by case-insensitive kind text = %q", methodByCaseKind.Text)
		}
		methodByBareName := runGoOp(t, sys, SymbolOp, map[string]any{"name": "Run", "path": "pkg/service", "max_results": 10})
		if !strings.Contains(methodByBareName.Text, "method Service.Run") {
			t.Fatalf("method by bare name text = %q", methodByBareName.Text)
		}
		symbolDocs := runGoOp(t, sys, SymbolOp, map[string]any{"name": "NewService", "path": "pkg/service", "include_docs": true})
		if !strings.Contains(symbolDocs.Text, "doc: NewService creates a service.") {
			t.Fatalf("symbol docs text = %q, want visible docs", symbolDocs.Text)
		}
		invalidLanguage := runGoResult(t, sys, PackagesOp, map[string]any{"language": "python", "path": "pkg/service"})
		if invalidLanguage.Status != operation.StatusFailed || invalidLanguage.Error == nil || !strings.Contains(invalidLanguage.Error.Message, "unsupported language") {
			t.Fatalf("invalid language result = %#v, want unsupported language failure", invalidLanguage)
		}
		vendorSymbols := runGoOp(t, sys, SymbolOp, map[string]any{"query": "Vendored", "path": ".", "max_results": 10})
		if strings.Contains(vendorSymbols.Text, "VendoredRoot") || strings.Contains(vendorSymbols.Text, "VendoredNested") {
			t.Fatalf("vendor symbols text = %q, want vendored symbols excluded", vendorSymbols.Text)
		}
		providers, err := New(sys).ContextProviders(context.Background(), pluginhost.Context{})
		if err != nil {
			t.Fatalf("ContextProviders: %v", err)
		}
		if len(providers) != 1 || providers[0].Spec().Annotations[corecontext.AnnotationAutoContext] != "true" {
			t.Fatalf("providers = %#v, want one auto provider", providers)
		}
		blocks, err := providers[0].Build(context.Background(), corecontext.Request{})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "Go workspace summary:") || !strings.Contains(blocks[0].Content, "go_packages") {
			t.Fatalf("blocks = %#v", blocks)
		}
	})
}

func runGoPluginBackends(t *testing.T, fn func(*testing.T, system.System)) {
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

func runGoOp(t *testing.T, sys system.System, name string, input map[string]any) operation.Rendered {
	t.Helper()
	result := runGoResult(t, sys, name, input)
	if result.Status != operation.StatusOK {
		t.Fatalf("%s status = %s error = %#v", name, result.Status, result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("%s output = %#v, want Rendered", name, result.Output)
	}
	return rendered
}

func runGoResult(t *testing.T, sys system.System, name string, input map[string]any) operation.Result {
	t.Helper()
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op.Run(operation.NewContext(context.Background(), nil), input)
		}
	}
	t.Fatalf("operation %s not found", name)
	return operation.Result{}
}

func writeGoFile(t *testing.T, ws system.Workspace, rel, content string) {
	t.Helper()
	if _, err := ws.WriteFile(context.Background(), rel, []byte(content), 0644, true); err != nil {
		t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}
