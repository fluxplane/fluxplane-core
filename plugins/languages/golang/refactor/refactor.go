// Package refactor provides small Go source refactoring helpers used by the
// developer go-refactor command.
package refactor

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// FileSystem is the filesystem boundary used by refactor operations.
type FileSystem interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
	MkdirAll(name string, perm fs.FileMode) error
	Rename(oldpath, newpath string) error
	RemoveAll(name string) error
}

// MovePackageOptions describes one package move and rename operation.
type MovePackageOptions struct {
	FS            FileSystem
	From          string
	To            string
	PackageName   string
	DryRun        bool
	MergeExisting bool
}

// Result summarizes a refactor operation.
type Result struct {
	MovedDir string
	Changes  []Change
}

// Change describes one planned or applied file-level change.
type Change struct {
	Path   string
	Action string
}

// MovePackage moves a package directory, renames its package clause, rewrites
// imports from the old import path to the new one, and updates default selector
// qualifiers in importing files.
func MovePackage(opts MovePackageOptions) (Result, error) {
	if opts.FS == nil {
		return Result{}, errors.New("filesystem is required")
	}
	fromRel, err := cleanRel(opts.From, "from")
	if err != nil {
		return Result{}, err
	}
	toRel, err := cleanRel(opts.To, "to")
	if err != nil {
		return Result{}, err
	}
	if err := validatePackageName(opts.PackageName); err != nil {
		return Result{}, err
	}

	modulePath, err := modulePath(opts.FS)
	if err != nil {
		return Result{}, err
	}
	if info, err := opts.FS.Stat(fromRel); err != nil {
		return Result{}, fmt.Errorf("source package dir %q: %w", fromRel, err)
	} else if !info.IsDir() {
		return Result{}, fmt.Errorf("source package dir %q is not a directory", fromRel)
	}
	destExists := false
	if info, err := opts.FS.Stat(toRel); err == nil {
		if !info.IsDir() {
			return Result{}, fmt.Errorf("destination package dir %q already exists and is not a directory", toRel)
		}
		if !opts.MergeExisting {
			return Result{}, fmt.Errorf("destination package dir %q already exists", toRel)
		}
		destExists = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("destination package dir %q: %w", toRel, err)
	}
	if opts.MergeExisting && destExists {
		if err := ensureMergeHasNoCollisions(opts.FS, fromRel, toRel); err != nil {
			return Result{}, err
		}
	}

	oldPackage, err := packageName(opts.FS, fromRel)
	if err != nil {
		return Result{}, err
	}
	oldImport := modulePath + "/" + fromRel
	newImport := modulePath + "/" + toRel
	result := Result{MovedDir: fromRel + " -> " + toRel}

	if !opts.DryRun {
		if err := opts.FS.MkdirAll(path.Dir(toRel), 0o755); err != nil {
			return Result{}, err
		}
		if opts.MergeExisting && destExists {
			if err := mergePackageDir(opts.FS, fromRel, toRel); err != nil {
				return Result{}, err
			}
		} else if err := opts.FS.Rename(fromRel, toRel); err != nil {
			return Result{}, fmt.Errorf("move package dir %q to %q: %w", fromRel, toRel, err)
		}
	}

	packageDir := toRel
	if opts.DryRun {
		packageDir = fromRel
	}
	packageChanges, err := rewritePackageClauses(opts.FS, packageDir, toRel, oldPackage, opts.PackageName, opts.DryRun)
	if err != nil {
		return Result{}, err
	}
	result.Changes = append(result.Changes, packageChanges...)

	importChanges, err := rewriteImportsAndSelectors(opts.FS, oldImport, newImport, oldPackage, opts.PackageName, opts.DryRun)
	if err != nil {
		return Result{}, err
	}
	result.Changes = append(result.Changes, importChanges...)
	sortChanges(result.Changes)
	return result, nil
}

func cleanRel(value, name string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("--%s is required", name)
	}
	cleaned := path.Clean(strings.TrimPrefix(value, "./"))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." || !fs.ValidPath(cleaned) {
		return "", fmt.Errorf("--%s must be a repo-relative path", name)
	}
	return cleaned, nil
}

func validatePackageName(name string) error {
	if !token.IsIdentifier(name) || name == "_" || token.Lookup(name).IsKeyword() {
		return fmt.Errorf("invalid package name %q", name)
	}
	return nil
}

func modulePath(sys FileSystem) (string, error) {
	data, err := sys.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1], nil
			}
		}
	}
	return "", errors.New("go.mod does not declare a module path")
}

func packageName(sys FileSystem, dir string) (string, error) {
	files, err := directGoFiles(sys, dir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("source package dir %q has no Go files", dir)
	}
	names := map[string]bool{}
	for _, name := range files {
		data, err := sys.ReadFile(name)
		if err != nil {
			return "", err
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, data, parser.PackageClauseOnly)
		if err != nil {
			return "", err
		}
		packageName := strings.TrimSuffix(file.Name.Name, "_test")
		names[packageName] = true
	}
	if len(names) != 1 {
		var sorted []string
		for name := range names {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		return "", fmt.Errorf("source package dir has multiple package names: %s", strings.Join(sorted, ", "))
	}
	for name := range names {
		return name, nil
	}
	return "", errors.New("source package dir has no package name")
}

func rewritePackageClauses(sys FileSystem, dir, outputRelDir, oldName, newName string, dryRun bool) ([]Change, error) {
	files, err := directGoFiles(sys, dir)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, name := range files {
		changed, err := rewriteGoFile(sys, name, dryRun, func(file *ast.File) bool {
			switch file.Name.Name {
			case oldName:
				file.Name.Name = newName
				return true
			case oldName + "_test":
				file.Name.Name = newName + "_test"
				return true
			default:
				return false
			}
		})
		if err != nil {
			return nil, err
		}
		if changed {
			changePath := name
			if dryRun {
				changePath = path.Join(outputRelDir, path.Base(name))
			}
			changes = append(changes, Change{Path: changePath, Action: "rename package clause"})
		}
	}
	return changes, nil
}

func rewriteImportsAndSelectors(sys FileSystem, oldImport, newImport, oldName, newName string, dryRun bool) ([]Change, error) {
	files, err := allGoFiles(sys)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, name := range files {
		action := ""
		changed, err := rewriteGoFile(sys, name, dryRun, func(file *ast.File) bool {
			qualifiers := map[string]bool{}
			fileChanged := false
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil || importPath != oldImport {
					continue
				}
				spec.Path.Value = strconv.Quote(newImport)
				fileChanged = true
				action = "rewrite import"
				if spec.Name == nil {
					qualifiers[oldName] = true
					continue
				}
				switch spec.Name.Name {
				case ".", "_":
				case oldName:
					spec.Name = nil
					qualifiers[oldName] = true
				default:
					// Explicit aliases are preserved and keep their selector qualifier.
				}
			}
			if len(qualifiers) == 0 {
				return fileChanged
			}
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || !qualifiers[ident.Name] {
					return true
				}
				ident.Name = newName
				fileChanged = true
				action = "rewrite import and selectors"
				return true
			})
			return fileChanged
		})
		if err != nil {
			return nil, err
		}
		if changed {
			changes = append(changes, Change{Path: name, Action: action})
		}
	}
	return changes, nil
}

func ensureMergeHasNoCollisions(sys FileSystem, fromRel, toRel string) error {
	return sys.WalkDir(fromRel, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == fromRel {
			return nil
		}
		rel := strings.TrimPrefix(name, fromRel+"/")
		target := path.Join(toRel, rel)
		targetInfo, err := sys.Stat(target)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("check merge target %q: %w", target, err)
		}
		if entry.IsDir() && targetInfo.IsDir() {
			return nil
		}
		return fmt.Errorf("merge target %q already exists", target)
	})
}

func mergePackageDir(sys FileSystem, fromRel, toRel string) error {
	entries, err := sys.ReadDir(fromRel)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		src := path.Join(fromRel, entry.Name())
		dst := path.Join(toRel, entry.Name())
		if err := mergePath(sys, src, dst); err != nil {
			return err
		}
	}
	return sys.RemoveAll(fromRel)
}

func mergePath(sys FileSystem, src, dst string) error {
	info, err := sys.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		if err := sys.MkdirAll(path.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := sys.Rename(src, dst); err != nil {
			return fmt.Errorf("move package file %q to %q: %w", src, dst, err)
		}
		return nil
	}
	if targetInfo, err := sys.Stat(dst); errors.Is(err, fs.ErrNotExist) {
		if err := sys.MkdirAll(path.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := sys.Rename(src, dst); err != nil {
			return fmt.Errorf("move package dir %q to %q: %w", src, dst, err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("check merge target %q: %w", dst, err)
	} else if !targetInfo.IsDir() {
		return fmt.Errorf("merge target %q already exists", dst)
	}
	entries, err := sys.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := mergePath(sys, path.Join(src, entry.Name()), path.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return sys.RemoveAll(src)
}

func rewriteGoFile(sys FileSystem, name string, dryRun bool, rewrite func(*ast.File) bool) (bool, error) {
	data, err := sys.ReadFile(name)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, data, parser.ParseComments)
	if err != nil {
		return false, err
	}
	if !rewrite(file) {
		return false, nil
	}
	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return false, err
	}
	if bytes.Equal(data, out.Bytes()) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	info, err := sys.Stat(name)
	if err != nil {
		return false, err
	}
	if err := sys.WriteFile(name, out.Bytes(), info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

func directGoFiles(sys FileSystem, dir string) ([]string, error) {
	entries, err := sys.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		files = append(files, path.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func allGoFiles(sys FileSystem) ([]string, error) {
	var files []string
	err := sys.WalkDir(".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != "." && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			switch entry.Name() {
			case ".git", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, name)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func sortChanges(changes []Change) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Action < changes[j].Action
		}
		return changes[i].Path < changes[j].Path
	})
}
