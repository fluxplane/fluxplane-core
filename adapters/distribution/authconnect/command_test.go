package authconnect

import (
	"bytes"
	"strings"
	"testing"

	coresecret "github.com/fluxplane/agentruntime/core/secret"
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
