package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/language/golang"
	runtimesystem "github.com/fluxplane/fluxplane-core/runtime/system"
	"golang.org/x/tools/go/packages"
)

const typeCheckedImplementationWarning = "Type-checked implementations: results use go/packages and go/types for loaded workspace packages; packages that fail to load are reported as diagnostics."

type typeCheckedImplementationIndex struct {
	packages      []*packages.Package
	typeNames     []*types.TypeName
	symbols       map[*types.TypeName]language.Symbol
	selected      *types.TypeName
	diagnostics   []language.Diagnostic
	complete      bool
	workspaceRoot string
}

func (p Plugin) typeCheckedImplementations(ctx context.Context, req golang.ImplementationQuery, selected language.Symbol) (golang.ImplementationResult, bool, []language.Diagnostic) {
	if selected.Kind == language.SymbolMethod {
		return golang.ImplementationResult{}, false, nil
	}
	index, ok := p.typeCheckedImplementationIndex(ctx, req, selected)
	if !ok || index.selected == nil {
		return golang.ImplementationResult{}, false, index.diagnostics
	}
	result := golang.ImplementationResult{
		Symbol:         index.symbols[index.selected],
		ResolutionMode: "type_checked",
		Complete:       index.complete,
		Warnings:       []string{typeCheckedImplementationWarning},
		Diagnostics:    index.diagnostics,
	}
	matches := index.typeCheckedMatches(maxResults(req.MaxResults))
	result.Matches = matches
	return result, true, nil
}

func (p Plugin) typeCheckedImplementationIndex(ctx context.Context, req golang.ImplementationQuery, selected language.Symbol) (typeCheckedImplementationIndex, bool) {
	root := ""
	if p.workspace != nil {
		root = p.workspace.Root()
	}
	if strings.TrimSpace(root) == "" {
		return typeCheckedImplementationIndex{}, false
	}
	if _, ok := p.workspace.(*runtimesystem.HostWorkspace); !ok {
		return typeCheckedImplementationIndex{}, false
	}
	root = filepath.Clean(root)
	selectedRel := cleanRel(req.Path)
	dir := filepath.Join(root, pathDir(selectedRel))
	patterns := []string{"."}
	if req.Scope == golang.ImplementationScopeModule {
		moduleRoot, _ := p.nearestModule(ctx, selectedRel)
		dir = filepath.Join(root, cleanRel(moduleRoot))
		patterns = []string{"./..."}
	}
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Fset:    fset,
		Tests:   includeReferenceTests(req.IncludeTests),
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports |
			packages.NeedModule,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	index := typeCheckedImplementationIndex{
		packages:      pkgs,
		symbols:       map[*types.TypeName]language.Symbol{},
		workspaceRoot: root,
		complete:      true,
	}
	seenSymbols := map[string]bool{}
	if err != nil {
		index.complete = false
		index.diagnostics = append(index.diagnostics, language.Diagnostic{Severity: "warning", Code: "packages_load_failed", Message: err.Error(), Target: selected.Name})
	}
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			index.complete = false
			index.diagnostics = append(index.diagnostics, language.Diagnostic{Path: index.relForFilename(pkgErr.Pos), Severity: "warning", Code: "packages_load_error", Message: pkgErr.Msg, Target: selected.Name})
		}
		if pkg.Types == nil || pkg.TypesInfo == nil {
			continue
		}
		for ident, obj := range pkg.TypesInfo.Defs {
			typeName, ok := obj.(*types.TypeName)
			if !ok || typeName == nil || ident == nil || typeName.Pkg() == nil {
				continue
			}
			symbol := index.typeNameSymbol(pkg, ident, typeName)
			if symbol.Name == "" {
				continue
			}
			if seenSymbols[typeCheckedSymbolKey(symbol)] {
				continue
			}
			seenSymbols[typeCheckedSymbolKey(symbol)] = true
			index.typeNames = append(index.typeNames, typeName)
			index.symbols[typeName] = symbol
			if index.selected == nil && symbolMatchesSelected(symbol, selected) {
				index.selected = typeName
			}
		}
	}
	if len(index.typeNames) == 0 {
		index.complete = false
		index.diagnostics = append(index.diagnostics, language.Diagnostic{Path: selectedRel, Severity: "warning", Code: "typecheck_unavailable", Message: "no type-checked Go packages were loaded", Target: selected.Name})
		return index, false
	}
	return index, index.selected != nil
}

func typeCheckedSymbolKey(symbol language.Symbol) string {
	return string(symbol.Kind) + ":" + cleanRel(symbol.Location.Path) + ":" + symbol.Name + ":" + fmtPosition(symbol.Location.Range.Start)
}

func fmtPosition(pos language.Position) string {
	return fmt.Sprintf("%d:%d", pos.Line, pos.Column)
}

func (idx typeCheckedImplementationIndex) typeCheckedMatches(limit int) []golang.ImplementationMatch {
	selectedType := idx.selected.Type()
	if iface, ok := selectedType.Underlying().(*types.Interface); ok {
		return idx.typeCheckedMatchesForInterface(idx.selected, iface.Complete(), limit)
	}
	selectedNamed, ok := selectedType.(*types.Named)
	if !ok {
		return nil
	}
	return idx.typeCheckedMatchesForConcrete(idx.selected, selectedNamed, limit)
}

func (idx typeCheckedImplementationIndex) typeCheckedMatchesForInterface(ifaceObj *types.TypeName, iface *types.Interface, limit int) []golang.ImplementationMatch {
	var matches []golang.ImplementationMatch
	for _, candidate := range idx.sortedTypeNames() {
		if candidate == ifaceObj {
			continue
		}
		named, ok := candidate.Type().(*types.Named)
		if !ok || isInterfaceType(candidate.Type()) {
			continue
		}
		if match, ok := idx.typeCheckedMatch(ifaceObj, iface, candidate, named); ok {
			matches = append(matches, match)
			if len(matches) >= limit {
				break
			}
		}
	}
	return matches
}

func (idx typeCheckedImplementationIndex) typeCheckedMatchesForConcrete(concreteObj *types.TypeName, concrete *types.Named, limit int) []golang.ImplementationMatch {
	var matches []golang.ImplementationMatch
	for _, candidate := range idx.sortedTypeNames() {
		if candidate == concreteObj || !isInterfaceType(candidate.Type()) {
			continue
		}
		iface := candidate.Type().Underlying().(*types.Interface).Complete()
		if match, ok := idx.typeCheckedMatch(candidate, iface, concreteObj, concrete); ok {
			matches = append(matches, match)
			if len(matches) >= limit {
				break
			}
		}
	}
	return matches
}

func (idx typeCheckedImplementationIndex) typeCheckedMatch(ifaceObj *types.TypeName, iface *types.Interface, concreteObj *types.TypeName, concrete *types.Named) (golang.ImplementationMatch, bool) {
	var relation golang.ImplementationRelation
	switch {
	case safeImplements(concrete, iface):
		relation = golang.ImplementationRelationValue
	case safeImplements(types.NewPointer(concrete), iface):
		relation = golang.ImplementationRelationPointer
	default:
		return golang.ImplementationMatch{}, false
	}
	ifaceSymbol := idx.symbols[ifaceObj]
	concreteSymbol := idx.symbols[concreteObj]
	return golang.ImplementationMatch{
		Interface:      ifaceSymbol,
		Concrete:       concreteSymbol,
		Relation:       relation,
		MatchedMethods: interfaceMethodNamesFromTypes(iface),
		Locations:      []language.Location{ifaceSymbol.Location, concreteSymbol.Location},
	}, true
}

func (idx typeCheckedImplementationIndex) sortedTypeNames() []*types.TypeName {
	out := append([]*types.TypeName(nil), idx.typeNames...)
	sort.SliceStable(out, func(i, j int) bool {
		left := idx.symbols[out[i]]
		right := idx.symbols[out[j]]
		if left.Location.Path == right.Location.Path {
			return left.Location.Range.Start.Line < right.Location.Range.Start.Line
		}
		return left.Location.Path < right.Location.Path
	})
	return out
}

func (idx typeCheckedImplementationIndex) typeNameSymbol(pkg *packages.Package, ident *ast.Ident, obj *types.TypeName) language.Symbol {
	rel := idx.relForPosition(pkg.Fset, ident.Pos())
	if rel == "" {
		return language.Symbol{}
	}
	kind := language.SymbolType
	if isInterfaceType(obj.Type()) {
		kind = language.SymbolInterface
	} else if _, ok := obj.Type().Underlying().(*types.Struct); ok {
		kind = language.SymbolStruct
	}
	loc := idx.locationForRange(pkg.Fset, rel, ident.Pos(), ident.End())
	return language.Symbol{
		ID:             symbolID(rel, kind, obj.Name(), ident.Pos()),
		Language:       language.LanguageGo,
		Kind:           kind,
		Name:           obj.Name(),
		PackageID:      packageID(pathDir(rel), pkg.Name),
		Location:       loc,
		Range:          loc.Range,
		SelectionRange: loc.Range,
		Signature:      "type " + obj.Name(),
	}
}

func (idx typeCheckedImplementationIndex) locationForRange(fset *token.FileSet, rel string, start, end token.Pos) language.Location {
	sp := fset.Position(start)
	ep := fset.Position(end)
	return language.Location{
		Path: rel,
		Range: language.Range{
			Start: language.Position{Line: sp.Line, Column: sp.Column},
			End:   language.Position{Line: ep.Line, Column: ep.Column},
		},
	}
}

func (idx typeCheckedImplementationIndex) relForPosition(fset *token.FileSet, pos token.Pos) string {
	return idx.relForFilename(fset.Position(pos).Filename)
}

func (idx typeCheckedImplementationIndex) relForFilename(filename string) string {
	if filename == "" {
		return ""
	}
	rel, err := filepath.Rel(idx.workspaceRoot, filepath.Clean(filename))
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	if rel == "." {
		return ""
	}
	return rel
}

func symbolMatchesSelected(candidate language.Symbol, selected language.Symbol) bool {
	return candidate.Name == selected.Name &&
		candidate.Kind == selected.Kind &&
		cleanRel(candidate.Location.Path) == cleanRel(selected.Location.Path)
}

func isInterfaceType(typ types.Type) bool {
	_, ok := typ.Underlying().(*types.Interface)
	return ok
}

func safeImplements(typ types.Type, iface *types.Interface) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return types.Implements(typ, iface)
}

func interfaceMethodNamesFromTypes(iface *types.Interface) []string {
	var names []string
	for i := 0; i < iface.NumMethods(); i++ {
		names = append(names, iface.Method(i).Name())
	}
	sort.Strings(names)
	return names
}
