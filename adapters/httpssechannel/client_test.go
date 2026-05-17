package httpssechannel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestClientSendsInputThroughHTTPAndSSE(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-input"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Input == nil || result.Input.Status != session.InputStatusOK {
		t.Fatalf("input result = %#v", result.Input)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "agent: hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	assertRemoteRunEvent(t, run, clientapi.EventInputCompleted)
}

func TestClientSendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q, want bearer token", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]clientapi.SessionSummary{})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), BearerToken: "test-token"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.ListSessions(context.Background(), clientapi.ListSessionsRequest{}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
}

func TestClientUsesUnixSocketTransport(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "agentsdk.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions" {
			t.Fatalf("path = %q, want /sessions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]clientapi.SessionSummary{})
	})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		<-done
	})

	client, err := NewClient(ClientConfig{BaseURL: "http://unix", UnixSocket: socketPath})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.ListSessions(context.Background(), clientapi.ListSessionsRequest{}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
}

func TestClientSendsCommandThroughHTTPAndSSE(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-command"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{
		Path:  command.Path{"echo"},
		Input: "hello",
	}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Command == nil || result.Command.Status != session.CommandStatusOK {
		t.Fatalf("command result = %#v", result.Command)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
	assertRemoteRunEvent(t, run, clientapi.EventCommandCompleted)
}

func TestClientRoundTripsOperationLifecycleWithCallIDs(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-operation-events"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{
		Path:  command.Path{"echo"},
		Input: "hello",
	}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	events := drainRunEvents(run)

	var requestedID, runtimeStartedID, runtimeCompletedID, completedID operation.CallID
	for _, event := range events {
		switch event.Kind {
		case clientapi.EventOperationRequested:
			if event.Operation == nil {
				t.Fatalf("operation requested event missing operation: %#v", event)
			}
			requestedID = event.Operation.CallID
			if requestedID == "" || event.Operation.Input != "hello" {
				t.Fatalf("requested operation = %#v", event.Operation)
			}
		case clientapi.EventOperationCompleted:
			if event.Operation == nil || event.Operation.Result == nil {
				t.Fatalf("operation completed event missing result: %#v", event)
			}
			completedID = event.Operation.CallID
			if completedID == "" || event.Operation.Result.Output != "hello" {
				t.Fatalf("completed operation = %#v", event.Operation)
			}
		case clientapi.EventRuntimeEmitted:
			if event.Runtime == nil {
				continue
			}
			switch event.Runtime.Name {
			case operation.EventStartedName:
				runtimeStartedID = runtimeCallID(t, event)
			case operation.EventCompletedName:
				runtimeCompletedID = runtimeCallID(t, event)
			}
		}
	}
	if requestedID == "" || requestedID != runtimeStartedID || requestedID != runtimeCompletedID || requestedID != completedID {
		t.Fatalf("call ids requested=%q runtime_started=%q runtime_completed=%q completed=%q", requestedID, runtimeStartedID, runtimeCompletedID, completedID)
	}
}

func TestClientRoundTripsRuntimeUsageAndStreamingEvents(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-runtime-events"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	events := drainRunEvents(run)

	var sawUsage, sawStream bool
	for _, event := range events {
		if event.Kind != clientapi.EventRuntimeEmitted || event.Runtime == nil {
			continue
		}
		switch event.Runtime.Name {
		case usage.EventRecordedName:
			payload, ok := event.Runtime.Payload.(usage.Recorded)
			if !ok {
				t.Fatalf("usage payload = %T, want usage.Recorded", event.Runtime.Payload)
			}
			if payload.Source != "test-runtime" {
				t.Fatalf("usage payload = %#v", payload)
			}
			sawUsage = true
		case llmagent.EventModelStreamedName:
			payload, ok := event.Runtime.Payload.(llmagent.ModelStreamed)
			if !ok {
				t.Fatalf("stream payload = %T, want llmagent.ModelStreamed", event.Runtime.Payload)
			}
			if payload.Event.Kind != llmagent.StreamContentDelta || payload.Event.Text != "agent:" {
				t.Fatalf("stream payload = %#v", payload)
			}
			sawStream = true
		}
	}
	if !sawUsage || !sawStream {
		t.Fatalf("saw usage=%v stream=%v in events %#v", sawUsage, sawStream, events)
	}
}

func TestClientDecodeEventUsesRegistryAndLeavesUnknownPayloadRaw(t *testing.T) {
	client := &Client{registry: testEventRegistry(t)}
	knownRaw, err := json.Marshal(clientapi.Event{
		Kind:    clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: usage.Recorded{Source: "test-runtime"}},
	})
	if err != nil {
		t.Fatalf("Marshal known event: %v", err)
	}
	known, err := client.decodeEvent(knownRaw)
	if err != nil {
		t.Fatalf("decodeEvent known: %v", err)
	}
	if _, ok := known.Runtime.Payload.(usage.Recorded); !ok {
		t.Fatalf("known payload = %T, want usage.Recorded", known.Runtime.Payload)
	}

	unknownRaw, err := json.Marshal(clientapi.Event{
		Kind:    clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{Name: coreevent.Name("custom.event"), Payload: map[string]any{"value": "ok"}},
	})
	if err != nil {
		t.Fatalf("Marshal unknown event: %v", err)
	}
	unknown, err := client.decodeEvent(unknownRaw)
	if err != nil {
		t.Fatalf("decodeEvent unknown: %v", err)
	}
	payload, ok := unknown.Runtime.Payload.(json.RawMessage)
	if !ok {
		t.Fatalf("unknown payload = %T, want json.RawMessage", unknown.Runtime.Payload)
	}
	if string(payload) != `{"value":"ok"}` {
		t.Fatalf("unknown payload = %s", payload)
	}
}

func TestClientReceivesLargeStreamingBurst(t *testing.T) {
	ctx := context.Background()
	const total = clientapi.DefaultRunEventBuffer + 64
	service := testRuntimeWithAgent(t, streamingBurstAgent{count: total})
	server, err := NewServer(ServerConfig{Client: service})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	client, err := NewClient(ClientConfig{BaseURL: httpServer.URL, HTTPClient: httpServer.Client(), Events: testEventRegistry(t)})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-streaming-burst"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	eventsDone := make(chan []clientapi.Event, 1)
	go func() {
		eventsDone <- drainRunEvents(run)
	}()
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var events []clientapi.Event
	select {
	case events = <-eventsDone:
	case <-time.After(time.Second):
		t.Fatal("timed out draining run events")
	}
	if got := countStreamContentDeltas(events); got != total {
		t.Fatalf("stream deltas = %d, want %d", got, total)
	}
	if len(events) == 0 || events[len(events)-1].Kind != clientapi.EventRunCompleted {
		t.Fatalf("last event = %#v, want run.completed", events[len(events)-1])
	}
}

func TestClientReadsLargeSSEEventWithoutScannerLimit(t *testing.T) {
	largeText := strings.Repeat("x", 2<<20)
	raw, err := json.Marshal(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: llmagent.EventModelStreamedName,
			Payload: llmagent.ModelStreamed{
				Event: llmagent.StreamEvent{Kind: llmagent.StreamContentDelta, Text: largeText},
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal event: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client(), Events: testEventRegistry(t)})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()
	events, cancel, _, err := client.openEventStream(ctx, "thread-1", clientapi.EventOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("openEventStream: %v", err)
	}
	defer cancel()

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event stream closed before large event")
		}
		payload, ok := event.Runtime.Payload.(llmagent.ModelStreamed)
		if !ok {
			t.Fatalf("payload = %T, want llmagent.ModelStreamed", event.Runtime.Payload)
		}
		if payload.Event.Text != largeText {
			t.Fatalf("payload text len = %d, want %d", len(payload.Event.Text), len(largeText))
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for large event")
	}
}

func TestClientRejectsOversizedSSEEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("x", maxSSEEventBytes+1))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()
	_, cancel, errs, err := client.openEventStream(ctx, "thread-1", clientapi.EventOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("openEventStream: %v", err)
	}
	defer cancel()

	select {
	case err := <-errs:
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("stream error = %v, want exceeds cap", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for oversized event error")
	}
}

func TestClientReadsMultilineSSEEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: ignored\n"))
		_, _ = w.Write([]byte(": comment\n"))
		_, _ = w.Write([]byte("data: {\"kind\":\"run.completed\",\n"))
		_, _ = w.Write([]byte("data: \"run_id\":\"run_1\"}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()
	events, cancel, _, err := client.openEventStream(ctx, "thread-1", clientapi.EventOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("openEventStream: %v", err)
	}
	defer cancel()

	select {
	case event := <-events:
		if event.Kind != clientapi.EventRunCompleted || event.RunID != "run_1" {
			t.Fatalf("event = %#v, want run.completed run_1", event)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for multiline event")
	}
}

func TestSSEDataFragmentPreservesMeaningfulWhitespace(t *testing.T) {
	chunk, ok := sseDataFragment([]byte(`data:  {"text":" keep "} `))
	if !ok {
		t.Fatal("sseDataFragment ok = false")
	}
	if got, want := string(chunk), ` {"text":" keep "} `; got != want {
		t.Fatalf("chunk = %q, want %q", got, want)
	}
	if _, ok := sseDataFragment([]byte("event: message")); ok {
		t.Fatal("non-data field parsed as data")
	}
}

func TestClientListsResumesAndReplaysSessionEvents(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-replay"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	summaries, err := client.ListSessions(ctx, clientapi.ListSessionsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries len = %d, want 1", len(summaries))
	}

	resumed, err := client.Resume(ctx, clientapi.ResumeRequest{ThreadID: sessionHandle.Info().Thread.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	events, cancel, err := resumed.Events(ctx, clientapi.EventOptions{Buffer: 8, Replay: true})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	defer cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind != clientapi.EventOutboundProduced {
				continue
			}
			if !event.Replayed {
				t.Fatalf("event = %#v, want replayed", event)
			}
			if event.Cursor.Sequence == 0 {
				t.Fatalf("cursor = %#v, want sequence", event.Cursor)
			}
			if event.Outbound == nil || event.Outbound.Message == nil || event.Outbound.Message.Content != "hello" {
				t.Fatalf("outbound = %#v", event.Outbound)
			}
			return
		case <-deadline:
			t.Fatal("expected replayed outbound event")
		}
	}
}

func TestSessionEventsNormalizesNegativeBuffer(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-negative-buffer"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, cancel, err := sessionHandle.Events(ctx, clientapi.EventOptions{Buffer: -1})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	cancel()
}

func TestResumedSessionSubmitUsesResumedThread(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	opened, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-resume-submit"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	resumed, err := client.Resume(ctx, clientapi.ResumeRequest{ThreadID: opened.Info().Thread.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	run, err := resumed.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Session.Thread.ID != opened.Info().Thread.ID {
		t.Fatalf("result thread = %q, want %q", result.Session.Thread.ID, opened.Info().Thread.ID)
	}
	if result.Outbound == nil || result.Outbound.Message == nil || result.Outbound.Message.Content != "hello" {
		t.Fatalf("outbound = %#v", result.Outbound)
	}
}

func TestRunHandleAdoptsServerNormalizedSubmission(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-normalized-run"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	submission := run.Submission()
	if submission.Caller.Kind != policy.CallerUser {
		t.Fatalf("caller = %#v, want user", submission.Caller)
	}
	if submission.Trust.Level != policy.TrustVerified {
		t.Fatalf("trust = %#v, want verified", submission.Trust)
	}
}

func TestRemoteSubmitIgnoresRawAuthorityFields(t *testing.T) {
	service := testRuntime(t)
	server, err := NewServer(ServerConfig{
		Client: service,
		Authority: Authority{
			Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "socket-user"}},
			Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	body := []byte(`{
		"session": {"thread": {"id": "thread-spoof"}, "conversation": {"id": "conv-spoof"}},
		"submission": {
			"id": "run-spoof",
			"kind": "command",
			"command": {"path": ["echo"], "input": "hello"},
			"caller": {"kind": "system", "principal": {"kind": "user", "id": "attacker"}},
			"trust": {"kind": "invocation", "level": "system"}
		}
	}`)
	resp, err := httpServer.Client().Post(httpServer.URL+"/sessions/thread-spoof/submit", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200", resp.Status)
	}
	var result clientapi.Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if result.Submission.Caller.Principal.ID != "socket-user" || result.Submission.Caller.Kind != policy.CallerUser {
		t.Fatalf("caller = %#v, want listener authority", result.Submission.Caller)
	}
	if result.Submission.Trust.Level != policy.TrustVerified {
		t.Fatalf("trust = %#v, want verified listener authority", result.Submission.Trust)
	}
}

func TestRemoteSubmitTrustDowngradeRunsBelowListenerAuthority(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClientWithAuthority(t, Authority{
		Caller:              policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "socket-user"}},
		Trust:               policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		AllowTrustDowngrade: true,
	})
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{Conversation: channel.ConversationRef{ID: "conv-downgrade"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().
		WithCommand(command.Invocation{Path: command.Path{"echo"}, Input: "hello"}).
		WithTrustDowngrade(clientapi.TrustDowngrade{Level: policy.TrustUntrusted, Reason: "simulate_public"}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Command == nil || result.Command.Status != session.CommandStatusRejected {
		t.Fatalf("command = %#v, want rejected by downgraded trust", result.Command)
	}
	if run.Submission().Trust.Level != policy.TrustUntrusted {
		t.Fatalf("submission trust = %#v, want untrusted", run.Submission().Trust)
	}
}

func TestRemoteSubmitRejectsTrustDowngradeUpgrade(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClientWithAuthority(t, Authority{
		Caller:              policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "socket-user"}},
		Trust:               policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		AllowTrustDowngrade: true,
	})
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{Conversation: channel.ConversationRef{ID: "conv-upgrade"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().
		WithText("hello").
		WithTrustDowngrade(clientapi.TrustDowngrade{Level: policy.TrustPrivileged}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err == nil || !strings.Contains(err.Error(), "authority_exceeds_transport") {
		t.Fatalf("Wait error = %v, want authority_exceeds_transport", err)
	}
}

func TestRemoteSubmitRejectsTrustDowngradeScopeEscalation(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClientWithAuthority(t, Authority{
		Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "socket-user"}},
		Trust: policy.Trust{
			Kind:   policy.TrustInvocation,
			Level:  policy.TrustVerified,
			Scopes: []policy.Scope{"read"},
		},
		AllowTrustDowngrade: true,
	})
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{Conversation: channel.ConversationRef{ID: "conv-scope-upgrade"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().
		WithText("hello").
		WithTrustDowngrade(clientapi.TrustDowngrade{
			Level:  policy.TrustVerified,
			Scopes: []policy.Scope{"admin"},
		}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err == nil || !strings.Contains(err.Error(), "authority_exceeds_transport") {
		t.Fatalf("Wait error = %v, want authority_exceeds_transport", err)
	}
}

func TestRemoteSubmitRejectsTrustDowngradeWhenListenerDisallows(t *testing.T) {
	ctx := context.Background()
	client := testRemoteClientWithAuthority(t, Authority{
		Caller: policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "socket-user"}},
		Trust:  policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{Conversation: channel.ConversationRef{ID: "conv-downgrade-denied"}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	run, err := sessionHandle.Submit(ctx, clientapi.NewSubmission().
		WithText("hello").
		WithTrustDowngrade(clientapi.TrustDowngrade{Level: policy.TrustUntrusted}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err == nil || !strings.Contains(err.Error(), "trust downgrade is not allowed") {
		t.Fatalf("Wait error = %v, want downgrade disallowed", err)
	}
}

func TestRunWaitReturnsSSEDecodeError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {not-json}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	mux.HandleFunc("POST /sessions/thread-1/submit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(clientapi.Result{
			RunID: "run-1",
			Session: clientapi.SessionInfo{
				Thread: openedThread("thread-1"),
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sessionHandle := &Session{
		client: client,
		info: clientapi.SessionInfo{
			Thread: openedThread("thread-1"),
		},
	}
	run, err := sessionHandle.Submit(context.Background(), clientapi.NewSubmission().WithText("hello"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(context.Background()); err == nil {
		t.Fatal("Wait error is nil, want SSE decode error")
	}
}

func TestRunWaitReturnsSubmitErrorWithRunIdentity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /sessions/thread-1/submit", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("boom"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sessionHandle := &Session{
		client: client,
		info: clientapi.SessionInfo{
			Thread: openedThread("thread-1"),
		},
	}
	run, err := sessionHandle.Submit(context.Background(), clientapi.Submission{
		ID:    "run_1",
		Kind:  clientapi.SubmissionInput,
		Input: &clientapi.Input{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(context.Background())
	if err == nil {
		t.Fatal("Wait error is nil, want submit error")
	}
	if result.RunID != "run_1" {
		t.Fatalf("result run id = %q, want run_1", result.RunID)
	}
	if result.Session.Thread.ID != "thread-1" {
		t.Fatalf("result thread = %q, want thread-1", result.Session.Thread.ID)
	}
	if result.Submission.Kind != clientapi.SubmissionInput {
		t.Fatalf("result submission = %#v, want input", result.Submission)
	}
}

func TestRunHandleRequestsLargeEventBuffer(t *testing.T) {
	bufferSeen := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, r *http.Request) {
		bufferSeen <- r.URL.Query().Get("buffer")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`event: run.completed
data: {"kind":"run.completed","run_id":"run_1"}

`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	mux.HandleFunc("POST /sessions/thread-1/submit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(clientapi.Result{
			RunID: "run_1",
			Session: clientapi.SessionInfo{
				Thread: openedThread("thread-1"),
			},
			Submission: clientapi.Submission{ID: "run_1", Kind: clientapi.SubmissionInput},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sessionHandle := &Session{client: client, info: clientapi.SessionInfo{Thread: openedThread("thread-1")}}
	run, err := sessionHandle.Submit(context.Background(), clientapi.Submission{
		ID:    "run_1",
		Kind:  clientapi.SubmissionInput,
		Input: &clientapi.Input{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case got := <-bufferSeen:
		if got != "1024" {
			t.Fatalf("buffer = %q, want 1024", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event stream request")
	}
}

func TestRunWaitFailsWhenSSEClosesBeforeTerminalEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/thread-1/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`event: submission.received
data: {"kind":"submission.received","run_id":"run_1"}

`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	mux.HandleFunc("POST /sessions/thread-1/submit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(clientapi.Result{
			RunID: "run_1",
			Session: clientapi.SessionInfo{
				Thread: openedThread("thread-1"),
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	sessionHandle := &Session{
		client: client,
		info: clientapi.SessionInfo{
			Thread: openedThread("thread-1"),
		},
	}
	run, err := sessionHandle.Submit(context.Background(), clientapi.Submission{
		ID:    "run_1",
		Kind:  clientapi.SubmissionInput,
		Input: &clientapi.Input{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(context.Background()); err == nil {
		t.Fatal("Wait error is nil, want terminal event error")
	}
}

func TestEventSubmissionRequiresTypedEventCodec(t *testing.T) {
	client := testRemoteClient(t)
	sessionHandle, err := client.Open(context.Background(), clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "conv-event"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = sessionHandle.Submit(context.Background(), clientapi.Submission{
		Kind:  clientapi.SubmissionEvent,
		Event: stubEvent{},
	})
	if err == nil {
		t.Fatal("Submit error is nil, want typed event codec error")
	}
}

func assertRemoteRunEvent(t *testing.T, run clientapi.RunHandle, kind clientapi.EventKind) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				t.Fatalf("events closed before %s", kind)
			}
			if event.Kind == kind {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}

func drainRunEvents(run clientapi.RunHandle) []clientapi.Event {
	var events []clientapi.Event
	for event := range run.Events() {
		events = append(events, event)
	}
	return events
}

func countStreamContentDeltas(events []clientapi.Event) int {
	var count int
	for _, event := range events {
		if event.Kind != clientapi.EventRuntimeEmitted || event.Runtime == nil || event.Runtime.Name != llmagent.EventModelStreamedName {
			continue
		}
		payload, ok := event.Runtime.Payload.(llmagent.ModelStreamed)
		if !ok {
			continue
		}
		if payload.Event.Kind == llmagent.StreamContentDelta {
			count++
		}
	}
	return count
}

func runtimeCallID(t *testing.T, event clientapi.Event) operation.CallID {
	t.Helper()
	switch payload := event.Runtime.Payload.(type) {
	case operation.OperationStarted:
		return payload.CallID
	case operation.OperationCompleted:
		return payload.CallID
	case operation.OperationFailed:
		return payload.CallID
	case operation.OperationRejected:
		return payload.CallID
	case operation.OperationCanceled:
		return payload.CallID
	default:
		t.Fatalf("runtime payload = %T, want operation event", event.Runtime.Payload)
	}
	return ""
}

type stubEvent struct{}

func (stubEvent) EventName() coreevent.Name { return "stub.event" }

func openedThread(id string) corethread.Ref {
	return corethread.Ref{ID: corethread.ID(id), BranchID: corethread.MainBranch}
}

func testRemoteClient(t *testing.T) *Client {
	t.Helper()
	return testRemoteClientWithAuthority(t, Authority{})
}

func testRemoteClientWithAuthority(t *testing.T, authority Authority) *Client {
	t.Helper()
	service := testRuntime(t)
	server, err := NewServer(ServerConfig{Client: service, Authority: authority})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	client, err := NewClient(ClientConfig{BaseURL: httpServer.URL, HTTPClient: httpServer.Client(), Events: testEventRegistry(t)})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func testEventRegistry(t *testing.T) *coreevent.Registry {
	t.Helper()
	registry := coreevent.NewRegistry()
	for _, sample := range []coreevent.Event{
		operation.OperationStarted{},
		operation.OperationCompleted{},
		operation.OperationFailed{},
		operation.OperationRejected{},
		operation.OperationCanceled{},
		usage.Recorded{},
		llmagent.ModelRequested{},
		llmagent.ModelStreamed{},
		llmagent.ModelCompleted{},
		llmagent.ModelFailed{},
	} {
		if err := registry.Register(sample); err != nil {
			t.Fatalf("register event %s: %v", sample.EventName(), err)
		}
	}
	return registry
}

func testRuntime(t *testing.T) *agentruntime.Service {
	t.Helper()
	return testRuntimeWithAgent(t, remoteEchoAgent{})
}

func testRuntimeWithAgent(t *testing.T, runtimeAgent agent.Agent) *agentruntime.Service {
	t.Helper()
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})); err != nil {
		t.Fatalf("register operation: %v", err)
	}

	commands := command.NewRegistry()
	if err := commands.Register(command.Spec{
		Path: command.Path{"echo"},
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "echo"},
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}

	service, err := agentruntime.New(agentruntime.Config{
		Agent:      runtimeAgent,
		Commands:   commands,
		Operations: ops,
		Channel:    channel.Ref{Name: "http"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "test-user",
			},
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

type remoteEchoAgent struct{}

func (remoteEchoAgent) Spec() agent.Spec {
	return agent.Spec{Name: "remote-echo"}
}

func (remoteEchoAgent) Step(ctx agent.Context, input agent.StepInput) agent.StepResult {
	var content any
	if len(input.Observations) > 0 {
		content = "agent: " + input.Observations[0].Content.(string)
	}
	ctx.Events().Emit(usage.Recorded{
		Source: "test-runtime",
		Subject: usage.Subject{
			Kind: usage.SubjectLLM,
			Name: "fake-model",
		},
		Measurements: []usage.Measurement{{
			Metric:   usage.MetricLLMInputTokens,
			Quantity: 1,
			Unit:     usage.UnitToken,
		}},
	})
	ctx.Events().Emit(llmagent.ModelStreamed{
		Agent: "remote-echo",
		Model: "fake-model",
		Event: llmagent.StreamEvent{
			Kind: llmagent.StreamContentDelta,
			Text: "agent:",
		},
	})
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: content},
		},
	}
}

type streamingBurstAgent struct {
	count int
}

func (a streamingBurstAgent) Spec() agent.Spec {
	return agent.Spec{Name: "streaming-burst"}
}

func (a streamingBurstAgent) Step(ctx agent.Context, input agent.StepInput) agent.StepResult {
	for i := 0; i < a.count; i++ {
		ctx.Events().Emit(llmagent.ModelStreamed{
			Agent: "streaming-burst",
			Model: "fake-model",
			Event: llmagent.StreamEvent{
				Kind: llmagent.StreamContentDelta,
				Text: "x",
			},
		})
	}
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: "done"},
		},
	}
}
