package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
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
