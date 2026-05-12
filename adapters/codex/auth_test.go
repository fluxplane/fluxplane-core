package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAuthUsesExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"test-token"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	auth, err := loadAuth(path, nil)
	if err != nil {
		t.Fatalf("loadAuth: %v", err)
	}
	token, err := auth.token(t.Context())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token = %q, want auth file token", token)
	}
}
