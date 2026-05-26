package launch

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/channel"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	orchestrationsession "github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/slack"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
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
	if err == nil || !strings.Contains(err.Error(), "fluxplane init sample") {
		t.Fatalf("validateServeLaunch error = %v, want init guidance", err)
	}
}

func TestValidateServeLaunchRequiresEntryPointForManifest(t *testing.T) {
	err := validateServeLaunch(distribution.Loaded{
		Manifest: "/repo/sample/fluxplane.yaml",
		Distribution: distribution.Distribution{
			Spec: coredistribution.Spec{Name: "sample"},
		},
	}, "sample")
	if err == nil || !strings.Contains(err.Error(), "no daemon listeners or channels") {
		t.Fatalf("validateServeLaunch error = %v, want no daemon listeners or channels", err)
	}
}

func TestDefaultServeChannelClientAppliesDistributionDefaults(t *testing.T) {
	base := &captureChannelClient{}
	defaultSessionID := resource.ResourceID{Kind: "session", Origin: "embedded", Namespace: resource.NewNamespace("coder"), Name: "main"}
	sessionCatalog := orchestrationsession.SessionCatalog{
		defaultSessionID.Address(): {ID: defaultSessionID},
	}
	client := defaultServeChannelClient(base, coredistribution.Spec{
		DefaultSession:      coresession.Ref{Name: "main"},
		DefaultConversation: channel.ConversationRef{ID: "conversation"},
	}, sessionCatalog)

	session, err := client.Open(context.Background(), clientapi.OpenRequest{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if base.open.Session.Name != "main" {
		t.Fatalf("open session = %#v, want main", base.open.Session)
	}
	if base.open.Conversation.ID != "conversation" {
		t.Fatalf("open conversation = %#v, want conversation", base.open.Conversation)
	}
	if session.Info().Session.Name != "main" || session.Info().Conversation.ID != "conversation" {
		t.Fatalf("session info = %#v, want defaults", session.Info())
	}

	base.open = clientapi.OpenRequest{}
	resumed, err := client.Resume(context.Background(), clientapi.ResumeRequest{ThreadID: "thread-2"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if base.resume.ThreadID != "thread-2" {
		t.Fatalf("resume request = %#v, want thread-2", base.resume)
	}
	if base.open.ThreadID != "thread-2" {
		t.Fatalf("resume open thread = %#v, want thread-2", base.open)
	}
	if base.open.Session.Name != "main" || base.open.Conversation.ID != "conversation" {
		t.Fatalf("resume open = %#v, want defaults", base.open)
	}
	if resumed.Info().Session.Name != "main" || resumed.Info().Conversation.ID != "conversation" {
		t.Fatalf("resumed info = %#v, want defaults", resumed.Info())
	}

	base.summaries = []clientapi.SessionSummary{{
		Info: clientapi.SessionInfo{
			Thread: corethread.Ref{ID: "thread-3", BranchID: corethread.MainBranch},
		},
	}}
	summaries, err := client.ListSessions(context.Background(), clientapi.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 || string(summaries[0].Info.Session.Name) != defaultSessionID.Address() || summaries[0].Info.Conversation.ID != "conversation" {
		t.Fatalf("summaries = %#v, want defaults", summaries)
	}
}

func TestDefaultServeChannelClientForwardsEventWatcher(t *testing.T) {
	base := &eventingChannelClient{}
	client := defaultServeChannelClient(base, coredistribution.Spec{}, nil)

	watcher, ok := client.(serveEventWatcher)
	if !ok {
		t.Fatal("default serve client does not expose event watcher")
	}
	stop, err := watcher.OnEvent(context.Background(), func(clientapi.Event) {})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	stop()
	if !base.watched {
		t.Fatal("OnEvent was not delegated to base client")
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

func TestServeCommandForwardsVerbose(t *testing.T) {
	var got Options
	cmd := NewServeCommandWithRunner(func(_ context.Context, opts Options) error {
		got = opts
		return nil
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--verbose"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !got.Verbose {
		t.Fatalf("verbose = false, want true")
	}
}

func TestWaitServeShutdownReturnsWhenRunnersStop(t *testing.T) {
	errs := make(chan error, 1)
	errs <- nil

	if !waitServeShutdown(errs, 1, time.Second) {
		t.Fatal("waitServeShutdown returned false, want true")
	}
}

func TestWaitServeShutdownTimesOutWhenRunnerHangs(t *testing.T) {
	errs := make(chan error)
	start := time.Now()

	if waitServeShutdown(errs, 1, 10*time.Millisecond) {
		t.Fatal("waitServeShutdown returned true, want false")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitServeShutdown took %s, want bounded wait", elapsed)
	}
}

type captureChannelClient struct {
	open      clientapi.OpenRequest
	resume    clientapi.ResumeRequest
	summaries []clientapi.SessionSummary
}

type eventingChannelClient struct {
	captureChannelClient
	watched bool
}

func (c *eventingChannelClient) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	c.watched = true
	return func() {}, nil
}

func (c *captureChannelClient) Open(_ context.Context, req clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	c.open = req
	return &fakeRunSession{info: clientapi.SessionInfo{
		Session:      req.Session,
		Thread:       corethread.Ref{ID: firstThreadID(req.ThreadID, "thread-1"), BranchID: corethread.MainBranch},
		Conversation: req.Conversation,
	}}, nil
}

func (c *captureChannelClient) Resume(_ context.Context, req clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	c.resume = req
	return &fakeRunSession{info: clientapi.SessionInfo{
		Thread: corethread.Ref{ID: req.ThreadID, BranchID: corethread.MainBranch},
	}}, nil
}

func (c *captureChannelClient) ListSessions(context.Context, clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	return append([]clientapi.SessionSummary(nil), c.summaries...), nil
}

func firstThreadID(values ...corethread.ID) corethread.ID {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
