package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fluxplane/engine/plugins/languages/golang/refactor"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "move-package":
		return runMovePackage(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown operation %q\n\n%s", args[0], usageText())
	}
}

func runMovePackage(args []string) error {
	flags := flag.NewFlagSet("move-package", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var opts refactor.MovePackageOptions
	var root string
	flags.StringVar(&root, "root", ".", "repository root containing go.mod")
	flags.StringVar(&opts.From, "from", "", "existing repo-relative package directory")
	flags.StringVar(&opts.To, "to", "", "destination repo-relative package directory")
	flags.StringVar(&opts.PackageName, "package", "", "new Go package name")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "print planned changes without writing")
	flags.BoolVar(&opts.MergeExisting, "merge-existing", false, "allow destination directory to exist when moved files do not collide")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("move-package does not accept positional arguments")
	}
	sys, err := newLocalFS(root)
	if err != nil {
		return err
	}
	opts.FS = sys
	result, err := refactor.MovePackage(opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		fmt.Println("dry-run: true")
	}
	fmt.Println("move:", result.MovedDir)
	for _, change := range result.Changes {
		fmt.Printf("%s: %s\n", change.Action, change.Path)
	}
	return nil
}

func usageError() error {
	return fmt.Errorf("missing operation\n\n%s", usageText())
}

func printUsage() {
	fmt.Fprint(os.Stderr, usageText())
}

func usageText() string {
	return `usage:
  go-refactor move-package --from <dir> --to <dir> --package <name> [--dry-run] [--merge-existing]

operations:
  move-package   move a Go package directory and rewrite package/import references
`
}

type localFS struct {
	root string
}

func newLocalFS(root string) (localFS, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return localFS{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return localFS{}, err
	}
	if !info.IsDir() {
		return localFS{}, fmt.Errorf("root %q is not a directory", root)
	}
	return localFS{root: abs}, nil
}

func (f localFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(f.abs(name))
}

func (f localFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(f.abs(name), data, perm)
}

func (f localFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(f.abs(name))
}

func (f localFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(f.abs(name))
}

func (f localFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(f.abs(root), func(name string, entry fs.DirEntry, err error) error {
		rel, relErr := filepath.Rel(f.root, name)
		if relErr != nil && err == nil {
			err = relErr
		}
		if rel == "." {
			return fn(".", entry, err)
		}
		return fn(filepath.ToSlash(rel), entry, err)
	})
}

func (f localFS) MkdirAll(name string, perm fs.FileMode) error {
	return os.MkdirAll(f.abs(name), perm)
}

func (f localFS) Rename(oldpath, newpath string) error {
	return os.Rename(f.abs(oldpath), f.abs(newpath))
}

func (f localFS) RemoveAll(name string) error {
	return os.RemoveAll(f.abs(name))
}

func (f localFS) abs(name string) string {
	if name == "." || name == "" {
		return f.root
	}
	return filepath.Join(f.root, filepath.FromSlash(name))
}
