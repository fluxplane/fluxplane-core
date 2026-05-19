package system

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/policy"
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
	_, _, _, err = sys.Workspace().ReadFile(context.Background(), "link/secret.txt", 1024)
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
	_, err = sys.Workspace().WriteFile(context.Background(), "link/new.txt", []byte("x"), 0644, false)
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

	src, dst, written, err := sys.Workspace().CopyFile(context.Background(), "src.bin", "nested/dst.bin", false)
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

	data, firstLine, truncated, resolved, err := sys.Workspace().ReadFileLines(context.Background(), "large.txt", 5500, 5500, 1024)
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

	matches, _, err := sys.Workspace().Glob(context.Background(), "**/*.md", GlobOptions{Base: ".", MaxResults: 20})
	if err != nil {
		t.Fatalf("Glob **/*.md: %v", err)
	}
	if !resolvedContains(matches, "README.md") || !resolvedContains(matches, filepath.ToSlash(filepath.Join("docs", "README.md"))) {
		t.Fatalf("matches = %#v, want root and nested markdown files", matches)
	}
	matches, _, err = sys.Workspace().Glob(context.Background(), "**/eval-review.md", GlobOptions{Base: ".", MaxResults: 20})
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
	target := filepath.Join("apps", "coder", "resources", ".agents", "commands", "reflect.yaml")
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

	matches, truncated, err := sys.Workspace().Glob(context.Background(), "apps/coder/resources/.agents/**/reflect.yaml", GlobOptions{Base: ".", MaxResults: 1, MaxScanned: 200})
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

	matches, _, err := sys.Workspace().Glob(context.Background(), ".agents/{designs,plans,reviews}/**/*", GlobOptions{Base: ".", MaxResults: 20})
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

	_, _, _, err = sys.Workspace().MoveFile(context.Background(), "src.txt", "dst.txt", false)
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
	tmp := filepath.Join(t.TempDir(), "agentruntime-coder")
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

	logical, err := sys.Workspace().WriteFile(context.Background(), "@tmp/logical.txt", []byte("logical"), 0644, false)
	if err != nil {
		t.Fatalf("WriteFile logical: %v", err)
	}
	if logical.Rel != "@tmp/logical.txt" {
		t.Fatalf("logical rel = %q, want @tmp/logical.txt", logical.Rel)
	}
	absolutePath := filepath.Join(tmp, "absolute.txt")
	absolute, err := sys.Workspace().WriteFile(context.Background(), absolutePath, []byte("absolute"), 0644, false)
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
	_, err = sys.Workspace().WriteFile(context.Background(), filepath.Join(t.TempDir(), "out.txt"), []byte("x"), 0644, false)
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
	_, err = sys.Workspace().WriteFile(context.Background(), "@tmp/link/out.txt", []byte("x"), 0644, false)
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
	data, _, resolved, err := sys.Workspace().ReadFile(context.Background(), "@docs/README.md", 1024)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "docs" || resolved.Rel != "@docs/README.md" {
		t.Fatalf("data=%q resolved=%#v, want docs in @docs", data, resolved)
	}
	_, err = sys.Workspace().WriteFile(context.Background(), "@docs/new.md", []byte("x"), 0644, false)
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

func TestHostProcessPreservesSSHAgentSocket(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SSH_AUTH_SOCK", "/tmp/test-ssh-agent.sock")
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	run, err := sys.Process().Run(context.Background(), ProcessRequest{Command: "env", Timeout: time.Second})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(run.Stdout, "SSH_AUTH_SOCK=/tmp/test-ssh-agent.sock\n") {
		t.Fatalf("stdout = %q, want SSH_AUTH_SOCK", run.Stdout)
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
	scratch, err := sys.Workspace().CreateScratch(context.Background(), "agentruntime-test-*")
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
	if !strings.HasPrefix(resolved.Rel, "@tmp/agentruntime-test-") || !strings.HasSuffix(resolved.Rel, "/out.txt") {
		t.Fatalf("scratch rel = %q, want @tmp/agentruntime-test-*/out.txt", resolved.Rel)
	}
}

func TestHostNetworkDoesNotRetryNonIdempotentRequests(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "retry", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	network := &HostNetwork{allowPrivate: true}
	resp, err := network.DoHTTP(context.Background(), HTTPRequest{
		URL:    server.URL,
		Method: http.MethodPost,
		Body:   "side effect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestHostNetworkRetriesIdempotentRequests(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	network := &HostNetwork{allowPrivate: true}
	resp, err := network.DoHTTP(context.Background(), HTTPRequest{
		URL:    server.URL,
		Method: http.MethodGet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestAuthorizedSystemEnforcesWorkspaceActions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("docs"), 0644); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := WithAuthorization(host, AuthorizationConfig{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})

	if _, _, _, err := sys.Workspace().ReadFile(ctx, "README.md", 1024); err != nil {
		t.Fatalf("ReadFile denied: %v", err)
	}
	_, err = sys.Workspace().WriteFile(ctx, "out.txt", []byte("x"), 0644, false)
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("WriteFile error = %v, want authorization deny", err)
	}
}

func TestAuthorizedSystemAuthorizesCanonicalWorkspacePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "README.md"), []byte("docs"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := WithAuthorization(host, AuthorizationConfig{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "docs/**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})

	if _, _, _, err := sys.Workspace().ReadFile(ctx, "docs/README.md", 1024); err != nil {
		t.Fatalf("ReadFile docs/README.md denied: %v", err)
	}
	_, _, _, err = sys.Workspace().ReadFile(ctx, "docs/../secret.txt", 1024)
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("ReadFile traversal error = %v, want authorization deny", err)
	}
}

func TestAuthorizedSystemEnforcesEnvironmentSecretRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("AGENTRUNTIME_SYSTEM_TEST_SECRET=secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(Config{Root: root, Workspace: WorkspaceConfig{EnvFiles: []string{".env"}}})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := WithAuthorization(host, AuthorizationConfig{})
	denied := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})
	if _, _, err := sys.Environment().Lookup(denied, "AGENTRUNTIME_SYSTEM_TEST_SECRET"); err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("Lookup denied error = %v, want authorization deny", err)
	}

	allowed := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "env/AGENTRUNTIME_SYSTEM_TEST_SECRET"}},
		Actions:   []policy.Action{policy.ActionSecretRead},
	}})
	value, ok, err := sys.Environment().Lookup(allowed, "AGENTRUNTIME_SYSTEM_TEST_SECRET")
	if err != nil || !ok || value != "secret" {
		t.Fatalf("Lookup = %q, %v, %v; want secret, true, nil", value, ok, err)
	}
}

func TestAuthorizedSystemEnforcesNetworkActions(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	host, err := NewHost(Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := WithAuthorization(host, AuthorizationConfig{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceNetwork, Name: "*"}},
		Actions:   []policy.Action{policy.ActionNetworkFetch},
	}})
	if _, err := sys.Network().DoHTTP(ctx, HTTPRequest{URL: server.URL, Method: http.MethodPost}); err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("POST error = %v, want authorization deny", err)
	}
	if calls != 0 {
		t.Fatalf("server calls = %d, want 0", calls)
	}
	if _, err := sys.Network().DoHTTP(ctx, HTTPRequest{URL: server.URL, Method: http.MethodGet}); err != nil {
		t.Fatalf("GET denied: %v", err)
	}
}

func TestAuthorizedSystemEnforcesBrowserNetworkAccess(t *testing.T) {
	browser := &recordingBrowser{}
	sys := WithAuthorization(testSystemBoundary{browser: browser}, AuthorizationConfig{})
	denied := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})
	if _, err := sys.Browser().Open(denied, BrowserOpenRequest{URL: "https://example.com"}); err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("Open denied error = %v, want authorization deny", err)
	}
	if browser.openCalls != 0 {
		t.Fatalf("browser open calls = %d, want 0", browser.openCalls)
	}

	allowed := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceNetwork, Name: "*"}},
		Actions:   []policy.Action{policy.ActionNetworkFetch},
	}})
	if _, err := sys.Browser().Open(allowed, BrowserOpenRequest{URL: "https://example.com"}); err != nil {
		t.Fatalf("Open allowed denied: %v", err)
	}
	if browser.openCalls != 1 {
		t.Fatalf("browser open calls = %d, want 1", browser.openCalls)
	}
}

func TestAuthorizedSystemEnforcesProcessExec(t *testing.T) {
	host, err := NewHost(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := WithAuthorization(host, AuthorizationConfig{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})
	_, err = sys.Process().Run(ctx, ProcessRequest{Command: "go", Args: []string{"version"}, Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("Run error = %v, want authorization deny", err)
	}
}

type testSystemBoundary struct {
	workspace Workspace
	network   Network
	process   ProcessManager
	browser   BrowserManager
	env       Environment
}

func (s testSystemBoundary) Workspace() Workspace     { return s.workspace }
func (s testSystemBoundary) Network() Network         { return s.network }
func (s testSystemBoundary) Process() ProcessManager  { return s.process }
func (s testSystemBoundary) Browser() BrowserManager  { return s.browser }
func (s testSystemBoundary) Clarifier() Clarifier     { return nil }
func (s testSystemBoundary) Environment() Environment { return s.env }

type recordingBrowser struct {
	openCalls int
}

func (b *recordingBrowser) Open(context.Context, BrowserOpenRequest) (BrowserOpenResult, error) {
	b.openCalls++
	return BrowserOpenResult{SessionID: "browser-1", URL: "https://example.com"}, nil
}
func (*recordingBrowser) Navigate(context.Context, BrowserSessionRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Click(context.Context, BrowserSelectorRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Type(context.Context, BrowserTypeRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Select(context.Context, BrowserSelectRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Read(context.Context, BrowserReadRequest) (BrowserReadResult, error) {
	return BrowserReadResult{}, nil
}
func (*recordingBrowser) Screenshot(context.Context, BrowserSessionRequest) (BrowserArtifact, error) {
	return BrowserArtifact{}, nil
}
func (*recordingBrowser) Evaluate(context.Context, BrowserEvaluateRequest) (BrowserEvaluateResult, error) {
	return BrowserEvaluateResult{}, nil
}
func (*recordingBrowser) Wait(context.Context, BrowserWaitRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Scroll(context.Context, BrowserScrollRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Hover(context.Context, BrowserSelectorRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Back(context.Context, BrowserSessionRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) Forward(context.Context, BrowserSessionRequest) (BrowserPageResult, error) {
	return BrowserPageResult{}, nil
}
func (*recordingBrowser) PDF(context.Context, BrowserSessionRequest) (BrowserArtifact, error) {
	return BrowserArtifact{}, nil
}
func (*recordingBrowser) Close(context.Context, BrowserSessionRequest) error { return nil }

func authorizedTestContext(grants []policy.Grant) context.Context {
	return policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: grants},
		Subjects: []policy.SubjectRef{
			{Kind: policy.SubjectUser, ID: "timo@localhost"},
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
	})
}

func resolvedContains(paths []ResolvedPath, rel string) bool {
	for _, path := range paths {
		if path.Rel == rel {
			return true
		}
	}
	return false
}
