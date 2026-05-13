package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testDefaultSession = "slack-main"
	testDefaultSocket  = "agentsdk-local.sock"
)

func TestResolveTargetRequiresExactlyOneTarget(t *testing.T) {
	_, err := ResolveTarget(context.Background(), Options{Session: testDefaultSession})
	if err == nil || !strings.Contains(err.Error(), "specify one target") {
		t.Fatalf("missing target error = %v, want specify one target", err)
	}
	_, err = ResolveTarget(context.Background(), Options{URL: "http://127.0.0.1:8787", Local: true, Session: testDefaultSession})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("conflicting target error = %v, want mutually exclusive", err)
	}
}

func TestResolveTargetLocalUsesDefaultSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	target, err := ResolveTarget(context.Background(), Options{Local: true, Session: testDefaultSession, DefaultSocket: testDefaultSocket})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.BaseURL != "http://unix" {
		t.Fatalf("baseURL = %q, want http://unix", target.BaseURL)
	}
	want := filepath.Join(runtimeDir, testDefaultSocket)
	if target.Socket != want {
		t.Fatalf("socket = %q, want %q", target.Socket, want)
	}
	if target.Session != testDefaultSession {
		t.Fatalf("session = %q, want default", target.Session)
	}
}

func TestResolveAppTargetUsesDirectChannelListener(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	appDir := t.TempDir()
	data := []byte(`
kind: app
name: remote-test
daemon:
  listeners:
    - name: control
      type: http
      addr: agentsdk-local.sock
      auth:
        mode: local_socket
  channels:
    - name: local
      type: direct
      listener: control
      session: custom-session
---
kind: session
name: custom-session
agent: echo
---
kind: agent
name: echo
`)
	if err := os.WriteFile(filepath.Join(appDir, "agentsdk.app.yaml"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	target, err := ResolveTarget(context.Background(), Options{AppDir: appDir, Session: testDefaultSession})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.BaseURL != "http://unix" {
		t.Fatalf("baseURL = %q, want http://unix", target.BaseURL)
	}
	if target.Socket != filepath.Join(runtimeDir, "agentsdk-local.sock") {
		t.Fatalf("socket = %q", target.Socket)
	}
	if target.Session != "custom-session" {
		t.Fatalf("session = %q, want custom-session", target.Session)
	}
}

func TestResolveAppTargetReportsAmbiguousDirectChannels(t *testing.T) {
	appDir := t.TempDir()
	data := []byte(`
kind: app
name: remote-test
daemon:
  listeners:
    - name: a
      type: http
      addr: a.sock
      auth: {mode: local_socket}
    - name: b
      type: http
      addr: b.sock
      auth: {mode: local_socket}
  channels:
    - name: local-a
      type: direct
      listener: a
      session: a-session
    - name: local-b
      type: direct
      listener: b
      session: b-session
---
kind: session
name: a-session
agent: echo
---
kind: session
name: b-session
agent: echo
---
kind: agent
name: echo
`)
	if err := os.WriteFile(filepath.Join(appDir, "agentsdk.app.yaml"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := ResolveTarget(context.Background(), Options{AppDir: appDir, Session: testDefaultSession})
	if err == nil || !strings.Contains(err.Error(), "multiple direct channels") || !strings.Contains(err.Error(), "local-a") || !strings.Contains(err.Error(), "local-b") {
		t.Fatalf("ResolveTarget error = %v, want ambiguous channels", err)
	}
}
