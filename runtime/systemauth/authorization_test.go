package systemauth

import (
	"context"
	"github.com/fluxplane/fluxplane-policy/policyauth"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-policy"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

func TestSystemEnforcesWorkspaceActions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("docs"), 0644); err != nil {
		t.Fatal(err)
	}
	host, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})

	if _, err := sys.Workspace().ResolveExisting(ctx, "README.md"); err != nil {
		t.Fatalf("ResolveExisting denied: %v", err)
	}
	_, err = sys.Workspace().ResolveCreate(ctx, "out.txt")
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("ResolveCreate error = %v, want authorization deny", err)
	}
}

func TestWorkspaceAllowsSemanticOperations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("line1\nline2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	host, err := system.NewHost(system.Config{
		Root: root,
		Workspace: system.WorkspaceConfig{
			Roots: []system.WorkspaceRootConfig{{
				Name:   "scratch",
				Path:   filepath.Join(root, "scratch-root"),
				Access: system.WorkspaceAccessReadWrite,
				Create: true,
			}},
			ScratchRoot: "scratch",
		},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	ctx := authorizedTestContext([]policy.Grant{
		{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
			Actions:   []policy.Action{policy.ActionWorkspaceRead, policy.ActionWorkspaceWrite},
		},
		{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceWorkspace, Name: "scratch"}},
			Actions:   []policy.Action{policy.ActionWorkspaceWrite},
		},
	})

	if got := sys.Workspace().Root(); got == "" {
		t.Fatal("Root() returned empty root")
	}
	if roots := sys.Workspace().Roots(); len(roots) != 2 {
		t.Fatalf("Roots() len = %d, want 2", len(roots))
	}
	if _, err := sys.Workspace().ResolveExisting(ctx, "README.md"); err != nil {
		t.Fatalf("ResolveExisting: %v", err)
	}
	if _, err := sys.Workspace().ResolveCreate(ctx, "nested/out.txt"); err != nil {
		t.Fatalf("ResolveCreate: %v", err)
	}
	scratch, err := sys.Workspace().CreateScratch(ctx, "auth-test-*")
	if err != nil {
		t.Fatalf("CreateScratch: %v", err)
	}
	if scratch.Root() == "" {
		t.Fatal("scratch Root() returned empty root")
	}
	if _, err := scratch.WriteFile(ctx, "out.txt", []byte("scratch"), 0644); err != nil {
		t.Fatalf("scratch WriteFile: %v", err)
	}
	if err := scratch.RemoveAll(ctx); err != nil {
		t.Fatalf("scratch RemoveAll: %v", err)
	}
}

func TestSystemAuthorizesCanonicalWorkspacePath(t *testing.T) {
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
	host, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "docs/**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})

	if _, err := sys.Workspace().ResolveExisting(ctx, "docs/README.md"); err != nil {
		t.Fatalf("ResolveExisting docs/README.md denied: %v", err)
	}
	_, err = sys.Workspace().ResolveExisting(ctx, "docs/../secret.txt")
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("ResolveExisting traversal error = %v, want authorization deny", err)
	}
}

func TestSystemEnforcesEnvironmentSecretRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("FLUXPLANE_SYSTEM_TEST_SECRET=secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	host, err := system.NewHost(system.Config{Root: root, Workspace: system.WorkspaceConfig{EnvFiles: []string{".env"}}})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	denied := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})
	if _, _, err := sys.Environment().Lookup(denied, "FLUXPLANE_SYSTEM_TEST_SECRET"); err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("Lookup denied error = %v, want authorization deny", err)
	}

	allowed := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "env/FLUXPLANE_SYSTEM_TEST_SECRET"}},
		Actions:   []policy.Action{policy.ActionSecretRead},
	}})
	value, ok, err := sys.Environment().Lookup(allowed, "FLUXPLANE_SYSTEM_TEST_SECRET")
	if err != nil || !ok || value != "secret" {
		t.Fatalf("Lookup = %q, %v, %v; want secret, true, nil", value, ok, err)
	}
}

func TestSystemEnforcesNetworkActions(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	host, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceNetwork, Name: "*"}},
		Actions:   []policy.Action{policy.ActionNetworkFetch},
	}})
	if _, err := systemkit.DoHTTP(ctx, sys.Network(), systemkit.HTTPRequest{URL: server.URL, Method: http.MethodGet}); err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("GET error = %v, want authorization deny", err)
	}
	if calls != 0 {
		t.Fatalf("server calls = %d, want 0", calls)
	}
	allowed := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceNetwork, Name: "*"}},
		Actions:   []policy.Action{policy.ActionNetworkConnect},
	}})
	if _, err := systemkit.DoHTTP(allowed, sys.Network(), systemkit.HTTPRequest{URL: server.URL, Method: http.MethodGet}); err != nil {
		t.Fatalf("GET denied: %v", err)
	}
}

func TestNetworkURLAuthorizerEnforcesNetworkAccess(t *testing.T) {
	authorize := NetworkURLAuthorizer(Config{})
	denied := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})
	if err := authorize(denied, "https://example.com"); err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("authorize denied error = %v, want authorization deny", err)
	}

	allowed := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceNetwork, Name: "*"}},
		Actions:   []policy.Action{policy.ActionNetworkFetch},
	}})
	if err := authorize(allowed, "https://example.com"); err != nil {
		t.Fatalf("authorize allowed denied: %v", err)
	}
}

func TestSystemEnforcesProcessExec(t *testing.T) {
	host, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourcePath, Path: "**"}},
		Actions:   []policy.Action{policy.ActionWorkspaceRead},
	}})
	_, err = sys.Process().Run(ctx, system.ProcessRequest{Command: "go", Args: []string{"version"}, Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("Run error = %v, want authorization deny", err)
	}
}

func TestProcessManagerAllowsManagedLifecycle(t *testing.T) {
	host, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	sys := System(host, Config{})
	ctx := authorizedTestContext([]policy.Grant{{
		Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Resources: []policy.ResourceRef{{Kind: policy.ResourceProcess, Name: "*"}},
		Actions:   []policy.Action{policy.ActionProcessExec, policy.ActionProcessAdmin},
	}})
	manager := sys.Process()
	handle, created, err := manager.Ensure(ctx, system.ProcessRequest{
		Command: "sh",
		Args:    []string{"-c", "printf hello"},
		Label:   "short",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !created {
		t.Fatal("Ensure created = false, want new process")
	}
	if handle.ID() == "" || handle.Info().Label != "short" {
		t.Fatalf("handle info = %#v, want labeled process", handle.Info())
	}
	if _, err := manager.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := handle.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	capture, err := fpsystem.RunProcessCapture(ctx, manager, system.ProcessRequest{
		Command: "sh",
		Args:    []string{"-c", "printf hello"},
		Timeout: time.Second,
	}, 1024)
	if err != nil {
		t.Fatalf("RunProcessCapture: %v", err)
	}
	if capture.Stdout != "hello" {
		t.Fatalf("stdout = %q, want hello", capture.Stdout)
	}
	stopHandle, err := manager.Start(ctx, system.ProcessRequest{Command: "sh", Args: []string{"-c", "sleep 10"}, Group: "admin-stop", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Start stop process: %v", err)
	}
	if err := manager.Group("admin-stop").Stop(ctx); err != nil {
		t.Fatalf("Group Stop: %v", err)
	}
	_, _ = stopHandle.Wait(ctx)
	killHandle, err := manager.Start(ctx, system.ProcessRequest{Command: "sh", Args: []string{"-c", "sleep 10"}, Group: "admin-kill", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Start kill process: %v", err)
	}
	if err := manager.Group("admin-kill").Kill(ctx); err != nil {
		t.Fatalf("Group Kill: %v", err)
	}
	_, _ = killHandle.Wait(ctx)
}

func authorizedTestContext(grants []policy.Grant) context.Context {
	return policyauth.ContextWithAuthorization(context.Background(), policyauth.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: grants},
		Subjects: []policy.SubjectRef{
			{Kind: policy.SubjectUser, ID: "timo@localhost"},
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
	})
}
