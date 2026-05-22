package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type arrayFlag []string

func (f *arrayFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *arrayFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*f = append(*f, part)
		}
	}
	return nil
}

type options struct {
	outDir  string
	targets []string
	install bool
}

type buildApp struct {
	name string
	dir  string
	pkg  string
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "build: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var targets arrayFlag
	opts := options{outDir: "bin"}
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.outDir, "out", opts.outDir, "output directory for compiled binaries")
	fs.Var(&targets, "target", "GOOS/GOARCH target; may be repeated or comma-separated")
	fs.BoolVar(&opts.install, "install", false, "install binaries with go install instead of cross-compiling")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.targets = targets
	apps := fs.Args()
	if len(apps) == 0 {
		return errors.New("usage: go run ./cmd/build/main.go [--install] [--target GOOS/GOARCH] apps/fluxplane")
	}
	if len(opts.targets) == 0 {
		opts.targets = defaultTargets()
	}
	for _, appPath := range apps {
		app, err := appFromPath(appPath)
		if err != nil {
			return err
		}
		if opts.install {
			if err := install(ctx, app, stdout, stderr); err != nil {
				return err
			}
			continue
		}
		for _, target := range opts.targets {
			if err := buildTarget(ctx, opts, app, target, stdout, stderr); err != nil {
				return err
			}
		}
	}
	return nil
}

func appFromPath(path string) (buildApp, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == string(filepath.Separator) || clean == "" {
		return buildApp{}, fmt.Errorf("invalid app path %q", path)
	}
	app := strings.TrimSpace(filepath.Base(filepath.Clean(path)))
	if nestedCmd := filepath.Join(clean, "cmd", app); isDir(nestedCmd) {
		return buildApp{name: app, dir: clean, pkg: "./cmd/" + app}, nil
	}
	cmdPath := filepath.Join("cmd", app)
	if stat, err := os.Stat(cmdPath); err != nil {
		return buildApp{}, fmt.Errorf("%s: command package %s not found: %w", path, cmdPath, err)
	} else if !stat.IsDir() {
		return buildApp{}, fmt.Errorf("%s: command package %s is not a directory", path, cmdPath)
	}
	return buildApp{name: app, dir: ".", pkg: "./cmd/" + app}, nil
}

func isDir(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.IsDir()
}

func install(ctx context.Context, app buildApp, stdout, stderr io.Writer) error {
	_, _ = fmt.Fprintf(stdout, "install: %s\n", filepath.ToSlash(filepath.Join(app.dir, app.pkg)))
	cmd := exec.CommandContext(ctx, "go", "install", app.pkg)
	cmd.Dir = app.dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func buildTarget(ctx context.Context, opts options, app buildApp, target string, stdout, stderr io.Writer) error {
	goos, goarch, ok := strings.Cut(strings.TrimSpace(target), "/")
	if !ok || goos == "" || goarch == "" {
		return fmt.Errorf("invalid target %q, want GOOS/GOARCH", target)
	}
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	if err := os.MkdirAll(opts.outDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(opts.outDir, fmt.Sprintf("%s_%s_%s%s", app.name, goos, goarch, ext))
	if !filepath.IsAbs(out) {
		abs, err := filepath.Abs(out)
		if err != nil {
			return err
		}
		out = abs
	}
	_, _ = fmt.Fprintf(stdout, "build: %s\n", out)
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, app.pkg)
	cmd.Dir = app.dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func defaultTargets() []string {
	host := runtime.GOOS + "/" + runtime.GOARCH
	candidates := []string{
		"linux/amd64",
		"linux/arm64",
		"darwin/amd64",
		"darwin/arm64",
		"windows/amd64",
		"windows/arm64",
		host,
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates))
	for _, target := range candidates {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	return out
}
