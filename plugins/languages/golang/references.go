package golang

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/fluxplane/engine/core/language"
	"github.com/fluxplane/engine/core/language/golang"
	"github.com/fluxplane/engine/core/operation"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
)

func (p Plugin) goReferences() operationruntime.TypedResultHandler[golang.ReferenceQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.ReferenceQuery) operation.Result {
		navReq := referenceNavigationQuery(req)
		resolveReq := navReq
		if resolveReq.Scope == golang.NavigationScopeFile {
			resolveReq.Scope = golang.NavigationScopePackage
		}
		nav, err := p.resolveNavigation(ctx, resolveReq, false)
		if err != nil {
			return operation.Failed("go_references_failed", err.Error(), nil)
		}
		result := golang.ReferenceResult{
			Target:         nav.Target,
			Diagnostics:    nav.Diagnostics,
			ResolutionMode: "ast",
			Complete:       false,
			Warnings:       nav.Warnings,
		}
		if len(nav.Symbols) == 0 {
			lines := renderReferences("Go references", result)
			return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"references": result, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
		}

		selected, err := p.parseGoSource(ctx, cleanRel(req.Path), maxBytes(req.MaxBytes))
		if err != nil {
			return operation.Failed("go_references_failed", err.Error(), nil)
		}
		files, diagnostics := p.navigationFiles(ctx, selected, navReq)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		files = filterReferencePackageFiles(files, selected)
		files = filterReferenceFiles(files, includeReferenceTests(req.IncludeTests), selected.rel)

		result.Symbol = nav.Symbols[0]
		var truncated bool
		result.References, truncated = findGoReferences(files, result.Symbol, req.IncludeDeclaration, maxResults(req.MaxResults))
		if truncated {
			result.Diagnostics = append(result.Diagnostics, language.Diagnostic{Severity: "info", Code: "max_results", Message: "reference results reached max_results limit"})
		}
		lines := renderReferences("Go references", result)
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"references": result, "symbol": compactSymbol(result.Symbol, false), "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
	}
}

func referenceNavigationQuery(req golang.ReferenceQuery) golang.NavigationQuery {
	return golang.NavigationQuery{
		Language:   req.Language,
		Path:       req.Path,
		Line:       req.Line,
		Column:     req.Column,
		Offset:     req.Offset,
		Scope:      req.Scope,
		MaxResults: 1,
		MaxBytes:   req.MaxBytes,
		Refresh:    req.Refresh,
	}
}

func includeReferenceTests(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func filterReferenceFiles(files []parsedGoFile, includeTests bool, selected string) []parsedGoFile {
	if includeTests {
		return files
	}
	out := files[:0]
	for _, file := range files {
		if file.rel == selected || !strings.HasSuffix(file.rel, "_test.go") {
			out = append(out, file)
		}
	}
	return out
}

func filterReferencePackageFiles(files []parsedGoFile, selected parsedGoFile) []parsedGoFile {
	selectedDir := pathDir(selected.rel)
	out := files[:0]
	for _, file := range files {
		if file.pkgID == selected.pkgID && pathDir(file.rel) == selectedDir {
			out = append(out, file)
		}
	}
	return out
}

func findGoReferences(files []parsedGoFile, symbol language.Symbol, includeDeclaration bool, limit int) ([]language.Reference, bool) {
	var refs []language.Reference
	for _, file := range files {
		refs = append(refs, fileReferences(file, symbol, includeDeclaration)...)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		left := refs[i].Location
		right := refs[j].Location
		if left.Path == right.Path {
			if left.Range.Start.Line == right.Range.Start.Line {
				return left.Range.Start.Column < right.Range.Start.Column
			}
			return left.Range.Start.Line < right.Range.Start.Line
		}
		return left.Path < right.Path
	})
	refs = uniqueReferences(refs)
	if len(refs) > limit {
		return refs[:limit], true
	}
	return refs, false
}

func fileReferences(parsed parsedGoFile, symbol language.Symbol, includeDeclaration bool) []language.Reference {
	switch symbol.Kind {
	case language.SymbolPackage:
		return packageReferences(parsed, symbol, includeDeclaration)
	case language.SymbolImport:
		return importReferences(parsed, symbol, includeDeclaration)
	case language.SymbolMethod, language.SymbolField:
		return memberReferences(parsed, symbol, includeDeclaration)
	default:
		return identReferences(parsed, symbol, includeDeclaration)
	}
}

func packageReferences(parsed parsedGoFile, symbol language.Symbol, includeDeclaration bool) []language.Reference {
	if parsed.file.Name == nil || parsed.file.Name.Name != symbol.Name || !includeDeclaration {
		return nil
	}
	return []language.Reference{referenceForIdent(parsed, symbol, parsed.file.Name, "declaration")}
}

func importReferences(parsed parsedGoFile, symbol language.Symbol, includeDeclaration bool) []language.Reference {
	var refs []language.Reference
	for _, imp := range parsed.file.Imports {
		current := importSymbol(parsed, imp)
		if current.Name != symbol.Name || importPath(current) != importPath(symbol) {
			continue
		}
		if includeDeclaration {
			refs = append(refs, language.Reference{
				SymbolID: symbol.ID,
				Kind:     "declaration",
				Name:     symbol.Name,
				Location: language.Location{Path: parsed.rel, Range: current.SelectionRange},
				Preview:  linePreview(parsed.data, current.SelectionRange.Start.Line),
			})
		}
		ast.Inspect(parsed.file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok || ident.Name != symbol.Name || importAt(parsed.file, ident.Pos()) != nil {
				return true
			}
			refs = append(refs, referenceForIdent(parsed, symbol, ident, "reference"))
			return true
		})
	}
	return refs
}

func memberReferences(parsed parsedGoFile, symbol language.Symbol, includeDeclaration bool) []language.Reference {
	var refs []language.Reference
	ast.Inspect(parsed.file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncDecl:
			if symbol.Kind != language.SymbolMethod || n.Name == nil || n.Name.Name != bareSymbolName(symbol) || !receiverMatches(parsed.fset, n.Recv, symbol.Container) {
				return true
			}
			ref := referenceForIdent(parsed, symbol, n.Name, "declaration")
			if includeDeclaration {
				refs = append(refs, ref)
			}
		case *ast.Field:
			if n.Names == nil {
				return true
			}
			for _, name := range n.Names {
				if name.Name != symbol.Name || !sameRange(location(parsed.fset, parsed.rel, name.Pos(), name.End()).Range, symbol.SelectionRange) {
					continue
				}
				if includeDeclaration {
					refs = append(refs, referenceForIdent(parsed, symbol, name, "declaration"))
				}
			}
		case *ast.SelectorExpr:
			if n.Sel == nil || n.Sel.Name != bareSymbolName(symbol) || selectorIsImported(parsed, n) || !selectorReceiverMatches(parsed, n, symbol.Container) {
				return true
			}
			refs = append(refs, referenceForIdent(parsed, symbol, n.Sel, "reference"))
		case *ast.CompositeLit:
			if symbol.Kind != language.SymbolField || normalizeGoType(exprString(parsed.fset, n.Type)) != normalizeGoType(symbol.Container) {
				return true
			}
			for _, elt := range n.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if ok && key.Name == symbol.Name {
					refs = append(refs, referenceForIdent(parsed, symbol, key, "reference"))
				}
			}
		}
		return true
	})
	return refs
}

func identReferences(parsed parsedGoFile, symbol language.Symbol, includeDeclaration bool) []language.Reference {
	var refs []language.Reference
	ast.Inspect(parsed.file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != symbol.Name || ident == parsed.file.Name {
			return true
		}
		if selector := selectorForIdent(parsed.file, ident); selector != nil && selector.Sel == ident {
			return true
		}
		if !identMatchesSymbol(parsed, ident, symbol) {
			return true
		}
		kind := "reference"
		if sameRange(location(parsed.fset, parsed.rel, ident.Pos(), ident.End()).Range, symbol.SelectionRange) {
			kind = "declaration"
			if !includeDeclaration {
				return true
			}
		}
		refs = append(refs, referenceForIdent(parsed, symbol, ident, kind))
		return true
	})
	return refs
}

func identMatchesSymbol(parsed parsedGoFile, ident *ast.Ident, symbol language.Symbol) bool {
	if local, ok := localSymbolForIdent(parsed, ident); ok {
		return local.ID == symbol.ID
	}
	if symbol.PackageID != "" && symbol.PackageID != parsed.pkgID {
		return false
	}
	switch symbol.Kind {
	case language.SymbolFunction, language.SymbolStruct, language.SymbolInterface, language.SymbolType, language.SymbolConst, language.SymbolVar:
		return symbol.Name == ident.Name
	default:
		return false
	}
}

func selectorIsImported(parsed parsedGoFile, selector *ast.SelectorExpr) bool {
	x, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	for _, imp := range parsed.file.Imports {
		if importName(imp) == x.Name {
			return true
		}
	}
	return false
}

func selectorReceiverMatches(parsed parsedGoFile, selector *ast.SelectorExpr, container string) bool {
	x, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	receiverType := normalizeGoType(x.Name)
	if inferred := localInferredType(parsed, x.Name, selector.Pos()); inferred != "" {
		receiverType = inferred
	}
	return receiverType == normalizeGoType(container)
}

func receiverMatches(fset *token.FileSet, fields *ast.FieldList, container string) bool {
	if fields == nil || len(fields.List) == 0 {
		return false
	}
	return normalizeGoType(exprString(fset, fields.List[0].Type)) == normalizeGoType(container)
}

func referenceForIdent(parsed parsedGoFile, symbol language.Symbol, ident *ast.Ident, kind string) language.Reference {
	loc := location(parsed.fset, parsed.rel, ident.Pos(), ident.End())
	return language.Reference{
		SymbolID: symbol.ID,
		Kind:     kind,
		Name:     symbol.Name,
		Location: loc,
		Preview:  linePreview(parsed.data, loc.Range.Start.Line),
	}
}

func uniqueReferences(refs []language.Reference) []language.Reference {
	seen := map[string]bool{}
	out := make([]language.Reference, 0, len(refs))
	for _, ref := range refs {
		key := fmt.Sprintf("%s:%d:%d:%d:%d:%s:%s", ref.Location.Path, ref.Location.Range.Start.Line, ref.Location.Range.Start.Column, ref.Location.Range.End.Line, ref.Location.Range.End.Column, ref.Kind, ref.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	return out
}

func sameRange(left, right language.Range) bool {
	return left.Start.Line == right.Start.Line &&
		left.Start.Column == right.Start.Column &&
		left.End.Line == right.End.Line &&
		left.End.Column == right.End.Column
}

func importPath(symbol language.Symbol) string {
	if strings.HasPrefix(symbol.Signature, "import ") {
		raw := strings.TrimSpace(strings.TrimPrefix(symbol.Signature, "import "))
		if fields := strings.Fields(raw); len(fields) > 0 {
			raw = fields[len(fields)-1]
		}
		return strings.Trim(raw, `"`)
	}
	return ""
}

func linePreview(data []byte, line int) string {
	if line <= 0 {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

func renderReferences(title string, result golang.ReferenceResult) []string {
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
	for _, ref := range result.References {
		lines = append(lines, fmt.Sprintf("- %s %s %s:%d:%d", ref.Kind, ref.Name, ref.Location.Path, ref.Location.Range.Start.Line, ref.Location.Range.Start.Column))
		if ref.Preview != "" {
			lines = append(lines, "  "+ref.Preview)
		}
	}
	if len(result.References) == 0 {
		lines = append(lines, "- no AST-level references found")
	}
	for _, diag := range result.Diagnostics {
		lines = append(lines, fmt.Sprintf("Diagnostic: %s %s %s", diag.Severity, diag.Code, diag.Message))
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warning: "+strings.Join(result.Warnings, " "))
	}
	return lines
}
