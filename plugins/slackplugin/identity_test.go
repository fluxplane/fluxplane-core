package slackplugin

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if result.Trust.Level != policy.TrustUntrusted {
		t.Fatalf("trust = %#v, want unchanged inbound trust", result.Trust)
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
