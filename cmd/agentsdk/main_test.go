package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
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

func TestRootCommandHasServeAndConnect(t *testing.T) {
	cmd := newRootCommand()
	var names []string
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "serve") || !strings.Contains(got, "connect") {
		t.Fatalf("commands = %s, want serve and connect", got)
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
}

func TestCoderDefaultModel(t *testing.T) {
	if coder.DefaultModel != "gpt-5.5" {
		t.Fatalf("DefaultModel = %q, want gpt-5.5", coder.DefaultModel)
	}
}

func TestUsageFromEventParsesTypedAndMapPayloads(t *testing.T) {
	typed := usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []usage.Measurement{{
			Metric:   usage.MetricLLMInputTokens,
			Quantity: 12,
			Unit:     usage.UnitToken,
		}},
	}
	for _, evt := range []agentruntime.Event{
		{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: typed}},
		{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: map[string]any{
			"subject": map[string]any{"kind": "llm", "provider": "openai", "name": "gpt-test"},
			"measurements": []any{map[string]any{
				"metric":   "llm.input_tokens",
				"quantity": float64(12),
				"unit":     "token",
			}},
		}}},
	} {
		got, ok := usageFromEvent(evt)
		if !ok || got.Subject.Provider != "openai" || len(got.Measurements) != 1 {
			t.Fatalf("usageFromEvent = %#v, %v", got, ok)
		}
	}
	if _, ok := usageFromEvent(agentruntime.Event{Runtime: &clientapi.RuntimeEvent{Name: event.Name("other")}}); ok {
		t.Fatalf("usageFromEvent accepted non-usage event")
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
