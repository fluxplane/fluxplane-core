package serve

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/appconfig"
)

func TestListenerRequiresTCPAuthAndEnforcesBearer(t *testing.T) {
	_, err := ListenerHandler(appconfig.ListenerDoc{Name: "control", Type: "http", Addr: "127.0.0.1:0"}, http.NewServeMux())
	if err == nil || !strings.Contains(err.Error(), "requires auth") {
		t.Fatalf("ListenerHandler error = %v, want requires auth", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	handler, err := ListenerHandler(appconfig.ListenerDoc{
		Name: "control",
		Type: "http",
		Addr: "127.0.0.1:0",
		Auth: map[string]any{"mode": "bearer", "token": "secret"},
	}, next)
	if err != nil {
		t.Fatalf("ListenerHandler bearer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized code = %d, want 401", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "ok" {
		t.Fatalf("authorized response = %d %q, want 200 ok", rr.Code, rr.Body.String())
	}
}

func TestListenRemovesStaleUnixSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentsdk-local.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen stale socket: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("Close stale socket: %v", err)
	}

	ln, display, cleanup, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if display != "unix:"+path {
		t.Fatalf("display = %q, want unix path", display)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("Close listener: %v", err)
	}
	cleanup()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket exists after cleanup: %v", err)
	}
}

func TestListenRefusesLiveUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentsdk-local.sock")
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen live socket: %v", err)
	}
	defer func() { _ = live.Close() }()

	_, _, _, err = Listen(path)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("Listen error = %v, want already in use", err)
	}
}
