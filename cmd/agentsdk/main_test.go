package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/toolprojection"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestCoderCommandHasREPLAndUsageFlag(t *testing.T) {
	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"coder", "repl", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "interactive session") {
		t.Fatalf("help = %q, want repl help", help)
	}
	if !strings.Contains(help, "--usage") {
		t.Fatalf("help = %q, want inherited usage flag", help)
	}
	if !strings.Contains(help, "--provider") {
		t.Fatalf("help = %q, want inherited provider flag", help)
	}
	if strings.Contains(help, "--openai-store") {
		t.Fatalf("help = %q, want openai-store removed", help)
	}
}

func TestCoderToolProjectionIncludesPlanExecOperations(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{coder.Bundle()},
		Plugins: []pluginhost.Plugin{codingplugin.New(sys), planexecplugin.New()},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	cfg := coderToolProjectionConfig()
	cfg.Commands = composition.CommandCatalog
	cfg.Operations = composition.OperationCatalog
	cfg.Caller = policy.Caller{Kind: policy.CallerAgent}
	cfg.Trust = policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified}

	projected := toolprojection.Project(cfg)
	names := map[string]bool{}
	for _, spec := range projected.Tools {
		names[string(spec.Name)] = true
	}
	for _, want := range []string{"plan", "delegate"} {
		if !names[want] {
			t.Fatalf("projected tool names missing %q: %#v", want, names)
		}
	}
}

func TestRootCommandHasServeAndConnect(t *testing.T) {
	cmd := newRootCommand()
	var names []string
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"serve", "connect", "remote"} {
		if !strings.Contains(got, want) {
			t.Fatalf("commands = %s, want %s", got, want)
		}
	}
}

func TestRemoteHelpIncludesTargetAndRenderingFlags(t *testing.T) {
	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"remote", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"--app", "--url", "--socket", "--local", "--session", "--conversation", "--input", "--debug", "--usage"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestTrackPlanRuntimeEventTracksActivePlansAndSeenKeys(t *testing.T) {
	active := map[string]bool{}
	seen := map[string]bool{}
	started := agentruntime.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    "plan.execution_started",
			Payload: map[string]any{"plan_id": "plan_1"},
		},
	}
	trackPlanRuntimeEvent(started, active, seen)
	if !active["plan_1"] {
		t.Fatalf("active = %#v, want plan_1 active", active)
	}
	if key := runtimeEventKey(started); key == "" || !seen[key] {
		t.Fatalf("seen missing runtime key %q: %#v", key, seen)
	}
	trackPlanRuntimeEvent(agentruntime.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    "plan.completed",
			Payload: map[string]any{"plan_id": "plan_1"},
		},
	}, active, seen)
	if active["plan_1"] {
		t.Fatalf("active = %#v, want plan_1 removed", active)
	}
}

func TestResolveRemoteTargetRequiresExactlyOneTarget(t *testing.T) {
	_, err := resolveRemoteTarget(context.Background(), remoteOptions{session: defaultRemoteSession})
	if err == nil || !strings.Contains(err.Error(), "specify one target") {
		t.Fatalf("missing target error = %v, want specify one target", err)
	}
	_, err = resolveRemoteTarget(context.Background(), remoteOptions{url: "http://127.0.0.1:8787", local: true, session: defaultRemoteSession})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("conflicting target error = %v, want mutually exclusive", err)
	}
}

func TestResolveRemoteTargetLocalUsesDefaultSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	target, err := resolveRemoteTarget(context.Background(), remoteOptions{local: true, session: defaultRemoteSession})
	if err != nil {
		t.Fatalf("resolveRemoteTarget: %v", err)
	}
	if target.baseURL != "http://unix" {
		t.Fatalf("baseURL = %q, want http://unix", target.baseURL)
	}
	want := filepath.Join(runtimeDir, defaultRemoteSocket)
	if target.socket != want {
		t.Fatalf("socket = %q, want %q", target.socket, want)
	}
	if target.session != defaultRemoteSession {
		t.Fatalf("session = %q, want default", target.session)
	}
}

func TestResolveRemoteAppTargetUsesDirectChannelListener(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	appDir := t.TempDir()
	data := []byte(`
kind: app
name: remote-test
daemon:
  listeners:
    - name: control
      type: http
      addr: agentsdk-local.sock
      auth:
        mode: local_socket
  channels:
    - name: local
      type: direct
      listener: control
      session: custom-session
---
kind: session
name: custom-session
agent: echo
---
kind: agent
name: echo
`)
	if err := os.WriteFile(filepath.Join(appDir, "agentsdk.app.yaml"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	target, err := resolveRemoteTarget(context.Background(), remoteOptions{appDir: appDir, session: defaultRemoteSession})
	if err != nil {
		t.Fatalf("resolveRemoteTarget: %v", err)
	}
	if target.baseURL != "http://unix" {
		t.Fatalf("baseURL = %q, want http://unix", target.baseURL)
	}
	if target.socket != filepath.Join(runtimeDir, "agentsdk-local.sock") {
		t.Fatalf("socket = %q", target.socket)
	}
	if target.session != "custom-session" {
		t.Fatalf("session = %q, want custom-session", target.session)
	}
}

func TestResolveRemoteAppTargetReportsAmbiguousDirectChannels(t *testing.T) {
	appDir := t.TempDir()
	data := []byte(`
kind: app
name: remote-test
daemon:
  listeners:
    - name: a
      type: http
      addr: a.sock
      auth: {mode: local_socket}
    - name: b
      type: http
      addr: b.sock
      auth: {mode: local_socket}
  channels:
    - name: local-a
      type: direct
      listener: a
      session: a-session
    - name: local-b
      type: direct
      listener: b
      session: b-session
---
kind: session
name: a-session
agent: echo
---
kind: session
name: b-session
agent: echo
---
kind: agent
name: echo
`)
	if err := os.WriteFile(filepath.Join(appDir, "agentsdk.app.yaml"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := resolveRemoteTarget(context.Background(), remoteOptions{appDir: appDir, session: defaultRemoteSession})
	if err == nil || !strings.Contains(err.Error(), "multiple direct channels") || !strings.Contains(err.Error(), "local-a") || !strings.Contains(err.Error(), "local-b") {
		t.Fatalf("resolveRemoteTarget error = %v, want ambiguous channels", err)
	}
}

func TestConnectHelpIsNativeCommand(t *testing.T) {
	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"connect", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "connect [provider]") {
		t.Fatalf("help = %q, want native provider argument", help)
	}
	for _, forbidden := range []string{"List available and connected connectors", "exec", "docs"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help = %q, contains upstream connector CLI text %q", help, forbidden)
		}
	}
}

func TestConnectStatusListsStoredInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	instances := credential.NewInstanceStore(filepath.Join(dir, "instances"))
	credentials := credential.NewFileStore(filepath.Join(dir, "credentials"))
	if err := instances.Save(ctx, credential.Instance{
		ID:         "slack-prod",
		Connector:  "slack",
		AuthMethod: "token",
		Source:     "manual",
	}); err != nil {
		t.Fatalf("Save instance: %v", err)
	}
	if err := credentials.Save(ctx, "slack-prod", connector.Credentials{
		Auth: connector.AuthState{Kind: connector.AuthToken, Token: "xoxb-test"},
	}); err != nil {
		t.Fatalf("Save credentials: %v", err)
	}

	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"connect", "--connectors-path", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"PROVIDER", "slack", "slack-prod", "ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status = %q, want %q", got, want)
		}
	}
}

func TestConnectProviderInfoUsesRegisteredProviders(t *testing.T) {
	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"connect", "slack", "--info"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Slack (slack)") || !strings.Contains(got, "Auth methods:") {
		t.Fatalf("info = %q, want slack connect info", got)
	}
}

func TestRegisteredConnectorProvidersIncludeGitLabAndJira(t *testing.T) {
	providers, err := registeredConnectorProviderNames(context.Background())
	if err != nil {
		t.Fatalf("registeredConnectorProviderNames: %v", err)
	}
	got := "," + strings.Join(providers, ",") + ","
	for _, want := range []string{",gitlab,", ",jira,", ",slack,"} {
		if !strings.Contains(got, want) {
			t.Fatalf("providers = %#v, want %s", providers, strings.Trim(want, ","))
		}
	}
}

func TestServeListenerRequiresTCPAuthAndEnforcesBearer(t *testing.T) {
	_, err := serveListenerHandler(appconfig.ListenerDoc{Name: "control", Type: "http", Addr: "127.0.0.1:0"}, http.NewServeMux())
	if err == nil || !strings.Contains(err.Error(), "requires auth") {
		t.Fatalf("serveListenerHandler error = %v, want requires auth", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	handler, err := serveListenerHandler(appconfig.ListenerDoc{
		Name: "control",
		Type: "http",
		Addr: "127.0.0.1:0",
		Auth: map[string]any{"mode": "bearer", "token": "secret"},
	}, next)
	if err != nil {
		t.Fatalf("serveListenerHandler bearer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized code = %d, want 401", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "ok" {
		t.Fatalf("authorized response = %d %q, want 200 ok", rr.Code, rr.Body.String())
	}
}

func TestListenServeRemovesStaleUnixSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentsdk-local.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen stale socket: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("Close stale socket: %v", err)
	}

	ln, display, cleanup, err := listenServe(path)
	if err != nil {
		t.Fatalf("listenServe: %v", err)
	}
	if display != "unix:"+path {
		t.Fatalf("display = %q, want unix path", display)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("Close listener: %v", err)
	}
	cleanup()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket exists after cleanup: %v", err)
	}
}

func TestListenServeRefusesLiveUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentsdk-local.sock")
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen live socket: %v", err)
	}
	defer func() { _ = live.Close() }()

	_, _, _, err = listenServe(path)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("listenServe error = %v, want already in use", err)
	}
}

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
	}}, serveOptions{authPath: dir}, slackplugin.NewDispatcher())
	if err != nil {
		t.Fatalf("serveChannels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels len = %d, want 1", len(channels))
	}
}

func TestCoderBundleAppliesModelOverride(t *testing.T) {
	bundle := coderBundle("codex", "gpt-test")
	if bundle.Apps[0].Model.Model != "gpt-test" {
		t.Fatalf("app model = %q, want gpt-test", bundle.Apps[0].Model.Model)
	}
	if bundle.Apps[0].Model.Provider != "codex" {
		t.Fatalf("app provider = %q, want codex", bundle.Apps[0].Model.Provider)
	}
	if bundle.Agents[0].Inference.Model != "gpt-test" {
		t.Fatalf("agent model = %q, want gpt-test", bundle.Agents[0].Inference.Model)
	}
	if bundle.Agents[0].Name != coder.AgentName {
		t.Fatalf("agent name = %q", bundle.Agents[0].Name)
	}
}

func TestResolveModelSelectionParsesProviderPrefix(t *testing.T) {
	got := resolveModelSelection(coderOptions{provider: "openai", model: "codex/gpt-5.5"})
	if got.Provider != "codex" || got.Model != "gpt-5.5" {
		t.Fatalf("selection = %#v, want codex/gpt-5.5", got)
	}
	got = resolveModelSelection(coderOptions{provider: "openai", model: "anthropic/claude-haiku-4-5-20251001"})
	if got.Provider != "anthropic" || got.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("selection = %#v, want anthropic/claude-haiku-4-5-20251001", got)
	}
	got = resolveModelSelection(coderOptions{provider: "openai", model: "minimax/MiniMax-M2.7"})
	if got.Provider != "minimax" || got.Model != "MiniMax-M2.7" {
		t.Fatalf("selection = %#v, want minimax/MiniMax-M2.7", got)
	}
}

func TestResolveModelSelectionKeepsOpenRouterSlashModel(t *testing.T) {
	got := resolveModelSelection(coderOptions{provider: "openai", model: "openrouter/anthropic/claude-sonnet-4.6"})
	if got.Provider != "openrouter" || got.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("selection = %#v, want openrouter/anthropic/claude-sonnet-4.6", got)
	}
	got = resolveModelSelection(coderOptions{provider: "openrouter", model: "anthropic/claude-sonnet-4.6"})
	if got.Provider != "openrouter" || got.Model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("selection = %#v, want explicit openrouter provider", got)
	}
}

func TestCoderDefaultModel(t *testing.T) {
	if coder.DefaultModel != "gpt-5.5" {
		t.Fatalf("DefaultModel = %q, want gpt-5.5", coder.DefaultModel)
	}
}

func TestNewCoderModelRejectsUnknownOpenRouterModel(t *testing.T) {
	_, err := newCoderModel(modelSelection{Provider: "openrouter", Model: "gpt-5.5"}, coderOptions{})
	if err == nil || !strings.Contains(err.Error(), "exact OpenRouter model id") {
		t.Fatalf("error = %v, want exact OpenRouter model id", err)
	}
}

func TestNewCoderModelSupportsOpenRouterResponsesModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	model, err := newCoderModel(modelSelection{Provider: "openrouter", Model: "anthropic/claude-sonnet-4.6"}, coderOptions{})
	if err != nil {
		t.Fatalf("newCoderModel: %v", err)
	}
	if model == nil {
		t.Fatalf("model is nil")
	}
}

func TestNewCoderModelSupportsAnthropicMessagesModels(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	model, err := newCoderModel(modelSelection{Provider: "anthropic", Model: "claude-haiku-4-5-20251001"}, coderOptions{})
	if err != nil {
		t.Fatalf("newCoderModel anthropic: %v", err)
	}
	if model == nil {
		t.Fatal("anthropic model is nil")
	}
	t.Setenv("MINIMAX_API_KEY", "test-key")
	model, err = newCoderModel(modelSelection{Provider: "minimax", Model: "MiniMax-M2.7"}, coderOptions{})
	if err != nil {
		t.Fatalf("newCoderModel minimax: %v", err)
	}
	if model == nil {
		t.Fatal("minimax model is nil")
	}
}

func TestOpenRouterReasoningDefaultsPreferMinimalAndAuto(t *testing.T) {
	_, modelSpec, ok := modelcatalog.Find("openrouter", "moonshotai/kimi-k2-thinking")
	if !ok {
		t.Fatal("openrouter moonshotai/kimi-k2-thinking missing from modeldb")
	}
	effort, summary := openRouterReasoningDefaults(modelSpec)
	if effort != "minimal" {
		t.Fatalf("effort = %q, want minimal", effort)
	}
	if summary != "auto" {
		t.Fatalf("summary = %q, want auto", summary)
	}
}

func TestUsageFromEventParsesTypedPayload(t *testing.T) {
	typed := usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []usage.Measurement{{
			Metric:   usage.MetricLLMInputTokens,
			Quantity: 12,
			Unit:     usage.UnitToken,
		}},
	}
	got, ok := usageFromEvent(agentruntime.Event{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: typed}})
	if !ok || got.Subject.Provider != "openai" || len(got.Measurements) != 1 {
		t.Fatalf("usageFromEvent = %#v, %v", got, ok)
	}
	if _, ok := usageFromEvent(agentruntime.Event{Runtime: &clientapi.RuntimeEvent{Name: event.Name("other")}}); ok {
		t.Fatalf("usageFromEvent accepted non-usage event")
	}
	if _, ok := usageFromEvent(agentruntime.Event{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: map[string]any{}}}); ok {
		t.Fatalf("usageFromEvent accepted untyped usage payload")
	}
}

func TestTerminalEventRegistryDecodesPluginCatalogEvents(t *testing.T) {
	registry, err := terminalEventRegistry()
	if err != nil {
		t.Fatalf("terminalEventRegistry: %v", err)
	}
	for _, sample := range eventcatalog.All() {
		raw, err := json.Marshal(sample)
		if err != nil {
			t.Fatalf("Marshal %s: %v", sample.EventName(), err)
		}
		decoded, ok, err := registry.TryDecode(sample.EventName(), raw)
		if err != nil {
			t.Fatalf("TryDecode %s: %v", sample.EventName(), err)
		}
		if !ok {
			t.Fatalf("event %s was not registered", sample.EventName())
		}
		if decoded.EventName() != sample.EventName() {
			t.Fatalf("decoded event name = %s, want %s", decoded.EventName(), sample.EventName())
		}
	}
}

func TestResultErrorReportsFailedInput(t *testing.T) {
	err := resultError(agentruntime.Result{
		Input: &session.InputResult{
			Status: session.InputStatusFailed,
			Error:  &session.CommandError{Code: "model_failed", Message: "boom"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "model_failed: boom") {
		t.Fatalf("err = %v, want model_failed", err)
	}
}

func TestRenderTerminalOutboundRendersMarkdown(t *testing.T) {
	var out bytes.Buffer
	renderTerminalOutbound(&out, agentruntime.Result{
		Outbound: &channel.Outbound{
			Message: &channel.Message{Content: "**Hi** `there`"},
		},
	})

	got := out.String()
	if !strings.Contains(got, "Hi") || !strings.Contains(got, "there") {
		t.Fatalf("out = %q, want rendered final outbound", got)
	}
	if strings.Contains(got, "**Hi**") || strings.Contains(got, "`there`") {
		t.Fatalf("out = %q, want markdown rendered without source markers", got)
	}
}
