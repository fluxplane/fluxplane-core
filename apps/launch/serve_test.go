package launch

import (
	"context"
	"io"
	"strings"
	"testing"

	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/plugins/integrations/slack"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
)

func TestServeChannelsUsesNativeSlackInstance(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	ref := resource.PluginRef{Name: slack.Name, Instance: "workspace-prod"}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{Ref: slack.BotTokenSecretRef(ref), Value: "slack-bot-token"}); err != nil {
		t.Fatalf("Save bot token: %v", err)
	}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{Ref: slack.AppTokenSecretRef(ref), Value: "slack-app-token"}); err != nil {
		t.Fatalf("Save app token: %v", err)
	}

	channels, err := serveChannels(ctx, []distribution.Channel{{
		Name:     "slack-main",
		Type:     "slack",
		Instance: "workspace-prod",
		Session:  "slack-main",
	}}, nil, Options{AuthPath: dir}, slack.NewDispatcher(), nil)
	if err != nil {
		t.Fatalf("serveChannels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(channels))
	}
}

func TestServeChannelsAllowsNativeSlackUserTokenInstance(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	ref := resource.PluginRef{Name: slack.Name, Instance: "workspace-prod"}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{Ref: slack.UserTokenSecretRef(ref), Value: "slack-user-token"}); err != nil {
		t.Fatalf("Save user token: %v", err)
	}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{Ref: slack.AppTokenSecretRef(ref), Value: "slack-app-token"}); err != nil {
		t.Fatalf("Save app token: %v", err)
	}

	channels, err := serveChannels(ctx, []distribution.Channel{{
		Name:     "slack-main",
		Type:     "slack",
		Instance: "workspace-prod",
		Session:  "slack-main",
	}}, nil, Options{AuthPath: dir}, slack.NewDispatcher(), nil)
	if err != nil {
		t.Fatalf("serveChannels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(channels))
	}
}

func TestServeChannelsHonorsSlackChannelTokenPreference(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	ref := resource.PluginRef{Name: slack.Name, Instance: "workspace-prod"}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{Ref: slack.UserTokenSecretRef(ref), Value: "slack-user-token"}); err != nil {
		t.Fatalf("Save user token: %v", err)
	}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{Ref: slack.AppTokenSecretRef(ref), Value: "slack-app-token"}); err != nil {
		t.Fatalf("Save app token: %v", err)
	}

	_, err := serveChannels(ctx, []distribution.Channel{{
		Name:     "slack-main",
		Type:     "slack",
		Instance: "workspace-prod",
		Session:  "slack-main",
	}}, []resource.ContributionBundle{{
		Plugins: []resource.PluginRef{{
			Name:     slack.Name,
			Instance: "workspace-prod",
			Config: map[string]any{
				"auth": map[string]any{"channel_token": slack.BotTokenPurpose},
			},
		}},
	}}, Options{AuthPath: dir}, slack.NewDispatcher(), nil)
	if err == nil || !strings.Contains(err.Error(), `channel token "bot_token" is empty`) {
		t.Fatalf("serveChannels error = %v, want missing bot token", err)
	}
}

func TestValidateServeLaunchSuggestsInitForUninitializedPath(t *testing.T) {
	err := validateServeLaunch(distribution.Loaded{
		Root: "/repo/sample",
		Distribution: distribution.Distribution{
			Spec: coredistribution.Spec{Name: "sample"},
		},
	}, "sample")
	if err == nil || !strings.Contains(err.Error(), "coder app init sample") {
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

func TestServeCommandForwardsEnvFileFlags(t *testing.T) {
	var got Options
	cmd := NewServeCommandWithRunner(func(_ context.Context, opts Options) error {
		got = opts
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--env-file", ".env", "--env-file=.env.local"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(got.EnvFiles) != 2 || got.EnvFiles[0] != ".env" || got.EnvFiles[1] != ".env.local" {
		t.Fatalf("env files = %#v, want root env files", got.EnvFiles)
	}
}

func TestServeCommandForwardsModelSelection(t *testing.T) {
	var got Options
	cmd := NewServeCommandWithRunner(func(_ context.Context, opts Options) error {
		got = opts
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--provider", "codex", "--model", "gpt-5.5", "examples/slack-bot"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.AppDir != "examples/slack-bot" {
		t.Fatalf("app dir = %q, want examples/slack-bot", got.AppDir)
	}
	if got.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", got.Provider)
	}
	if got.Model != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", got.Model)
	}
}

func TestServeCommandForwardsReasoningFlags(t *testing.T) {
	var got Options
	cmd := NewServeCommandWithRunner(func(_ context.Context, opts Options) error {
		got = opts
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--thinking", "on", "--effort", "medium"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Thinking != "on" || !got.ThinkingSet {
		t.Fatalf("thinking = %q set=%v, want on set", got.Thinking, got.ThinkingSet)
	}
	if got.Effort != "medium" || !got.EffortSet {
		t.Fatalf("effort = %q set=%v, want medium set", got.Effort, got.EffortSet)
	}
}

func TestServeCommandRejectsInvalidReasoningFlags(t *testing.T) {
	cmd := NewServeCommandWithRunner(func(_ context.Context, _ Options) error {
		t.Fatalf("runner should not be called")
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--thinking", "medium"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `invalid --thinking "medium"`) {
		t.Fatalf("Execute error = %v, want invalid thinking", err)
	}
}
