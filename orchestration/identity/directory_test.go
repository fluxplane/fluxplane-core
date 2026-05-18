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

func TestDirectoryResolverMapsVerifiedEmailAliasToCanonicalUser(t *testing.T) {
	resolver, err := NewDirectoryResolver(coreapp.IdentitySpec{
		Users: []user.User{{
			ID:       "timo.friedl@company.org",
			Username: "timo",
			Emails: []user.Email{
				{Address: "timo.friedl@company.org", Primary: true, Verified: true},
				{Address: "timo@company.org", Verified: true},
				{Address: "unverified@company.org"},
			},
		}},
	}, resolvedSlackFallback("U123", "timo@company.org"))
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}
	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: slackInbound("U123", policy.TrustUntrusted, false)})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if result.Actor.User.ID != "timo.friedl@company.org" {
		t.Fatalf("user = %#v, want canonical user from verified email alias", result.Actor.User)
	}
	if len(result.Actor.User.Emails) != 3 || !result.Actor.User.Emails[0].Primary || !result.Actor.User.Emails[1].Verified {
		t.Fatalf("emails = %#v, want configured aliases preserved", result.Actor.User.Emails)
	}
}

func TestDirectoryResolverAppliesResolvedSlackGroupRules(t *testing.T) {
	resolver, err := NewDirectoryResolver(slackBotIdentitySpec(), resolvedSlackFallback("U111", "mara@company.org"))
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}
	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: slackInbound("U111", policy.TrustUntrusted, false)})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if result.Actor.User.ID != "mara@company.org" || result.Actor.Resolution != user.ResolutionResolved {
		t.Fatalf("actor = %#v, want resolved email user", result.Actor)
	}
	if !hasActorGroup(result.Actor, "slack-bot-users") || hasActorGroup(result.Actor, "slack-bot-admin") {
		t.Fatalf("groups = %#v, want slack-bot-users only", result.Actor.Groups)
	}
	if result.Trust.Level != policy.TrustVerified {
		t.Fatalf("trust = %#v, want verified from users group", result.Trust)
	}
}

func TestDirectoryResolverAppliesSlackAdminRuleOnlyWhenResolved(t *testing.T) {
	resolver, err := NewDirectoryResolver(slackBotIdentitySpec(), resolvedSlackFallback("U03HY52RQLV", "admin@company.org"))
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}
	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: slackInbound("U03HY52RQLV", policy.TrustUntrusted, false)})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if !hasActorGroup(result.Actor, "slack-bot-admin") || !hasActorGroup(result.Actor, "slack-bot-users") {
		t.Fatalf("groups = %#v, want admin and users", result.Actor.Groups)
	}
	if result.Trust.Level != policy.TrustPrivileged {
		t.Fatalf("trust = %#v, want privileged from admin group", result.Trust)
	}

	unresolved, err := NewDirectoryResolver(slackBotIdentitySpec(), nil)
	if err != nil {
		t.Fatalf("NewDirectoryResolver unresolved: %v", err)
	}
	result, err = unresolved.ResolveIdentity(context.Background(), Request{Inbound: slackInbound("U03HY52RQLV", policy.TrustUntrusted, false)})
	if err != nil {
		t.Fatalf("ResolveIdentity unresolved: %v", err)
	}
	if result.Actor.Resolution != user.ResolutionUnresolved || !hasActorGroup(result.Actor, "anonymous") || hasActorGroup(result.Actor, "slack-bot-admin") {
		t.Fatalf("actor = %#v, want unresolved anonymous without admin", result.Actor)
	}
	if result.Trust.Level != policy.TrustUntrusted {
		t.Fatalf("trust = %#v, want untrusted unresolved", result.Trust)
	}
}

func TestDirectoryResolverRuleTrustRespectsDowngrade(t *testing.T) {
	resolver, err := NewDirectoryResolver(slackBotIdentitySpec(), resolvedSlackFallback("U03HY52RQLV", "admin@company.org"))
	if err != nil {
		t.Fatalf("NewDirectoryResolver: %v", err)
	}
	result, err := resolver.ResolveIdentity(context.Background(), Request{Inbound: slackInbound("U03HY52RQLV", policy.TrustUntrusted, true)})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if !hasActorGroup(result.Actor, "slack-bot-admin") {
		t.Fatalf("groups = %#v, want admin group still visible", result.Actor.Groups)
	}
	if result.Trust.Level != policy.TrustUntrusted {
		t.Fatalf("trust = %#v, want downgraded untrusted preserved", result.Trust)
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

func TestDirectoryResolverRejectsConflictingVerifiedEmailAlias(t *testing.T) {
	_, err := NewDirectoryResolver(coreapp.IdentitySpec{
		Users: []user.User{
			{ID: "timo@company.org", Emails: []user.Email{{Address: "alias@company.org", Verified: true}}},
			{ID: "other@company.org", Emails: []user.Email{{Address: "alias@company.org", Verified: true}}},
		},
	}, nil)
	if err == nil {
		t.Fatal("NewDirectoryResolver succeeded, want email alias conflict")
	}
}

func slackBotIdentitySpec() coreapp.IdentitySpec {
	return coreapp.IdentitySpec{
		Groups: []user.Group{
			{ID: "slack-bot-admin", Trust: user.TrustOperator},
			{ID: "slack-bot-users", Trust: user.TrustInternal},
			{ID: "anonymous", Trust: user.TrustPublic},
		},
		Rules: []user.GroupRule{
			{Match: user.IdentityMatch{Provider: "slack", Resolution: user.ResolutionResolved}, Groups: []user.ID{"slack-bot-users"}},
			{Match: user.IdentityMatch{Provider: "slack", ProviderID: "U03HY52RQLV", Resolution: user.ResolutionResolved}, Groups: []user.ID{"slack-bot-admin"}},
			{Match: user.IdentityMatch{Provider: "slack", Resolution: user.ResolutionUnresolved}, Groups: []user.ID{"anonymous"}},
		},
	}
}

func resolvedSlackFallback(slackID, email string) Resolver {
	return ResolverFunc(func(_ context.Context, req Request) (Result, error) {
		result, err := (DefaultResolver{}).ResolveIdentity(context.Background(), req)
		if err != nil {
			return Result{}, err
		}
		result.Actor = user.Actor{
			User:       user.User{ID: user.ID(email), Username: email, Trust: user.TrustPublic},
			Identity:   user.Identity{Provider: "slack", ProviderID: slackID, Email: email},
			Trust:      user.TrustPublic,
			Resolution: user.ResolutionResolved,
		}
		return result, nil
	})
}

func slackInbound(slackID string, trust policy.TrustLevel, downgraded bool) channel.Inbound {
	return channel.Inbound{
		Caller: policy.Caller{
			Kind:      policy.CallerUser,
			Principal: policy.Principal{Kind: "slack_user", ID: slackID},
			Source:    "slack:main",
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: trust, Downgraded: downgraded},
	}
}

func hasActorGroup(actor user.Actor, id user.ID) bool {
	for _, groupID := range actor.User.Groups {
		if groupID == id {
			return true
		}
	}
	for _, group := range actor.Groups {
		if group.ID == id {
			return true
		}
	}
	return false
}
