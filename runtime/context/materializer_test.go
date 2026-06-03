package context

import (
	"context"
	"strings"
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
)

func TestMaterializerEmitsOnlyChangedBlocks(t *testing.T) {
	provider := &testProvider{
		spec: ProviderSpec{Name: "docs", DefaultPlacement: PlacementSystem},
		blocks: []Block{{
			ID:      "docs/agents",
			Content: "rules v1",
		}},
	}
	m := NewMaterializer([]Provider{provider}, nil)
	first, err := m.Build(context.Background(), BuildRequest{TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if len(first.Added) != 1 || first.Added[0].Placement != PlacementSystem {
		t.Fatalf("first added = %#v, want system block", first.Added)
	}

	second, err := m.Build(context.Background(), BuildRequest{TurnID: "turn-2"})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !second.EmptyDiff() {
		t.Fatalf("second diff = %#v, want empty", second)
	}

	provider.blocks[0].Content = "rules v2"
	third, err := m.Build(context.Background(), BuildRequest{TurnID: "turn-3"})
	if err != nil {
		t.Fatalf("third build: %v", err)
	}
	if len(third.Updated) != 1 || third.Updated[0].Content != "rules v2" {
		t.Fatalf("third updated = %#v, want changed block", third.Updated)
	}

	provider.blocks = nil
	fourth, err := m.Build(context.Background(), BuildRequest{TurnID: "turn-4"})
	if err != nil {
		t.Fatalf("fourth build: %v", err)
	}
	if len(fourth.Removed) != 1 || fourth.Removed[0].ID != "docs/agents" {
		t.Fatalf("fourth removed = %#v, want docs/agents", fourth.Removed)
	}
}

func TestMaterializerFingerprintSkipsBuild(t *testing.T) {
	provider := &testProvider{
		spec:        ProviderSpec{Name: "env"},
		fingerprint: "same",
		blocks:      []Block{{ID: "env/1", Content: "stable"}},
	}
	m := NewMaterializer([]Provider{provider}, nil)
	first, err := m.Build(context.Background(), BuildRequest{})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	provider.fingerprint = first.Records["env"].Fingerprint
	provider.blocks = []Block{{ID: "env/1", Content: "changed but skipped"}}
	second, err := m.Build(context.Background(), BuildRequest{})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !second.EmptyDiff() || provider.builds != 1 || provider.fingerprints != 1 {
		t.Fatalf("second = %#v builds=%d fingerprints=%d, want skipped", second, provider.builds, provider.fingerprints)
	}
}

func TestMaterializerPassesObservationsToProviders(t *testing.T) {
	provider := &testProvider{
		spec:   ProviderSpec{Name: "env"},
		blocks: []Block{{ID: "env/1", Content: "context"}},
	}
	m := NewMaterializer([]Provider{provider}, nil)
	_, err := m.Build(context.Background(), BuildRequest{
		Observations: []coreevidence.Observation{{
			Kind:    "kubernetes.context",
			Content: "k3d-ai",
		}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(provider.lastReq.Observations) != 1 || provider.lastReq.Observations[0].Kind != "kubernetes.context" {
		t.Fatalf("provider observations = %#v, want kubernetes observation", provider.lastReq.Observations)
	}
}

func TestMaterializerPassesObservationsToFingerprinter(t *testing.T) {
	provider := &testProvider{
		spec:        ProviderSpec{Name: "env"},
		fingerprint: "different",
		blocks:      []Block{{ID: "env/1", Content: "context"}},
	}
	m := NewMaterializer([]Provider{provider}, map[ProviderName]ProviderRenderRecord{
		"env": {Provider: "env", Fingerprint: "previous"},
	})
	_, err := m.Build(context.Background(), BuildRequest{
		Observations: []coreevidence.Observation{{Kind: "channel.message", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(provider.lastFingerprintReq.Observations) != 1 || provider.lastFingerprintReq.Observations[0].Kind != "channel.message" {
		t.Fatalf("fingerprint observations = %#v, want channel message", provider.lastFingerprintReq.Observations)
	}
}

func TestRenderDiffSeparatesPlacement(t *testing.T) {
	result := BuildResult{
		Providers: []ProviderDiff{{
			Provider: "mixed",
			Added: []Block{
				{ID: "mixed/user", Provider: "mixed", Placement: PlacementUser, Content: "user"},
				{ID: "mixed/system", Provider: "mixed", Placement: PlacementSystem, Content: "system"},
			},
		}},
		Added: []Block{
			{ID: "mixed/user", Provider: "mixed", Placement: PlacementUser, Content: "user"},
			{ID: "mixed/system", Provider: "mixed", Placement: PlacementSystem, Content: "system"},
		},
	}
	user, ok := RenderDiff(result, PlacementUser)
	if !ok || !contains(user, "mixed/user") || contains(user, "mixed/system") {
		t.Fatalf("user diff = %q, want only user block", user)
	}
	system, ok := RenderDiff(result, PlacementSystem)
	if !ok || !contains(system, "mixed/system") || contains(system, "mixed/user") {
		t.Fatalf("system diff = %q, want only system block", system)
	}
}

type testProvider struct {
	spec               ProviderSpec
	blocks             []Block
	fingerprint        string
	builds             int
	fingerprints       int
	lastReq            Request
	lastFingerprintReq Request
}

func (p *testProvider) Spec() ProviderSpec { return p.spec }

func (p *testProvider) Build(_ context.Context, req Request) ([]Block, error) {
	p.builds++
	p.lastReq = req
	return append([]Block(nil), p.blocks...), nil
}

func (p *testProvider) StateFingerprint(_ context.Context, req Request) (string, bool, error) {
	p.fingerprints++
	p.lastFingerprintReq = req
	if p.fingerprint == "" {
		return "", false, nil
	}
	return p.fingerprint, true, nil
}

func contains(text, part string) bool {
	return strings.Contains(text, part)
}
