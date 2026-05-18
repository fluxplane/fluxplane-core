package identityplugin

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
)

func TestCurrentProviderRendersResolvedUser(t *testing.T) {
	provider := currentProvider{}
	blocks, err := provider.Build(context.Background(), corecontext.Request{Scope: map[string]string{
		"user.resolution":      "resolved",
		"user.id":              "timo@localhost",
		"user.username":        "timo@localhost",
		"user.groups":          "local_operators,local_users",
		"identity.provider":    "local",
		"identity.provider_id": "timo",
		"caller.source":        "local",
		"trust.level":          "privileged",
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks len = %d, want 1", len(blocks))
	}
	content := blocks[0].Content
	for _, want := range []string{
		"- resolved: true",
		"- user: timo@localhost",
		"- groups: local_operators, local_users",
		"- identity: local:timo",
		"- trust: privileged",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, want %q", content, want)
		}
	}
}

func TestCurrentProviderRendersUnresolvedChannelIdentityWithoutClaims(t *testing.T) {
	provider := currentProvider{}
	blocks, err := provider.Build(context.Background(), corecontext.Request{Scope: map[string]string{
		"user.resolution":      "unresolved",
		"user.id":              "slack_user:U123",
		"user.username":        "slack_user:U123",
		"identity.provider":    "slack_user",
		"identity.provider_id": "U123",
		"user.groups":          "anonymous",
		"caller.source":        "slack:main",
		"trust.level":          "untrusted",
		"is_admin":             "true",
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks len = %d, want 1", len(blocks))
	}
	content := blocks[0].Content
	for _, want := range []string{
		"- resolved: false",
		"- channel identity: slack_user:U123",
		"- note: no canonical core user has been resolved for this turn",
		"- groups: anonymous",
		"- trust: untrusted",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, want %q", content, want)
		}
	}
	for _, forbidden := range []string{"is_admin", "true"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("content = %q, want no raw claim %q", content, forbidden)
		}
	}
}
