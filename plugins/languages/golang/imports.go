package golang

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/language/golang"
	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"golang.org/x/mod/modfile"
)

const importResolutionWarning = "AST-only imports: no build-tag/cgo semantics, generated-file policy, or resolved module graph."

type parsedImportFile struct {
	rel     string
	pkgID   string
	pkgName string
	imports []language.Import
}

func (p Plugin) goImports() operationruntime.TypedResultHandler[golang.ImportQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.ImportQuery) operation.Result {
		if err := validateImportQuery(req); err != nil {
			return operation.Failed("invalid_go_imports_input", err.Error(), nil)
		}
		direction := req.Direction
		if direction == "" {
			direction = golang.ImportDirectionBoth
		}
		result := golang.ImportResult{
			ResolutionMode: "ast",
			Complete:       false,
			Warnings:       []string{importResolutionWarning},
		}
		max := maxResults(req.MaxResults)
		includeTests := includeReferenceTests(req.IncludeTests)

		if direction == golang.ImportDirectionDirect || direction == golang.ImportDirectionBoth {
			files, err := p.directImportFiles(ctx, req, includeTests)
			if err != nil {
				return operation.Failed("go_imports_failed", err.Error(), nil)
			}
			imports, diagnostics := p.collectImportEdges(
				ctx,
				files,
				req.MaxBytes,
				max,
				"",
				strings.TrimSpace(req.ImportPath),
			)
			result.DirectImports = imports
			result.Diagnostics = append(result.Diagnostics, diagnostics...)
		}

		if direction == golang.ImportDirectionReverse || direction == golang.ImportDirectionBoth {
			target := strings.TrimSpace(req.ImportPath)
			derivedTarget := target == ""
			if target == "" {
				target = p.deriveImportPath(ctx, importSelectionPath(req))
			}
			result.TargetImportPath = target
			if target == "" {
				result.Diagnostics = append(result.Diagnostics, language.Diagnostic{Severity: "info", Code: "target_import_path_unavailable", Message: "reverse import lookup needs import_path or a path/package_id inside a Go module"})
			} else {
				files, err := p.reverseImportFiles(ctx, req, includeTests, derivedTarget)
				if err != nil {
					return operation.Failed("go_imports_failed", err.Error(), nil)
				}
				remaining := max - len(result.DirectImports)
				if remaining < 0 {
					remaining = 0
				}
				imports, diagnostics := p.collectReverseImports(ctx, files, target, req.MaxBytes, remaining, "")
				result.ReverseImporters = imports
				result.Diagnostics = append(result.Diagnostics, diagnostics...)
			}
		}

		lines := renderImports(result)
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"imports": result, "diagnostics": result.Diagnostics, "warnings": result.Warnings}})
	}
}

func validateImportQuery(req golang.ImportQuery) error {
	if err := validateGoLanguage(req.Language); err != nil {
		return err
	}
	switch req.Direction {
	case "", golang.ImportDirectionDirect, golang.ImportDirectionReverse, golang.ImportDirectionBoth:
		return nil
	default:
		return fmt.Errorf("unsupported import direction %q", req.Direction)
	}
}

func importSelectionPath(req golang.ImportQuery) string {
	if strings.TrimSpace(req.Path) != "" {
		return cleanRel(req.Path)
	}
	return packagePath(req.PackageID)
}

func (p Plugin) directImportFiles(ctx context.Context, req golang.ImportQuery, includeTests bool) ([]string, error) {
	rel := importSelectionPath(req)
	if rel == "" {
		rel = "."
	}
	if info, _, err := statWorkspacePath(ctx, p.workspace, rel); err == nil && !info.IsDir() {
		if !strings.HasSuffix(rel, ".go") {
			return nil, fmt.Errorf("path is not a Go file")
		}
		if isVendoredPath(rel) {
			return nil, nil
		}
		return []string{rel}, nil
	}
	files, err := p.goFilesForPath(ctx, rel)
	if err != nil {
		return nil, err
	}
	dir := cleanRel(rel)
	if strings.HasSuffix(dir, ".go") {
		dir = pathDir(dir)
	}
	if req.PackageID == "" && p.hasGoMod(ctx, dir) {
		return filterTestFiles(files, includeTests), nil
	}
	filtered := files[:0]
	for _, file := range files {
		if pathDir(file) != dir {
			continue
		}
		if !includeTests && strings.HasSuffix(file, "_test.go") {
			continue
		}
		filtered = append(filtered, file)
	}
	if req.PackageID != "" {
		pkgDir := packagePath(req.PackageID)
		if pkgDir != "" {
			filtered = filterFilesByPackageID(ctx, p, filtered, req.PackageID, pkgDir, req.MaxBytes)
		}
	}
	return filtered, nil
}

func filterTestFiles(files []string, includeTests bool) []string {
	if includeTests {
		return files
	}
	out := files[:0]
	for _, file := range files {
		if !strings.HasSuffix(file, "_test.go") {
			out = append(out, file)
		}
	}
	return out
}

func (p Plugin) hasGoMod(ctx context.Context, dir string) bool {
	modPath := "go.mod"
	if dir != "" {
		modPath = path.Join(dir, "go.mod")
	}
	info, _, err := statWorkspacePath(ctx, p.workspace, modPath)
	return err == nil && !info.IsDir()
}

func (p Plugin) reverseImportFiles(ctx context.Context, req golang.ImportQuery, includeTests bool, derivedTarget bool) ([]string, error) {
	root := cleanRel(req.Path)
	if root == "" {
		root = packagePath(req.PackageID)
	}
	if root == "" {
		root = "."
	}
	if derivedTarget {
		if moduleDir, modulePath := p.nearestModule(ctx, root); modulePath != "" {
			root = moduleDir
			if root == "" {
				root = "."
			}
		}
	} else if info, _, err := statWorkspacePath(ctx, p.workspace, root); err == nil && !info.IsDir() {
		root = pathDir(root)
	}
	files, err := p.goFilesForPath(ctx, root)
	if err != nil {
		return nil, err
	}
	filtered := files[:0]
	for _, file := range files {
		if !includeTests && strings.HasSuffix(file, "_test.go") {
			continue
		}
		filtered = append(filtered, file)
	}
	return filtered, nil
}

func filterFilesByPackageID(ctx context.Context, p Plugin, files []string, packageID string, dir string, maxBytesValue int) []string {
	out := files[:0]
	for _, file := range files {
		if pathDir(file) != dir {
			continue
		}
		parsed, err := p.parseImportFile(ctx, file, maxBytes(maxBytesValue), "")
		if err == nil && parsed.pkgID == packageID {
			out = append(out, file)
		}
	}
	return out
}

func (p Plugin) collectImportEdges(ctx context.Context, files []string, maxBytesValue int, limit int, modulePath string, targetImportPath string) ([]language.Import, []language.Diagnostic) {
	var imports []language.Import
	var diagnostics []language.Diagnostic
	for _, file := range files {
		parsed, err := p.parseImportFile(ctx, file, maxBytes(maxBytesValue), modulePath)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(file, err))
			continue
		}
		for _, imp := range parsed.imports {
			if targetImportPath != "" && imp.Path != targetImportPath {
				continue
			}
			if len(imports) >= limit {
				diagnostics = append(diagnostics, language.Diagnostic{Severity: "info", Code: "max_results", Message: "import results reached max_results limit"})
				return imports, diagnostics
			}
			imports = append(imports, imp)
		}
	}
	sortImports(imports)
	return imports, diagnostics
}

func (p Plugin) collectReverseImports(ctx context.Context, files []string, target string, maxBytesValue int, limit int, modulePath string) ([]language.Import, []language.Diagnostic) {
	if limit <= 0 {
		return nil, []language.Diagnostic{{Severity: "info", Code: "max_results", Message: "import results reached max_results limit"}}
	}
	var imports []language.Import
	var diagnostics []language.Diagnostic
	for _, file := range files {
		parsed, err := p.parseImportFile(ctx, file, maxBytes(maxBytesValue), modulePath)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(file, err))
			continue
		}
		for _, imp := range parsed.imports {
			if imp.Path != target {
				continue
			}
			if len(imports) >= limit {
				diagnostics = append(diagnostics, language.Diagnostic{Severity: "info", Code: "max_results", Message: "import results reached max_results limit"})
				return imports, diagnostics
			}
			imports = append(imports, imp)
		}
	}
	sortImports(imports)
	return imports, diagnostics
}

func (p Plugin) parseImportFile(ctx context.Context, rel string, readLimit int, modulePath string) (parsedImportFile, error) {
	data, truncated, _, err := readWorkspaceFile(ctx, p.workspace, rel, int64(readLimit))
	if err != nil {
		return parsedImportFile{}, err
	}
	if truncated {
		return parsedImportFile{}, fmt.Errorf("source file exceeds parser byte limit (%d bytes)", readLimit)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, parser.ImportsOnly)
	if err != nil {
		return parsedImportFile{}, err
	}
	dir := pathDir(rel)
	pkgID := packageID(dir, file.Name.Name)
	if modulePath == "" {
		_, modulePath = p.nearestModule(ctx, rel)
	}
	parsed := parsedImportFile{rel: rel, pkgID: pkgID, pkgName: file.Name.Name}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		}
		parsed.imports = append(parsed.imports, language.Import{
			Path:        importPath,
			Name:        name,
			SourcePath:  rel,
			PackageID:   pkgID,
			PackageName: file.Name.Name,
			Class:       classifyImport(importPath, modulePath),
			Test:        strings.HasSuffix(rel, "_test.go"),
			Location:    location(fset, rel, spec.Pos(), spec.End()),
		})
	}
	return parsed, nil
}

func (p Plugin) deriveImportPath(ctx context.Context, rel string) string {
	rel = cleanRel(rel)
	if rel == "" {
		return ""
	}
	if info, _, err := statWorkspacePath(ctx, p.workspace, rel); err == nil && !info.IsDir() {
		rel = pathDir(rel)
	}
	moduleDir, modulePath := p.nearestModule(ctx, rel)
	if modulePath == "" {
		return ""
	}
	if moduleDir == "" {
		if rel == "" {
			return modulePath
		}
		return modulePath + "/" + rel
	}
	if rel == moduleDir {
		return modulePath
	}
	prefix := moduleDir + "/"
	if strings.HasPrefix(rel, prefix) {
		return modulePath + "/" + strings.TrimPrefix(rel, prefix)
	}
	return ""
}

func (p Plugin) nearestModule(ctx context.Context, rel string) (string, string) {
	rel = cleanRel(rel)
	if strings.HasSuffix(rel, ".go") {
		rel = pathDir(rel)
	}
	for {
		modPath := "go.mod"
		if rel != "" {
			modPath = path.Join(rel, "go.mod")
		}
		data, truncated, _, err := readWorkspaceFile(ctx, p.workspace, modPath, int64(maxBytes(0)))
		if err == nil && !truncated {
			if file, parseErr := modfile.Parse(modPath, data, nil); parseErr == nil && file.Module != nil {
				return rel, file.Module.Mod.Path
			}
		}
		if rel == "" {
			break
		}
		parent := path.Dir(rel)
		if parent == "." {
			rel = ""
		} else {
			rel = parent
		}
	}
	return "", ""
}

func classifyImport(importPath string, modulePath string) language.ImportClass {
	if importPath == "" {
		return language.ImportClassUnknown
	}
	if modulePath != "" && (importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")) {
		return language.ImportClassModuleLocal
	}
	first, _, _ := strings.Cut(importPath, "/")
	if !strings.Contains(first, ".") {
		return language.ImportClassStdlib
	}
	return language.ImportClassExternal
}

func sortImports(imports []language.Import) {
	sort.SliceStable(imports, func(i, j int) bool {
		if imports[i].SourcePath == imports[j].SourcePath {
			if imports[i].Location.Range.Start.Line == imports[j].Location.Range.Start.Line {
				return imports[i].Path < imports[j].Path
			}
			return imports[i].Location.Range.Start.Line < imports[j].Location.Range.Start.Line
		}
		return imports[i].SourcePath < imports[j].SourcePath
	})
}

func renderImports(result golang.ImportResult) []string {
	lines := []string{fmt.Sprintf("Go imports: direct=%d reverse=%d", len(result.DirectImports), len(result.ReverseImporters))}
	if len(result.DirectImports) > 0 {
		lines = append(lines, "Direct imports:")
		for _, imp := range result.DirectImports {
			lines = append(lines, renderImportEdge("- ", imp))
		}
	}
	if result.TargetImportPath != "" {
		lines = append(lines, "Reverse target: "+result.TargetImportPath)
	}
	if len(result.ReverseImporters) > 0 {
		lines = append(lines, "Reverse importers:")
		for _, imp := range result.ReverseImporters {
			lines = append(lines, renderImportEdge("- ", imp))
		}
	}
	if len(result.DirectImports) == 0 && len(result.ReverseImporters) == 0 {
		lines = append(lines, "- no imports found")
	}
	for _, diag := range result.Diagnostics {
		lines = append(lines, fmt.Sprintf("Diagnostic: %s %s %s", diag.Severity, diag.Code, diag.Message))
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warning: "+strings.Join(result.Warnings, " "))
	}
	return lines
}

func renderImportEdge(prefix string, imp language.Import) string {
	label := imp.Path
	if imp.Name != "" {
		label = imp.Name + " " + label
	}
	class := string(imp.Class)
	if class == "" {
		class = string(language.ImportClassUnknown)
	}
	test := ""
	if imp.Test {
		test = " test"
	}
	return fmt.Sprintf("%s%s [%s%s] %s:%d", prefix, label, class, test, imp.SourcePath, imp.Location.Range.Start.Line)
}
