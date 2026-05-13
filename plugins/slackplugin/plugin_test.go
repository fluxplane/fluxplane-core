package slackplugin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/user"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestAccessPolicyChecksKindAndTrust(t *testing.T) {
	policy := AccessPolicy{
		Mode:             "open",
		AllowKinds:       []string{"dm", "mention"},
		DefaultTrust:     user.TrustPublic,
		Operators:        []string{"Uadmin"},
		InternalChannels: []string{"Cinternal"},
	}
	if err := policy.Check(inboundMessage{UserID: "U1", ChannelID: "C1", Kind: "thread_reply"}); err == nil {
		t.Fatal("Check accepted disallowed kind")
	}
	if err := policy.Check(inboundMessage{UserID: "U1", ChannelID: "C1", Kind: "dm"}); err != nil {
		t.Fatalf("Check dm: %v", err)
	}
	if got := policy.TrustFor(inboundMessage{UserID: "Uadmin", ChannelID: "C1"}); got != user.TrustOperator {
		t.Fatalf("operator trust = %q", got)
	}
	if got := policy.TrustFor(inboundMessage{UserID: "U1", ChannelID: "Cinternal"}); got != user.TrustInternal {
		t.Fatalf("internal trust = %q", got)
	}
}

func TestChannelSendUsesCurrentSlackTarget(t *testing.T) {
	dispatcher := NewDispatcher()
	poster := &fakePoster{}
	dispatcher.Register("slack-main", poster)
	plugin := New(dispatcher)
	ctx := operation.NewContext(ContextWithTarget(context.Background(), Target{ChannelID: "C1", ThreadTS: "123.4"}), nil)

	result := plugin.channelSend(ctx, channelSendInput{Text: "working"})
	if result.IsError() {
		t.Fatalf("channelSend result = %#v", result)
	}
	if poster.channel != "C1" || poster.calls != 1 {
		t.Fatalf("posted = %#v", poster)
	}
}

func TestPluginContributesSlackConnectorProvider(t *testing.T) {
	providers, err := New(nil).ConnectorProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("ConnectorProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "slack" {
		t.Fatalf("providers = %#v, want slack", providers)
	}
}

func TestPluginContributesSlackDatasourceEntities(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
	got := entityTypes(providers[0].Entities())
	for _, want := range []coredatasource.EntityType{UserEntity, ChannelEntity, MessageEntity} {
		if !got[want] {
			t.Fatalf("entities = %#v, missing %s", got, want)
		}
	}
}

func TestPluginContributesSlackEntityDetectors(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	var channel, message coredatasource.EntitySpec
	for _, entity := range providers[0].Entities() {
		switch entity.Type {
		case ChannelEntity:
			channel = entity
		case MessageEntity:
			message = entity
		}
	}
	if len(channel.Detectors) != 2 {
		t.Fatalf("channel detectors = %#v, want ref and url detectors", channel.Detectors)
	}
	if len(message.Detectors) != 1 || message.Detectors[0].Kind != coredatasource.DetectorURL || message.Detectors[0].IDTemplate == "" {
		t.Fatalf("message detectors = %#v, want URL detector with stable id template", message.Detectors)
	}
}

func entityTypes(entities []coredatasource.EntitySpec) map[coredatasource.EntityType]bool {
	out := map[coredatasource.EntityType]bool{}
	for _, entity := range entities {
		out[entity.Type] = true
	}
	return out
}

func TestInboundFromMessageAllowsThreadReplyOnlyInDM(t *testing.T) {
	ch, err := NewChannel(ChannelConfig{
		Name:       "slack-main",
		Session:    coresession.Ref{Name: "slack-main"},
		BotToken:   "xoxb-test",
		AppToken:   "xapp-test",
		BotUserID:  "Ubot",
		Access:     AccessPolicy{Mode: "open"},
		API:        slack.New("xoxb-test"),
		SocketMode: socketModeTestClient(),
		Dispatcher: NewDispatcher(),
	})
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}
	channelEvent := &slackevents.MessageEvent{
		User:            "U1",
		Text:            "follow up",
		Channel:         "C1",
		TimeStamp:       "222.333",
		ThreadTimeStamp: "111.222",
		ChannelType:     "channel",
	}
	if _, ok := ch.inboundFromMessage("T1", channelEvent); ok {
		t.Fatal("inboundFromMessage accepted channel thread reply")
	}
	dmEvent := *channelEvent
	dmEvent.Channel = "D1"
	dmEvent.ChannelType = "im"
	inbound, ok := ch.inboundFromMessage("T1", &dmEvent)
	if !ok {
		t.Fatal("inboundFromMessage rejected DM thread reply")
	}
	if inbound.Kind != "thread_reply" || inbound.ChannelID != "D1" || !inbound.IsDirect {
		t.Fatalf("inbound = %#v, want DM thread reply", inbound)
	}
}

func TestHandleInboundSubmitsSlackCallerAndTrust(t *testing.T) {
	session := &capturingSession{}
	client := capturingClient{session: session}
	ch, err := NewChannel(ChannelConfig{
		Name:       "slack-main",
		Session:    coresession.Ref{Name: "slack-main"},
		BotToken:   "xoxb-test",
		AppToken:   "xapp-test",
		Access:     AccessPolicy{Mode: "open", Operators: []string{"Uadmin"}},
		API:        slack.New("xoxb-test"),
		SocketMode: socketModeTestClient(),
		Dispatcher: NewDispatcher(),
	})
	if err != nil {
		t.Fatalf("NewChannel: %v", err)
	}

	err = ch.handleInbound(context.Background(), client, inboundMessage{
		Text:      "hello",
		UserID:    "Uadmin",
		ChannelID: "C1",
		ThreadTS:  "111.222",
		TeamID:    "T1",
		Kind:      "dm",
	})
	if err != nil {
		t.Fatalf("handleInbound: %v", err)
	}
	if session.submission.Caller.Kind != policy.CallerUser ||
		session.submission.Caller.Principal.Kind != "slack_user" ||
		session.submission.Caller.Principal.ID != "Uadmin" {
		t.Fatalf("caller = %#v, want slack user", session.submission.Caller)
	}
	if session.submission.Trust.Kind != policy.TrustInvocation || session.submission.Trust.Level != policy.TrustPrivileged {
		t.Fatalf("trust = %#v, want privileged invocation", session.submission.Trust)
	}
	if session.sendInputCalled {
		t.Fatal("handleInbound used SendInput, want Submit with caller/trust")
	}
}

func TestRunObserverStreamsTaskUpdatesWithoutThinkingText(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.URL.Path+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"999.0001"}`))
	}))
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api, debug: true}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.ensureStarted(context.Background())
	observer.Handle(clientapi.Event{
		Kind:  clientapi.EventOperationRequested,
		RunID: "run-1",
		Operation: &clientapi.OperationEvent{
			CallID:    "call-1",
			Operation: operation.Ref{Name: "datasource_search"},
			Input:     map[string]any{"query": "DEV-381"},
		},
	})
	observer.Handle(clientapi.Event{
		Kind:  clientapi.EventRuntimeEmitted,
		RunID: "run-1",
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
				Kind: llmagent.StreamThinkingDelta,
				Text: "secret chain of thought",
			}},
		},
	})
	observer.Handle(clientapi.Event{
		Kind:  clientapi.EventOperationCompleted,
		RunID: "run-1",
		Operation: &clientapi.OperationEvent{
			CallID:    "call-1",
			Operation: operation.Ref{Name: "datasource_search"},
			Result: &operation.Result{
				Status: operation.StatusOK,
				Output: operation.Rendered{Data: map[string]any{
					"results": []any{
						map[string]any{"records": []any{map[string]any{"id": "DEV-381"}, map[string]any{"id": "DEV-382"}}},
					},
				}},
			},
		},
	})
	observer.Finish(context.Background())

	joined := strings.Join(requests, "\n")
	for _, want := range []string{"chat.startStream", "task_update", "Working+on+it", "searching+datasources", "2+results", "chat.stopStream"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests = %s\nmissing %q", joined, want)
		}
	}
	if strings.Contains(joined, "secret") || strings.Contains(joined, "chain") {
		t.Fatalf("requests leaked thinking text: %s", joined)
	}
}

type fakePoster struct {
	channel string
	calls   int
}

func (p *fakePoster) PostMessageContext(_ context.Context, channelID string, opts ...slack.MsgOption) (string, string, error) {
	_ = opts
	p.channel = channelID
	p.calls++
	return channelID, "456.7", nil
}

type capturingClient struct {
	session *capturingSession
}

func (c capturingClient) Open(context.Context, clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	return c.session, nil
}

func (c capturingClient) Resume(context.Context, clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	return c.session, nil
}

func (c capturingClient) ListSessions(context.Context, clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	return nil, nil
}

type capturingSession struct {
	submission      clientapi.Submission
	sendInputCalled bool
}

func (s *capturingSession) Info() clientapi.SessionInfo { return clientapi.SessionInfo{} }

func (s *capturingSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submission = submission
	return capturingRun{submission: submission}, nil
}

func (s *capturingSession) SendCommand(context.Context, command.Invocation) (clientapi.RunHandle, error) {
	return nil, nil
}

func (s *capturingSession) SendInput(context.Context, clientapi.Input) (clientapi.RunHandle, error) {
	s.sendInputCalled = true
	return nil, nil
}

func (s *capturingSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s *capturingSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s *capturingSession) Close(context.Context) error { return nil }

type capturingRun struct {
	submission clientapi.Submission
}

func (r capturingRun) ID() clientapi.RunID { return r.submission.ID }

func (r capturingRun) Session() clientapi.SessionInfo { return clientapi.SessionInfo{} }

func (r capturingRun) Submission() clientapi.Submission { return r.submission }

func (r capturingRun) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}

func (r capturingRun) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r capturingRun) Err() error { return nil }

func (r capturingRun) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{}, nil
}

func socketModeTestClient() *socketmode.Client {
	return socketmode.New(slack.New("xoxb-test"))
}
