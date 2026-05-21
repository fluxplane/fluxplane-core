package golang

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/engine/core/context"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	corelanguage "github.com/fluxplane/engine/core/language"
	"github.com/fluxplane/engine/core/language/golang"
	"github.com/fluxplane/engine/core/operation"
	coresession "github.com/fluxplane/engine/core/session"
	"github.com/fluxplane/engine/core/testrun"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/engine/runtime/evidence"
	"github.com/fluxplane/engine/runtime/system"
	"github.com/fluxplane/engine/runtime/systemtest"
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
		packageIDImportResult := importResultFromRendered(t, packageIDImports)
		if got, want := len(packageIDImportResult.DirectImports), 5; got != want {
			t.Fatalf("package_id direct imports = %d, want %d; text = %q", got, want, packageIDImports.Text)
		}
		packageIDImportPaths := map[string]bool{}
		for _, imp := range packageIDImportResult.DirectImports {
			if imp.PackageID != "go:package:pkg/service:service" {
				t.Fatalf("package_id direct import PackageID = %q, want source package id", imp.PackageID)
			}
			if strings.HasPrefix(imp.SourcePath, "pkg/service/child/") {
				t.Fatalf("package_id direct import source = %q, want exact source package only", imp.SourcePath)
			}
			packageIDImportPaths[imp.Path] = true
		}
		for _, want := range []string{"context", "example.com/ext/lib", "example.com/app/pkg/model", "fmt", "strings"} {
			if !packageIDImportPaths[want] {
				t.Fatalf("package_id direct import paths = %#v, want %q", packageIDImportPaths, want)
			}
		}
		packageIDTargetImports := runGoOp(t, sys, ImportsOp, map[string]any{"package_id": "go:package:pkg/service:service", "direction": "direct", "import_path": "example.com/app/pkg/model", "include_tests": false})
		packageIDTargetImportResult := importResultFromRendered(t, packageIDTargetImports)
		if got := packageIDTargetImportResult.DirectImports; len(got) != 1 || got[0].Path != "example.com/app/pkg/model" {
			t.Fatalf("package_id target-filtered direct imports = %#v, want only model import", got)
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

func TestGoImplementationOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runGoPluginBackends(t, func(t *testing.T, sys system.System) {
		contract := `package contract

type Runner interface {
	Run() error
	Stop() error
}

type OnlyRun interface {
	Run() error
}

type TestOnly interface {
	TestHook()
}
`
		service := `package service

type Service struct{}

func (Service) Run() error {
	return nil
}

func (*Service) Stop() error {
	return nil
}

type Broken struct{}

func (Broken) Run() error {
	return nil
}
`
		sibling := `package sibling

type Sibling struct{}

func (Sibling) Run() error {
	return nil
}

func (Sibling) Stop() error {
	return nil
}
`
		testImpl := `package service

type TestImpl struct{}

func (TestImpl) TestHook() {}
`
		writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/impl\n\ngo 1.26\n")
		writeGoFile(t, sys.Workspace(), "pkg/contract/contract.go", contract)
		writeGoFile(t, sys.Workspace(), "pkg/service/service.go", service)
		writeGoFile(t, sys.Workspace(), "pkg/service/service_test.go", testImpl)
		writeGoFile(t, sys.Workspace(), "pkg/sibling/sibling.go", sibling)

		line, column := goPosition(t, contract, "Runner interface")
		runnerModule := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column, "scope": "module"})
		for _, want := range []string{"pointer Service implements Runner", "value Sibling implements Runner", "matched: Run, Stop"} {
			if !strings.Contains(runnerModule.Text, want) {
				t.Fatalf("runner module implementations text = %q, want %q", runnerModule.Text, want)
			}
		}
		if implementationResultFromRendered(t, runnerModule).ResolutionMode == "ast" && !strings.Contains(runnerModule.Text, "missing_methods") {
			t.Fatalf("runner module implementations text = %q, want AST partial-match diagnostics", runnerModule.Text)
		}
		if strings.Contains(runnerModule.Text, "packages_load") || strings.Contains(runnerModule.Text, "typecheck_unavailable") {
			t.Fatalf("runner module implementations text = %q, want clean AST fallback for virtual workspaces", runnerModule.Text)
		}

		runnerPackage := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column})
		if strings.Contains(runnerPackage.Text, "Service implements Runner") {
			t.Fatalf("runner package implementations text = %q, want package scope only", runnerPackage.Text)
		}
		packageImplementations := implementationResultFromRendered(t, runnerPackage)
		if packageImplementations.ResolutionMode == "type_checked" {
			if !strings.Contains(runnerPackage.Text, "no type-checked implementation matches") {
				t.Fatalf("runner package implementations text = %q, want type-checked no-match result", runnerPackage.Text)
			}
		} else if !strings.Contains(runnerPackage.Text, "no AST-level implementation matches") {
			t.Fatalf("runner package implementations text = %q, want AST no-match result", runnerPackage.Text)
		}

		writeGoFile(t, sys.Workspace(), "pkg/splitrun/service.go", `package splitrun

type Service struct{}

func (Service) Run() error {
	return nil
}
`)
		writeGoFile(t, sys.Workspace(), "pkg/splitstop/service.go", `package splitstop

type Service struct{}

func (Service) Stop() error {
	return nil
}
`)
		splitModule := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column, "scope": "module"})
		if strings.Contains(splitModule.Text, "splitrun") || strings.Contains(splitModule.Text, "splitstop") {
			t.Fatalf("split package implementations text = %q, want same bare type names kept package-qualified", splitModule.Text)
		}

		line, column = goPosition(t, service, "Service struct")
		serviceModule := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/service/service.go", "line": line, "column": column, "scope": "module"})
		if !strings.Contains(serviceModule.Text, "pointer Service implements Runner") || !strings.Contains(serviceModule.Text, "value Service implements OnlyRun") {
			t.Fatalf("service module implementations text = %q, want pointer Runner and value OnlyRun matches", serviceModule.Text)
		}

		line, column = goPosition(t, contract, "Run() error")
		methodMatches := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column, "scope": "module"})
		if !strings.Contains(methodMatches.Text, "method_correspondence Service implements Runner") || !strings.Contains(methodMatches.Text, "matched: Run") {
			t.Fatalf("method correspondence text = %q, want Run method correspondences", methodMatches.Text)
		}

		line, column = goPosition(t, contract, "TestOnly interface")
		withoutTests := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column, "scope": "module", "include_tests": false})
		if strings.Contains(withoutTests.Text, "TestImpl implements TestOnly") {
			t.Fatalf("implementations without tests text = %q, want test implementation excluded", withoutTests.Text)
		}
		withTests := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column, "scope": "module", "include_tests": true})
		if !strings.Contains(withTests.Text, "value TestImpl implements TestOnly") {
			t.Fatalf("implementations with tests text = %q, want test implementation included", withTests.Text)
		}

		invalid := runGoResult(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": 1, "column": 1, "scope": "workspace"})
		if invalid.Status != operation.StatusFailed || invalid.Error == nil || !strings.Contains(invalid.Error.Message, "unsupported implementation scope") {
			t.Fatalf("invalid implementations result = %#v, want unsupported scope failure", invalid)
		}
	})
}

func TestGoToolchainInfoEnvVersionWithHostWorkspace(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/toolchain\n\ngo 1.26\n")

	infoRendered := runGoOp(t, sys, InfoOp, map[string]any{"include_raw_env": true})
	info := goInfoResultFromRendered(t, infoRendered)
	if !strings.Contains(info.Version["go"], "go version") || info.Target["goos"] == "" || info.Target["goarch"] == "" {
		t.Fatalf("go info = %#v, want version and target", info)
	}
	if info.RawEnv["GOVERSION"] == "" {
		t.Fatalf("go info raw env = %#v, want GOVERSION", info.RawEnv)
	}
	if _, ok := info.Network["goproxy"].(golang.GoProxyConfig); !ok {
		t.Fatalf("go info network = %#v, want parsed goproxy", info.Network)
	}

	envRendered := runGoOp(t, sys, EnvOp, map[string]any{"vars": []string{"GOOS", "GOARCH"}})
	env := goEnvResultFromRendered(t, envRendered)
	if env.Values["GOOS"] == "" || env.Values["GOARCH"] == "" {
		t.Fatalf("go env values = %#v, want GOOS and GOARCH", env.Values)
	}
	if _, ok := env.Values["GOPATH"]; ok {
		t.Fatalf("go env values = %#v, want only requested vars", env.Values)
	}

	changed := runGoOp(t, sys, EnvOp, map[string]any{"changed": true})
	if !goEnvResultFromRendered(t, changed).Changed {
		t.Fatalf("changed go env = %#v, want changed result", changed.Data)
	}

	versionRendered := runGoOp(t, sys, VersionOp, map[string]any{})
	version := goVersionResultFromRendered(t, versionRendered)
	if !strings.Contains(version.Version, "go version") {
		t.Fatalf("go version = %#v, want toolchain version", version)
	}

	invalid := runGoResult(t, sys, EnvOp, map[string]any{"changed": true, "vars": []string{"GOOS"}})
	if invalid.Status != operation.StatusFailed || invalid.Error == nil || !strings.Contains(invalid.Error.Message, "vars cannot be combined") {
		t.Fatalf("invalid go env result = %#v, want changed+vars failure", invalid)
	}
}

func TestGoProxyParsing(t *testing.T) {
	proxy := parseGoProxy("https://primary|https://fallback,direct")
	if proxy.Raw != "https://primary|https://fallback,direct" || len(proxy.Groups) != 2 {
		t.Fatalf("proxy = %#v, want two fallback groups", proxy)
	}
	if got := strings.Join(proxy.Groups[0].Entries, ","); got != "https://primary,https://fallback" {
		t.Fatalf("first proxy group = %q, want pipe entries preserved", got)
	}
	if got := strings.Join(proxy.Groups[1].Entries, ","); got != "direct" {
		t.Fatalf("second proxy group = %q, want direct", got)
	}
}

func TestGoToolchainDocListWithHostWorkspace(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/doclist\n\ngo 1.26\n")
	service := `// Package service documents service behavior.
package service

// Service runs work.
type Service struct {
	// Name is the display name.
	Name string
}

// Run executes the service.
func (Service) Run() {}

// hidden is not exported.
type hidden struct{}
`
	writeGoFile(t, sys.Workspace(), "pkg/service/service.go", service)
	writeGoFile(t, sys.Workspace(), "pkg/broken/broken.go", `package broken

import _ "example.com/missing"
`)

	doc := runGoOp(t, sys, DocOp, map[string]any{"path": "pkg/service", "symbol": "Service"})
	if !strings.Contains(doc.Text, "type Service struct") || !strings.Contains(doc.Text, "Service runs work.") {
		t.Fatalf("go doc text = %q, want Service docs", doc.Text)
	}
	docData := goDocResultFromRendered(t, doc)
	if docData.Symbol != "Service" || docData.Workdir != "pkg/service" {
		t.Fatalf("go doc data = %#v, want Service in pkg/service", docData)
	}

	line, column := goPosition(t, service, "Service struct")
	positionDoc := runGoOp(t, sys, DocOp, map[string]any{"path": "pkg/service/service.go", "line": line, "column": column})
	if !strings.Contains(positionDoc.Text, "type Service struct") || goDocResultFromRendered(t, positionDoc).Symbol != "Service" {
		t.Fatalf("position go doc text = %q, want position-derived Service docs", positionDoc.Text)
	}
	unexportedDoc := runGoOp(t, sys, DocOp, map[string]any{"path": "pkg/service", "symbol": "hidden", "include_unexported": true})
	if !strings.Contains(unexportedDoc.Text, "type hidden struct") || !strings.Contains(unexportedDoc.Text, "hidden is not exported") {
		t.Fatalf("unexported go doc text = %q, want hidden docs", unexportedDoc.Text)
	}
	missingDoc := runGoOp(t, sys, DocOp, map[string]any{"path": "pkg/service", "symbol": "Missing"})
	if len(goDocResultFromRendered(t, missingDoc).Diagnostics) == 0 {
		t.Fatalf("missing go doc text = %q, want no-doc diagnostics", missingDoc.Text)
	}

	list := runGoOp(t, sys, ListOp, map[string]any{"patterns": []string{"./pkg/service"}})
	listData := goListResultFromRendered(t, list)
	if len(listData.Records) != 1 || goListRecordString(listData.Records[0], "ImportPath") != "example.com/doclist/pkg/service" {
		t.Fatalf("go list data = %#v, want service package record", listData)
	}
	if !listData.Complete {
		t.Fatalf("go list complete = false, want true")
	}

	modules := runGoOp(t, sys, ListOp, map[string]any{"modules": true, "patterns": []string{"."}})
	moduleData := goListResultFromRendered(t, modules)
	if len(moduleData.Records) != 1 || goListRecordString(moduleData.Records[0], "Path") != "example.com/doclist" {
		t.Fatalf("go list modules data = %#v, want root module", moduleData)
	}
	writeGoFile(t, sys.Workspace(), "pkg/service/service_test.go", `package service

import "testing"

func TestService(t *testing.T) {}
`)
	testList := runGoOp(t, sys, ListOp, map[string]any{"patterns": []string{"./pkg/service"}, "test": true})
	foundTestRecord := false
	for _, record := range goListResultFromRendered(t, testList).Records {
		if goListRecordString(record, "ForTest") == "example.com/doclist/pkg/service" {
			foundTestRecord = true
		}
	}
	if !foundTestRecord {
		t.Fatalf("go list test data = %#v, want test package metadata", goListResultFromRendered(t, testList))
	}

	broken := runGoOp(t, sys, ListOp, map[string]any{"patterns": []string{"./pkg/broken"}, "include_errors": true})
	if len(goListResultFromRendered(t, broken).Diagnostics) == 0 {
		t.Fatalf("broken go list text = %q, want diagnostics", broken.Text)
	}

	invalid := runGoResult(t, sys, ListOp, map[string]any{"patterns": []string{"-bad"}})
	if invalid.Status != operation.StatusFailed || invalid.Error == nil || !strings.Contains(invalid.Error.Message, "must not start") {
		t.Fatalf("invalid go list result = %#v, want rejected pattern", invalid)
	}
}

func TestGoToolchainCheckFmtInstallWithHostWorkspace(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/checks\n\ngo 1.26\n")
	writeGoFile(t, sys.Workspace(), "pkg/checks/checks.go", `package checks

func Add(a, b int) int { return a + b }
`)
	writeGoFile(t, sys.Workspace(), "pkg/checks/checks_test.go", `package checks

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad add")
	}
}

func TestFail(t *testing.T) {
	t.Fatal("intentional failure")
}
`)
	writeGoFile(t, sys.Workspace(), "pkg/vet/vet.go", `package vet

import "fmt"

func Bad() {
	fmt.Printf("%d", "x")
}
`)
	writeGoFile(t, sys.Workspace(), "pkg/badbuild/bad.go", `package badbuild

func Broken() {
	_ = Missing()
}
`)
	writeGoFile(t, sys.Workspace(), "pkg/format/format.go", "package format\n\nfunc Bad( ){ }\n")
	writeGoFile(t, sys.Workspace(), "cmd/tool/main.go", `package main

func main() {}
`)

	testResult := runGoOp(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/checks"}, "run": "TestAdd", "count": 1})
	testData := goTestResultFromRendered(t, testResult)
	if !testData.Passed || len(testData.Packages) != 1 || testData.Packages[0].Passed != 1 {
		t.Fatalf("go test data = %#v, want passing TestAdd", testData)
	}
	failResult := runGoOp(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/checks"}, "run": "TestFail", "count": 1})
	failData := goTestResultFromRendered(t, failResult)
	if failData.Passed || len(failData.Packages) != 1 || failData.Packages[0].Failed != 1 {
		t.Fatalf("go test fail data = %#v, want structured test failure", failData)
	}
	if failData.TestRunEvent.Status != testrun.StatusFailed || !testrun.HasFailureKind(failData.TestRunEvent, testrun.FailureAssertion) || !strings.Contains(failResult.Text, "intentional failure") {
		t.Fatalf("go test assertion event/text = %#v / %q, want assertion failure details", failData.TestRunEvent, failResult.Text)
	}
	comboResult := runGoOp(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/checks"}, "run": "TestAdd|TestFail", "skip": "TestFail", "count": 1})
	comboData := goTestResultFromRendered(t, comboResult)
	if !comboData.Passed || len(comboData.Packages) != 1 || comboData.Packages[0].Passed != 1 {
		t.Fatalf("go test alternation data = %#v text = %q, want TestAdd only", comboData, comboResult.Text)
	}
	invalidRun := runGoResult(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/checks"}, "run": "("})
	if invalidRun.Status != operation.StatusFailed || invalidRun.Error == nil || !strings.Contains(invalidRun.Error.Message, "regular expression") {
		t.Fatalf("invalid run regex result = %#v, want regex validation failure", invalidRun)
	}
	invalidSkip := runGoResult(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/checks"}, "skip": "TestAdd\nTestFail"})
	if invalidSkip.Status != operation.StatusFailed || invalidSkip.Error == nil || !strings.Contains(invalidSkip.Error.Message, "control character") {
		t.Fatalf("invalid skip regex result = %#v, want control character validation failure", invalidSkip)
	}
	compileResult := runGoOp(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/badbuild"}})
	compileData := goTestResultFromRendered(t, compileResult)
	if compileData.Passed || !strings.Contains(compileResult.Text, "Missing") || !testrun.HasFailureKind(compileData.TestRunEvent, testrun.FailureBuild) {
		t.Fatalf("go test compile result = %#v text = %q, want compile failure diagnostics", compileData.TestRunEvent, compileResult.Text)
	}
	invalidTimeout := runGoResult(t, sys, TestOp, map[string]any{"patterns": []string{"./pkg/checks"}, "timeout": "soon"})
	if invalidTimeout.Status != operation.StatusFailed || invalidTimeout.Error == nil || !strings.Contains(invalidTimeout.Error.Message, "duration") {
		t.Fatalf("invalid timeout result = %#v, want duration validation", invalidTimeout)
	}

	vetResult := runGoOp(t, sys, VetOp, map[string]any{"patterns": []string{"./pkg/vet"}, "json": true})
	vetData := goVetResultFromRendered(t, vetResult)
	if vetData.Passed || len(vetData.Diagnostics) == 0 || !strings.Contains(vetData.Diagnostics[0].Message, "Printf") {
		t.Fatalf("go vet data = %#v, want printf diagnostic", vetData)
	}
	invalidVet := runGoResult(t, sys, VetOp, map[string]any{"patterns": []string{"./pkg/vet"}, "fix": true})
	if invalidVet.Status != operation.StatusFailed || invalidVet.Error == nil || !strings.Contains(invalidVet.Error.Message, "unsupported") {
		t.Fatalf("invalid vet result = %#v, want unsupported fix", invalidVet)
	}
	invalidVetDiff := runGoResult(t, sys, VetOp, map[string]any{"patterns": []string{"./pkg/vet"}, "diff": true})
	if invalidVetDiff.Status != operation.StatusFailed || invalidVetDiff.Error == nil || !strings.Contains(invalidVetDiff.Error.Message, "unsupported") {
		t.Fatalf("invalid vet diff result = %#v, want unsupported diff", invalidVetDiff)
	}

	buildOK := runGoOp(t, sys, BuildOp, map[string]any{"patterns": []string{"./pkg/checks"}})
	if !goBuildResultFromRendered(t, buildOK).Passed {
		t.Fatalf("go build ok text = %q, want pass", buildOK.Text)
	}
	buildBad := runGoOp(t, sys, BuildOp, map[string]any{"patterns": []string{"./pkg/badbuild"}})
	if goBuildResultFromRendered(t, buildBad).Passed || !strings.Contains(buildBad.Text, "undefined: Missing") {
		t.Fatalf("go build bad text = %q, want compile diagnostic", buildBad.Text)
	}

	fmtDryRun := runGoOp(t, sys, FmtOp, map[string]any{"patterns": []string{"./pkg/format"}})
	fmtDryRunData := goFmtResultFromRendered(t, fmtDryRun)
	if !fmtDryRunData.DryRun || !fmtDryRunData.WouldWrite || len(fmtDryRunData.Files) == 0 {
		t.Fatalf("go fmt dry-run data = %#v, want would-write files", fmtDryRunData)
	}
	falseValue := false
	fmtReal := runGoOp(t, sys, FmtOp, map[string]any{"patterns": []string{"./pkg/format"}, "dry_run": falseValue})
	if !goFmtResultFromRendered(t, fmtReal).Changed {
		t.Fatalf("go fmt real text = %q, want changed file", fmtReal.Text)
	}
	formatted, _, _, err := sys.Workspace().ReadFile(context.Background(), "pkg/format/format.go", 1024)
	if err != nil {
		t.Fatalf("ReadFile formatted: %v", err)
	}
	if !strings.Contains(string(formatted), "func Bad() {}") {
		t.Fatalf("formatted file = %q, want gofmt output", string(formatted))
	}

	install := runGoOp(t, sys, InstallOp, map[string]any{"packages": []string{"./cmd/tool"}})
	installData := goInstallResultFromRendered(t, install)
	if !installData.DryRun || installData.Installed {
		t.Fatalf("go install data = %#v, want dry-run only", installData)
	}
	installArgs, installPackages, _, _, err := goInstallArgs(golang.GoInstallQuery{Packages: []string{"example.com/tool"}, Version: "v1.2.3"})
	if err != nil {
		t.Fatalf("goInstallArgs version: %v", err)
	}
	if !strings.Contains(strings.Join(installArgs, " "), "example.com/tool@v1.2.3") || installPackages[0] != "example.com/tool@v1.2.3" {
		t.Fatalf("versioned install args = %#v packages = %#v, want pkg@version", installArgs, installPackages)
	}
	emptyInstall := runGoResult(t, sys, InstallOp, map[string]any{})
	if emptyInstall.Status != operation.StatusFailed || emptyInstall.Error == nil || !strings.Contains(emptyInstall.Error.Message, "packages are required") {
		t.Fatalf("empty install result = %#v, want package validation", emptyInstall)
	}
	invalidInstall := runGoResult(t, sys, InstallOp, map[string]any{"packages": []string{"./cmd/tool"}, "env": map[string]string{"PATH": "/tmp"}})
	if invalidInstall.Status != operation.StatusFailed || invalidInstall.Error == nil || !strings.Contains(invalidInstall.Error.Message, "unsupported") {
		t.Fatalf("invalid install result = %#v, want rejected env", invalidInstall)
	}
	installBin := t.TempDir()
	realInstall := runGoOp(t, sys, InstallOp, map[string]any{"packages": []string{"./cmd/tool"}, "dry_run": falseValue, "env": map[string]string{"GOBIN": installBin}})
	if !goInstallResultFromRendered(t, realInstall).Installed {
		t.Fatalf("real install text = %q, want installed result", realInstall.Text)
	}

	getDryRun := runGoOp(t, sys, GetOp, map[string]any{"packages": []string{"example.com/dep@v1.2.3"}})
	getDryRunData := goGetResultFromRendered(t, getDryRun)
	if !getDryRunData.DryRun || getDryRunData.Changed || !strings.Contains(getDryRunData.Command, "go get example.com/dep@v1.2.3") {
		t.Fatalf("go get dry-run data = %#v text = %q, want preview command", getDryRunData, getDryRun.Text)
	}
	getArgs, getPackages, getDryRunFlag, err := goGetArgs(golang.GoGetQuery{Packages: []string{"example.com/dep@v1.2.3"}, DryRun: &falseValue})
	if err != nil {
		t.Fatalf("goGetArgs: %v", err)
	}
	if getDryRunFlag || strings.Join(getArgs, " ") != "get example.com/dep@v1.2.3" || getPackages[0] != "example.com/dep@v1.2.3" {
		t.Fatalf("goGetArgs = %#v packages=%#v dryRun=%v, want real go get args", getArgs, getPackages, getDryRunFlag)
	}
	emptyGet := runGoResult(t, sys, GetOp, map[string]any{})
	if emptyGet.Status != operation.StatusFailed || emptyGet.Error == nil || !strings.Contains(emptyGet.Error.Message, "packages are required") {
		t.Fatalf("empty get result = %#v, want package validation", emptyGet)
	}

	modTidyDryRun := runGoOp(t, sys, ModTidyOp, map[string]any{})
	modTidyDryRunData := goModTidyResultFromRendered(t, modTidyDryRun)
	if !modTidyDryRunData.DryRun || !strings.Contains(modTidyDryRunData.Command, "go mod tidy -diff") {
		t.Fatalf("go mod tidy dry-run data = %#v text = %q, want diff preview", modTidyDryRunData, modTidyDryRun.Text)
	}
	modTidyArgs, modTidyDryRunFlag, err := goModTidyArgs(golang.GoModTidyQuery{DryRun: &falseValue, Compat: "1.26", Go: "1.26", V: true})
	if err != nil {
		t.Fatalf("goModTidyArgs: %v", err)
	}
	if modTidyDryRunFlag || strings.Join(modTidyArgs, " ") != "mod tidy -compat=1.26 -go=1.26 -v" {
		t.Fatalf("goModTidyArgs = %#v dryRun=%v, want real tidy args", modTidyArgs, modTidyDryRunFlag)
	}

	buildBinary, err := sys.Process().Run(context.Background(), system.ProcessRequest{
		Command: "go",
		Args:    []string{"build", "-buildvcs=false", "-o", "toolbin", "./cmd/tool"},
		Env:     system.DefaultProcessEnv(),
	})
	if err != nil {
		t.Fatalf("build test binary: %v output=%#v", err, buildBinary)
	}
	version := runGoOp(t, sys, VersionOp, map[string]any{"files": []string{"toolbin"}, "module_info": true})
	if records := goVersionResultFromRendered(t, version).Records; len(records) != 1 || records[0].Path != "example.com/checks/cmd/tool" {
		t.Fatalf("go version records = %#v, want module build info for tool", records)
	}
}

func TestGoLanguageSupportIncludesDependencyToolchainOperations(t *testing.T) {
	support := LanguageSupport().SupportSpec()
	var found bool
	for _, set := range support.ToolchainOperationSets {
		if set.Name != ToolchainSet {
			continue
		}
		found = true
		refs := map[operation.Name]bool{}
		for _, ref := range set.Operations {
			refs[ref.Name] = true
		}
		for _, name := range []operation.Name{operation.Name(GetOp), operation.Name(ModTidyOp)} {
			if !refs[name] {
				t.Fatalf("toolchain set missing %s in %#v", name, set.Operations)
			}
		}
	}
	if !found {
		t.Fatalf("toolchain operation set %q not found", ToolchainSet)
	}
}

func TestGoPluginContributesPostEditFmtCheck(t *testing.T) {
	bundle, err := Plugin{}.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.PostEditChecks) != 1 {
		t.Fatalf("post-edit checks = %#v, want one Go formatter check", bundle.PostEditChecks)
	}
	check := bundle.PostEditChecks[0]
	if check.Name != "golang.fmt" || check.Operation.Name != operation.Name(FmtOp) {
		t.Fatalf("post-edit check = %#v, want golang.fmt using go_fmt", check)
	}
	if check.Mode != coresession.PostEditCheckModeFix {
		t.Fatalf("post-edit check mode = %q, want fix", check.Mode)
	}
}

func TestGoToolchainObserverAndAssertionDeriver(t *testing.T) {
	plugin := New(systemtest.NewMemory())
	observers, err := plugin.EnvironmentObservers(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("EnvironmentObservers: %v", err)
	}
	if len(observers) != 1 || observers[0].Spec().Name != ToolchainObserver {
		t.Fatalf("observers = %#v, want Go toolchain observer", observers)
	}
	observations, err := observers[0].Observe(context.Background(), runtimeevidence.ObservationRequest{Phase: coreevidence.PhaseSessionOpen})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(observations) != 1 || observations[0].Kind != ObservationToolchainStatus {
		t.Fatalf("observations = %#v, want toolchain status observation", observations)
	}

	derivers, err := plugin.AssertionDerivers(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("AssertionDerivers: %v", err)
	}
	assertions, err := derivers[0].Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{Observations: []coreevidence.Observation{{
		ID:      "toolchain:go",
		Kind:    ObservationToolchainStatus,
		Content: corelanguage.ToolchainStatus{ID: "go", Available: true, Version: "go version go1.26 linux/amd64"},
	}}})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 1 || assertions[0].Kind != AssertionToolchainAvailable || assertions[0].Target != "go" {
		t.Fatalf("assertions = %#v, want Go toolchain availability", assertions)
	}
	if assertions[0].Metadata["version"] == "" {
		t.Fatalf("assertion metadata = %#v, want version", assertions[0].Metadata)
	}
}

func TestGoImplementationsUsesTypeCheckedHostBackend(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	contract := `package api

import "context"

type Accessor interface {
	Open(context.Context) error
	Close() error
}
`
	impl := `package impl

import (
	ctx "context"
	"example.com/typeimpl/pkg/api"
)

type closer struct{}

func (closer) Close() error {
	return nil
}

type Provider struct {
	closer
}

func (*Provider) Open(ctx.Context) error {
	return nil
}

var _ api.Accessor = (*Provider)(nil)
`
	writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/typeimpl\n\ngo 1.26\n")
	writeGoFile(t, sys.Workspace(), "pkg/api/api.go", contract)
	writeGoFile(t, sys.Workspace(), "pkg/impl/impl.go", impl)

	line, column := goPosition(t, contract, "Accessor interface")
	rendered := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/api/api.go", "line": line, "column": column, "scope": "module"})
	implementations := implementationResultFromRendered(t, rendered)
	if implementations.ResolutionMode != "type_checked" {
		t.Fatalf("resolution mode = %q, want type_checked; text = %q", implementations.ResolutionMode, rendered.Text)
	}
	if !strings.Contains(rendered.Text, "pointer Provider implements Accessor") || !strings.Contains(rendered.Text, "matched: Close, Open") {
		t.Fatalf("type-checked implementation text = %q, want promoted method implementation match", rendered.Text)
	}
	if strings.Contains(rendered.Text, "AST-only implementations") {
		t.Fatalf("type-checked implementation text = %q, want no AST-only implementation warning", rendered.Text)
	}
}

func TestGoImplementationsTypeCheckedZeroMatchesDoNotFallbackToAST(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	contract := `package contract

type Runner interface {
	Run(int) error
}
`
	impl := `package impl

type BadRunner struct{}

func (BadRunner) Run(string) error {
	return nil
}
`
	writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/mismatch\n\ngo 1.26\n")
	writeGoFile(t, sys.Workspace(), "pkg/contract/contract.go", contract)
	writeGoFile(t, sys.Workspace(), "pkg/impl/impl.go", impl)

	line, column := goPosition(t, contract, "Runner interface")
	rendered := runGoOp(t, sys, ImplementationsOp, map[string]any{"path": "pkg/contract/contract.go", "line": line, "column": column, "scope": "module"})
	implementations := implementationResultFromRendered(t, rendered)
	if implementations.ResolutionMode != "type_checked" {
		t.Fatalf("resolution mode = %q, want type_checked; text = %q", implementations.ResolutionMode, rendered.Text)
	}
	if len(implementations.Matches) != 0 {
		t.Fatalf("matches = %#v, want none for incompatible method signatures", implementations.Matches)
	}
	if strings.Contains(rendered.Text, "BadRunner implements Runner") || !strings.Contains(rendered.Text, "no type-checked implementation matches") {
		t.Fatalf("type-checked zero-match text = %q, want no AST fallback false positive", rendered.Text)
	}
}

func TestGoCallOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runGoPluginBackends(t, func(t *testing.T, sys system.System) {
		service := `package service

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Run() {
	helper()
	s.Stop()
}

func (s *Service) Stop() {}

func helper() {}
`
		use := `package service

func Use() {
	svc := NewService()
	svc.Run()
	helper()
}

func Wrapper() {
	Use()
}
`
		testUse := `package service

func TestUse() {
	Use()
}
`
		externalTestUse := `package service_test

import "example.com/calls/pkg/service"

func TestExternalUse() {
	_ = service.NewService()
}
`
		consumer := `package consumer

import "example.com/calls/pkg/service"

func Cross() {
	_ = service.NewService()
}
`
		child := `package service

func Child() {
	_ = NewService
}
`
		writeGoFile(t, sys.Workspace(), "go.mod", "module example.com/calls\n\ngo 1.26\n")
		writeGoFile(t, sys.Workspace(), "pkg/service/service.go", service)
		writeGoFile(t, sys.Workspace(), "pkg/service/use.go", use)
		writeGoFile(t, sys.Workspace(), "pkg/service/use_test.go", testUse)
		writeGoFile(t, sys.Workspace(), "pkg/service/zz_external_test.go", externalTestUse)
		writeGoFile(t, sys.Workspace(), "pkg/consumer/consumer.go", consumer)
		writeGoFile(t, sys.Workspace(), "pkg/service/child/child.go", child)

		line, column := goPosition(t, service, "Run() {")
		callees := runGoOp(t, sys, CalleesOp, map[string]any{"path": "pkg/service/service.go", "line": line, "column": column})
		for _, want := range []string{"symbol: method Service.Run", "callee Service.Run -> helper", "callee Service.Run -> Service.Stop"} {
			if !strings.Contains(callees.Text, want) {
				t.Fatalf("callees text = %q, want %q", callees.Text, want)
			}
		}
		if !strings.Contains(callees.Text, "Warning: AST-only") {
			t.Fatalf("callees text = %q, want AST limitation warning", callees.Text)
		}

		line, column = goPosition(t, service, "NewService")
		packageCallers := runGoOp(t, sys, CallersOp, map[string]any{"path": "pkg/service/service.go", "line": line, "column": column, "include_tests": false})
		if !strings.Contains(packageCallers.Text, "caller Use -> NewService") || strings.Contains(packageCallers.Text, "Cross") || strings.Contains(packageCallers.Text, "child/child.go") || strings.Contains(packageCallers.Text, "use_test.go") {
			t.Fatalf("package callers text = %q, want same package non-test callers only", packageCallers.Text)
		}

		moduleCallers := runGoOp(t, sys, CallersOp, map[string]any{"path": "pkg/service/service.go", "line": line, "column": column, "scope": "module", "include_tests": false})
		if !strings.Contains(moduleCallers.Text, "caller Use -> NewService") || !strings.Contains(moduleCallers.Text, "caller Cross -> NewService") || strings.Contains(moduleCallers.Text, "child/child.go") {
			t.Fatalf("module callers text = %q, want package and module-local import callers", moduleCallers.Text)
		}
		moduleCallersWithDefaultTests := runGoOp(t, sys, CallersOp, map[string]any{"path": "pkg/service/service.go", "line": line, "column": column, "scope": "module"})
		if !strings.Contains(moduleCallersWithDefaultTests.Text, "caller Cross -> NewService") || !strings.Contains(moduleCallersWithDefaultTests.Text, "caller TestExternalUse -> NewService") {
			t.Fatalf("module callers with default tests text = %q, want production package ID preserved with external tests", moduleCallersWithDefaultTests.Text)
		}

		line, column = goPosition(t, consumer, "NewService")
		selectorCallers := runGoOp(t, sys, CallersOp, map[string]any{"path": "pkg/consumer/consumer.go", "line": line, "column": column, "scope": "module", "include_tests": false})
		if !strings.Contains(selectorCallers.Text, "symbol: function NewService") || !strings.Contains(selectorCallers.Text, "caller Cross -> NewService") {
			t.Fatalf("selector callers text = %q, want module-local selector to resolve to function", selectorCallers.Text)
		}

		line, column = goPosition(t, use, "Use() {")
		callersWithTests := runGoOp(t, sys, CallersOp, map[string]any{"path": "pkg/service/use.go", "line": line, "column": column, "include_tests": true})
		if !strings.Contains(callersWithTests.Text, "caller Wrapper -> Use") || !strings.Contains(callersWithTests.Text, "caller TestUse -> Use") {
			t.Fatalf("callers with tests text = %q, want production and test callers", callersWithTests.Text)
		}
		callersWithoutTests := runGoOp(t, sys, CallersOp, map[string]any{"path": "pkg/service/use.go", "line": line, "column": column, "include_tests": false})
		if strings.Contains(callersWithoutTests.Text, "TestUse") {
			t.Fatalf("callers without tests text = %q, want tests excluded", callersWithoutTests.Text)
		}

		invalid := runGoResult(t, sys, CallersOp, map[string]any{"path": "pkg/service/use.go", "line": 1, "column": 1, "scope": "workspace"})
		if invalid.Status != operation.StatusFailed || invalid.Error == nil || !strings.Contains(invalid.Error.Message, "unsupported call scope") {
			t.Fatalf("invalid callers result = %#v, want unsupported scope failure", invalid)
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

func importResultFromRendered(t *testing.T, rendered operation.Rendered) golang.ImportResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	imports, ok := data["imports"].(golang.ImportResult)
	if !ok {
		t.Fatalf("imports data = %#v, want golang.ImportResult", data["imports"])
	}
	return imports
}

func implementationResultFromRendered(t *testing.T, rendered operation.Rendered) golang.ImplementationResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	implementations, ok := data["implementations"].(golang.ImplementationResult)
	if !ok {
		t.Fatalf("implementations data = %#v, want golang.ImplementationResult", data["implementations"])
	}
	return implementations
}

func goInfoResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoInfoResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	info, ok := data["info"].(golang.GoInfoResult)
	if !ok {
		t.Fatalf("info data = %#v, want golang.GoInfoResult", data["info"])
	}
	return info
}

func goEnvResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoEnvResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	env, ok := data["env"].(golang.GoEnvResult)
	if !ok {
		t.Fatalf("env data = %#v, want golang.GoEnvResult", data["env"])
	}
	return env
}

func goVersionResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoVersionResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	version, ok := data["version"].(golang.GoVersionResult)
	if !ok {
		t.Fatalf("version data = %#v, want golang.GoVersionResult", data["version"])
	}
	return version
}

func goDocResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoDocResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	doc, ok := data["doc"].(golang.GoDocResult)
	if !ok {
		t.Fatalf("doc data = %#v, want golang.GoDocResult", data["doc"])
	}
	return doc
}

func goListResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoListResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	list, ok := data["list"].(golang.GoListResult)
	if !ok {
		t.Fatalf("list data = %#v, want golang.GoListResult", data["list"])
	}
	return list
}

func goTestResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoTestResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["test"].(golang.GoTestResult)
	if !ok {
		t.Fatalf("test data = %#v, want golang.GoTestResult", data["test"])
	}
	return result
}

func goVetResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoVetResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["vet"].(golang.GoVetResult)
	if !ok {
		t.Fatalf("vet data = %#v, want golang.GoVetResult", data["vet"])
	}
	return result
}

func goBuildResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoBuildResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["build"].(golang.GoBuildResult)
	if !ok {
		t.Fatalf("build data = %#v, want golang.GoBuildResult", data["build"])
	}
	return result
}

func goFmtResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoFmtResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["fmt"].(golang.GoFmtResult)
	if !ok {
		t.Fatalf("fmt data = %#v, want golang.GoFmtResult", data["fmt"])
	}
	return result
}

func goInstallResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoInstallResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["install"].(golang.GoInstallResult)
	if !ok {
		t.Fatalf("install data = %#v, want golang.GoInstallResult", data["install"])
	}
	return result
}

func goGetResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoGetResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["get"].(golang.GoGetResult)
	if !ok {
		t.Fatalf("get data = %#v, want golang.GoGetResult", data["get"])
	}
	return result
}

func goModTidyResultFromRendered(t *testing.T, rendered operation.Rendered) golang.GoModTidyResult {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("rendered data = %#v, want map", rendered.Data)
	}
	result, ok := data["mod_tidy"].(golang.GoModTidyResult)
	if !ok {
		t.Fatalf("mod_tidy data = %#v, want golang.GoModTidyResult", data["mod_tidy"])
	}
	return result
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
