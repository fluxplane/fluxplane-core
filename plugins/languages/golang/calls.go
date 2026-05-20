package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/language/golang"
	"github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const callResolutionWarning = "AST-only calls: direct calls only; no type checking, interface dispatch, function values, reflection, build-tag/cgo semantics, or external dependency resolution."

type callIndex struct {
	functions           map[string]language.Symbol
	methods             map[string]language.Symbol
	importPathPackageID map[string]string
}

func (p Plugin) goCallers() operationruntime.TypedResultHandler[golang.CallQuery, operation.Rendered] {
	return p.goCalls(true)
}

func (p Plugin) goCallees() operationruntime.TypedResultHandler[golang.CallQuery, operation.Rendered] {
	return p.goCalls(false)
}

func (p Plugin) goCalls(callers bool) operationruntime.TypedResultHandler[golang.CallQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.CallQuery) operation.Result {
		if err := validateCallQuery(req); err != nil {
			return operation.Failed("invalid_go_calls_input", err.Error(), nil)
		}
		nav, err := p.resolveNavigation(ctx, callNavigationQuery(req), true)
		if err != nil {
			return operation.Failed("go_calls_failed", err.Error(), nil)
		}
		result := golang.CallResult{
			Target:         nav.Target,
			Diagnostics:    nav.Diagnostics,
			ResolutionMode: "ast",
			Complete:       false,
			Warnings:       append(append([]string{}, nav.Warnings...), callResolutionWarning),
		}
		selected, err := p.parseGoSource(ctx, cleanRel(req.Path), maxBytes(req.MaxBytes))
		if err != nil {
			return operation.Failed("go_calls_failed", err.Error(), nil)
		}
		files, diagnostics := p.callFiles(ctx, selected, req)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		index := p.indexCalls(ctx, files)
		symbol := firstCallable(nav.Symbols)
		if symbol == nil {
			pos, posErr := navigationPosition(selected, callNavigationQuery(req))
			if posErr != nil {
				return operation.Failed("go_calls_failed", posErr.Error(), nil)
			}
			if resolved, ok := callableAtPosition(selected, index, pos); ok {
				symbol = &resolved
			}
		}
		if symbol == nil {
			result.Diagnostics = append(result.Diagnostics, language.Diagnostic{Severity: "info", Code: "unsupported_symbol", Message: "call hierarchy supports functions and methods", Target: nav.Target.Name, Line: nav.Target.Location.Range.Start.Line})
			lines := renderCalls(callTitle(callers), result, callers)
			return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"calls": result, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
		}
		result.Symbol = *symbol

		edges, truncated := collectCallEdges(files, index, *symbol, callers, maxResults(req.MaxResults))
		if callers {
			result.Callers = edges
		} else {
			result.Callees = edges
		}
		if truncated {
			result.Diagnostics = append(result.Diagnostics, language.Diagnostic{Severity: "info", Code: "max_results", Message: "call results reached max_results limit"})
		}
		lines := renderCalls(callTitle(callers), result, callers)
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"calls": result, "symbol": compactSymbol(result.Symbol, false), "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
	}
}

func validateCallQuery(req golang.CallQuery) error {
	if err := validateGoLanguage(req.Language); err != nil {
		return err
	}
	if strings.TrimSpace(req.Path) == "" {
		return fmt.Errorf("path is required")
	}
	if !strings.HasSuffix(cleanRel(req.Path), ".go") {
		return fmt.Errorf("path must be a Go source file")
	}
	if req.Offset == nil && (req.Line <= 0 || req.Column <= 0) {
		return fmt.Errorf("line and column are required unless offset is set")
	}
	switch req.Scope {
	case "", golang.CallScopeFile, golang.CallScopePackage, golang.CallScopeModule:
		return nil
	default:
		return fmt.Errorf("unsupported call scope %q", req.Scope)
	}
}

func callNavigationQuery(req golang.CallQuery) golang.NavigationQuery {
	return golang.NavigationQuery{
		Language:   req.Language,
		Path:       req.Path,
		Line:       req.Line,
		Column:     req.Column,
		Offset:     req.Offset,
		Scope:      golang.NavigationScopePackage,
		MaxResults: 1,
		MaxBytes:   req.MaxBytes,
		Refresh:    req.Refresh,
	}
}

func firstCallable(symbols []language.Symbol) *language.Symbol {
	for i := range symbols {
		if symbols[i].Kind == language.SymbolFunction || symbols[i].Kind == language.SymbolMethod {
			return &symbols[i]
		}
	}
	return nil
}

func (p Plugin) callFiles(ctx context.Context, selected parsedGoFile, req golang.CallQuery) ([]parsedGoFile, []language.Diagnostic) {
	includeTests := includeReferenceTests(req.IncludeTests)
	switch req.Scope {
	case golang.CallScopeFile:
		return []parsedGoFile{selected}, nil
	case golang.CallScopeModule:
		root, modulePath := p.nearestModule(ctx, selected.rel)
		if modulePath == "" {
			root = pathDir(selected.rel)
		}
		if root == "" {
			root = "."
		}
		files, err := p.goFilesForPath(ctx, root)
		if err != nil {
			return []parsedGoFile{selected}, []language.Diagnostic{diagnostic(root, err)}
		}
		return p.parseImplementationFiles(ctx, selected, files, includeTests, req.MaxBytes)
	default:
		files, diagnostics := p.navigationFiles(ctx, selected, golang.NavigationQuery{Path: selected.rel, Scope: golang.NavigationScopePackage, MaxBytes: req.MaxBytes})
		files = filterReferencePackageFiles(files, selected)
		files = filterImplementationTestFiles(files, includeTests, selected.rel)
		return files, diagnostics
	}
}

func (p Plugin) indexCalls(ctx context.Context, files []parsedGoFile) callIndex {
	index := callIndex{
		functions:           map[string]language.Symbol{},
		methods:             map[string]language.Symbol{},
		importPathPackageID: map[string]string{},
	}
	for _, file := range files {
		if importPath := p.packageImportPath(ctx, pathDir(file.rel)); importPath != "" {
			if index.importPathPackageID[importPath] == "" || !strings.HasSuffix(file.file.Name.Name, "_test") {
				index.importPathPackageID[importPath] = file.pkgID
			}
		}
		for _, symbol := range flattenSymbols(fileSymbols(file, false, 0)) {
			switch symbol.Kind {
			case language.SymbolFunction:
				index.functions[callFunctionKey(symbol.PackageID, symbol.Name)] = symbol
			case language.SymbolMethod:
				if key := callMethodKey(symbol.PackageID, symbol.Container, bareSymbolName(symbol)); key != "" {
					index.methods[key] = symbol
				}
			}
		}
	}
	return index
}

func (p Plugin) packageImportPath(ctx context.Context, dir string) string {
	dir = cleanRel(dir)
	moduleDir, modulePath := p.nearestModule(ctx, dir)
	if modulePath == "" {
		return ""
	}
	if moduleDir == "" {
		if dir == "" {
			return modulePath
		}
		return modulePath + "/" + dir
	}
	if dir == moduleDir {
		return modulePath
	}
	prefix := moduleDir + "/"
	if strings.HasPrefix(dir, prefix) {
		return modulePath + "/" + strings.TrimPrefix(dir, prefix)
	}
	return ""
}

func collectCallEdges(files []parsedGoFile, index callIndex, selected language.Symbol, callers bool, limit int) ([]golang.CallEdge, bool) {
	var edges []golang.CallEdge
	for _, file := range files {
		edges = append(edges, fileCallEdges(file, index)...)
	}
	edges = uniqueCallEdges(edges)
	sortCallEdges(edges)

	var out []golang.CallEdge
	for _, edge := range edges {
		if callers && edge.CalleeID != selected.ID {
			continue
		}
		if !callers && edge.CallerID != selected.ID {
			continue
		}
		if len(out) >= limit {
			return out, true
		}
		out = append(out, edge)
	}
	return out, false
}

func fileCallEdges(parsed parsedGoFile, index callIndex) []golang.CallEdge {
	var edges []golang.CallEdge
	for _, decl := range parsed.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		caller := funcDeclSymbol(parsed.fset, parsed.rel, parsed.pkgID, false, 0, fn)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncLit:
				return false
			case *ast.CallExpr:
				callee, loc, name, ok := resolveCallExpr(parsed, index, n)
				if !ok {
					return true
				}
				edges = append(edges, golang.CallEdge{
					CallerID: caller.ID,
					CalleeID: callee.ID,
					Caller:   caller,
					Callee:   callee,
					Name:     name,
					Kind:     "direct",
					Location: loc,
					Preview:  linePreview(parsed.data, loc.Range.Start.Line),
				})
			}
			return true
		})
	}
	return edges
}

func callableAtPosition(parsed parsedGoFile, index callIndex, pos token.Pos) (language.Symbol, bool) {
	var symbol language.Symbol
	var ok bool
	ast.Inspect(parsed.file, func(node ast.Node) bool {
		if node == nil || ok || !containsToken(node.Pos(), node.End(), pos) {
			return true
		}
		call, isCall := node.(*ast.CallExpr)
		if !isCall || !containsToken(call.Fun.Pos(), call.Fun.End(), pos) {
			return true
		}
		symbol, _, _, ok = resolveCallExpr(parsed, index, call)
		return false
	})
	return symbol, ok
}

func resolveCallExpr(parsed parsedGoFile, index callIndex, call *ast.CallExpr) (language.Symbol, language.Location, string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		symbol, ok := index.functions[callFunctionKey(parsed.pkgID, fun.Name)]
		return symbol, location(parsed.fset, parsed.rel, fun.Pos(), fun.End()), fun.Name, ok
	case *ast.SelectorExpr:
		if fun.Sel == nil {
			return language.Symbol{}, language.Location{}, "", false
		}
		name := fun.Sel.Name
		x, ok := fun.X.(*ast.Ident)
		if !ok {
			return language.Symbol{}, language.Location{}, "", false
		}
		if importPath, ok := importedPath(parsed, x.Name); ok {
			pkgID := index.importPathPackageID[importPath]
			if pkgID == "" {
				return language.Symbol{}, language.Location{}, "", false
			}
			symbol, ok := index.functions[callFunctionKey(pkgID, name)]
			return symbol, location(parsed.fset, parsed.rel, fun.Sel.Pos(), fun.Sel.End()), x.Name + "." + name, ok
		}
		receiverType := normalizeGoType(x.Name)
		if inferred := localInferredType(parsed, x.Name, fun.Pos()); inferred != "" {
			receiverType = inferred
		}
		symbol, ok := index.methods[callMethodKey(parsed.pkgID, receiverType, name)]
		return symbol, location(parsed.fset, parsed.rel, fun.Sel.Pos(), fun.Sel.End()), x.Name + "." + name, ok
	default:
		return language.Symbol{}, language.Location{}, "", false
	}
}

func importedPath(parsed parsedGoFile, name string) (string, bool) {
	for _, imp := range parsed.file.Imports {
		if importName(imp) == name {
			return strings.Trim(imp.Path.Value, `"`), true
		}
	}
	return "", false
}

func callFunctionKey(packageID, name string) string {
	return packageID + "\x00" + name
}

func callMethodKey(packageID, container, name string) string {
	container = normalizeGoType(container)
	if container == "" || name == "" {
		return ""
	}
	return packageID + "\x00" + container + "." + name
}

func uniqueCallEdges(edges []golang.CallEdge) []golang.CallEdge {
	seen := map[string]bool{}
	out := make([]golang.CallEdge, 0, len(edges))
	for _, edge := range edges {
		key := fmt.Sprintf("%s:%s:%s:%d:%d", edge.CallerID, edge.CalleeID, edge.Location.Path, edge.Location.Range.Start.Line, edge.Location.Range.Start.Column)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, edge)
	}
	return out
}

func sortCallEdges(edges []golang.CallEdge) {
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].Location.Path == edges[j].Location.Path {
			if edges[i].Location.Range.Start.Line == edges[j].Location.Range.Start.Line {
				return edges[i].Location.Range.Start.Column < edges[j].Location.Range.Start.Column
			}
			return edges[i].Location.Range.Start.Line < edges[j].Location.Range.Start.Line
		}
		return edges[i].Location.Path < edges[j].Location.Path
	})
}

func callTitle(callers bool) string {
	if callers {
		return "Go callers"
	}
	return "Go callees"
}

func renderCalls(title string, result golang.CallResult, callers bool) []string {
	target := result.Target.Name
	if target == "" {
		target = result.Target.Text
	}
	if target == "" {
		target = "position"
	}
	lines := []string{fmt.Sprintf("%s: %s", title, target)}
	if result.Symbol.Name != "" {
		lines = append(lines, fmt.Sprintf("- symbol: %s %s %s:%d", result.Symbol.Kind, result.Symbol.Name, result.Symbol.Location.Path, result.Symbol.Location.Range.Start.Line))
	}
	edges := result.Callees
	if callers {
		edges = result.Callers
	}
	for _, edge := range edges {
		if callers {
			lines = append(lines, fmt.Sprintf("- caller %s -> %s at %s:%d:%d", edge.Caller.Name, edge.Callee.Name, edge.Location.Path, edge.Location.Range.Start.Line, edge.Location.Range.Start.Column))
		} else {
			lines = append(lines, fmt.Sprintf("- callee %s -> %s at %s:%d:%d", edge.Caller.Name, edge.Callee.Name, edge.Location.Path, edge.Location.Range.Start.Line, edge.Location.Range.Start.Column))
		}
		if edge.Preview != "" {
			lines = append(lines, "  "+edge.Preview)
		}
	}
	if len(edges) == 0 {
		lines = append(lines, "- no AST-level direct calls found")
	}
	for _, diag := range result.Diagnostics {
		lines = append(lines, fmt.Sprintf("Diagnostic: %s %s %s", diag.Severity, diag.Code, diag.Message))
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warning: "+strings.Join(result.Warnings, " "))
	}
	return lines
}
