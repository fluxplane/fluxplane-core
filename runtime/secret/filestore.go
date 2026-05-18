package secret

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	coresecret "github.com/fluxplane/agentruntime/core/secret"
)

const DefaultFileStorePath = "~/.agentruntime/auth"

// StoredSecret is one persisted plugin secret plus non-sensitive metadata.
type StoredSecret struct {
	Ref       coresecret.Ref    `json:"ref"`
	Kind      coresecret.Kind   `json:"kind,omitempty"`
	Value     string            `json:"value"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// FileStore stores plugin secrets as one JSON file per logical secret ref.
type FileStore struct {
	Dir string
	Now func() time.Time
}

// NewFileStore returns a JSON-backed secret store.
func NewFileStore(dir string) FileStore {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultFileStorePath
	}
	return FileStore{Dir: expandHome(dir), Now: time.Now}
}

// SaveSecret writes a plugin secret with restrictive permissions.
func (s FileStore) SaveSecret(_ context.Context, secret StoredSecret) error {
	ref := secret.Ref.Normalize()
	if ref.ResourceName() == "" {
		return fmt.Errorf("secret store: ref is empty")
	}
	if strings.TrimSpace(secret.Value) == "" {
		return fmt.Errorf("secret store: value is empty")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("secret store: create dir: %w", err)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	secret.Ref = ref
	if secret.Kind == "" {
		secret.Kind = coresecret.KindBearerToken
	}
	secret.UpdatedAt = now().UTC()
	data, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return fmt.Errorf("secret store: marshal: %w", err)
	}
	if err := os.WriteFile(s.path(ref), data, 0o600); err != nil {
		return fmt.Errorf("secret store: write: %w", err)
	}
	return nil
}

// LoadSecret reads a stored plugin secret.
func (s FileStore) LoadSecret(_ context.Context, ref coresecret.Ref) (StoredSecret, bool, error) {
	ref = ref.Normalize()
	data, err := os.ReadFile(s.path(ref))
	if err != nil {
		if os.IsNotExist(err) {
			return StoredSecret{}, false, nil
		}
		return StoredSecret{}, false, fmt.Errorf("secret store: read: %w", err)
	}
	var out StoredSecret
	if err := json.Unmarshal(data, &out); err != nil {
		return StoredSecret{}, false, fmt.Errorf("secret store: parse: %w", err)
	}
	if out.Ref.Normalize().ResourceName() == "" {
		out.Ref = ref
	}
	return out, true, nil
}

// ResolveSecret implements Resolver.
func (s FileStore) ResolveSecret(ctx context.Context, ref coresecret.Ref) (coresecret.Material, bool, error) {
	stored, ok, err := s.LoadSecret(ctx, ref)
	if err != nil || !ok {
		return coresecret.Material{}, ok, err
	}
	if strings.TrimSpace(stored.Value) == "" {
		return coresecret.Material{}, false, nil
	}
	kind := stored.Kind
	if kind == "" {
		kind = coresecret.KindBearerToken
	}
	return coresecret.Material{Kind: kind, Value: stored.Value}, true, nil
}

func (s FileStore) path(ref coresecret.Ref) string {
	return filepath.Join(s.Dir, secretFilename(ref))
}

func secretFilename(ref coresecret.Ref) string {
	name := ref.Normalize().ResourceName()
	name = strings.Trim(name, "/")
	if name == "" {
		name = "secret"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String() + ".json"
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
