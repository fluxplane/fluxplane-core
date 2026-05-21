package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/fluxplane/engine/core/language"
	"github.com/fluxplane/engine/core/language/golang"
	"github.com/fluxplane/engine/core/operation"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
)

const astResolutionWarning = "AST-only resolution: no type checking, external dependency resolution, build-tag/cgo semantics, interface dispatch, or function-value dispatch."

type parsedGoFile struct {
	rel   string
	fset  *token.FileSet
	file  *ast.File
	data  []byte
	pkgID string
}

type navigationContext struct {
	selected     parsedGoFile
	position     token.Pos
	target       golang.NavigationTarget
	targetIdent  *ast.Ident
	targetImport *ast.ImportSpec
	enclosing    *language.Symbol
	symbols      []language.Symbol
	imports      map[string]language.Symbol
	diagnostics  []language.Diagnostic
	warnings     []string
	maxResults   int
}

func (p Plugin) goDefinition() operationruntime.TypedResultHandler[golang.NavigationQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.NavigationQuery) operation.Result {
		result, err := p.resolveNavigation(ctx, req, false)
		if err != nil {
			return operation.Failed("go_definition_failed", err.Error(), nil)
		}
		lines := renderNavigation("Go definition", result, req.IncludeDocs)
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"navigation": result, "symbols": compactSymbols(result.Symbols, req.IncludeDocs), "locations": result.Locations, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
	}
}

func (p Plugin) goSymbolInfo() operationruntime.TypedResultHandler[golang.NavigationQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.NavigationQuery) operation.Result {
		result, err := p.resolveNavigation(ctx, req, true)
		if err != nil {
			return operation.Failed("go_symbol_info_failed", err.Error(), nil)
		}
		lines := renderNavigation("Go symbol info", result, req.IncludeDocs)
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"navigation": result, "symbols": compactSymbols(result.Symbols, req.IncludeDocs), "locations": result.Locations, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
	}
}

func (p Plugin) resolveNavigation(ctx context.Context, req golang.NavigationQuery, fallbackEnclosing bool) (golang.NavigationResult, error) {
	if err := validateNavigationQuery(req); err != nil {
		return golang.NavigationResult{}, err
	}
	rel := cleanRel(req.Path)
	if isVendoredPath(rel) {
		return golang.NavigationResult{}, fmt.Errorf("path is vendored and excluded from Go navigation")
	}
	selected, err := p.parseGoSource(ctx, rel, maxBytes(req.MaxBytes))
	if err != nil {
		return golang.NavigationResult{}, err
	}
	pos, err := navigationPosition(selected, req)
	if err != nil {
		return golang.NavigationResult{}, err
	}
	nav := navigationContext{
		selected:   selected,
		position:   pos,
		imports:    map[string]language.Symbol{},
		warnings:   []string{astResolutionWarning},
		maxResults: maxResults(req.MaxResults),
	}
	nav.target, nav.targetIdent, nav.targetImport = selected.navigationTarget(pos)
	nav.enclosing = enclosingSymbol(selected, pos, req.IncludeDocs, maxDocBytes(req.MaxBytes))
	if nav.enclosing != nil {
		nav.target.EnclosingSymbol = nav.enclosing
	}
	parsedFiles, diagnostics := p.navigationFiles(ctx, selected, req)
	nav.diagnostics = append(nav.diagnostics, diagnostics...)
	nav.indexSymbols(parsedFiles, req.IncludeDocs, maxDocBytes(req.MaxBytes))

	if symbol, ok := nav.resolveSelectedImport(); ok {
		return nav.result([]language.Symbol{symbol}), nil
	}
	if symbol, ok := nav.resolvePackageName(); ok {
		return nav.result([]language.Symbol{symbol}), nil
	}
	if symbol, ok, terminal := nav.resolveSelectedIdentifier(); ok {
		return nav.result([]language.Symbol{symbol}), nil
	} else if terminal {
		return nav.result(nil), nil
	}
	if fallbackEnclosing && nav.enclosing != nil {
		nav.diagnostics = append(nav.diagnostics, language.Diagnostic{Path: rel, Severity: "info", Code: "enclosing_symbol", Message: "no identifier definition resolved; returned enclosing declaration", Line: nav.target.Location.Range.Start.Line})
		return nav.result([]language.Symbol{*nav.enclosing}), nil
	}
	name := nav.target.Name
	if name == "" {
		name = nav.target.Text
	}
	nav.diagnostics = append(nav.diagnostics, language.Diagnostic{Path: rel, Severity: "warning", Code: "unresolved_identifier", Message: "no AST-level definition found for selected position", Target: name, Line: nav.target.Location.Range.Start.Line})
	return nav.result(nil), nil
}

func validateNavigationQuery(req golang.NavigationQuery) error {
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
	case "", golang.NavigationScopePackage, golang.NavigationScopeFile:
		return nil
	default:
		return fmt.Errorf("unsupported navigation scope %q", req.Scope)
	}
}

func (p Plugin) parseGoSource(ctx context.Context, rel string, readLimit int) (parsedGoFile, error) {
	if info, _, err := p.system.Workspace().Stat(ctx, rel); err != nil {
		return parsedGoFile{}, err
	} else if info.IsDir() {
		return parsedGoFile{}, fmt.Errorf("path must be a Go source file")
	}
	data, truncated, _, err := p.system.Workspace().ReadFile(ctx, rel, int64(readLimit))
	if err != nil {
		return parsedGoFile{}, err
	}
	if truncated {
		return parsedGoFile{}, fmt.Errorf("source file exceeds parser byte limit (%d bytes)", readLimit)
	}
	mode := parser.ParseComments
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, mode)
	if err != nil {
		return parsedGoFile{}, err
	}
	return parsedGoFile{rel: rel, fset: fset, file: file, data: data, pkgID: packageID(pathDir(rel), file.Name.Name)}, nil
}

func navigationPosition(parsed parsedGoFile, req golang.NavigationQuery) (token.Pos, error) {
	file := parsed.fset.File(parsed.file.Package)
	if file == nil {
		return token.NoPos, fmt.Errorf("source position metadata is unavailable")
	}
	offset := 0
	if req.Offset != nil {
		offset = *req.Offset
	} else {
		var err error
		offset, err = offsetForLineColumn(parsed.data, req.Line, req.Column)
		if err != nil {
			return token.NoPos, err
		}
	}
	if offset < 0 || offset > file.Size() {
		return token.NoPos, fmt.Errorf("offset %d is outside file bounds", offset)
	}
	return file.Pos(offset), nil
}

func offsetForLineColumn(data []byte, line, column int) (int, error) {
	if line <= 0 || column <= 0 {
		return 0, fmt.Errorf("line and column must be positive")
	}
	currentLine := 1
	lineStart := 0
	for i, b := range data {
		if currentLine == line {
			lineEnd := i
			for lineEnd < len(data) && data[lineEnd] != '\n' {
				lineEnd++
			}
			if column > lineEnd-lineStart+1 {
				return 0, fmt.Errorf("column %d is outside line %d", column, line)
			}
			return lineStart + column - 1, nil
		}
		if b == '\n' {
			currentLine++
			lineStart = i + 1
		}
	}
	if currentLine == line {
		if column > len(data)-lineStart+1 {
			return 0, fmt.Errorf("column %d is outside line %d", column, line)
		}
		return lineStart + column - 1, nil
	}
	return 0, fmt.Errorf("line %d is outside file bounds", line)
}

func (f parsedGoFile) navigationTarget(pos token.Pos) (golang.NavigationTarget, *ast.Ident, *ast.ImportSpec) {
	if imp := importAt(f.file, pos); imp != nil {
		target := golang.NavigationTarget{
			Text:      strings.Trim(imp.Path.Value, `"`),
			Name:      importName(imp),
			NodeKind:  "import",
			PackageID: f.pkgID,
			Location:  location(f.fset, f.rel, imp.Pos(), imp.End()),
		}
		return target, nil, imp
	}
	if f.file.Name != nil && containsToken(f.file.Name.Pos(), f.file.Name.End(), pos) {
		target := golang.NavigationTarget{
			Text:      f.file.Name.Name,
			Name:      f.file.Name.Name,
			NodeKind:  "package",
			PackageID: f.pkgID,
			Location:  location(f.fset, f.rel, f.file.Name.Pos(), f.file.Name.End()),
		}
		return target, f.file.Name, nil
	}
	ident := identAt(f.file, pos)
	if ident == nil {
		target := golang.NavigationTarget{
			NodeKind:  "position",
			PackageID: f.pkgID,
			Location:  location(f.fset, f.rel, pos, pos),
		}
		return target, nil, nil
	}
	target := golang.NavigationTarget{
		Text:      ident.Name,
		Name:      ident.Name,
		NodeKind:  "ident",
		PackageID: f.pkgID,
		Location:  location(f.fset, f.rel, ident.Pos(), ident.End()),
	}
	return target, ident, nil
}

func (p Plugin) navigationFiles(ctx context.Context, selected parsedGoFile, req golang.NavigationQuery) ([]parsedGoFile, []language.Diagnostic) {
	files := []string{selected.rel}
	if req.Scope == "" || req.Scope == golang.NavigationScopePackage {
		if scoped, err := p.goFilesForPath(ctx, pathDir(selected.rel)); err == nil {
			files = scoped
		}
	}
	var out []parsedGoFile
	var diagnostics []language.Diagnostic
	seen := map[string]bool{}
	for _, rel := range files {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if rel == selected.rel {
			out = append(out, selected)
			continue
		}
		parsed, err := p.parseGoSource(ctx, rel, maxBytes(req.MaxBytes))
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(rel, err))
			continue
		}
		if parsed.file.Name.Name != selected.file.Name.Name {
			continue
		}
		out = append(out, parsed)
	}
	return out, diagnostics
}

func (n *navigationContext) indexSymbols(files []parsedGoFile, includeDocs bool, docLimit int) {
	for _, parsed := range files {
		for _, symbol := range fileSymbols(parsed, includeDocs, docLimit) {
			n.symbols = append(n.symbols, flattenSymbols([]language.Symbol{symbol})...)
		}
		for _, imp := range parsed.file.Imports {
			symbol := importSymbol(parsed, imp)
			n.imports[symbol.Name] = symbol
		}
	}
	sort.SliceStable(n.symbols, func(i, j int) bool {
		if n.symbols[i].Location.Path == n.symbols[j].Location.Path {
			return n.symbols[i].Location.Range.Start.Line < n.symbols[j].Location.Range.Start.Line
		}
		return n.symbols[i].Location.Path < n.symbols[j].Location.Path
	})
}

func (n navigationContext) resolveSelectedImport() (language.Symbol, bool) {
	if n.targetImport == nil {
		return language.Symbol{}, false
	}
	return importSymbol(n.selected, n.targetImport), true
}

func (n navigationContext) resolvePackageName() (language.Symbol, bool) {
	if n.targetIdent == nil || n.targetIdent != n.selected.file.Name {
		return language.Symbol{}, false
	}
	return language.Symbol{
		ID:             symbolID(n.selected.rel, language.SymbolPackage, n.selected.file.Name.Name, n.selected.file.Name.Pos()),
		Language:       language.LanguageGo,
		Kind:           language.SymbolPackage,
		Name:           n.selected.file.Name.Name,
		PackageID:      n.selected.pkgID,
		Location:       location(n.selected.fset, n.selected.rel, n.selected.file.Name.Pos(), n.selected.file.Name.End()),
		Range:          location(n.selected.fset, n.selected.rel, n.selected.file.Name.Pos(), n.selected.file.Name.End()).Range,
		SelectionRange: location(n.selected.fset, n.selected.rel, n.selected.file.Name.Pos(), n.selected.file.Name.End()).Range,
		Signature:      "package " + n.selected.file.Name.Name,
	}, true
}

func (n *navigationContext) resolveSelectedIdentifier() (language.Symbol, bool, bool) {
	if n.targetIdent == nil {
		return language.Symbol{}, false, false
	}
	if selector := selectorForIdent(n.selected.file, n.targetIdent); selector != nil && selector.Sel == n.targetIdent {
		if symbol, ok, terminal := n.resolveSelector(selector); ok {
			return symbol, true, true
		} else if terminal {
			return language.Symbol{}, false, true
		}
	}
	if symbol, ok := n.resolveLocalIdentifier(n.targetIdent); ok {
		return symbol, true, true
	}
	if symbol, ok := n.imports[n.targetIdent.Name]; ok {
		return symbol, true, true
	}
	if symbol, ok := n.findSymbol(n.targetIdent.Name); ok {
		return symbol, true, true
	}
	return language.Symbol{}, false, false
}

func (n *navigationContext) resolveSelector(selector *ast.SelectorExpr) (language.Symbol, bool, bool) {
	xIdent, ok := selector.X.(*ast.Ident)
	if !ok {
		return language.Symbol{}, false, false
	}
	if _, ok := n.imports[xIdent.Name]; ok {
		n.diagnostics = append(n.diagnostics, language.Diagnostic{
			Path:     n.selected.rel,
			Severity: "warning",
			Code:     "external_selector",
			Message:  "selector belongs to an imported package; external definitions are not resolved by AST-only navigation",
			Target:   xIdent.Name + "." + selector.Sel.Name,
			Line:     n.selected.fset.Position(selector.Sel.Pos()).Line,
		})
		return language.Symbol{}, false, true
	}
	receiverType := normalizeGoType(xIdent.Name)
	if inferred := localInferredType(n.selected, xIdent.Name, selector.Pos()); inferred != "" {
		receiverType = inferred
	}
	for _, name := range []string{receiverType + "." + selector.Sel.Name, "*" + receiverType + "." + selector.Sel.Name} {
		if symbol, ok := n.findSymbol(name); ok {
			return symbol, true, true
		}
	}
	for _, symbol := range n.symbols {
		if symbol.Kind == language.SymbolField && symbol.Container == receiverType && symbol.Name == selector.Sel.Name {
			return symbol, true, true
		}
	}
	return language.Symbol{}, false, false
}

func (n navigationContext) resolveLocalIdentifier(ident *ast.Ident) (language.Symbol, bool) {
	return localSymbolForIdent(n.selected, ident)
}

func (n navigationContext) findSymbol(name string) (language.Symbol, bool) {
	var matches []language.Symbol
	for _, symbol := range n.symbols {
		if symbol.Name == name || bareSymbolName(symbol) == name {
			matches = append(matches, symbol)
		}
	}
	if len(matches) == 0 {
		return language.Symbol{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Location.Path == matches[j].Location.Path {
			return matches[i].Location.Range.Start.Line < matches[j].Location.Range.Start.Line
		}
		return matches[i].Location.Path < matches[j].Location.Path
	})
	return matches[0], true
}

func (n navigationContext) result(symbols []language.Symbol) golang.NavigationResult {
	if len(symbols) > n.maxResults {
		symbols = symbols[:n.maxResults]
	}
	locations := make([]language.Location, 0, len(symbols))
	for _, symbol := range symbols {
		locations = append(locations, symbol.Location)
	}
	return golang.NavigationResult{
		Target:         n.target,
		Symbols:        symbols,
		Locations:      locations,
		Diagnostics:    n.diagnostics,
		ResolutionMode: "ast",
		Complete:       false,
		Warnings:       n.warnings,
	}
}

func fileSymbols(parsed parsedGoFile, includeDocs bool, docLimit int) []language.Symbol {
	pkgID := packageID(pathDir(parsed.rel), parsed.file.Name.Name)
	var symbols []language.Symbol
	for _, decl := range parsed.file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			symbols = append(symbols, funcDeclSymbol(parsed.fset, parsed.rel, pkgID, includeDocs, docLimit, d))
		case *ast.GenDecl:
			symbols = append(symbols, genDeclSymbols(parsed.fset, parsed.rel, pkgID, includeDocs, docLimit, d)...)
		}
	}
	return symbols
}

func funcDeclSymbol(fset *token.FileSet, rel, pkgID string, includeDocs bool, docLimit int, decl *ast.FuncDecl) language.Symbol {
	kind := language.SymbolFunction
	container := ""
	name := decl.Name.Name
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		kind = language.SymbolMethod
		container = exprString(fset, decl.Recv.List[0].Type)
		name = strings.TrimPrefix(container, "*") + "." + decl.Name.Name
	}
	return language.Symbol{
		ID:             symbolID(rel, kind, name, decl.Pos()),
		Language:       language.LanguageGo,
		Kind:           kind,
		Name:           name,
		Container:      container,
		PackageID:      pkgID,
		Location:       location(fset, rel, decl.Pos(), decl.End()),
		Range:          location(fset, rel, decl.Pos(), decl.End()).Range,
		SelectionRange: location(fset, rel, decl.Name.Pos(), decl.Name.End()).Range,
		Signature:      funcSignature(fset, decl),
		Doc:            docText(includeDocs, decl.Doc, docLimit),
	}
}

func importSymbol(parsed parsedGoFile, imp *ast.ImportSpec) language.Symbol {
	name := importName(imp)
	importPath := strings.Trim(imp.Path.Value, `"`)
	return language.Symbol{
		ID:             symbolID(parsed.rel, language.SymbolImport, importPath, imp.Pos()),
		Language:       language.LanguageGo,
		Kind:           language.SymbolImport,
		Name:           name,
		PackageID:      parsed.pkgID,
		Location:       location(parsed.fset, parsed.rel, imp.Pos(), imp.End()),
		Range:          location(parsed.fset, parsed.rel, imp.Pos(), imp.End()).Range,
		SelectionRange: importSelectionRange(parsed, imp),
		Signature:      importSignature(imp),
	}
}

func importSelectionRange(parsed parsedGoFile, imp *ast.ImportSpec) language.Range {
	if imp.Name != nil {
		return location(parsed.fset, parsed.rel, imp.Name.Pos(), imp.Name.End()).Range
	}
	return location(parsed.fset, parsed.rel, imp.Path.Pos(), imp.Path.End()).Range
}

func importName(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	importPath := strings.Trim(imp.Path.Value, `"`)
	return path.Base(importPath)
}

func importSignature(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return "import " + imp.Name.Name + " " + imp.Path.Value
	}
	return "import " + imp.Path.Value
}

type localDecl struct {
	symbol language.Symbol
	pos    token.Pos
	scope  ast.Node
}

func localSymbolForIdent(parsed parsedGoFile, ident *ast.Ident) (language.Symbol, bool) {
	var decls []localDecl
	for _, decl := range parsed.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !containsToken(fn.Pos(), fn.End(), ident.Pos()) {
			continue
		}
		decls = append(decls, localFieldDecls(parsed, fn, fn.Recv)...)
		decls = append(decls, localFieldDecls(parsed, fn, fn.Type.Params)...)
		decls = append(decls, localFieldDecls(parsed, fn, fn.Type.Results)...)
		if fn.Body != nil {
			decls = append(decls, localBodyDecls(parsed, fn, fn.Body)...)
		}
	}
	var best *localDecl
	for i := range decls {
		decl := &decls[i]
		if decl.symbol.Name != ident.Name || decl.pos > ident.Pos() {
			continue
		}
		if decl.scope != nil && !containsToken(decl.scope.Pos(), decl.scope.End(), ident.Pos()) {
			continue
		}
		if best == nil || decl.pos > best.pos {
			best = decl
		}
	}
	if best == nil {
		return language.Symbol{}, false
	}
	return best.symbol, true
}

func localFieldDecls(parsed parsedGoFile, scope ast.Node, fields *ast.FieldList) []localDecl {
	if fields == nil {
		return nil
	}
	var out []localDecl
	for _, field := range fields.List {
		for _, name := range field.Names {
			out = append(out, localDecl{symbol: localVarSymbol(parsed, name, field.End(), name.Name+" "+exprString(parsed.fset, field.Type)), pos: name.Pos(), scope: scope})
		}
	}
	return out
}

func localBodyDecls(parsed parsedGoFile, fn *ast.FuncDecl, body *ast.BlockStmt) []localDecl {
	var out []localDecl
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			for _, lhs := range n.Lhs {
				if name, ok := lhs.(*ast.Ident); ok && name.Name != "_" {
					out = append(out, localDecl{symbol: localVarSymbol(parsed, name, n.End(), name.Name+" :="), pos: name.Pos(), scope: declarationScope(fn, body, n, name.Pos())})
				}
			}
		case *ast.RangeStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			for _, expr := range []ast.Expr{n.Key, n.Value} {
				if name, ok := expr.(*ast.Ident); ok && name.Name != "_" {
					out = append(out, localDecl{symbol: localVarSymbol(parsed, name, n.End(), name.Name+" range"), pos: name.Pos(), scope: n})
				}
			}
		case *ast.DeclStmt:
			gen, ok := n.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range gen.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					kind := "var"
					if gen.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						out = append(out, localDecl{symbol: localVarSymbol(parsed, name, s.End(), kind+" "+name.Name), pos: name.Pos(), scope: declarationScope(fn, body, n, name.Pos())})
					}
				case *ast.TypeSpec:
					out = append(out, localDecl{symbol: localTypeSymbol(parsed, s), pos: s.Name.Pos(), scope: declarationScope(fn, body, n, s.Name.Pos())})
				}
			}
		}
		return true
	})
	return out
}

func declarationScope(fn *ast.FuncDecl, body *ast.BlockStmt, node ast.Node, pos token.Pos) ast.Node {
	if scope := nearestScopeNode(body, node, pos); scope != nil {
		return scope
	}
	return fn
}

func nearestScopeNode(root ast.Node, node ast.Node, pos token.Pos) ast.Node {
	var best ast.Node
	ast.Inspect(root, func(current ast.Node) bool {
		if current == nil || !containsToken(current.Pos(), current.End(), pos) {
			return true
		}
		var candidate ast.Node
		switch n := current.(type) {
		case *ast.BlockStmt:
			candidate = n
		case *ast.IfStmt:
			if n.Init != nil && containsToken(n.Init.Pos(), n.Init.End(), pos) {
				candidate = n
			}
		case *ast.ForStmt:
			if n.Init != nil && containsToken(n.Init.Pos(), n.Init.End(), pos) {
				candidate = n
			}
		case *ast.SwitchStmt:
			if n.Init != nil && containsToken(n.Init.Pos(), n.Init.End(), pos) {
				candidate = n
			}
		case *ast.TypeSwitchStmt:
			if n.Init != nil && containsToken(n.Init.Pos(), n.Init.End(), pos) {
				candidate = n
			}
		case *ast.RangeStmt:
			if containsToken(n.Pos(), n.Body.Pos(), pos) {
				candidate = n
			}
		}
		if candidate != nil && containsToken(candidate.Pos(), candidate.End(), node.Pos()) {
			if best == nil || candidate.Pos() >= best.Pos() {
				best = candidate
			}
		}
		return true
	})
	return best
}

func localVarSymbol(parsed parsedGoFile, ident *ast.Ident, end token.Pos, signature string) language.Symbol {
	return language.Symbol{
		ID:             symbolID(parsed.rel, language.SymbolVar, ident.Name, ident.Pos()),
		Language:       language.LanguageGo,
		Kind:           language.SymbolVar,
		Name:           ident.Name,
		PackageID:      parsed.pkgID,
		Location:       location(parsed.fset, parsed.rel, ident.Pos(), end),
		Range:          location(parsed.fset, parsed.rel, ident.Pos(), end).Range,
		SelectionRange: location(parsed.fset, parsed.rel, ident.Pos(), ident.End()).Range,
		Signature:      signature,
	}
}

func localTypeSymbol(parsed parsedGoFile, spec *ast.TypeSpec) language.Symbol {
	kind := language.SymbolType
	switch spec.Type.(type) {
	case *ast.StructType:
		kind = language.SymbolStruct
	case *ast.InterfaceType:
		kind = language.SymbolInterface
	}
	return language.Symbol{
		ID:             symbolID(parsed.rel, kind, spec.Name.Name, spec.Pos()),
		Language:       language.LanguageGo,
		Kind:           kind,
		Name:           spec.Name.Name,
		PackageID:      parsed.pkgID,
		Location:       location(parsed.fset, parsed.rel, spec.Pos(), spec.End()),
		Range:          location(parsed.fset, parsed.rel, spec.Pos(), spec.End()).Range,
		SelectionRange: location(parsed.fset, parsed.rel, spec.Name.Pos(), spec.Name.End()).Range,
		Signature:      "type " + spec.Name.Name + " " + exprString(parsed.fset, spec.Type),
	}
}

type inferredTypeDecl struct {
	name  string
	typ   string
	pos   token.Pos
	scope ast.Node
}

func localInferredType(parsed parsedGoFile, name string, pos token.Pos) string {
	var decls []inferredTypeDecl
	for _, decl := range parsed.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !containsToken(fn.Pos(), fn.End(), pos) {
			continue
		}
		decls = append(decls, inferredFieldTypes(parsed.fset, fn, fn.Recv)...)
		decls = append(decls, inferredFieldTypes(parsed.fset, fn, fn.Type.Params)...)
		if fn.Body != nil {
			decls = append(decls, inferredBodyTypes(parsed, fn, fn.Body)...)
		}
	}
	var best *inferredTypeDecl
	for i := range decls {
		decl := &decls[i]
		if decl.name != name || decl.typ == "" || decl.pos > pos {
			continue
		}
		if decl.scope != nil && !containsToken(decl.scope.Pos(), decl.scope.End(), pos) {
			continue
		}
		if best == nil || decl.pos > best.pos {
			best = decl
		}
	}
	if best == nil {
		return ""
	}
	return best.typ
}

func inferredFieldTypes(fset *token.FileSet, scope ast.Node, fields *ast.FieldList) []inferredTypeDecl {
	if fields == nil {
		return nil
	}
	var out []inferredTypeDecl
	for _, field := range fields.List {
		typ := normalizeGoType(exprString(fset, field.Type))
		for _, name := range field.Names {
			out = append(out, inferredTypeDecl{name: name.Name, typ: typ, pos: name.Pos(), scope: scope})
		}
	}
	return out
}

func inferredBodyTypes(parsed parsedGoFile, fn *ast.FuncDecl, body *ast.BlockStmt) []inferredTypeDecl {
	var out []inferredTypeDecl
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ValueSpec:
			for i, name := range n.Names {
				typ := ""
				if n.Type != nil {
					typ = normalizeGoType(exprString(parsed.fset, n.Type))
				}
				if typ == "" && i < len(n.Values) {
					typ = inferExprType(parsed.fset, n.Values[i])
				}
				if typ != "" {
					out = append(out, inferredTypeDecl{name: name.Name, typ: typ, pos: name.Pos(), scope: declarationScope(fn, body, n, name.Pos())})
				}
			}
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range n.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok || i >= len(n.Rhs) {
					continue
				}
				if typ := inferExprType(parsed.fset, n.Rhs[i]); typ != "" {
					out = append(out, inferredTypeDecl{name: name.Name, typ: typ, pos: name.Pos(), scope: declarationScope(fn, body, n, name.Pos())})
				}
			}
		}
		return true
	})
	return out
}

func inferExprType(fset *token.FileSet, expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return normalizeGoType(exprString(fset, e.Type))
	case *ast.UnaryExpr:
		return inferExprType(fset, e.X)
	case *ast.CallExpr:
		switch fun := e.Fun.(type) {
		case *ast.Ident:
			if strings.HasPrefix(fun.Name, "New") && len(fun.Name) > len("New") {
				return normalizeGoType(strings.TrimPrefix(fun.Name, "New"))
			}
			if fun.Name == "new" && len(e.Args) == 1 {
				return normalizeGoType(exprString(fset, e.Args[0]))
			}
		}
	}
	return ""
}

func normalizeGoType(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "*")
	raw = strings.TrimPrefix(raw, "[]")
	raw = strings.TrimPrefix(raw, "...")
	if strings.Contains(raw, ".") {
		parts := strings.Split(raw, ".")
		raw = parts[len(parts)-1]
	}
	return strings.TrimSpace(raw)
}

func importAt(file *ast.File, pos token.Pos) *ast.ImportSpec {
	for _, imp := range file.Imports {
		if containsToken(imp.Pos(), imp.End(), pos) || containsToken(imp.Path.Pos(), imp.Path.End(), pos) {
			return imp
		}
	}
	return nil
}

func identAt(root ast.Node, pos token.Pos) *ast.Ident {
	var best *ast.Ident
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if !containsToken(node.Pos(), node.End(), pos) {
			return true
		}
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name == "_" {
			return true
		}
		if best == nil || ident.End()-ident.Pos() <= best.End()-best.Pos() {
			best = ident
		}
		return true
	})
	return best
}

func selectorForIdent(root ast.Node, ident *ast.Ident) *ast.SelectorExpr {
	var out *ast.SelectorExpr
	ast.Inspect(root, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel == ident || containsToken(selector.Sel.Pos(), selector.Sel.End(), ident.Pos()) {
			out = selector
			return false
		}
		return true
	})
	return out
}

func enclosingSymbol(parsed parsedGoFile, pos token.Pos, includeDocs bool, docLimit int) *language.Symbol {
	for _, symbol := range flattenSymbols(fileSymbols(parsed, includeDocs, docLimit)) {
		if locationContainsToken(parsed.fset, symbol.Location, pos) {
			copied := symbol
			return &copied
		}
	}
	return nil
}

func locationContainsToken(fset *token.FileSet, loc language.Location, pos token.Pos) bool {
	p := fset.Position(pos)
	start := loc.Range.Start
	end := loc.Range.End
	if p.Line < start.Line || p.Line > end.Line {
		return false
	}
	if p.Line == start.Line && p.Column < start.Column {
		return false
	}
	if p.Line == end.Line && p.Column > end.Column {
		return false
	}
	return true
}

func containsToken(start, end, pos token.Pos) bool {
	return start.IsValid() && end.IsValid() && pos >= start && pos <= end
}

func renderNavigation(title string, result golang.NavigationResult, includeDocs bool) []string {
	target := result.Target.Name
	if target == "" {
		target = result.Target.Text
	}
	if target == "" {
		target = "position"
	}
	lines := []string{fmt.Sprintf("%s: %s", title, target)}
	for _, symbol := range result.Symbols {
		lines = append(lines, fmt.Sprintf("- %s %s %s:%d", symbol.Kind, symbol.Name, symbol.Location.Path, symbol.Location.Range.Start.Line))
		if symbol.Signature != "" {
			lines = append(lines, "  sig: "+symbol.Signature)
		}
		if includeDocs && firstDocLine(symbol.Doc) != "" {
			lines = append(lines, "  doc: "+firstDocLine(symbol.Doc))
		}
	}
	if len(result.Symbols) == 0 {
		lines = append(lines, "- no AST-level definition found")
	}
	for _, diag := range result.Diagnostics {
		lines = append(lines, fmt.Sprintf("Diagnostic: %s %s %s", diag.Severity, diag.Code, diag.Message))
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warning: "+strings.Join(result.Warnings, " "))
	}
	return lines
}
