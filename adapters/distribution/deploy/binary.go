package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func buildEmbeddedBinary(ctx context.Context, loadedRoot, output string, assets []string, dryRun, force bool, out, errOut io.Writer, runner CommandRunner) ([]string, error) {
	command := []string{"go", "build", "-trimpath", "-o", output, "."}
	if dryRun {
		_, _ = fmt.Fprintf(out, "write=%s\n", output)
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return command, nil
	}
	if _, err := os.Stat(output); err == nil && !force {
		return nil, fmt.Errorf("distribution build: %s already exists; pass --force to overwrite", output)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	repoRoot, err := findRepoRoot(loadedRoot)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "fluxplane-app-binary-*")
	if err != nil {
		return nil, fmt.Errorf("distribution build: create binary build dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	if len(assets) == 0 {
		assets = []string{"fluxplane.yaml"}
	}
	if _, err := copyAssets(ctx, loadedRoot, filepath.Join(tempDir, "app"), assets); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(binaryGoMod(repoRoot)), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(binaryMainGo()), 0o600); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return nil, err
	}
	if runner == nil {
		runner = execRunner{}
	}
	if err := runner.Run(ctx, tempDir, command[0], command[1:], out, errOut); err != nil {
		return nil, err
	}
	return command, nil
}

func binaryGoMod(repoRoot string) string {
	return fmt.Sprintf("module fluxplane-app-binary\n\ngo 1.24\n\nrequire github.com/fluxplane/fluxplane-core v0.0.0\n\nreplace github.com/fluxplane/fluxplane-core => %s\n", filepath.ToSlash(repoRoot))
}

func binaryMainGo() string {
	return `package main

import (
	"embed"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	fluxplaneapp "github.com/fluxplane/fluxplane-core/apps/fluxplane"
)

//go:embed all:app
var appFS embed.FS

func main() {
	appDir, err := os.MkdirTemp("", "fluxplane-embedded-app-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(appDir)
	if err := copyEmbeddedApp(appDir); err != nil {
		panic(err)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"run", appDir}
	}
	cmd := fluxplaneapp.NewCommand()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func copyEmbeddedApp(dst string) error {
	root, err := fs.Sub(appFS, "app")
	if err != nil {
		return err
	}
	return fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := root.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
`
}
