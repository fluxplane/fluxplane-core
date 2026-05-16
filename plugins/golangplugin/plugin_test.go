package golangplugin

import (
	"context"
	"fmt"
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

import (
	"context"
	alias "example.com/ext/lib"
	"example.com/app/pkg/model"
	"fmt"
)

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
		writeGoFile(t, sys.Workspace(), "pkg/service/extra.go", `package service

import "strings"

func Extra() {
	_ = strings.TrimSpace
}
`)
		writeGoFile(t, sys.Workspace(), "pkg/service/service_test.go", `package service

import "testing"

func TestRun() {}

func TestExtra(t *testing.T) {}
`)
		writeGoFile(t, sys.Workspace(), "pkg/service/child/child.go", `package service

import "bytes"

func ChildOnly() {
	_ = bytes.Buffer{}
}
`)
		writeGoFile(t, sys.Workspace(), "pkg/model/model.go", `package model

type Model struct{}
`)
		writeGoFile(t, sys.Workspace(), "pkg/consumer/consumer.go", `package consumer

import "example.com/app/pkg/service"

func Use() {
	service.Extra()
}
`)
		writeGoFile(t, sys.Workspace(), "pkg/other/other.go", `package other

import "example.com/ext/lib"

func Other() {}
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
		fileImports := runGoOp(t, sys, ImportsOp, map[string]any{"path": "pkg/service/service.go", "direction": "direct"})
		for _, want := range []string{"context [stdlib]", "fmt [stdlib]", "example.com/app/pkg/model [module_local]", "alias example.com/ext/lib [external]"} {
			if !strings.Contains(fileImports.Text, want) {
				t.Fatalf("file imports text = %q, want %q", fileImports.Text, want)
			}
		}
		packageImports := runGoOp(t, sys, ImportsOp, map[string]any{"path": "pkg/service", "direction": "direct", "include_tests": false})
		if !strings.Contains(packageImports.Text, "strings [stdlib]") || strings.Contains(packageImports.Text, "testing [stdlib") || strings.Contains(packageImports.Text, "child/child.go") {
			t.Fatalf("package imports text = %q, want exact package imports without tests or nested package", packageImports.Text)
		}
		packageImportsWithTests := runGoOp(t, sys, ImportsOp, map[string]any{"path": "pkg/service", "direction": "direct", "include_tests": true})
		if !strings.Contains(packageImportsWithTests.Text, "testing [stdlib test]") {
			t.Fatalf("package imports with tests text = %q, want test imports", packageImportsWithTests.Text)
		}
		packageIDImports := runGoOp(t, sys, ImportsOp, map[string]any{"package_id": "go:package:pkg/service:service", "direction": "direct", "include_tests": false})
		if !strings.Contains(packageIDImports.Text, "pkg/service/service.go") || strings.Contains(packageIDImports.Text, "pkg/service/child/child.go") {
			t.Fatalf("package_id imports text = %q, want exact package only", packageIDImports.Text)
		}
		reverseImports := runGoOp(t, sys, ImportsOp, map[string]any{"path": ".", "direction": "reverse", "import_path": "example.com/app/pkg/service", "include_tests": false})
		if !strings.Contains(reverseImports.Text, "Reverse target: example.com/app/pkg/service") || !strings.Contains(reverseImports.Text, "pkg/consumer/consumer.go") || strings.Contains(reverseImports.Text, "vendor") {
			t.Fatalf("reverse imports text = %q, want consumer importer and no vendor", reverseImports.Text)
		}
		moduleDirectImports := runGoOp(t, sys, ImportsOp, map[string]any{"path": ".", "direction": "direct", "include_tests": false})
		if !strings.Contains(moduleDirectImports.Text, "pkg/consumer/consumer.go") || !strings.Contains(moduleDirectImports.Text, "pkg/service/service.go") || strings.Contains(moduleDirectImports.Text, "vendor") {
			t.Fatalf("module direct imports text = %q, want module-scoped imports and no vendor", moduleDirectImports.Text)
		}
		derivedReverseImports := runGoOp(t, sys, ImportsOp, map[string]any{"path": "pkg/service/service.go", "direction": "reverse", "include_tests": false})
		if !strings.Contains(derivedReverseImports.Text, "Reverse target: example.com/app/pkg/service") || !strings.Contains(derivedReverseImports.Text, "pkg/consumer/consumer.go") {
			t.Fatalf("derived reverse imports text = %q, want module-derived target and consumer importer", derivedReverseImports.Text)
		}
		packageDerivedReverseImports := runGoOp(t, sys, ImportsOp, map[string]any{"path": "pkg/service", "direction": "reverse", "include_tests": false})
		if !strings.Contains(packageDerivedReverseImports.Text, "Reverse target: example.com/app/pkg/service") || !strings.Contains(packageDerivedReverseImports.Text, "pkg/consumer/consumer.go") {
			t.Fatalf("package-derived reverse imports text = %q, want module-scoped consumer importer", packageDerivedReverseImports.Text)
		}
		packageIDDerivedReverseImports := runGoOp(t, sys, ImportsOp, map[string]any{"package_id": "go:package:pkg/service:service", "direction": "reverse", "include_tests": false})
		if !strings.Contains(packageIDDerivedReverseImports.Text, "Reverse target: example.com/app/pkg/service") || !strings.Contains(packageIDDerivedReverseImports.Text, "pkg/consumer/consumer.go") {
			t.Fatalf("package_id-derived reverse imports text = %q, want module-scoped consumer importer", packageIDDerivedReverseImports.Text)
		}
		invalidImportDirection := runGoResult(t, sys, ImportsOp, map[string]any{"path": "pkg/service", "direction": "sideways"})
		if invalidImportDirection.Status != operation.StatusFailed || invalidImportDirection.Error == nil || !strings.Contains(invalidImportDirection.Error.Message, "unsupported import direction") {
			t.Fatalf("invalid import direction result = %#v, want unsupported direction failure", invalidImportDirection)
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

func TestGoImportsClassifiesDotlessModuleLocalImports(t *testing.T) {
	runGoPluginBackends(t, func(t *testing.T, sys system.System) {
		writeGoFile(t, sys.Workspace(), "go.mod", "module app\n\ngo 1.26\n")
		writeGoFile(t, sys.Workspace(), "pkg/model/model.go", `package model

type Model struct{}
`)
		writeGoFile(t, sys.Workspace(), "pkg/service/service.go", `package service

import "app/pkg/model"

func Use(model.Model) {}
`)

		imports := runGoOp(t, sys, ImportsOp, map[string]any{"path": "pkg/service/service.go", "direction": "direct"})
		if !strings.Contains(imports.Text, "app/pkg/model [module_local]") || strings.Contains(imports.Text, "app/pkg/model [stdlib]") {
			t.Fatalf("dotless module imports text = %q, want module_local classification", imports.Text)
		}
	})
}

func TestGoNavigationOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runGoPluginBackends(t, func(t *testing.T, sys system.System) {
		defs := `package nav

import alias "context"

// DefaultName is the fallback name.
const DefaultName = "world"

var Enabled = true

// Service runs work.
type Service struct {
	Name string
}

type Context struct{}

type Other struct {
	Name string
}

// NewService creates a service.
func NewService(name string) *Service {
	return &Service{Name: name}
}

func NewOther() *Other {
	return &Other{}
}

// Run executes work.
func (s *Service) Run(ctx alias.Context) error {
	local := DefaultName
	for _, item := range []string{local} {
		_ = item
	}
	_ = s.Name
	return nil
}

func (o *Other) Run() error {
	return nil
}

func Shadow(ok bool) {
	x := 0
	if ok {
		x := 1
		_ = x
	}
	_ = x
}
`
		use := `package nav

func Use() {
	svc := NewService("x")
	_ = svc.Name
	_ = svc.Run
	_ = Enabled
}

func UseOther() {
	svc := NewOther()
	_ = svc.Run
}
`
		writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/nav\n\ngo 1.26\n")
		writeGoFile(t, sys.Workspace(), "pkg/nav/defs.go", defs)
		writeGoFile(t, sys.Workspace(), "pkg/nav/use.go", use)
		writeGoFile(t, sys.Workspace(), "pkg/nav/use_test.go", `package nav

func TestEnabled() {
	_ = Enabled
}
`)
		writeGoFile(t, sys.Workspace(), "pkg/nav/child/child.go", `package nav

func ChildUse() {
	_ = Enabled
}
`)

		for _, tc := range []struct {
			name   string
			path   string
			source string
			needle string
			want   []string
		}{
			{name: "package", path: "pkg/nav/defs.go", source: defs, needle: "nav", want: []string{"package nav"}},
			{name: "import alias", path: "pkg/nav/defs.go", source: defs, needle: "alias", want: []string{"import alias"}},
			{name: "top function", path: "pkg/nav/use.go", source: use, needle: "NewService", want: []string{"function NewService", "defs.go"}},
			{name: "top var", path: "pkg/nav/use.go", source: use, needle: "Enabled", want: []string{"var Enabled", "defs.go"}},
			{name: "method", path: "pkg/nav/use.go", source: use, needle: "Run", want: []string{"method Service.Run", "defs.go"}},
			{name: "field", path: "pkg/nav/use.go", source: use, needle: "Name", want: []string{"field Name", "defs.go"}},
			{name: "local", path: "pkg/nav/defs.go", source: defs, needle: "local :=", want: []string{"var local"}},
			{name: "parameter", path: "pkg/nav/defs.go", source: defs, needle: "name string", want: []string{"var name"}},
			{name: "receiver", path: "pkg/nav/defs.go", source: defs, needle: "s *Service", want: []string{"var s"}},
			{name: "range var", path: "pkg/nav/defs.go", source: defs, needle: "item :=", want: []string{"var item"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				line, column := goPosition(t, tc.source, tc.needle)
				result := runGoOp(t, sys, DefinitionOp, map[string]any{"path": tc.path, "line": line, "column": column, "include_docs": true})
				for _, want := range tc.want {
					if !strings.Contains(result.Text, want) {
						t.Fatalf("definition text = %q, want %q", result.Text, want)
					}
				}
				if !strings.Contains(result.Text, "Warning: AST-only resolution") {
					t.Fatalf("definition text = %q, want AST limitation warning", result.Text)
				}
			})
		}

		line, column := goPosition(t, use, "NewService")
		info := runGoOp(t, sys, SymbolInfoOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "include_docs": true})
		if !strings.Contains(info.Text, "function NewService") || !strings.Contains(info.Text, "doc: NewService creates a service.") {
			t.Fatalf("symbol info text = %q, want function docs", info.Text)
		}

		line, column = goPosition(t, defs, "return nil")
		enclosing := runGoOp(t, sys, SymbolInfoOp, map[string]any{"path": "pkg/nav/defs.go", "line": line, "column": column})
		if !strings.Contains(enclosing.Text, "method Service.Run") || !strings.Contains(enclosing.Text, "enclosing_symbol") {
			t.Fatalf("enclosing symbol info text = %q, want enclosing method fallback", enclosing.Text)
		}

		line, column = goPosition(t, defs, "Context)")
		external := runGoOp(t, sys, DefinitionOp, map[string]any{"path": "pkg/nav/defs.go", "line": line, "column": column})
		if !strings.Contains(external.Text, "external_selector") || strings.Contains(external.Text, "type Context") {
			t.Fatalf("external selector definition text = %q, want unresolved external selector", external.Text)
		}

		line, column = goPosition(t, use, "Run")
		scopedReceiver := runGoOp(t, sys, DefinitionOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column})
		if !strings.Contains(scopedReceiver.Text, "method Service.Run") || strings.Contains(scopedReceiver.Text, "method Other.Run") {
			t.Fatalf("scoped receiver text = %q, want Service.Run only", scopedReceiver.Text)
		}

		line, column = goPosition(t, defs, "_ = x\n}")
		outerLine, _ := goPosition(t, defs, "x := 0")
		shadow := runGoOp(t, sys, DefinitionOp, map[string]any{"path": "pkg/nav/defs.go", "line": line, "column": column + len("_ = ")})
		if !strings.Contains(shadow.Text, fmt.Sprintf("defs.go:%d", outerLine)) {
			t.Fatalf("shadow text = %q, want outer x on line %d", shadow.Text, outerLine)
		}

		zeroOffset := runGoOp(t, sys, DefinitionOp, map[string]any{"path": "pkg/nav/defs.go", "offset": 0})
		if !strings.Contains(zeroOffset.Text, "Go definition") {
			t.Fatalf("zero offset text = %q, want successful offset 0 query", zeroOffset.Text)
		}

		invalid := runGoResult(t, sys, DefinitionOp, map[string]any{"path": "pkg/nav/use.go"})
		if invalid.Status != operation.StatusFailed || invalid.Error == nil || !strings.Contains(invalid.Error.Message, "line and column") {
			t.Fatalf("invalid navigation result = %#v", invalid)
		}

		line, column = goPosition(t, use, "NewService")
		newServiceRefs := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "include_declaration": true})
		for _, want := range []string{"symbol: function NewService", "declaration NewService", "reference NewService", "defs.go", "use.go"} {
			if !strings.Contains(newServiceRefs.Text, want) {
				t.Fatalf("NewService references text = %q, want %q", newServiceRefs.Text, want)
			}
		}

		fileScopedRefs := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "scope": "file", "include_declaration": true})
		if strings.Contains(fileScopedRefs.Text, "declaration NewService") || !strings.Contains(fileScopedRefs.Text, "reference NewService") {
			t.Fatalf("file-scoped references text = %q, want only use.go reference", fileScopedRefs.Text)
		}

		line, column = goPosition(t, use, "Run")
		runRefs := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "include_declaration": true})
		if !strings.Contains(runRefs.Text, "method Service.Run") || !strings.Contains(runRefs.Text, "declaration Service.Run") || !strings.Contains(runRefs.Text, "reference Service.Run") || strings.Contains(runRefs.Text, "Other.Run") {
			t.Fatalf("Run references text = %q, want Service.Run references only", runRefs.Text)
		}

		line, column = goPosition(t, use, "Name")
		fieldRefs := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "include_declaration": true})
		if !strings.Contains(fieldRefs.Text, "field Name") || !strings.Contains(fieldRefs.Text, "declaration Name") || !strings.Contains(fieldRefs.Text, "reference Name") || strings.Contains(fieldRefs.Text, "Other struct") {
			t.Fatalf("field references text = %q, want Service.Name references only", fieldRefs.Text)
		}

		line, column = goPosition(t, use, "Enabled")
		withoutTests := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "include_tests": false})
		if strings.Contains(withoutTests.Text, "use_test.go") {
			t.Fatalf("references without tests text = %q, want test file excluded", withoutTests.Text)
		}
		withTests := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/use.go", "line": line, "column": column, "include_tests": true})
		if !strings.Contains(withTests.Text, "use_test.go") {
			t.Fatalf("references with tests text = %q, want test file included", withTests.Text)
		}
		if strings.Contains(withTests.Text, "child/child.go") {
			t.Fatalf("references with nested same-name package text = %q, want child package excluded", withTests.Text)
		}

		line, column = goPosition(t, defs, "_ = x\n}")
		innerUseLine, _ := goPosition(t, defs, "_ = x\n\t}")
		shadowRefs := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/defs.go", "line": line, "column": column + len("_ = "), "include_declaration": true})
		if !strings.Contains(shadowRefs.Text, "x := 0") || strings.Contains(shadowRefs.Text, fmt.Sprintf("defs.go:%d:", innerUseLine)) {
			t.Fatalf("shadow references text = %q, want outer x only", shadowRefs.Text)
		}

		line, column = goPosition(t, defs, "Context)")
		externalRefs := runGoOp(t, sys, ReferencesOp, map[string]any{"path": "pkg/nav/defs.go", "line": line, "column": column})
		if !strings.Contains(externalRefs.Text, "external_selector") || !strings.Contains(externalRefs.Text, "no AST-level references found") {
			t.Fatalf("external selector references text = %q, want unresolved external selector", externalRefs.Text)
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

func goPosition(t *testing.T, source, needle string) (int, int) {
	t.Helper()
	idx := strings.Index(source, needle)
	if idx < 0 {
		t.Fatalf("needle %q not found in source", needle)
	}
	line := 1
	lineStart := 0
	for i := 0; i < idx; i++ {
		if source[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return line, idx - lineStart + 1
}
