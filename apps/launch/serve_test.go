package launch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
)

func TestServeChannelsUsesEmptySlackConnectorFallback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	instances := credential.NewInstanceStore(filepath.Join(dir, "instances"))
	credentials := credential.NewFileStore(filepath.Join(dir, "credentials"))
	if err := instances.Save(ctx, credential.Instance{
		ID:        "workspace-prod",
		Connector: "slack",
	}); err != nil {
		t.Fatalf("Save instance: %v", err)
	}
	if err := credentials.Save(ctx, "workspace-prod", connector.Credentials{
		Auth:   connector.AuthState{Kind: connector.AuthToken, Token: "xoxb-test"},
		Fields: map[string]string{"app_token": "xapp-test"},
	}); err != nil {
		t.Fatalf("Save credentials: %v", err)
	}

	channels, err := serveChannels(ctx, []appconfig.ChannelDoc{{
		Name:    "slack-main",
		Type:    "slack",
		Session: "slack-main",
	}}, Options{AuthPath: dir}, slackplugin.NewDispatcher())
	if err != nil {
		t.Fatalf("serveChannels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(channels))
	}
}
