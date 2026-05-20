package golang

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/language/golang"
	"github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const implementationResolutionWarning = "AST-only implementations: method matching is name-based and does not resolve aliases, embedded/promoted methods, generics, build tags, cgo, external packages, or type-checked assignability."

type implementationContext struct {
	interfaces map[string]language.Symbol
	concretes  map[string]language.Symbol
	methods    map[string][]language.Symbol
}

type implementationSymbol struct {
	key    string
	symbol language.Symbol
}

func (p Plugin) goImplementations() operationruntime.TypedResultHandler[golang.ImplementationQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.ImplementationQuery) operation.Result {
		if err := validateImplementationQuery(req); err != nil {
			return operation.Failed("invalid_go_implementations_input", err.Error(), nil)
		}
		nav, err := p.resolveNavigation(ctx, implementationNavigationQuery(req), false)
		if err != nil {
			return operation.Failed("go_implementations_failed", err.Error(), nil)
		}
		result := golang.ImplementationResult{
			Target:         nav.Target,
			Diagnostics:    nav.Diagnostics,
			ResolutionMode: "ast",
			Complete:       false,
			Warnings:       append([]string{}, nav.Warnings...),
		}
		result.Warnings = append(result.Warnings, implementationResolutionWarning)
		if len(nav.Symbols) == 0 {
			lines := renderImplementations(result)
			return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"implementations": result, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
		}
		result.Symbol = nav.Symbols[0]
		if typedResult, ok, diagnostics := p.typeCheckedImplementations(ctx, req, nav.Symbols[0]); ok {
			typedResult.Target = nav.Target
			typedResult.Diagnostics = append(append([]language.Diagnostic{}, nav.Diagnostics...), append(diagnostics, typedResult.Diagnostics...)...)
			typedResult.Warnings = append([]string{}, typedResult.Warnings...)
			lines := renderImplementations(typedResult)
			return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"implementations": typedResult, "matches": typedResult.Matches, "diagnostics": typedResult.Diagnostics, "warnings": typedResult.Warnings}})
		} else {
			result.Diagnostics = append(result.Diagnostics, diagnostics...)
		}

		selected, err := p.parseGoSource(ctx, cleanRel(req.Path), maxBytes(req.MaxBytes))
		if err != nil {
			return operation.Failed("go_implementations_failed", err.Error(), nil)
		}
		files, diagnostics := p.implementationFiles(ctx, selected, req)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		index := indexImplementationSymbols(files)
		matches, diagnostics := findImplementationMatches(index, result.Symbol, maxResults(req.MaxResults))
		result.Matches = matches
		result.Diagnostics = append(result.Diagnostics, diagnostics...)

		lines := renderImplementations(result)
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"implementations": result, "matches": result.Matches, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
	}
}

func validateImplementationQuery(req golang.ImplementationQuery) error {
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
	case "", golang.ImplementationScopePackage, golang.ImplementationScopeModule:
		return nil
	default:
		return fmt.Errorf("unsupported implementation scope %q", req.Scope)
	}
}

func implementationNavigationQuery(req golang.ImplementationQuery) golang.NavigationQuery {
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

func (p Plugin) implementationFiles(ctx context.Context, selected parsedGoFile, req golang.ImplementationQuery) ([]parsedGoFile, []language.Diagnostic) {
	includeTests := includeReferenceTests(req.IncludeTests)
	if req.Scope == golang.ImplementationScopeModule {
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
	}
	files, diagnostics := p.navigationFiles(ctx, selected, golang.NavigationQuery{Path: selected.rel, Scope: golang.NavigationScopePackage, MaxBytes: req.MaxBytes})
	files = filterReferencePackageFiles(files, selected)
	files = filterImplementationTestFiles(files, includeTests, selected.rel)
	return files, diagnostics
}

func (p Plugin) parseImplementationFiles(ctx context.Context, selected parsedGoFile, files []string, includeTests bool, maxBytesValue int) ([]parsedGoFile, []language.Diagnostic) {
	var out []parsedGoFile
	var diagnostics []language.Diagnostic
	seen := map[string]bool{}
	for _, rel := range files {
		if !includeTests && strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if rel == selected.rel {
			out = append(out, selected)
			continue
		}
		parsed, err := p.parseGoSource(ctx, rel, maxBytes(maxBytesValue))
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(rel, err))
			continue
		}
		out = append(out, parsed)
	}
	return out, diagnostics
}

func filterImplementationTestFiles(files []parsedGoFile, includeTests bool, selected string) []parsedGoFile {
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

func indexImplementationSymbols(files []parsedGoFile) implementationContext {
	index := implementationContext{
		interfaces: map[string]language.Symbol{},
		concretes:  map[string]language.Symbol{},
		methods:    map[string][]language.Symbol{},
	}
	for _, file := range files {
		for _, symbol := range flattenSymbols(fileSymbols(file, false, 0)) {
			switch symbol.Kind {
			case language.SymbolInterface:
				index.interfaces[implementationTypeKey(symbol)] = symbol
			case language.SymbolStruct, language.SymbolType:
				index.concretes[implementationTypeKey(symbol)] = symbol
			case language.SymbolMethod:
				container := implementationContainerKey(symbol)
				if container != "" {
					index.methods[container] = append(index.methods[container], symbol)
				}
			}
		}
	}
	return index
}

func findImplementationMatches(index implementationContext, selected language.Symbol, limit int) ([]golang.ImplementationMatch, []language.Diagnostic) {
	switch selected.Kind {
	case language.SymbolInterface:
		return matchesForInterface(index, selected, limit)
	case language.SymbolStruct, language.SymbolType:
		return matchesForConcrete(index, selected, limit)
	case language.SymbolMethod:
		return matchesForMethod(index, selected, limit)
	default:
		return nil, []language.Diagnostic{{Severity: "info", Code: "unsupported_symbol", Message: "implementation lookup supports interfaces, concrete types, and methods", Target: selected.Name, Line: selected.Location.Range.Start.Line}}
	}
}

func matchesForInterface(index implementationContext, iface language.Symbol, limit int) ([]golang.ImplementationMatch, []language.Diagnostic) {
	required := interfaceMethodNames(iface)
	var matches []golang.ImplementationMatch
	var diagnostics []language.Diagnostic
	for _, concrete := range sortedImplementationSymbols(index.concretes) {
		match, missing, partial := implementationMatch(iface, concrete.symbol, index.methods[concrete.key], required)
		if len(missing) == 0 {
			matches = append(matches, match)
			if len(matches) >= limit {
				break
			}
			continue
		}
		if partial {
			diagnostics = append(diagnostics, missingMethodsDiagnostic(concrete.symbol, missing))
		}
	}
	return matches, diagnostics
}

func matchesForConcrete(index implementationContext, concrete language.Symbol, limit int) ([]golang.ImplementationMatch, []language.Diagnostic) {
	methods := index.methods[implementationTypeKey(concrete)]
	var matches []golang.ImplementationMatch
	var diagnostics []language.Diagnostic
	for _, iface := range sortedImplementationSymbols(index.interfaces) {
		match, missing, partial := implementationMatch(iface.symbol, concrete, methods, interfaceMethodNames(iface.symbol))
		if len(missing) == 0 {
			matches = append(matches, match)
			if len(matches) >= limit {
				break
			}
			continue
		}
		if partial {
			diagnostics = append(diagnostics, missingMethodsDiagnostic(iface.symbol, missing))
		}
	}
	return matches, diagnostics
}

func matchesForMethod(index implementationContext, method language.Symbol, limit int) ([]golang.ImplementationMatch, []language.Diagnostic) {
	methodKey := implementationContainerKey(method)
	if iface, ok := index.interfaces[methodKey]; ok {
		return methodMatchesForInterfaceMethod(index, iface, method, limit), nil
	}
	concrete, ok := index.concretes[methodKey]
	if !ok {
		return nil, []language.Diagnostic{{Severity: "info", Code: "unsupported_method", Message: "method container is not a parsed concrete type or interface", Target: method.Name, Line: method.Location.Range.Start.Line}}
	}
	return methodMatchesForConcreteMethod(index, concrete, method, limit), nil
}

func implementationMatch(iface language.Symbol, concrete language.Symbol, methods []language.Symbol, required []string) (golang.ImplementationMatch, []string, bool) {
	valueMethods, pointerMethods := methodNameSets(methods)
	missing := missingMethods(pointerMethods, required)
	matched := matchedMethods(pointerMethods, required)
	relation := golang.ImplementationRelationPointer
	if len(missing) == 0 && len(missingMethods(valueMethods, required)) == 0 {
		relation = golang.ImplementationRelationValue
	}
	match := golang.ImplementationMatch{
		Interface:      iface,
		Concrete:       concrete,
		Relation:       relation,
		MatchedMethods: matched,
		MissingMethods: missing,
		Locations:      []language.Location{iface.Location, concrete.Location},
	}
	return match, missing, len(matched) > 0
}

func methodMatchesForInterfaceMethod(index implementationContext, iface language.Symbol, method language.Symbol, limit int) []golang.ImplementationMatch {
	required := interfaceMethodNames(iface)
	var matches []golang.ImplementationMatch
	for _, concrete := range sortedImplementationSymbols(index.concretes) {
		match, missing, _ := implementationMatch(iface, concrete.symbol, index.methods[concrete.key], required)
		if len(missing) > 0 || !containsString(match.MatchedMethods, method.Name) {
			continue
		}
		match.Relation = golang.ImplementationRelationMethodCorrespondence
		match.MatchedMethods = []string{method.Name}
		match.Locations = append(match.Locations, method.Location)
		if concreteMethod := methodByName(index.methods[concrete.key], method.Name); concreteMethod != nil {
			match.Locations = append(match.Locations, concreteMethod.Location)
		}
		matches = append(matches, match)
		if len(matches) >= limit {
			break
		}
	}
	return matches
}

func methodMatchesForConcreteMethod(index implementationContext, concrete language.Symbol, method language.Symbol, limit int) []golang.ImplementationMatch {
	methodName := bareSymbolName(method)
	var matches []golang.ImplementationMatch
	for _, iface := range sortedImplementationSymbols(index.interfaces) {
		match, missing, _ := implementationMatch(iface.symbol, concrete, index.methods[implementationTypeKey(concrete)], interfaceMethodNames(iface.symbol))
		if len(missing) > 0 || !containsString(match.MatchedMethods, methodName) {
			continue
		}
		match.Relation = golang.ImplementationRelationMethodCorrespondence
		match.MatchedMethods = []string{methodName}
		match.Locations = append(match.Locations, method.Location)
		if ifaceMethod := interfaceMethodByName(iface.symbol, methodName); ifaceMethod != nil {
			match.Locations = append(match.Locations, ifaceMethod.Location)
		}
		matches = append(matches, match)
		if len(matches) >= limit {
			break
		}
	}
	return matches
}

func interfaceMethodNames(iface language.Symbol) []string {
	var names []string
	for _, child := range iface.Children {
		if child.Kind == language.SymbolMethod {
			names = append(names, child.Name)
		}
	}
	sort.Strings(names)
	return names
}

func methodNameSets(methods []language.Symbol) (map[string]bool, map[string]bool) {
	value := map[string]bool{}
	pointer := map[string]bool{}
	for _, method := range methods {
		name := bareSymbolName(method)
		pointer[name] = true
		if !strings.HasPrefix(strings.TrimSpace(method.Container), "*") {
			value[name] = true
		}
	}
	return value, pointer
}

func matchedMethods(methods map[string]bool, required []string) []string {
	var out []string
	for _, name := range required {
		if methods[name] {
			out = append(out, name)
		}
	}
	return out
}

func missingMethods(methods map[string]bool, required []string) []string {
	var out []string
	for _, name := range required {
		if !methods[name] {
			out = append(out, name)
		}
	}
	return out
}

func methodByName(methods []language.Symbol, name string) *language.Symbol {
	for i := range methods {
		if bareSymbolName(methods[i]) == name {
			return &methods[i]
		}
	}
	return nil
}

func interfaceMethodByName(iface language.Symbol, name string) *language.Symbol {
	for i := range iface.Children {
		if iface.Children[i].Kind == language.SymbolMethod && iface.Children[i].Name == name {
			return &iface.Children[i]
		}
	}
	return nil
}

func implementationTypeKey(symbol language.Symbol) string {
	return symbol.PackageID + "\x00" + symbol.Name
}

func implementationContainerKey(symbol language.Symbol) string {
	container := normalizeGoType(symbol.Container)
	if container == "" {
		return ""
	}
	return symbol.PackageID + "\x00" + container
}

func sortedImplementationSymbols(symbols map[string]language.Symbol) []implementationSymbol {
	out := make([]implementationSymbol, 0, len(symbols))
	for key, symbol := range symbols {
		out = append(out, implementationSymbol{key: key, symbol: symbol})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].symbol.Location.Path == out[j].symbol.Location.Path {
			return out[i].symbol.Location.Range.Start.Line < out[j].symbol.Location.Range.Start.Line
		}
		return out[i].symbol.Location.Path < out[j].symbol.Location.Path
	})
	return out
}

func missingMethodsDiagnostic(symbol language.Symbol, missing []string) language.Diagnostic {
	return language.Diagnostic{
		Path:     symbol.Location.Path,
		Severity: "info",
		Code:     "missing_methods",
		Message:  "partial implementation is missing methods: " + strings.Join(missing, ", "),
		Target:   symbol.Name,
		Line:     symbol.Location.Range.Start.Line,
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func renderImplementations(result golang.ImplementationResult) []string {
	target := result.Target.Name
	if target == "" {
		target = result.Target.Text
	}
	if target == "" {
		target = "position"
	}
	lines := []string{fmt.Sprintf("Go implementations: %s", target)}
	if result.Symbol.Name != "" {
		lines = append(lines, fmt.Sprintf("- symbol: %s %s %s:%d", result.Symbol.Kind, result.Symbol.Name, result.Symbol.Location.Path, result.Symbol.Location.Range.Start.Line))
	}
	for _, match := range result.Matches {
		lines = append(lines, fmt.Sprintf("- %s %s implements %s", match.Relation, match.Concrete.Name, match.Interface.Name))
		if len(match.MatchedMethods) > 0 {
			lines = append(lines, "  matched: "+strings.Join(match.MatchedMethods, ", "))
		}
		if len(match.MissingMethods) > 0 {
			lines = append(lines, "  missing: "+strings.Join(match.MissingMethods, ", "))
		}
	}
	if len(result.Matches) == 0 {
		switch result.ResolutionMode {
		case "type_checked":
			lines = append(lines, "- no type-checked implementation matches found")
		default:
			lines = append(lines, "- no AST-level implementation matches found")
		}
	}
	for _, diag := range result.Diagnostics {
		lines = append(lines, fmt.Sprintf("Diagnostic: %s %s %s", diag.Severity, diag.Code, diag.Message))
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warning: "+strings.Join(result.Warnings, " "))
	}
	return lines
}
