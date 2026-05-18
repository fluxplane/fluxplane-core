package authconnect

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
)

func TestCollectFieldsRejectsSensitivePromptOnNonTerminal(t *testing.T) {
	_, err := collectFields(options{
		in:  strings.NewReader("secret\n"),
		out: bytes.NewBuffer(nil),
	}, []coresecret.SetupFieldSpec{{
		Name:      "client_secret",
		Required:  true,
		Sensitive: true,
	}})
	if err == nil {
		t.Fatal("collectFields succeeded, want non-terminal sensitive prompt error")
	}
}

func TestCollectFieldsUsesEnvironmentForSensitiveField(t *testing.T) {
	t.Setenv("TEST_CLIENT_SECRET", "secret")
	fields, err := collectFields(options{
		in:  strings.NewReader(""),
		out: bytes.NewBuffer(nil),
	}, []coresecret.SetupFieldSpec{{
		Name:      "client_secret",
		Required:  true,
		Sensitive: true,
		Env:       coresecret.EnvSpec{Aliases: []string{"TEST_CLIENT_SECRET"}},
	}})
	if err != nil {
		t.Fatalf("collectFields: %v", err)
	}
	if fields["client_secret"] != "secret" {
		t.Fatalf("client_secret = %q", fields["client_secret"])
	}
}

func TestRunStoredWritesSetupFieldsToNativeSecretStore(t *testing.T) {
	dir := t.TempDir()
	out := bytes.Buffer{}
	ref := resource.PluginRef{Name: "slack", Instance: "main"}
	err := runStored(context.Background(), options{
		authPath: dir,
		fields:   []string{"bot_token=xoxb-test", "app_token=xapp-test"},
		out:      &out,
	}, ref, coresecret.AuthMethodSpec{
		Name:   "bot_token",
		Method: coresecret.AuthMethodStored,
		Kind:   coresecret.KindBearerToken,
		Secret: coresecret.Plugin("slack", "main", "bot_token"),
		SetupFields: []coresecret.SetupFieldSpec{
			{Name: "bot_token", Required: true, Sensitive: true},
			{Name: "app_token", Sensitive: true},
		},
	})
	if err != nil {
		t.Fatalf("runStored: %v", err)
	}
	store := runtimesecret.NewFileStore(dir)
	bot, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("slack", "main", "bot_token"))
	if err != nil || !ok || bot.Value != "xoxb-test" {
		t.Fatalf("bot secret = %#v ok=%v err=%v", bot, ok, err)
	}
	app, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("slack", "main", "app_token"))
	if err != nil || !ok || app.Value != "xapp-test" {
		t.Fatalf("app secret = %#v ok=%v err=%v", app, ok, err)
	}
	if !strings.Contains(out.String(), "Connected slack instance main") {
		t.Fatalf("output = %q", out.String())
	}
}
