package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
)

func TestRootCommandHasServeAndConnect(t *testing.T) {
	cmd := newRootCommand()
	var names []string
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"coder", "run", "serve", "connect", "remote"} {
		if !strings.Contains(got, want) {
			t.Fatalf("commands = %s, want %s", got, want)
		}
	}
}

func TestRunHelpIncludesLaunchFlags(t *testing.T) {
	cmd := newRootCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"run [path]", "--session", "--conversation", "--provider", "--model", "--input", "--debug", "--usage"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestRunUsesLoadedDistributionAndSubmitsInput(t *testing.T) {
	runtime := &fakeRunRuntime{}
	loader := func(context.Context, string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Distribution: distribution.Distribution{
				Spec: coredistribution.Spec{
					Name:           "sample",
					DefaultSession: coresession.Ref{Name: "main"},
				},
				Runtime: runtime,
			},
		}, nil
	}
	out := bytes.Buffer{}
	errOut := bytes.Buffer{}
	err := runLocalDistribution(context.Background(), loader, runOptions{
		session:      "custom",
		conversation: "conv",
		input:        "hello",
	}, "ignored", strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("runLocalDistribution: %v", err)
	}
	if runtime.request.Session.Name != "custom" {
		t.Fatalf("session = %q, want custom", runtime.request.Session.Name)
	}
	if runtime.request.Conversation.ID != "conv" {
		t.Fatalf("conversation = %q, want conv", runtime.request.Conversation.ID)
	}
	if runtime.session.submission.Input == nil || runtime.session.submission.Input.Text != "hello" {
		t.Fatalf("submission = %#v, want input hello", runtime.session.submission)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("output = %q, want ok", out.String())
	}
}

func TestRunRequiresDefaultOrExplicitSession(t *testing.T) {
	loader := func(context.Context, string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Distribution: distribution.Distribution{
				Spec:    coredistribution.Spec{Name: "sample"},
				Runtime: &fakeRunRuntime{},
			},
		}, nil
	}
	err := runLocalDistribution(context.Background(), loader, runOptions{input: "hello"}, "ignored", strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no default session") {
		t.Fatalf("runLocalDistribution error = %v, want no default session", err)
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

type fakeRunRuntime struct {
	request distribution.OpenRequest
	session *fakeRunSession
}

func (r *fakeRunRuntime) OpenSession(_ context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	r.request = req
	r.session = &fakeRunSession{
		info: clientapi.SessionInfo{
			Session:      req.Session,
			Thread:       corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch},
			Conversation: req.Conversation,
		},
	}
	return r.session, nil
}

type fakeRunSession struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (s *fakeRunSession) Info() clientapi.SessionInfo { return s.info }

func (s *fakeRunSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submission = submission
	return fakeRunHandle{info: s.info, submission: submission}, nil
}

func (s *fakeRunSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s *fakeRunSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s *fakeRunSession) Close(context.Context) error { return nil }

type fakeRunHandle struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (r fakeRunHandle) ID() clientapi.RunID { return "run-1" }

func (r fakeRunHandle) Session() clientapi.SessionInfo { return r.info }

func (r fakeRunHandle) Submission() clientapi.Submission { return r.submission }

func (r fakeRunHandle) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}

func (r fakeRunHandle) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r fakeRunHandle) Err() error { return nil }

func (r fakeRunHandle) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{
		RunID:      r.ID(),
		Session:    r.info,
		Submission: r.submission,
		Input:      &sessionruntime.InputResult{Status: sessionruntime.InputStatusOK},
		Outbound: &channel.Outbound{
			Kind:    channel.OutboundMessage,
			Message: &channel.Message{Content: "ok"},
		},
	}, nil
}
