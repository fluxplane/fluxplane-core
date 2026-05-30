package secret

import (
	"context"
	"os"
	"strings"
	"testing"

	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
)

func TestFileStoreSavesAndResolvesPluginSecret(t *testing.T) {
	store := NewFileStore(t.TempDir())
	ref := coresecret.Plugin("jira", "main", "oauth2_token")
	if err := store.SaveSecret(context.Background(), StoredSecret{
		Ref:      ref,
		Kind:     coresecret.KindOAuth2Token,
		Value:    "access-token",
		Metadata: map[string]string{"cloud_id": "cloud-1"},
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	stored, ok, err := store.LoadSecret(context.Background(), ref)
	if err != nil || !ok {
		t.Fatalf("LoadSecret = %#v, %v, %v; want stored", stored, ok, err)
	}
	if stored.Metadata["cloud_id"] != "cloud-1" {
		t.Fatalf("metadata = %#v, want cloud id", stored.Metadata)
	}
	material, ok, err := store.ResolveSecret(context.Background(), ref)
	if err != nil || !ok {
		t.Fatalf("ResolveSecret = %#v, %v, %v; want material", material, ok, err)
	}
	if material.Kind != coresecret.KindOAuth2Token || string(material.Value) != "access-token" {
		t.Fatalf("material = %#v, want oauth token", material)
	}
}

func TestFileStoreSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(dir)
	ref := coresecret.Plugin("jira", "main", "oauth2_token")
	for i := 0; i < 5; i++ {
		if err := store.SaveSecret(context.Background(), StoredSecret{
			Ref:   ref,
			Value: "access-token",
		}); err != nil {
			t.Fatalf("SaveSecret iter %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("leaked tmp file %q after Save; atomic write should clean up", entry.Name())
		}
	}
}
