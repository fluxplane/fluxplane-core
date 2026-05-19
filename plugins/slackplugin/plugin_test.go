package slackplugin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	coretask "github.com/fluxplane/agentruntime/core/task"
	"github.com/fluxplane/agentruntime/core/user"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
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

func TestAccessPolicyAudienceTrustDoesNotInheritSharedChannelSenderTrust(t *testing.T) {
	policy := AccessPolicy{
		Mode:         "open",
		DefaultTrust: user.TrustPublic,
		Operators:    []string{"Uadmin"},
	}
	msg := inboundMessage{UserID: "Uadmin", ChannelID: "Cpublic", Kind: "mention"}
	if got := policy.TrustFor(msg); got != user.TrustOperator {
		t.Fatalf("sender trust = %q, want operator", got)
	}
	if got := policy.AudienceTrustFor(msg); got != user.TrustPublic {
		t.Fatalf("audience trust = %q, want public for shared channel", got)
	}
}

func TestAccessPolicyAudienceTrustCanFollowDirectConversationSenderTrust(t *testing.T) {
	policy := AccessPolicy{
		Mode:         "open",
		DefaultTrust: user.TrustPublic,
		Operators:    []string{"Uadmin"},
	}
	msg := inboundMessage{UserID: "Uadmin", ChannelID: "D1", Kind: "dm", IsDirect: true}
	if got := policy.AudienceTrustFor(msg); got != user.TrustOperator {
		t.Fatalf("audience trust = %q, want operator for direct conversation", got)
	}
}

func TestSlackInputContentKeepsAudienceTrustOutOfSenderIdentity(t *testing.T) {
	content := slackInputContent(inboundMessage{
		Text:      "hello",
		UserID:    "Uadmin",
		ChannelID: "Cpublic",
		ThreadTS:  "111.222",
		TeamID:    "T1",
		Kind:      "mention",
	}, user.TrustPublic, "strict")
	if content.Text != "hello" {
		t.Fatalf("text = %q, want hello", content.Text)
	}
	if content.SlackContext.AudienceTrust != user.TrustPublic {
		t.Fatalf("audience trust = %q, want public", content.SlackContext.AudienceTrust)
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	if strings.Contains(string(data), "sender_trust") {
		t.Fatalf("content JSON = %s, want no sender_trust", data)
	}
}

func TestSlackInputContentOmitsAudienceTrustForDirectMessages(t *testing.T) {
	content := slackInputContent(inboundMessage{
		Text:      "hello",
		UserID:    "Uadmin",
		ChannelID: "D1",
		ThreadTS:  "111.222",
		TeamID:    "T1",
		Kind:      "dm",
		IsDirect:  true,
	}, user.TrustOperator, "strict")
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	if strings.Contains(string(data), "audience_trust") {
		t.Fatalf("content JSON = %s, want no audience_trust for direct message", data)
	}
}

func TestChannelSendUsesCurrentSlackTarget(t *testing.T) {
	dispatcher := NewDispatcher()
	poster := &fakePoster{}
	dispatcher.Register("slack-main", poster)
	plugin := NewWithDispatcher(nil, dispatcher)
	ctx := operation.NewContext(ContextWithTarget(context.Background(), Target{ChannelID: "C1", ThreadTS: "123.4"}), nil)

	result := plugin.channelSend(ctx, channelSendInput{Text: "working"})
	if result.IsError() {
		t.Fatalf("channelSend result = %#v", result)
	}
	if poster.channel != "C1" || poster.calls != 1 {
		t.Fatalf("posted = %#v", poster)
	}
}

func TestPluginIsNotConnectorProvider(t *testing.T) {
	if _, ok := any(New(nil)).(pluginhost.ConnectorProviderContributor); ok {
		t.Fatal("Slack plugin must not contribute connector providers")
	}
}

func TestPluginDeclaresStoredBotTokenAndOAuthAuthMethods(t *testing.T) {
	methods, err := New(nil).AuthMethods(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "main"}})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(methods) != 3 {
		t.Fatalf("methods len = %d, want stored, env, oauth2", len(methods))
	}
	if methods[0].Name != BotTokenMethod || methods[0].Secret.ResourceName() != "plugin/slack/main/bot_token" {
		t.Fatalf("stored method = %#v", methods[0])
	}
	if methods[2].Name != OAuth2Method || methods[2].Secret.ResourceName() != "plugin/slack/main/oauth2_token" {
		t.Fatalf("oauth2 method = %#v", methods[2])
	}
}

func TestResolveUsesStoredBotTokenWithoutAppTokenForDatasource(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: BotTokenSecretRef(ref), Value: "xoxb-test"}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	session, err := Resolve(context.Background(), nil, store, ref, Config{Auth: AuthConfig{Method: BotTokenMethod}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if session.BotToken != "xoxb-test" || session.AppToken != "" {
		t.Fatalf("session = %#v, want bot token only", session)
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
	for _, want := range []coredatasource.EntityType{UserEntity, ChannelEntity, MessageEntity, ThreadMessageEntity} {
		if !got[want] {
			t.Fatalf("entities = %#v, missing %s", got, want)
		}
	}
}

func TestPluginContributesSlackChannelMembersRelation(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	var channel coredatasource.EntitySpec
	for _, entity := range providers[0].Entities() {
		if entity.Type == ChannelEntity {
			channel = entity
			break
		}
	}
	if !channel.Supports(coredatasource.EntityCapabilityRelation) {
		t.Fatalf("channel capabilities = %#v, want relation", channel.Capabilities)
	}
	if len(channel.Relations) != 1 || channel.Relations[0].Name != "members" || channel.Relations[0].TargetEntity != UserEntity || !channel.Relations[0].Exact {
		t.Fatalf("channel relations = %#v, want exact members -> slack.user", channel.Relations)
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

func TestNormalizeSlackMessageRecordKeepsCanonicalChannelMetadata(t *testing.T) {
	record := normalizeSlackMessageRecord(coredatasource.Record{
		Entity:  MessageEntity,
		Title:   "lyse-internal",
		Content: "deployment note",
		URL:     "https://example.slack.com/archives/C04LYSEINTERNAL/p1710000000000100",
		Metadata: map[string]string{
			"permalink": "https://example.slack.com/archives/C04LYSEINTERNAL/p1710000000000100",
		},
	})

	if record.Metadata["channel"] != "lyse-internal" {
		t.Fatalf("channel metadata = %q, want lyse-internal", record.Metadata["channel"])
	}
	if record.Metadata["channel_id"] != "C04LYSEINTERNAL" {
		t.Fatalf("channel_id metadata = %q, want permalink-derived channel id", record.Metadata["channel_id"])
	}
	if record.Metadata["permalink"] == "" {
		t.Fatalf("metadata = %#v, want permalink preserved", record.Metadata)
	}
}

func TestSlackMessageDatasourceSearchPreservesChannelIdentity(t *testing.T) {
	server := slackDatasourceServer(t)
	defer server.Close()
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "slack-bot"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   BotTokenSecretRef(ref),
		Kind:  "bearer_token",
		Value: "xoxb-test",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	plugin := New(nil, store)
	plugin.clientFactory = func(token, appToken string) *slack.Client {
		if token != "xoxb-test" || appToken != "" {
			t.Fatalf("client token=%q app=%q, want bot token only", token, appToken)
		}
		return slack.New(token, slack.OptionAPIURL(server.URL+"/"))
	}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{Ref: ref})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{
		Name:     "slack-bot",
		Kind:     Name,
		Entities: []coredatasource.EntityType{MessageEntity},
		Config:   map[string]string{"instance": "slack-bot"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{
		Entity: MessageEntity,
		Query:  "lyse",
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("records = %#v, want one", result.Records)
	}
	record := result.Records[0]
	if record.Title != "lyse-internal" {
		t.Fatalf("title = %q, want exact Slack channel name", record.Title)
	}
	if record.Metadata["channel"] != "lyse-internal" || record.Metadata["channel_id"] != "C04LYSEINTERNAL" || record.Metadata["permalink"] == "" {
		t.Fatalf("metadata = %#v, want exact channel, permalink-derived channel_id, permalink", record.Metadata)
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
	if session.submission.Kind != clientapi.SubmissionInput || session.submission.Input == nil {
		t.Fatalf("submission = %#v, want input submission", session.submission)
	}
	content, ok := session.submission.Input.Content.(slackInputPayload)
	if !ok {
		t.Fatalf("input content = %#v, want slackInputPayload", session.submission.Input.Content)
	}
	if content.SlackContext.AudienceTrust != "" {
		t.Fatalf("audience trust = %q, want empty for direct admin conversation", content.SlackContext.AudienceTrust)
	}
}

func TestRunObserverOperationEventsUseStatusNotTaskCards(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api, debug: true}, Target{ChannelID: "C1", ThreadTS: "111.222"})
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
	observer.Finish(context.Background(), "")

	joined := joinSlackRequests(requests)
	if strings.Contains(joined, "chat.startStream") || strings.Contains(joined, "task_update") {
		t.Fatalf("operation status created task stream: %s", joined)
	}
	for _, want := range []string{"assistant.threads.setStatus", "Searching+datasources..."} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests = %s\nmissing %q", joined, want)
		}
	}
	if strings.Contains(joined, "reading+a+datasource+record") {
		t.Fatalf("operation details leaked into Slack status: %s", joined)
	}
	if strings.Contains(joined, "DEV-381") || strings.Contains(joined, "secret") || strings.Contains(joined, "chain") {
		t.Fatalf("requests leaked thinking text: %s", joined)
	}
}

func TestRunObserverOperationEventsDoNotCreateProgressPanel(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Handle(clientapi.Event{
		Kind:  clientapi.EventOperationRequested,
		RunID: "run-1",
		Operation: &clientapi.OperationEvent{
			CallID:    "call-1",
			Operation: operation.Ref{Name: "web_search"},
			Input:     map[string]any{"query": "private customer details"},
		},
	})
	observer.Handle(clientapi.Event{
		Kind:  clientapi.EventOperationRequested,
		RunID: "run-1",
		Operation: &clientapi.OperationEvent{
			CallID:    "call-2",
			Operation: operation.Ref{Name: "go_test"},
		},
	})
	observer.Finish(context.Background(), "")

	joined := joinSlackRequests(requests)
	if strings.Contains(joined, "/chat.postMessage") || strings.Contains(joined, "/chat.update") {
		t.Fatalf("requests = %s\nprogress panel should not be posted or updated", joined)
	}
	if !strings.Contains(joined, "/assistant.threads.setStatus") || !strings.Contains(joined, "Searching+the+web") {
		t.Fatalf("requests = %s\nwant assistant status updates only", joined)
	}
	if strings.Contains(joined, "private") || strings.Contains(joined, "customer") {
		t.Fatalf("operation status leaked raw operation input: %s", joined)
	}
}

func TestRunObserverStreamsRepeatedContentDeltas(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	for i := 0; i < 2; i++ {
		observer.Handle(clientapi.Event{
			Kind:  clientapi.EventRuntimeEmitted,
			RunID: "run-1",
			Runtime: &clientapi.RuntimeEvent{
				Name: llmagent.EventModelStreamedName,
				Payload: llmagent.ModelStreamed{Event: llmagent.StreamEvent{
					Kind: llmagent.StreamContentDelta,
					Text: "ha",
				}},
			},
		})
	}
	if err := observer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	observer.Finish(context.Background(), "haha")

	markdown := joinSlackMarkdownChunks(requests)
	if !strings.Contains(markdown, "haha") {
		t.Fatalf("markdown = %s\nwant repeated deltas preserved as haha", markdown)
	}
	if strings.Contains(markdown, `"type":"task_update"`) {
		t.Fatalf("markdown stream used task chunks: %s", joinSlackRequests(requests))
	}
}

func TestRunObserverTaskEventsUseTaskCards(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Handle(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventCreatedName,
			Payload: coretask.Created{
				TaskID: "task_1",
				Task: coretask.Task{
					ID:          "task_1",
					Title:       "Investigate issue",
					Description: "Read the trace",
					Status:      coretask.StatusReady,
					Steps:       []coretask.Step{{ID: "step_1", Title: "Check logs"}},
				},
			},
		},
	})
	observer.Handle(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventStepProgressedName,
			Payload: coretask.StepProgressed{TaskID: "task_1", StepID: "step_1", Message: "reading"},
		},
	})
	observer.Finish(context.Background(), "done")

	joined := joinSlackRequests(requests)
	chunks := joinSlackChunks(requests)
	for _, want := range []string{"chat.startStream", "task_update", "Investigate+issue", "chat.stopStream", "markdown_text=done"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests = %s\nmissing %q", joined, want)
		}
	}
	for _, want := range []string{`"details":"Read the trace"`, `"details":"reading"`} {
		if !strings.Contains(chunks, want) {
			t.Fatalf("chunks = %s\nmissing %q", chunks, want)
		}
	}
}

func TestRunObserverKeepsMarkdownAndTaskStreamParametersSeparate(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Append("**Summary**\n- item one\n")
	if err := observer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	observer.Handle(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventCreatedName,
			Payload: coretask.Created{
				TaskID: "task_1",
				Task:   coretask.Task{ID: "task_1", Title: "Plan", Status: coretask.StatusReady, Steps: []coretask.Step{{ID: "step_1", Title: "Step"}}},
			},
		},
	})
	observer.Finish(context.Background(), "**Summary**\n- item one\nDone")

	var sawMarkdownAppend, sawTaskAppend bool
	for _, request := range *requests {
		if request.path != "/chat.appendStream" {
			continue
		}
		markdown := request.values.Get("markdown_text")
		chunks := request.values.Get("chunks")
		if markdown != "" && chunks != "" {
			t.Fatalf("append request mixed markdown_text and chunks: %#v", request.values)
		}
		if strings.Contains(chunks, `"type":"markdown_text"`) {
			sawMarkdownAppend = true
			if strings.Contains(chunks, `"type":"task_update"`) {
				t.Fatalf("markdown append mixed in task chunks: %s", chunks)
			}
			if !strings.Contains(chunks, `**Summary**\n- item one\n`) {
				t.Fatalf("markdown chunks = %q, want raw markdown preserved", chunks)
			}
		}
		if strings.Contains(chunks, `"type":"task_update"`) {
			sawTaskAppend = true
			if strings.Contains(chunks, `"type":"markdown_text"`) {
				t.Fatalf("task append mixed in markdown chunks: %s", chunks)
			}
		}
	}
	if !sawMarkdownAppend || !sawTaskAppend {
		t.Fatalf("saw markdown append=%v task append=%v requests=%s", sawMarkdownAppend, sawTaskAppend, joinSlackRequests(requests))
	}
}

func TestRunObserverRequeuesFailedMarkdownAppend(t *testing.T) {
	appendAttempts := 0
	server, requests := slackCaptureServer(t, func(w http.ResponseWriter, r *http.Request, _ string) bool {
		if r.URL.Path == "/chat.appendStream" {
			appendAttempts++
			if appendAttempts == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":false,"error":"temporary_failure"}`))
				return true
			}
		}
		return false
	})
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Append("recover me")
	if err := observer.Flush(); err == nil {
		t.Fatal("first Flush succeeded, want append failure")
	}
	if err := observer.Flush(); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	observer.Finish(context.Background(), "recover me")

	if appendAttempts != 2 {
		t.Fatalf("append attempts = %d, want 2", appendAttempts)
	}
	markdown := joinSlackMarkdownChunks(requests)
	if !strings.Contains(markdown, "recover me") {
		t.Fatalf("markdown = %s\nwant requeued markdown append", markdown)
	}
}

func TestRunObserverClearsStatusBeforeStreamingContent(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.setStatus(context.Background(), slackWorkingStatus)
	observer.Append("Final answer")
	if err := observer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	observer.Finish(context.Background(), "Final answer")

	statusSetIndex, statusClearIndex, streamStartIndex := -1, -1, -1
	for i, request := range *requests {
		switch request.path {
		case "/assistant.threads.setStatus":
			if request.values.Get("status") == slackWorkingStatus {
				statusSetIndex = i
			}
			if _, ok := request.values["status"]; ok && request.values.Get("status") == "" {
				statusClearIndex = i
			}
		case "/chat.startStream":
			streamStartIndex = i
		}
	}
	if statusSetIndex < 0 || statusClearIndex < 0 || streamStartIndex < 0 {
		t.Fatalf("requests = %s\nwant status set, status clear, and stream start", joinSlackRequests(requests))
	}
	if statusSetIndex >= statusClearIndex || statusClearIndex >= streamStartIndex {
		t.Fatalf("request order = %s\nwant status cleared before stream start", joinSlackRequests(requests))
	}
}

func TestRunObserverFinalizesMissingMarkdownSuffix(t *testing.T) {
	server, requests := slackCaptureServer(t, nil)
	defer server.Close()

	api := slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	observer := newRunObserver(&SlackChannel{name: "slack-main", api: api}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Append("hello")
	if err := observer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	observer.Finish(context.Background(), "hello world")

	var stop url.Values
	for _, req := range *requests {
		if req.path == "/chat.stopStream" {
			stop = req.values
		}
	}
	if got := stop.Get("markdown_text"); got != " world" {
		t.Fatalf("stop markdown_text = %q, want missing suffix", got)
	}
}

func TestRunObserverTracksBackgroundTaskLifecycle(t *testing.T) {
	observer := newRunObserver(&SlackChannel{name: "slack-main"}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Handle(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventExecutionStartedName,
			Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1"},
		},
	})
	summary := observer.snapshotSummary()
	if !summary.ActiveTasks["task_1"] {
		t.Fatalf("active tasks = %#v, want task_1", summary.ActiveTasks)
	}
	observer.Handle(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventExecutionCompletedName,
			Payload: coretask.ExecutionCompleted{TaskID: "task_1", ExecutionID: "exec_1"},
		},
	})
	summary = observer.snapshotSummary()
	if summary.ActiveTasks["task_1"] {
		t.Fatalf("active tasks = %#v, want task_1 removed", summary.ActiveTasks)
	}
}

func TestRunObserverFollowsBackgroundTasksAfterObservedCursor(t *testing.T) {
	observer := newRunObserver(&SlackChannel{name: "slack-main"}, Target{ChannelID: "C1", ThreadTS: "111.222"})
	observer.Handle(clientapi.Event{
		Kind:   clientapi.EventRuntimeEmitted,
		Cursor: clientapi.EventCursor{Sequence: 7},
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventExecutionStartedName,
			Payload: coretask.ExecutionStarted{TaskID: "task_1", ExecutionID: "exec_1"},
		},
	})
	session := &capturingSession{}

	observer.FollowTasks(context.Background(), session, map[string]bool{"task_1": true})

	if got := session.eventOptions.After.Sequence; got != 7 {
		t.Fatalf("follow task after sequence = %d, want 7", got)
	}
	if !session.eventOptions.Replay {
		t.Fatal("follow task did not request replay")
	}
}

func slackCaptureServer(t *testing.T, intercept func(http.ResponseWriter, *http.Request, string) bool) (*httptest.Server, *[]capturedSlackRequest) {
	t.Helper()
	var requests []capturedSlackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if intercept != nil && intercept(w, r, string(body)) {
			return
		}
		values, _ := url.ParseQuery(string(body))
		requests = append(requests, capturedSlackRequest{path: r.URL.Path, body: string(body), values: values})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"999.0001"}`))
	}))
	return server, &requests
}

type capturedSlackRequest struct {
	path   string
	body   string
	values url.Values
}

func joinSlackRequests(requests *[]capturedSlackRequest) string {
	var lines []string
	for _, request := range *requests {
		lines = append(lines, request.path+" "+request.body)
	}
	return strings.Join(lines, "\n")
}

func joinSlackChunks(requests *[]capturedSlackRequest) string {
	var chunks []string
	for _, request := range *requests {
		if chunk := request.values.Get("chunks"); chunk != "" {
			chunks = append(chunks, chunk)
		}
	}
	return strings.Join(chunks, "\n")
}

func joinSlackMarkdownChunks(requests *[]capturedSlackRequest) string {
	var chunks []string
	for _, request := range *requests {
		chunk := request.values.Get("chunks")
		if strings.Contains(chunk, `"type":"markdown_text"`) {
			chunks = append(chunks, chunk)
		}
	}
	return strings.Join(chunks, "\n")
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
	submission   clientapi.Submission
	eventOptions clientapi.EventOptions
}

func (s *capturingSession) Info() clientapi.SessionInfo { return clientapi.SessionInfo{} }

func (s *capturingSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submission = submission
	return capturingRun{submission: submission}, nil
}

func (s *capturingSession) Events(_ context.Context, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	s.eventOptions = opts
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

func slackDatasourceServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.messages" {
			t.Fatalf("unexpected Slack path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("query"); got != "lyse" {
			t.Fatalf("query = %q, want lyse", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"messages": {
				"total": 1,
				"matches": [{
					"type": "message",
					"ts": "1710000000.000100",
					"text": "The ticket has a short description first.",
					"permalink": "https://example.slack.com/archives/C04LYSEINTERNAL/p1710000000000100",
					"channel": {"id": "C04LYSEINTERNAL", "name": "lyse-internal"},
					"user": "U1"
				}]
			}
		}`))
	}))
}
