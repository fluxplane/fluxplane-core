package endpoint

import (
	"testing"
	"time"

	coreendpoint "github.com/fluxplane/engine/core/endpoint"
)

func TestRegistryResolveFreshRecord(t *testing.T) {
	registry := NewRegistry(time.Minute)
	ref, err := registry.Put(Record{Spec: coreendpoint.Spec{Name: "dev-loki", URL: "http://loki:3100", Product: "loki"}})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	resolved, ok := registry.Resolve(ref)
	if !ok {
		t.Fatalf("Resolve(%q) ok=false", ref)
	}
	if resolved.URL != "http://loki:3100" || resolved.Ref != "@endpoint/dev-loki" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestRegistrySkipsExpiredRecord(t *testing.T) {
	registry := NewRegistry(time.Minute)
	ref, err := registry.Put(Record{
		Spec:    coreendpoint.Spec{Name: "old-loki", URL: "http://loki:3100", Product: "loki"},
		Expires: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, ok := registry.Resolve(ref); ok {
		t.Fatalf("Resolve(%q) ok=true, want expired", ref)
	}
}
