package system

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-system/systemkit"
)

func TestHostWorkspaceRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, _, _, err = sys.workspace.ReadFile(context.Background(), "link/secret.txt", 1024)
	if err == nil {
		t.Fatal("ReadFile through escaping symlink succeeded, want error")
	}
}

func TestHostWorkspaceCreateRejectsSymlinkParentEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.workspace.WriteFile(context.Background(), "link/new.txt", []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile through escaping symlink parent succeeded, want error")
	}
}

func TestHostWorkspaceCopyFileCopiesCompleteFile(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("x"), 1024*1024+17)
	if err := os.WriteFile(filepath.Join(root, "src.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	src, dst, written, err := sys.workspace.CopyFile(context.Background(), "src.bin", "nested/dst.bin", false)
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if src.Rel != "src.bin" || dst.Rel != "nested/dst.bin" || written != int64(len(data)) {
		t.Fatalf("src=%#v dst=%#v written=%d, want complete copy", src, dst, written)
	}
	copied, err := os.ReadFile(filepath.Join(root, "nested", "dst.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, data) {
		t.Fatalf("copied data len=%d, want %d identical bytes", len(copied), len(data))
	}
}

func TestHostWorkspaceReadFileLinesPastInitialWindow(t *testing.T) {
	root := t.TempDir()
	var content bytes.Buffer
	for i := 1; i <= 6000; i++ {
		if i == 5500 {
			content.WriteString("target\n")
			continue
		}
		content.WriteString("padding padding padding padding\n")
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), content.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	data, firstLine, truncated, resolved, err := sys.workspace.ReadFileLines(context.Background(), "large.txt", 5500, 5500, 1024)
	if err != nil {
		t.Fatalf("ReadFileLines: %v", err)
	}
	if resolved.Rel != "large.txt" || firstLine != 5500 || truncated || string(data) != "target\n" {
		t.Fatalf("resolved=%#v firstLine=%d truncated=%v data=%q", resolved, firstLine, truncated, data)
	}
}

func TestHostWorkspaceGlobMatchesRootLevelGlobstar(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"README.md", "eval-review.md", filepath.Join("docs", "README.md")} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	matches, _, err := sys.workspace.Glob(context.Background(), "**/*.md", GlobOptions{Base: ".", MaxResults: 20})
	if err != nil {
		t.Fatalf("Glob **/*.md: %v", err)
	}
	if !resolvedContains(matches, "README.md") || !resolvedContains(matches, filepath.ToSlash(filepath.Join("docs", "README.md"))) {
		t.Fatalf("matches = %#v, want root and nested markdown files", matches)
	}
	matches, _, err = sys.workspace.Glob(context.Background(), "**/eval-review.md", GlobOptions{Base: ".", MaxResults: 20})
	if err != nil {
		t.Fatalf("Glob **/eval-review.md: %v", err)
	}
	if !resolvedContains(matches, "eval-review.md") {
		t.Fatalf("matches = %#v, want root eval-review.md", matches)
	}
}

func TestHostWorkspaceGlobMaxResultsDoesNotLimitTraversal(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 75; i++ {
		path := filepath.Join(root, "padding", fmt.Sprintf("file-%03d.txt", i))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join("apps", "assistant", "resources", ".agents", "commands", "reflect.yaml")
	targetPath := filepath.Join(root, target)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	matches, truncated, err := sys.workspace.Glob(context.Background(), "apps/assistant/resources/.agents/**/reflect.yaml", GlobOptions{Base: ".", MaxResults: 1, MaxScanned: 200})
	if err != nil {
		t.Fatalf("Glob reflect.yaml: %v", err)
	}
	if !resolvedContains(matches, filepath.ToSlash(target)) {
		t.Fatalf("matches = %#v, want %s", matches, filepath.ToSlash(target))
	}
	if truncated {
		t.Fatalf("truncated = true, want false because max_results no longer stops traversal")
	}
}

func TestHostWorkspaceGlobSkipsConfiguredNoisyDirs(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 250; i++ {
		path := filepath.Join(root, ".cache", "go-build", fmt.Sprintf("file-%03d.txt", i))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join("apps", "assistant", "resources", ".agents", "commands", "reflect.yaml")
	targetPath := filepath.Join(root, target)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	matches, truncated, err := sys.workspace.Glob(context.Background(), "apps/assistant/resources/.agents/**/reflect.yaml", GlobOptions{Base: ".", MaxResults: 10, MaxScanned: 50, SkipDirs: []string{".cache"}})
	if err != nil {
		t.Fatalf("Glob reflect.yaml: %v", err)
	}
	if !resolvedContains(matches, filepath.ToSlash(target)) {
		t.Fatalf("matches = %#v, want %s", matches, filepath.ToSlash(target))
	}
	if truncated {
		t.Fatalf("truncated = true, want false after skipping .cache")
	}
}

func TestHostWorkspaceGlobMatchesBraceAlternation(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		filepath.Join(".agents", "designs", "design.md"),
		filepath.Join(".agents", "plans", "plan.md"),
		filepath.Join(".agents", "reviews", "2026", "review.md"),
		filepath.Join(".agents", "notes", "note.md"),
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	matches, _, err := sys.workspace.Glob(context.Background(), ".agents/{designs,plans,reviews}/**/*", GlobOptions{Base: ".", MaxResults: 20})
	if err != nil {
		t.Fatalf("Glob brace pattern: %v", err)
	}
	for _, want := range []string{
		".agents/designs/design.md",
		".agents/plans/plan.md",
		".agents/reviews/2026/review.md",
	} {
		if !resolvedContains(matches, want) {
			t.Fatalf("matches = %#v, want %s", matches, want)
		}
	}
	if resolvedContains(matches, ".agents/notes/note.md") {
		t.Fatalf("matches = %#v, did not want notes match", matches)
	}
}

func TestHostWorkspaceMoveFileLeavesSourceWhenDestinationWriteFails(t *testing.T) {
	root := t.TempDir()
	data := []byte("source")
	if err := os.WriteFile(filepath.Join(root, "src.txt"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dst.txt"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	_, _, _, err = sys.workspace.MoveFile(context.Background(), "src.txt", "dst.txt", false)
	if err == nil {
		t.Fatal("MoveFile succeeded, want overwrite error")
	}
	remaining, readErr := os.ReadFile(filepath.Join(root, "src.txt"))
	if readErr != nil {
		t.Fatalf("source missing after failed move: %v", readErr)
	}
	if !bytes.Equal(remaining, data) {
		t.Fatalf("source = %q, want %q", remaining, data)
	}
}

func TestHostWorkspaceNamedRootAllowsLogicalAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "fluxplane-assistant")
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "tmp",
			Path:   tmp,
			Access: WorkspaceAccessReadWrite,
			Create: true,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	logical, err := sys.workspace.WriteFile(context.Background(), "@tmp/logical.txt", []byte("logical"), 0644, false)
	if err != nil {
		t.Fatalf("WriteFile logical: %v", err)
	}
	if logical.Rel != "@tmp/logical.txt" {
		t.Fatalf("logical rel = %q, want @tmp/logical.txt", logical.Rel)
	}
	absolutePath := filepath.Join(tmp, "absolute.txt")
	absolute, err := sys.workspace.WriteFile(context.Background(), absolutePath, []byte("absolute"), 0644, false)
	if err != nil {
		t.Fatalf("WriteFile absolute: %v", err)
	}
	if absolute.Rel != "@tmp/absolute.txt" {
		t.Fatalf("absolute rel = %q, want @tmp/absolute.txt", absolute.Rel)
	}
}

func TestHostWorkspaceRejectsUnconfiguredAbsoluteTmpPath(t *testing.T) {
	sys, err := NewHost(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.workspace.WriteFile(context.Background(), filepath.Join(t.TempDir(), "out.txt"), []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile outside workspace succeeded, want error")
	}
}

func TestHostWorkspaceNamedRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(tmp, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "tmp",
			Path:   tmp,
			Access: WorkspaceAccessReadWrite,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.workspace.WriteFile(context.Background(), "@tmp/link/out.txt", []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile through named-root symlink escape succeeded, want error")
	}
}

func TestHostWorkspaceReadOnlyNamedRootRejectsWrite(t *testing.T) {
	root := t.TempDir()
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "README.md"), []byte("docs"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "docs",
			Path:   docs,
			Access: WorkspaceAccessReadOnly,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	data, _, resolved, err := sys.workspace.ReadFile(context.Background(), "@docs/README.md", 1024)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "docs" || resolved.Rel != "@docs/README.md" {
		t.Fatalf("data=%q resolved=%#v, want docs in @docs", data, resolved)
	}
	_, err = sys.workspace.WriteFile(context.Background(), "@docs/new.md", []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile into read-only root succeeded, want error")
	}
}

func TestHostProcessRejectsReadOnlyNamedRootWorkdir(t *testing.T) {
	root := t.TempDir()
	docs := t.TempDir()
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "docs",
			Path:   docs,
			Access: WorkspaceAccessReadOnly,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.Process().Run(context.Background(), ProcessRequest{
		Command: "go",
		Args:    []string{"version"},
		Workdir: "@docs",
		Timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("Run error = %v, want read-only workdir rejection", err)
	}
}

func TestHostProcessDetachedStartSurvivesCallerCancel(t *testing.T) {
	sys, err := NewHost(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	handle, err := sys.Process().Start(ctx, ProcessRequest{
		Command:  "sh",
		Args:     []string{"-c", "printf start; sleep 0.2; printf done"},
		Detached: true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	result, err := handle.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Stdout != "startdone" {
		t.Fatalf("stdout = %q, want detached process to complete after caller cancel", result.Stdout)
	}
}

func TestHostProcessCapturesShortLivedOutputBeforeExit(t *testing.T) {
	sys, err := NewHost(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	for i := 0; i < 200; i++ {
		want := fmt.Sprintf("hello-%03d", i)
		handle, err := sys.Process().Start(context.Background(), ProcessRequest{
			Command: "printf",
			Args:    []string{want},
			Timeout: 2 * time.Second,
		})
		if err != nil {
			t.Fatalf("Start iteration %d: %v", i, err)
		}
		eventsDone := make(chan struct{})
		events := make([]ProcessEvent, 0, 3)
		go func() {
			defer close(eventsDone)
			for event := range handle.Events() {
				events = append(events, event)
			}
		}()
		waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := handle.Wait(waitCtx)
		cancel()
		<-eventsDone
		if err != nil {
			t.Fatalf("Wait iteration %d: %v", i, err)
		}
		if result.Stdout != want {
			t.Fatalf("stdout iteration %d = %q, want %q", i, result.Stdout, want)
		}
		output, err := sys.Process().Output(context.Background(), handle.ID())
		if err != nil {
			t.Fatalf("Output iteration %d: %v", i, err)
		}
		if output.Stdout != want {
			t.Fatalf("output snapshot iteration %d = %q, want %q", i, output.Stdout, want)
		}
		startedIndex, outputIndex, exitedIndex := -1, -1, -1
		var eventOutput strings.Builder
		for index, event := range events {
			switch event.Kind {
			case "started":
				if startedIndex == -1 {
					startedIndex = index
				}
			case "output":
				if outputIndex == -1 {
					outputIndex = index
				}
				if event.Stream == "stdout" {
					eventOutput.WriteString(event.Data)
				}
			case "exited":
				if exitedIndex == -1 {
					exitedIndex = index
				}
			}
		}
		if startedIndex == -1 || outputIndex == -1 || exitedIndex == -1 {
			t.Fatalf("events iteration %d = %#v, want started/output/exited", i, events)
		}
		if outputIndex < startedIndex || exitedIndex < outputIndex {
			t.Fatalf("events iteration %d = %#v, want started before output before exited", i, events)
		}
		if eventOutput.String() != want {
			t.Fatalf("event output iteration %d = %q, want %q", i, eventOutput.String(), want)
		}
	}
}

func TestHostEnvironmentLoadsRootEnvFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ROOT_TOKEN=root\nSHARED=first\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("SHARED=last\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root, Workspace: WorkspaceConfig{EnvFiles: []string{".env", ".env.*"}}})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	value, ok, err := sys.Environment().Lookup(context.Background(), "ROOT_TOKEN")
	if err != nil || !ok || value != "root" {
		t.Fatalf("Lookup ROOT_TOKEN = %q, %v, %v; want root, true, nil", value, ok, err)
	}
	value, ok, err = sys.Environment().Lookup(context.Background(), "SHARED")
	if err != nil || !ok || value != "last" {
		t.Fatalf("Lookup SHARED = %q, %v, %v; want last, true, nil", value, ok, err)
	}
}

func TestHostProcessUsesWorkspaceScopedEnvFiles(t *testing.T) {
	root := t.TempDir()
	named := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ROOT_ONLY=root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, ".env"), []byte("NAMED_ONLY=named\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{
			EnvFiles: []string{".env"},
			Roots: []WorkspaceRootConfig{{
				Name:     "named",
				Path:     named,
				Access:   WorkspaceAccessReadWrite,
				EnvFiles: []string{".env"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	rootRun, err := sys.Process().Run(context.Background(), ProcessRequest{Command: "env", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Run root env: %v", err)
	}
	if !strings.Contains(rootRun.Stdout, "ROOT_ONLY=root\n") || strings.Contains(rootRun.Stdout, "NAMED_ONLY=named\n") {
		t.Fatalf("root env stdout = %q, want only root env", rootRun.Stdout)
	}

	namedRun, err := sys.Process().Run(context.Background(), ProcessRequest{Command: "env", Workdir: "@named", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Run named env: %v", err)
	}
	if !strings.Contains(namedRun.Stdout, "NAMED_ONLY=named\n") || strings.Contains(namedRun.Stdout, "ROOT_ONLY=root\n") {
		t.Fatalf("named env stdout = %q, want only named env", namedRun.Stdout)
	}
}

func TestHostProcessRejectsUnknownEnvOverride(t *testing.T) {
	sys, err := NewHost(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.Process().Run(context.Background(), ProcessRequest{
		Command: "env",
		Env:     []string{"UNCONFIGURED_SECRET=value"},
		Timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), `process env key "UNCONFIGURED_SECRET" is not allowed`) {
		t.Fatalf("Run error = %v, want disallowed env key", err)
	}
}

func TestHostProcessAllowsConfiguredAndToolchainEnvOverrides(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CONFIGURED=value\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root, Workspace: WorkspaceConfig{EnvFiles: []string{".env"}}})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	run, err := sys.Process().Run(context.Background(), ProcessRequest{
		Command: "env",
		Env:     []string{"CONFIGURED=override", "GOOS=testos"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(run.Stdout, "CONFIGURED=override\n") || !strings.Contains(run.Stdout, "GOOS=testos\n") {
		t.Fatalf("stdout = %q, want configured and toolchain overrides", run.Stdout)
	}
}

func TestHostProcessPreservesForwardedHostEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SSH_AUTH_SOCK", "/tmp/test-ssh-agent.sock")
	t.Setenv("DISPLAY", ":77")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/tmp/test-bus")
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/test-runtime")
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	run, err := sys.Process().Run(context.Background(), ProcessRequest{Command: "env", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"SSH_AUTH_SOCK=/tmp/test-ssh-agent.sock\n",
		"DISPLAY=:77\n",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/test-bus\n",
		"XDG_RUNTIME_DIR=/tmp/test-runtime\n",
	} {
		if !strings.Contains(run.Stdout, want) {
			t.Fatalf("stdout = %q, want %s", run.Stdout, want)
		}
	}
}

func TestEnvFileParsingPatternsAndExecutableResolution(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "bin", "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(root, ".env")
	envContent := strings.Join([]string{
		"# comment",
		"export SIMPLE=value # inline comment",
		`SINGLE='quoted value'`,
		`DOUBLE="line\nnext\tTabbed\\slash\"quote"`,
		"EMPTY=",
		"",
	}, "\n")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		t.Fatal(err)
	}
	values, err := parseEnvFile(envPath)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if values["SIMPLE"] != "value" || values["SINGLE"] != "quoted value" || values["EMPTY"] != "" {
		t.Fatalf("values = %#v, want parsed simple/single/empty values", values)
	}
	if values["DOUBLE"] != "line\nnext\tTabbed\\slash\"quote" {
		t.Fatalf("DOUBLE = %q, want unescaped double-quoted value", values["DOUBLE"])
	}
	files, err := resolveEnvFiles(root, []string{".missing", ".env", "*.env"})
	if err != nil {
		t.Fatalf("resolveEnvFiles: %v", err)
	}
	if len(files) != 2 || files[0] != envPath || files[1] != envPath {
		t.Fatalf("files = %#v, want explicit and glob-resolved .env", files)
	}
	if _, err := envFilePattern(root, "../escape.env"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("envFilePattern escape error = %v, want escapes", err)
	}
	if got := staticPatternDir(filepath.Join(root, "configs", "*.env")); got != filepath.Join(root, "configs") {
		t.Fatalf("staticPatternDir = %q, want configs dir", got)
	}
	resolved, ok, err := resolveExecutableInPath("tool", filepath.Join(root, "bin"))
	if err != nil || !ok || resolved != tool {
		t.Fatalf("resolveExecutableInPath = %q, %v, %v; want tool", resolved, ok, err)
	}
	if _, ok, err := resolveExecutableInPath("missing", filepath.Join(root, "bin")); err != nil || ok {
		t.Fatalf("resolve missing = %v, %v; want false nil", ok, err)
	}
}

func TestEnvFileParsingRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	tests := map[string]string{
		"invalid-key":        "1BAD=value\n",
		"unterminated-quote": `BAD="unterminated` + "\n",
		"unterminated-esc":   `BAD="unterminated\` + "\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name+".env")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := parseEnvFile(path); err == nil {
				t.Fatal("parseEnvFile returned nil error, want invalid env file")
			}
		})
	}
}

func TestHostWorkspaceCreateScratchUsesConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "scratch")
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{
			Roots: []WorkspaceRootConfig{{
				Name:   "tmp",
				Path:   tmp,
				Access: WorkspaceAccessReadWrite,
				Create: true,
			}},
			ScratchRoot: "tmp",
		},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	scratch, err := sys.workspace.CreateScratch(context.Background(), "fluxplane-test-*")
	if err != nil {
		t.Fatalf("CreateScratch: %v", err)
	}
	defer func() { _ = scratch.RemoveAll(context.Background()) }()
	if err := pathWithin(tmp, scratch.Root()); err != nil {
		t.Fatalf("scratch root = %q, want under %q: %v", scratch.Root(), tmp, err)
	}
	resolved, err := scratch.WriteFile(context.Background(), "out.txt", []byte("x"), 0644)
	if err != nil {
		t.Fatalf("scratch WriteFile: %v", err)
	}
	if !strings.HasPrefix(resolved.Rel, "@tmp/fluxplane-test-") || !strings.HasSuffix(resolved.Rel, "/out.txt") {
		t.Fatalf("scratch rel = %q, want @tmp/fluxplane-test-*/out.txt", resolved.Rel)
	}
}

func TestHostNetworkUsesRequestTLSConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	transport, ok := server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("test client transport = %T, want *http.Transport", server.Client().Transport)
	}
	network := &HostNetwork{allowPrivate: true}
	resp, err := systemkit.DoHTTP(context.Background(), network, systemkit.HTTPRequest{
		URL:       server.URL,
		Method:    http.MethodGet,
		TLSConfig: transport.TLSClientConfig,
	})
	if err != nil {
		t.Fatalf("DoHTTP: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHostSettersEnvironmentResolverAndNetworkGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "bin", "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PATH="+filepath.Join(root, "bin")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin"))
	host, err := NewHost(Config{Root: root, Workspace: WorkspaceConfig{EnvFiles: []string{".env"}}})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	resolver, ok := host.Environment().(ExecutableResolver)
	if !ok {
		t.Fatalf("environment = %T, want ExecutableResolver", host.Environment())
	}
	resolved, found, err := resolver.ResolveExecutable(context.Background(), "tool")
	if err != nil || !found || resolved != tool {
		t.Fatalf("ResolveExecutable = %q, %v, %v; want tool", resolved, found, err)
	}
	if got := DefaultProcessEnv(); got == nil {
		t.Fatal("DefaultProcessEnv returned nil")
	}
	if !systemkit.AllowedHTTPMethod(http.MethodPatch) || systemkit.AllowedHTTPMethod("TRACE") {
		t.Fatal("AllowedHTTPMethod returned unexpected method decisions")
	}
	loopback, _ := url.Parse("http://127.0.0.1")
	if err := ValidatePublicURL(loopback, false); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("ValidatePublicURL loopback error = %v, want private target rejection", err)
	}
	if err := ValidatePublicURL(loopback, true); err != nil {
		t.Fatalf("ValidatePublicURL allow private: %v", err)
	}
	if _, err := PublicNetworkTransport(false).(*http.Transport).DialContext(context.Background(), "tcp", "127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("PublicNetworkTransport dial error = %v, want private target rejection", err)
	}
	if !blockedIP(nil) || !blockedIP(net.ParseIP("127.0.0.1")) || blockedIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("blockedIP returned unexpected decisions")
	}
	if !matchFilterPattern("*.go", "nested/file.go", false) || matchFilterPattern("[", "file.go", false) {
		t.Fatal("matchFilterPattern returned unexpected decisions")
	}
}

type testSystemBoundary struct {
	workspace Workspace
	network   Network
	process   ProcessManager
	env       Environment
}

func (s testSystemBoundary) Workspace() Workspace     { return s.workspace }
func (s testSystemBoundary) Network() Network         { return s.network }
func (s testSystemBoundary) Process() ProcessManager  { return s.process }
func (s testSystemBoundary) Environment() Environment { return s.env }

func resolvedContains(paths []ResolvedPath, rel string) bool {
	for _, path := range paths {
		if path.Rel == rel {
			return true
		}
	}
	return false
}
