package identity

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/user"
)

func TestDefaultResolverMarksCanonicalUserResolved(t *testing.T) {
	resolved, err := (DefaultResolver{}).ResolveIdentity(context.Background(), Request{Inbound: channel.Inbound{
		Caller: policy.Caller{
			Kind:      policy.CallerUser,
			Principal: policy.Principal{Kind: "user", ID: "timo@localhost", Name: "timo"},
			Source:    "local",
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
	}})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if resolved.Actor.Resolution != user.ResolutionResolved {
		t.Fatalf("resolution = %q, want resolved", resolved.Actor.Resolution)
	}
	if resolved.Actor.User.ID != "timo@localhost" {
		t.Fatalf("user id = %q, want timo@localhost", resolved.Actor.User.ID)
	}
}

func TestDefaultResolverMarksProviderIdentityUnresolved(t *testing.T) {
	resolved, err := (DefaultResolver{}).ResolveIdentity(context.Background(), Request{Inbound: channel.Inbound{
		Caller: policy.Caller{
			Kind:      policy.CallerUser,
			Principal: policy.Principal{Kind: "slack_user", ID: "U123"},
			Source:    "slack:main",
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustUntrusted},
	}})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if resolved.Actor.Resolution != user.ResolutionUnresolved {
		t.Fatalf("resolution = %q, want unresolved", resolved.Actor.Resolution)
	}
	if resolved.Actor.User.ID != "slack_user:U123" {
		t.Fatalf("user id = %q, want slack_user:U123", resolved.Actor.User.ID)
	}
	if resolved.Actor.Identity.Provider != "slack_user" || resolved.Actor.Identity.ProviderID != "U123" {
		t.Fatalf("identity = %#v, want slack_user U123", resolved.Actor.Identity)
	}
}

func TestEnrichActorAddsExternalIdentities(t *testing.T) {
	actor := user.Actor{
		User:       user.User{ID: "timo@company.org", Identities: []user.Identity{{Provider: "slack", ProviderID: "U123"}}},
		Identity:   user.Identity{Provider: "slack", ProviderID: "U123"},
		Resolution: user.ResolutionResolved,
	}
	enriched := EnrichActor(context.Background(), actor, ChainExternalResolver{Resolvers: []ExternalResolver{
		ExternalResolverFunc(func(context.Context, ExternalRequest) (ExternalResult, error) {
			return ExternalResult{Identities: []user.Identity{{Provider: "gitlab/main", ProviderID: "tfriedl"}}}, nil
		}),
		ExternalResolverFunc(func(context.Context, ExternalRequest) (ExternalResult, error) {
			return ExternalResult{Identities: []user.Identity{{Provider: "gitlab/main", ProviderID: "tfriedl"}}}, nil
		}),
	}})
	if len(enriched.Identities) != 2 {
		t.Fatalf("identities = %#v, want entry plus deduped external identity", enriched.Identities)
	}
	if enriched.Identities[1].Provider != "gitlab/main" || enriched.Identities[1].ProviderID != "tfriedl" {
		t.Fatalf("identities = %#v, want GitLab identity", enriched.Identities)
	}
	if len(enriched.User.Identities) != 2 {
		t.Fatalf("user identities = %#v, want merged identities", enriched.User.Identities)
	}
}
