package connectauth

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
)

func TestLoadSlackResolvesConnectorCredentials(t *testing.T) {
	dir := t.TempDir()
	store := Store{
		Credentials: credential.NewFileStore(filepath.Join(dir, "credentials")),
		Instances:   credential.NewInstanceStore(filepath.Join(dir, "instances")),
	}
	ctx := context.Background()
	if err := store.Instances.Save(ctx, credential.Instance{
		ID:         "slack",
		Connector:  "slack",
		AuthMethod: "token",
		Fields:     map[string]string{"team_id": "T1"},
		Grants:     map[string]credential.Grant{"bot": {Role: "bot", PrincipalID: "Ubot"}},
	}); err != nil {
		t.Fatalf("Save instance: %v", err)
	}
	if err := store.Credentials.Save(ctx, "slack", connector.Credentials{
		Fields: map[string]string{"app_token": "xapp-test", "user_token": "xoxp-test"},
		Auth:   connector.AuthState{Kind: connector.AuthToken, Token: "xoxb-test"},
	}); err != nil {
		t.Fatalf("Save credentials: %v", err)
	}

	got, err := store.LoadSlack(ctx, "")
	if err != nil {
		t.Fatalf("LoadSlack: %v", err)
	}
	if got.BotToken != "xoxb-test" || got.AppToken != "xapp-test" || got.BotUserID != "Ubot" || got.TeamID != "T1" {
		t.Fatalf("credentials = %#v", got)
	}
	if got.UserToken != "xoxp-test" {
		t.Fatalf("user token = %q, want xoxp-test", got.UserToken)
	}
}
