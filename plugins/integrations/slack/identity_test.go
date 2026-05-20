package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/slack-go/slack"
)

func TestSlackIdentityResolverResolvesEmail(t *testing.T) {
	api := slack.New("xoxb-test", slack.OptionAPIURL(slackUserInfoServer(t, "timo@company.org").URL+"/"))
	resolver := NewIdentityResolver(IdentityResolverConfig{ChannelName: "main", API: api})
	result, err := resolver.ResolveIdentity(context.Background(), identity.Request{Inbound: channel.Inbound{
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
		t.Fatalf("user id = %q, want Slack profile email", result.Actor.User.ID)
	}
	if len(result.Actor.Identities) != 1 {
		t.Fatalf("actor identities = %d, want Slack entry identity", len(result.Actor.Identities))
	}
	if got := result.Actor.Identities[0]; got.Provider != "slack" || got.ProviderID != "U123" || got.Email != "timo@company.org" {
		t.Fatalf("actor identity = %#v, want resolved Slack identity", got)
	}
	if result.Trust.Level != policy.TrustUntrusted {
		t.Fatalf("trust = %#v, want unchanged inbound trust", result.Trust)
	}
}

func TestSlackIdentityResolverPrefersUserTokenForEmail(t *testing.T) {
	server := slackUserInfoTokenServer(t)
	defer server.Close()
	resolver := NewIdentityResolver(IdentityResolverConfig{
		ChannelName: "main",
		UserAPI:     slack.New("xoxp-user", slack.OptionAPIURL(server.URL+"/")),
		API:         slack.New("xoxb-bot", slack.OptionAPIURL(server.URL+"/")),
	})
	result, err := resolver.ResolveIdentity(context.Background(), identity.Request{Inbound: channel.Inbound{
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
	if result.Actor.User.ID != "timo@company.org" {
		t.Fatalf("user id = %q, want user-token email", result.Actor.User.ID)
	}
}

func TestSlackIdentityResolverUsesProfileEmailFallback(t *testing.T) {
	api := slack.New("xoxb-test", slack.OptionAPIURL(slackProfileEmailServer(t).URL+"/"))
	resolver := NewIdentityResolver(IdentityResolverConfig{ChannelName: "main", API: api})
	result, err := resolver.ResolveIdentity(context.Background(), identity.Request{Inbound: channel.Inbound{
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
	if result.Actor.User.ID != "timo@company.org" {
		t.Fatalf("user id = %q, want profile email", result.Actor.User.ID)
	}
}

func TestSlackIdentityResolverIgnoresOtherSlackChannel(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
		t.Fatal("users.info should not be called for another channel")
	}))
	defer server.Close()
	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	resolver := NewIdentityResolver(IdentityResolverConfig{ChannelName: "main", API: api})
	result, err := resolver.ResolveIdentity(context.Background(), identity.Request{Inbound: channel.Inbound{
		Caller: policy.Caller{
			Kind:      policy.CallerUser,
			Principal: policy.Principal{Kind: "slack_user", ID: "U123"},
			Source:    "slack:other",
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustUntrusted},
	}})
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if called {
		t.Fatal("users.info was called")
	}
	if result.Actor.Resolution != user.ResolutionUnresolved {
		t.Fatalf("resolution = %q, want fallback unresolved", result.Actor.Resolution)
	}
}

func slackUserInfoServer(t *testing.T, email string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("user"); got != "U123" {
			t.Fatalf("user = %q, want U123", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U123","name":"timo","real_name":"Timo","profile":{"email":"` + email + `","display_name":"Timo"}}}`))
	}))
}

func slackUserInfoTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		calls++
		if got := r.Form.Get("user"); got != "U123" {
			t.Fatalf("user = %q, want U123", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch token := r.Form.Get("token"); token {
		case "xoxp-user":
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U123","name":"timo","real_name":"Timo","profile":{"email":"timo@company.org","display_name":"Timo"}}}`))
		case "xoxb-bot":
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U123","name":"timo","real_name":"Timo","profile":{"display_name":"Timo"}}}`))
		default:
			t.Fatalf("unexpected token %q after %d calls", token, calls)
		}
	}))
}

func slackProfileEmailServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/users.info"):
			if got := r.Form.Get("user"); got != "U123" {
				t.Fatalf("users.info user = %q, want U123", got)
			}
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U123","name":"timo","real_name":"Timo","profile":{"display_name":"Timo"}}}`))
		case strings.HasSuffix(r.URL.Path, "/users.profile.get"):
			if got := r.Form.Get("user"); got != "U123" {
				t.Fatalf("users.profile.get user = %q, want U123", got)
			}
			_, _ = w.Write([]byte(`{"ok":true,"profile":{"email":"timo@company.org","display_name":"Timo","real_name":"Timo"}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
}
