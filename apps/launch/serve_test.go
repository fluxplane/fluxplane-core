package launch

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
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

	channels, err := serveChannels(ctx, []distribution.Channel{{
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

func TestValidateServeLaunchSuggestsInitForUninitializedPath(t *testing.T) {
	err := validateServeLaunch(distribution.Loaded{
		Root: "/repo/sample",
		Distribution: distribution.Distribution{
			Spec: coredistribution.Spec{Name: "sample"},
		},
	}, "sample")
	if err == nil || !strings.Contains(err.Error(), "agentsdk init sample") {
		t.Fatalf("validateServeLaunch error = %v, want init guidance", err)
	}
}

func TestValidateServeLaunchRequiresEntryPointForManifest(t *testing.T) {
	err := validateServeLaunch(distribution.Loaded{
		Manifest: "/repo/sample/agentsdk.app.yaml",
		Distribution: distribution.Distribution{
			Spec: coredistribution.Spec{Name: "sample"},
		},
	}, "sample")
	if err == nil || !strings.Contains(err.Error(), "no daemon listeners or channels") {
		t.Fatalf("validateServeLaunch error = %v, want no daemon listeners or channels", err)
	}
}

func TestServeCommandDefaultsPathToCurrentDirectory(t *testing.T) {
	var got Options
	cmd := NewServeCommandWithRunner(func(_ context.Context, opts Options) error {
		got = opts
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.AppDir != "." {
		t.Fatalf("app dir = %q, want .", got.AppDir)
	}
}

func TestServeCommandForwardsYolo(t *testing.T) {
	var got Options
	cmd := NewServeCommandWithRunner(func(_ context.Context, opts Options) error {
		got = opts
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--yolo"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !got.Yolo {
		t.Fatalf("yolo = false, want true")
	}
}
