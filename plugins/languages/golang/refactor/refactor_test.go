package refactor

import (
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMovePackageMovesRenamesAndRewritesReferences(t *testing.T) {
	sys := newModule(t)
	sys.write(t, "plugins/fooplugin/foo.go", `package fooplugin

const Name = "foo"
`)
	sys.write(t, "plugins/fooplugin/foo_test.go", `package fooplugin_test

import "testing"

func TestPackage(t *testing.T) {}
`)
	sys.write(t, "apps/app.go", `package apps

import "example.com/project/plugins/fooplugin"

var PluginName = fooplugin.Name
`)

	result, err := MovePackage(MovePackageOptions{
		FS:          sys,
		From:        "plugins/fooplugin",
		To:          "plugins/native/foo",
		PackageName: "foo",
	})
	if err != nil {
		t.Fatalf("MovePackage: %v", err)
	}
	if result.MovedDir != "plugins/fooplugin -> plugins/native/foo" {
		t.Fatalf("MovedDir = %q", result.MovedDir)
	}
	sys.assertNotExists(t, "plugins/fooplugin")
	sys.assertContains(t, "plugins/native/foo/foo.go", "package foo")
	sys.assertContains(t, "plugins/native/foo/foo_test.go", "package foo_test")
	sys.assertContains(t, "apps/app.go", `"example.com/project/plugins/native/foo"`)
	sys.assertContains(t, "apps/app.go", "foo.Name")
}

func TestMovePackageDropsOldPackageAliasAndPreservesCustomAlias(t *testing.T) {
	sys := newModule(t)
	sys.write(t, "plugins/fooplugin/foo.go", `package fooplugin

const Name = "foo"
`)
	sys.write(t, "apps/app.go", `package apps

import (
	fooplugin "example.com/project/plugins/fooplugin"
	legacy "example.com/project/plugins/fooplugin"
)

var DefaultName = fooplugin.Name
var LegacyName = legacy.Name
`)

	if _, err := MovePackage(MovePackageOptions{
		FS:          sys,
		From:        "plugins/fooplugin",
		To:          "plugins/native/foo",
		PackageName: "foo",
	}); err != nil {
		t.Fatalf("MovePackage: %v", err)
	}
	app := sys.read(t, "apps/app.go")
	if strings.Contains(app, `fooplugin "example.com/project/plugins/native/foo"`) {
		t.Fatalf("old package alias was preserved:\n%s", app)
	}
	assertContains(t, app, `"example.com/project/plugins/native/foo"`)
	assertContains(t, app, `legacy "example.com/project/plugins/native/foo"`)
	assertContains(t, app, "foo.Name")
	assertContains(t, app, "legacy.Name")
}

func TestMovePackageDryRunLeavesFilesUnchanged(t *testing.T) {
	sys := newModule(t)
	source := `package fooplugin

const Name = "foo"
`
	importer := `package apps

import "example.com/project/plugins/fooplugin"

var PluginName = fooplugin.Name
`
	sys.write(t, "plugins/fooplugin/foo.go", source)
	sys.write(t, "apps/app.go", importer)

	result, err := MovePackage(MovePackageOptions{
		FS:          sys,
		From:        "plugins/fooplugin",
		To:          "plugins/native/foo",
		PackageName: "foo",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("MovePackage dry-run: %v", err)
	}
	if len(result.Changes) == 0 {
		t.Fatal("dry-run reported no changes")
	}
	sys.assertExists(t, "plugins/fooplugin")
	sys.assertNotExists(t, "plugins/native/foo")
	if got := sys.read(t, "plugins/fooplugin/foo.go"); got != source {
		t.Fatalf("source changed during dry-run:\n%s", got)
	}
	if got := sys.read(t, "apps/app.go"); got != importer {
		t.Fatalf("importer changed during dry-run:\n%s", got)
	}
}

func TestMovePackageMergesIntoExistingDestination(t *testing.T) {
	sys := newModule(t)
	sys.write(t, "plugins/golangplugin/plugin.go", `package golangplugin

const Name = "golang"
`)
	sys.write(t, "plugins/golangplugin/subpkg/sub.go", `package subpkg

const Value = "sub"
`)
	sys.write(t, "plugins/languages/golang/refactor/refactor.go", `package refactor

const Tool = "go-refactor"
`)
	sys.write(t, "apps/app.go", `package apps

import "example.com/project/plugins/golangplugin"

var PluginName = golangplugin.Name
`)

	_, err := MovePackage(MovePackageOptions{
		FS:            sys,
		From:          "plugins/golangplugin",
		To:            "plugins/languages/golang",
		PackageName:   "golang",
		MergeExisting: true,
	})
	if err != nil {
		t.Fatalf("MovePackage: %v", err)
	}
	sys.assertNotExists(t, "plugins/golangplugin")
	sys.assertContains(t, "plugins/languages/golang/plugin.go", "package golang")
	sys.assertContains(t, "plugins/languages/golang/subpkg/sub.go", "package subpkg")
	sys.assertContains(t, "plugins/languages/golang/refactor/refactor.go", "package refactor")
	sys.assertContains(t, "apps/app.go", `"example.com/project/plugins/languages/golang"`)
	sys.assertContains(t, "apps/app.go", "golang.Name")
}

func TestMovePackageMergeExistingRejectsFileCollision(t *testing.T) {
	sys := newModule(t)
	sys.write(t, "plugins/golangplugin/plugin.go", "package golangplugin\n")
	sys.write(t, "plugins/languages/golang/plugin.go", "package golang\n")

	_, err := MovePackage(MovePackageOptions{
		FS:            sys,
		From:          "plugins/golangplugin",
		To:            "plugins/languages/golang",
		PackageName:   "golang",
		MergeExisting: true,
	})
	if err == nil || !strings.Contains(err.Error(), `merge target "plugins/languages/golang/plugin.go" already exists`) {
		t.Fatalf("error = %v, want merge collision", err)
	}
	sys.assertExists(t, "plugins/golangplugin")
}

func TestMovePackageRejectsInvalidInputs(t *testing.T) {
	t.Run("missing filesystem", func(t *testing.T) {
		_, err := MovePackage(MovePackageOptions{From: "plugins/fooplugin", To: "plugins/native/foo", PackageName: "foo"})
		if err == nil || !strings.Contains(err.Error(), "filesystem is required") {
			t.Fatalf("error = %v, want filesystem error", err)
		}
	})
	t.Run("missing go.mod", func(t *testing.T) {
		sys := newMemFS()
		sys.write(t, "plugins/fooplugin/foo.go", "package fooplugin\n")
		_, err := MovePackage(MovePackageOptions{FS: sys, From: "plugins/fooplugin", To: "plugins/native/foo", PackageName: "foo"})
		if err == nil || !strings.Contains(err.Error(), "go.mod") {
			t.Fatalf("error = %v, want go.mod error", err)
		}
	})
	t.Run("missing source", func(t *testing.T) {
		sys := newModule(t)
		_, err := MovePackage(MovePackageOptions{FS: sys, From: "plugins/fooplugin", To: "plugins/native/foo", PackageName: "foo"})
		if err == nil || !strings.Contains(err.Error(), "source package dir") {
			t.Fatalf("error = %v, want source package dir error", err)
		}
	})
	t.Run("existing destination", func(t *testing.T) {
		sys := newModule(t)
		sys.write(t, "plugins/fooplugin/foo.go", "package fooplugin\n")
		sys.write(t, "plugins/native/foo/foo.go", "package foo\n")
		_, err := MovePackage(MovePackageOptions{FS: sys, From: "plugins/fooplugin", To: "plugins/native/foo", PackageName: "foo"})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("error = %v, want existing destination error", err)
		}
	})
	t.Run("invalid package name", func(t *testing.T) {
		sys := newModule(t)
		sys.write(t, "plugins/fooplugin/foo.go", "package fooplugin\n")
		_, err := MovePackage(MovePackageOptions{FS: sys, From: "plugins/fooplugin", To: "plugins/native/foo", PackageName: "func"})
		if err == nil || !strings.Contains(err.Error(), "invalid package name") {
			t.Fatalf("error = %v, want invalid package name error", err)
		}
	})
}

func newModule(t *testing.T) *memFS {
	t.Helper()
	sys := newMemFS()
	sys.write(t, "go.mod", "module example.com/project\n\ngo 1.25\n")
	return sys
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("missing %q in:\n%s", needle, haystack)
	}
}

type memFS struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{
		files: map[string][]byte{},
		dirs:  map[string]bool{".": true},
	}
}

func (m *memFS) ReadFile(name string) ([]byte, error) {
	name = cleanTestPath(name)
	data, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (m *memFS) WriteFile(name string, data []byte, _ fs.FileMode) error {
	name = cleanTestPath(name)
	if !m.dirs[path.Dir(name)] {
		return fs.ErrNotExist
	}
	m.files[name] = append([]byte(nil), data...)
	return nil
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	name = cleanTestPath(name)
	if data, ok := m.files[name]; ok {
		return memInfo{name: path.Base(name), size: int64(len(data)), mode: 0o644}, nil
	}
	if m.dirs[name] {
		return memInfo{name: path.Base(name), mode: fs.ModeDir | 0o755}, nil
	}
	return nil, fs.ErrNotExist
}

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = cleanTestPath(name)
	if !m.dirs[name] {
		return nil, fs.ErrNotExist
	}
	seen := map[string]memEntry{}
	prefix := ""
	if name != "." {
		prefix = name + "/"
	}
	for dir := range m.dirs {
		if dir == name || !strings.HasPrefix(dir, prefix) {
			continue
		}
		rest := strings.TrimPrefix(dir, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		seen[rest] = memEntry{info: memInfo{name: rest, mode: fs.ModeDir | 0o755}}
	}
	for file, data := range m.files {
		if !strings.HasPrefix(file, prefix) {
			continue
		}
		rest := strings.TrimPrefix(file, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		seen[rest] = memEntry{info: memInfo{name: rest, size: int64(len(data)), mode: 0o644}}
	}
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, seen[name])
	}
	return out, nil
}

func (m *memFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	var walk func(string) error
	walk = func(name string) error {
		info, err := m.Stat(name)
		entry := memEntry{info: info}
		if err != nil {
			return fn(name, entry, err)
		}
		if err := fn(name, entry, nil); err != nil {
			if err == fs.SkipDir && info.IsDir() {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			return nil
		}
		entries, err := m.ReadDir(name)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := walk(path.Join(name, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(cleanTestPath(root))
}

func (m *memFS) MkdirAll(name string, _ fs.FileMode) error {
	name = cleanTestPath(name)
	if name == "." {
		return nil
	}
	var current string
	for _, part := range strings.Split(name, "/") {
		if current == "" {
			current = part
		} else {
			current = current + "/" + part
		}
		m.dirs[current] = true
	}
	return nil
}

func (m *memFS) Rename(oldpath, newpath string) error {
	oldpath = cleanTestPath(oldpath)
	newpath = cleanTestPath(newpath)
	if !m.dirs[path.Dir(newpath)] {
		return fs.ErrNotExist
	}
	if _, ok := m.files[newpath]; ok || m.dirs[newpath] {
		return fs.ErrExist
	}
	if data, ok := m.files[oldpath]; ok {
		m.files[newpath] = data
		delete(m.files, oldpath)
		return nil
	}
	if !m.dirs[oldpath] {
		return fs.ErrNotExist
	}
	var dirs []string
	for dir := range m.dirs {
		if dir == oldpath || strings.HasPrefix(dir, oldpath+"/") {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		renamed := newpath + strings.TrimPrefix(dir, oldpath)
		m.dirs[renamed] = true
		delete(m.dirs, dir)
	}
	for file, data := range m.files {
		if file == oldpath || strings.HasPrefix(file, oldpath+"/") {
			renamed := newpath + strings.TrimPrefix(file, oldpath)
			m.files[renamed] = data
			delete(m.files, file)
		}
	}
	return nil
}

func (m *memFS) RemoveAll(name string) error {
	name = cleanTestPath(name)
	delete(m.files, name)
	for file := range m.files {
		if strings.HasPrefix(file, name+"/") {
			delete(m.files, file)
		}
	}
	delete(m.dirs, name)
	for dir := range m.dirs {
		if strings.HasPrefix(dir, name+"/") {
			delete(m.dirs, dir)
		}
	}
	return nil
}

func (m *memFS) write(t *testing.T, name, content string) {
	t.Helper()
	if err := m.MkdirAll(path.Dir(cleanTestPath(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (m *memFS) read(t *testing.T, name string) string {
	t.Helper()
	data, err := m.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (m *memFS) assertContains(t *testing.T, name, needle string) {
	t.Helper()
	assertContains(t, m.read(t, name), needle)
}

func (m *memFS) assertExists(t *testing.T, name string) {
	t.Helper()
	if _, err := m.Stat(name); err != nil {
		t.Fatalf("%s should exist: %v", name, err)
	}
}

func (m *memFS) assertNotExists(t *testing.T, name string) {
	t.Helper()
	if _, err := m.Stat(name); err == nil {
		t.Fatalf("%s should not exist", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat %s: %v", name, err)
	}
}

type memInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return i.mode }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return i.mode.IsDir() }
func (i memInfo) Sys() any           { return nil }

type memEntry struct {
	info fs.FileInfo
}

func (e memEntry) Name() string {
	if e.info == nil {
		return ""
	}
	return e.info.Name()
}

func (e memEntry) IsDir() bool {
	return e.info != nil && e.info.IsDir()
}

func (e memEntry) Type() fs.FileMode {
	if e.info == nil {
		return 0
	}
	return e.info.Mode().Type()
}

func (e memEntry) Info() (fs.FileInfo, error) {
	if e.info == nil {
		return nil, fs.ErrNotExist
	}
	return e.info, nil
}

func cleanTestPath(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." {
		return "."
	}
	return path.Clean(strings.TrimPrefix(name, "./"))
}
