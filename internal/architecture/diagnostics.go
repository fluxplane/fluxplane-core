package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DiagnosticBoundaryViolation     = "boundary_violation"
	DiagnosticTestBoundaryViolation = "test_boundary_violation"
	DiagnosticInnerHostIO           = "inner_host_io"
	DiagnosticCoreFilepath          = "core_filepath"
	DiagnosticRuntimeHostIO         = "runtime_host_io"
	DiagnosticPluginHostEffect      = "plugin_host_effect"
	DiagnosticUnknownPackage        = "unknown_package"

	SeverityError   = "error"
	SeverityWarning = "warning"
)

var inertHostIOImports = map[string]bool{
	"os":                     true,
	"os/exec":                true,
	"syscall":                true,
	"net":                    true,
	"net/http":               true,
	"net/url":                true,
	"database/sql":           true,
	"github.com/spf13/cobra": true,
}

var runtimeHostIOImports = map[string]bool{
	"os":            true,
	"os/exec":       true,
	"os/user":       true,
	"syscall":       true,
	"net":           true,
	"net/http":      true,
	"net/url":       true,
	"database/sql":  true,
	"path/filepath": true,
}

var runtimeHostIOAllowlist = map[string]string{
	"runtime/datasource/semantic": "semantic JSON store persists local index state",
	"runtime/evidence":            "runtime observers read host identity and environment evidence",
	"runtime/httptransport":       "runtime HTTP transport implementation owns concrete network behavior",
	"runtime/oauth2client":        "runtime OAuth2 client builds provider-neutral HTTP requests",
	"runtime/operation":           "operation runtime validates file-backed operation inputs and schemas",
	"runtime/secret":              "secret runtime implements file-backed secret storage",
	"runtime/sqlclient":           "SQL runtime owns database client integration",
	"runtime/system":              "system runtime is the central host side-effect boundary",
	"runtime/systemtest":          "test fixture package implements in-memory runtime system helpers",
}

var pluginHighRiskSymbols = map[string]map[string]bool{
	"net": {
		"Dial":        true,
		"DialTimeout": true,
	},
	"net/http": {
		"DefaultClient": true,
		"Get":           true,
		"Post":          true,
	},
	"os": {
		"Getenv":      true,
		"LookupEnv":   true,
		"ReadFile":    true,
		"Stat":        true,
		"UserHomeDir": true,
		"WriteFile":   true,
	},
	"os/exec": {
		"Command":        true,
		"CommandContext": true,
	},
}

func boundaryDiagnosticKind(testOnly bool) string {
	if testOnly {
		return DiagnosticTestBoundaryViolation
	}
	return DiagnosticBoundaryViolation
}

func boundaryDiagnosticSeverity(testOnly bool) string {
	if testOnly {
		return SeverityWarning
	}
	return SeverityError
}

func importDiagnostics(modulePath string, pkg ListedPackage, layer Layer) []Diagnostic {
	var out []Diagnostic
	shortPkg := strings.TrimPrefix(pkg.ImportPath, modulePath+"/")
	for _, importPath := range pkg.Imports {
		switch layer {
		case LayerCore, LayerSDK, LayerOrchestration:
			if inertHostIOImports[importPath] {
				out = append(out, Diagnostic{
					Kind:     DiagnosticInnerHostIO,
					Severity: SeverityError,
					Package:  pkg.ImportPath,
					Import:   importPath,
					Reason:   "core, sdk, and orchestration production code must not import host IO directly",
				})
			}
			if layer == LayerCore && importPath == "path/filepath" {
				out = append(out, Diagnostic{
					Kind:     DiagnosticCoreFilepath,
					Severity: SeverityWarning,
					Package:  pkg.ImportPath,
					Import:   importPath,
					Reason:   "core should prefer logical slash paths unless host filesystem paths are explicitly justified",
				})
			}
		case LayerRuntime:
			if runtimeHostIOImports[importPath] {
				reason, allowed := runtimeHostIOAllowlist[shortPkg]
				if !allowed {
					out = append(out, Diagnostic{
						Kind:     DiagnosticRuntimeHostIO,
						Severity: SeverityError,
						Package:  pkg.ImportPath,
						Import:   importPath,
						Reason:   "runtime host IO imports require an explicit package allowlist reason",
					})
				} else {
					out = append(out, Diagnostic{
						Kind:     DiagnosticRuntimeHostIO,
						Severity: SeverityWarning,
						Package:  pkg.ImportPath,
						Import:   importPath,
						Allowed:  true,
						Reason:   reason,
					})
				}
			}
		}
	}
	return out
}

func pluginEffectDiagnostics(modulePath string, pkg ListedPackage) []Diagnostic {
	if layerOf(modulePath, pkg.ImportPath) != LayerPlugins || pkg.Dir == "" {
		return nil
	}
	var out []Diagnostic
	for _, name := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			out = append(out, Diagnostic{
				Kind:     DiagnosticPluginHostEffect,
				Severity: SeverityError,
				Package:  pkg.ImportPath,
				File:     path,
				Reason:   "plugin source could not be parsed for host effect scanning: " + err.Error(),
			})
			continue
		}
		imports := importAliases(file)
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath := imports[ident.Name]
			if importPath == "" {
				return true
			}
			if !pluginHighRiskSymbols[importPath][selector.Sel.Name] {
				return true
			}
			out = append(out, Diagnostic{
				Kind:     DiagnosticPluginHostEffect,
				Severity: SeverityError,
				Package:  pkg.ImportPath,
				Import:   importPath,
				Symbol:   ident.Name + "." + selector.Sel.Name,
				File:     path,
				Reason:   "plugin host side effects must go through runtime/system.System unless explicitly allowed",
			})
			return true
		})
	}
	return out
}

func importAliases(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		if spec.Name != nil {
			if spec.Name.Name == "." || spec.Name.Name == "_" {
				continue
			}
			out[spec.Name.Name] = importPath
			continue
		}
		_, name := filepath.Split(importPath)
		out[name] = importPath
	}
	return out
}

func unknownPackageAllowed(modulePath, importPath string) bool {
	if importPath == modulePath {
		return true
	}
	shortPkg := strings.TrimPrefix(importPath, modulePath+"/")
	return shortPkg == "internal/architecture"
}

func scoreReport(diagnostics []Diagnostic, couplingPenalty int, includeTests bool) Scores {
	productionViolations := countDiagnostics(diagnostics, DiagnosticBoundaryViolation, false)
	testViolations := countDiagnostics(diagnostics, DiagnosticTestBoundaryViolation, false)
	sideEffectErrors := countSideEffectErrors(diagnostics)
	coverageErrors := countDiagnostics(diagnostics, DiagnosticUnknownPackage, false)

	scores := Scores{
		Boundary:     100 - minInt(100, productionViolations*25),
		TestBoundary: 100,
		Coupling:     100 - minInt(40, couplingPenalty),
		SideEffect:   100 - minInt(60, sideEffectErrors*10),
		Coverage:     100 - minInt(100, coverageErrors*20),
	}
	if includeTests {
		scores.TestBoundary = 100 - minInt(100, testViolations*10)
	}
	if scores.Boundary < 100 {
		scores.Overall = scores.Boundary
		return scores
	}
	softImpact := ceilDiv(100-scores.Coupling, 10) + ceilDiv(100-scores.SideEffect, 20) + ceilDiv(100-scores.Coverage, 20)
	if includeTests {
		softImpact += ceilDiv(100-scores.TestBoundary, 20)
	}
	scores.Overall = 100 - minInt(10, softImpact)
	return scores
}

func countDiagnostics(diagnostics []Diagnostic, kind string, includeAllowed bool) int {
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == kind && (includeAllowed || !diagnostic.Allowed) {
			count++
		}
	}
	return count
}

func countSideEffectErrors(diagnostics []Diagnostic) int {
	count := 0
	for _, diagnostic := range diagnostics {
		switch diagnostic.Kind {
		case DiagnosticInnerHostIO, DiagnosticRuntimeHostIO, DiagnosticPluginHostEffect:
			if diagnostic.Severity == SeverityError && !diagnostic.Allowed {
				count++
			}
		}
	}
	return count
}

func sortDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	out := append([]Diagnostic(nil), diagnostics...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		if out[i].Import != out[j].Import {
			return out[i].Import < out[j].Import
		}
		if out[i].Symbol != out[j].Symbol {
			return out[i].Symbol < out[j].Symbol
		}
		return out[i].File < out[j].File
	})
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ceilDiv(n, d int) int {
	if n <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
