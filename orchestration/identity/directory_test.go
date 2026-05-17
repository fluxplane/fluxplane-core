package identity

import (
	"context"
	"testing"

	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/user"
)

func TestDirectoryResolverMapsSlackUserToCanonicalUserAndGroups(t *testing.T) {
	resolver, err := NewDirectoryResolver(coreapp.IdentitySpec{
		Users: []user.User{{
			ID:         "timo@company.org",
			Username:   "timo",
			Identities: []user.Identity{{Provider: "slack", ProviderID: "U123"}},
		}},
		Groups: []user.Group{{
			ID:      "admins",
			Members: []user.ID{"timo@company.org"},
			Trust:   user.TrustOperator,
		}},
	}, nil)
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}

	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: channel.Inbound{
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
	if result.Actor.Resolution != user.ResolutionResolved {
		t.Fatalf("resolution = %q, want resolved", result.Actor.Resolution)
	}
	if result.Actor.User.ID != "timo@company.org" {
		t.Fatalf("user = %#v, want canonical user", result.Actor.User)
	}
	if result.Actor.Identity.Provider != "slack" || result.Actor.Identity.ProviderID != "U123" {
		t.Fatalf("identity = %#v, want configured slack identity", result.Actor.Identity)
	}
	if len(result.Actor.Groups) != 1 || result.Actor.Groups[0].ID != "admins" {
		t.Fatalf("groups = %#v, want admins", result.Actor.Groups)
	}
	if result.Trust.Level != policy.TrustPrivileged {
		t.Fatalf("trust = %#v, want privileged from admin group", result.Trust)
	}
}

func TestDirectoryResolverDoesNotOverrideTrustDowngrade(t *testing.T) {
	resolver, err := NewDirectoryResolver(coreapp.IdentitySpec{
		Users: []user.User{{
			ID:         "timo@company.org",
			Trust:      user.TrustOperator,
			Identities: []user.Identity{{Provider: "slack", ProviderID: "U123"}},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}

	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: channel.Inbound{
		Caller: policy.Caller{
			Kind:      policy.CallerUser,
			Principal: policy.Principal{Kind: "slack_user", ID: "U123"},
			Source:    "slack:main",
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustUntrusted, Downgraded: true},
	}})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if result.Actor.User.ID != "timo@company.org" || result.Actor.Resolution != user.ResolutionResolved {
		t.Fatalf("actor = %#v, want resolved canonical user", result.Actor)
	}
	if result.Trust.Level != policy.TrustUntrusted {
		t.Fatalf("trust = %#v, want downgraded untrusted preserved", result.Trust)
	}
}

func TestDirectoryResolverOverlaysGroupsForFallbackUser(t *testing.T) {
	resolver, err := NewDirectoryResolver(coreapp.IdentitySpec{
		Groups: []user.Group{{
			ID:      "admins",
			Members: []user.ID{"timo@company.org"},
			Trust:   user.TrustOperator,
		}},
	}, ResolverFunc(func(_ context.Context, req Request) (Result, error) {
		result, err := (DefaultResolver{}).ResolveIdentity(context.Background(), req)
		if err != nil {
			return Result{}, err
		}
		result.Actor = user.Actor{
			User:       user.User{ID: "timo@company.org", Username: "timo@company.org", Trust: user.TrustInternal},
			Identity:   user.Identity{Provider: "slack", ProviderID: "U123", Email: "timo@company.org"},
			Trust:      user.TrustInternal,
			Resolution: user.ResolutionResolved,
		}
		return result, nil
	}))
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}
	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: channel.Inbound{
		Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "slack_user", ID: "U123"}},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	}})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if len(result.Actor.Groups) != 1 || result.Actor.Groups[0].ID != "admins" {
		t.Fatalf("groups = %#v, want admin overlay", result.Actor.Groups)
	}
	if result.Trust.Level != policy.TrustPrivileged {
		t.Fatalf("trust = %#v, want group trust overlay", result.Trust)
	}
}

func TestDirectoryResolverRejectsConflictingProviderMapping(t *testing.T) {
	_, err := NewDirectoryResolver(coreapp.IdentitySpec{
		Users: []user.User{
			{ID: "timo@company.org", Identities: []user.Identity{{Provider: "slack", ProviderID: "U123"}}},
			{ID: "other@company.org", Identities: []user.Identity{{Provider: "slack_user", ProviderID: "U123"}}},
		},
	}, nil)
	if err == nil {
		t.Fatal("NewDirectoryResolver succeeded, want conflict")
	}
}
