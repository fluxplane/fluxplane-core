package context

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
)

func TestMaterializerEmitsOnlyChangedBlocks(t *testing.T) {
	provider := &testProvider{
		spec: corecontext.ProviderSpec{Name: "docs", DefaultPlacement: corecontext.PlacementSystem},
		blocks: []corecontext.Block{{
			ID:      "docs/agents",
			Content: "rules v1",
		}},
	}
	m := NewMaterializer([]corecontext.Provider{provider}, nil)
	first, err := m.Build(context.Background(), corecontext.BuildRequest{TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if len(first.Added) != 1 || first.Added[0].Placement != corecontext.PlacementSystem {
		t.Fatalf("first added = %#v, want system block", first.Added)
	}

	second, err := m.Build(context.Background(), corecontext.BuildRequest{TurnID: "turn-2"})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !second.EmptyDiff() {
		t.Fatalf("second diff = %#v, want empty", second)
	}

	provider.blocks[0].Content = "rules v2"
	third, err := m.Build(context.Background(), corecontext.BuildRequest{TurnID: "turn-3"})
	if err != nil {
		t.Fatalf("third build: %v", err)
	}
	if len(third.Updated) != 1 || third.Updated[0].Content != "rules v2" {
		t.Fatalf("third updated = %#v, want changed block", third.Updated)
	}

	provider.blocks = nil
	fourth, err := m.Build(context.Background(), corecontext.BuildRequest{TurnID: "turn-4"})
	if err != nil {
		t.Fatalf("fourth build: %v", err)
	}
	if len(fourth.Removed) != 1 || fourth.Removed[0].ID != "docs/agents" {
		t.Fatalf("fourth removed = %#v, want docs/agents", fourth.Removed)
	}
}

func TestMaterializerFingerprintSkipsBuild(t *testing.T) {
	provider := &testProvider{
		spec:        corecontext.ProviderSpec{Name: "env"},
		fingerprint: "same",
		blocks:      []corecontext.Block{{ID: "env/1", Content: "stable"}},
	}
	m := NewMaterializer([]corecontext.Provider{provider}, nil)
	first, err := m.Build(context.Background(), corecontext.BuildRequest{})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	provider.fingerprint = first.Records["env"].Fingerprint
	provider.blocks = []corecontext.Block{{ID: "env/1", Content: "changed but skipped"}}
	second, err := m.Build(context.Background(), corecontext.BuildRequest{})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !second.EmptyDiff() || provider.builds != 1 || provider.fingerprints != 1 {
		t.Fatalf("second = %#v builds=%d fingerprints=%d, want skipped", second, provider.builds, provider.fingerprints)
	}
}

func TestRenderDiffSeparatesPlacement(t *testing.T) {
	result := corecontext.BuildResult{
		Providers: []corecontext.ProviderDiff{{
			Provider: "mixed",
			Added: []corecontext.Block{
				{ID: "mixed/user", Provider: "mixed", Placement: corecontext.PlacementUser, Content: "user"},
				{ID: "mixed/system", Provider: "mixed", Placement: corecontext.PlacementSystem, Content: "system"},
			},
		}},
		Added: []corecontext.Block{
			{ID: "mixed/user", Provider: "mixed", Placement: corecontext.PlacementUser, Content: "user"},
			{ID: "mixed/system", Provider: "mixed", Placement: corecontext.PlacementSystem, Content: "system"},
		},
	}
	user, ok := RenderDiff(result, corecontext.PlacementUser)
	if !ok || !contains(user, "mixed/user") || contains(user, "mixed/system") {
		t.Fatalf("user diff = %q, want only user block", user)
	}
	system, ok := RenderDiff(result, corecontext.PlacementSystem)
	if !ok || !contains(system, "mixed/system") || contains(system, "mixed/user") {
		t.Fatalf("system diff = %q, want only system block", system)
	}
}

type testProvider struct {
	spec         corecontext.ProviderSpec
	blocks       []corecontext.Block
	fingerprint  string
	builds       int
	fingerprints int
}

func (p *testProvider) Spec() corecontext.ProviderSpec { return p.spec }

func (p *testProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	p.builds++
	return append([]corecontext.Block(nil), p.blocks...), nil
}

func (p *testProvider) StateFingerprint(context.Context, corecontext.Request) (string, bool, error) {
	p.fingerprints++
	if p.fingerprint == "" {
		return "", false, nil
	}
	return p.fingerprint, true, nil
}

func contains(text, part string) bool {
	return strings.Contains(text, part)
}
