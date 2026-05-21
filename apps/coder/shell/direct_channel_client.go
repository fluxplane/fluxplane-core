package codershell

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	fluxplane "github.com/fluxplane/engine"
	"github.com/fluxplane/engine/adapters/channels/httpsse"
	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/command"
	"github.com/fluxplane/engine/core/operation"
	coreusage "github.com/fluxplane/engine/core/usage"
	clientapi "github.com/fluxplane/engine/orchestration/client"
	llmagent "github.com/fluxplane/engine/runtime/agent/llmagent"
	"github.com/fluxplane/engine/runtime/system"
)

// DirectChannelClient adapts the Fluxplane direct channel API to ShellClient.
type DirectChannelClient struct {
	client   fluxplane.ChannelClient
	session  fluxplane.SessionRef
	prefix   string
	commands []command.Spec
	mu       sync.Mutex
	handles  map[string]fluxplane.Session
}

// DirectChannelClientOptions configures a direct channel shell client.
type DirectChannelClientOptions struct {
	Client   fluxplane.ChannelClient
	Session  fluxplane.SessionRef
	Prefix   string
	Commands []command.Spec
}

// NewDirectChannelClient creates a ShellClient over an Fluxplane direct channel client.
func NewDirectChannelClient(opts DirectChannelClientOptions) *DirectChannelClient {
	if opts.Prefix == "" {
		opts.Prefix = "direct"
	}
	return &DirectChannelClient{client: opts.Client, session: opts.Session, prefix: opts.Prefix, commands: append([]command.Spec(nil), opts.Commands...), handles: map[string]fluxplane.Session{}}
}

func (c *DirectChannelClient) ConnectionDescription() string { return "direct-channel" }

func (c *DirectChannelClient) CreateSession(ctx context.Context, req CreateSessionRequest) (SessionInfo, error) {
	if c == nil || c.client == nil {
		return SessionInfo{}, fmt.Errorf("direct channel client unavailable")
	}
	handle, err := c.client.Open(ctx, fluxplane.OpenRequest{
		Session:      c.session,
		Conversation: channel.ConversationRef{ID: fmt.Sprintf("shell-%d", time.Now().UnixNano())},
		Metadata: map[string]string{
			"surface": "coder-shell",
			"cwd":     strings.TrimSpace(req.CWD),
		},
	})
	if err != nil {
		return SessionInfo{}, err
	}
	info := handle.Info()
	id := string(info.Thread.ID)
	if id == "" {
		id = fmt.Sprintf("%s-%d", c.prefix, time.Now().UnixNano())
	}
	c.mu.Lock()
	c.handles[id] = handle
	c.mu.Unlock()
	return SessionInfo{ID: id, CWD: strings.TrimSpace(req.CWD)}, nil
}

func (c *DirectChannelClient) CloseSession(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	handle := c.handles[sessionID]
	delete(c.handles, sessionID)
	c.mu.Unlock()
	if handle == nil {
		return nil
	}
	return handle.Close(ctx)
}

func (c *DirectChannelClient) SubmitCommand(ctx context.Context, sessionID string, req CommandRequest) ([]TranscriptEvent, error) {
	line := strings.TrimSpace(req.Line)
	start := TranscriptEvent{ID: newEventID("cmd-start"), SessionID: sessionID, Time: time.Now(), Kind: EventCommandStarted, Summary: line, Data: map[string]string{"cwd": req.CWD}}
	invocation, err := shellOperationInvocation(line, req.CWD)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	run, err := handle.Submit(ctx, fluxplane.NewSubmission().WithOperation(invocation.Operation, invocation.Input))
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	result, err := run.Wait(ctx)
	events := []TranscriptEvent{start}
	if err != nil {
		return events, err
	}
	return append(events, transcriptEventsForResultWithOptions(sessionID, result, EventCommandOutput, EventCommandComplete, shellCommandTranscriptOptions())...), nil
}

func (c *DirectChannelClient) SubmitCommandStream(ctx context.Context, sessionID string, req CommandRequest) (ShellRunStream, error) {
	line := strings.TrimSpace(req.Line)
	invocation, err := shellOperationInvocation(line, req.CWD)
	if err != nil {
		return ShellRunStream{}, err
	}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return ShellRunStream{}, err
	}
	run, err := handle.Submit(ctx, fluxplane.NewSubmission().WithOperation(invocation.Operation, invocation.Input))
	if err != nil {
		return ShellRunStream{}, err
	}
	return c.streamRunWithOptions(ctx, sessionID, run, EventCommandOutput, EventCommandComplete, shellCommandTranscriptOptions()), nil
}

func (c *DirectChannelClient) SubmitAsk(ctx context.Context, sessionID string, req AskRequest) ([]TranscriptEvent, error) {
	text := strings.TrimSpace(req.Text)
	start := TranscriptEvent{ID: newEventID("ask"), SessionID: sessionID, Time: time.Now(), Kind: EventAskSubmitted, Summary: text, Data: map[string]string{"cwd": req.CWD, "context_items": fmt.Sprintf("%d", len(req.Context))}}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	run, err := handle.Submit(ctx, fluxplane.NewSubmission().WithText(text))
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	result, err := run.Wait(ctx)
	events := []TranscriptEvent{start}
	if err != nil {
		return events, err
	}
	return append(events, transcriptEventsForResultWithOptions(sessionID, result, EventAskOutput, EventCommandComplete, transcriptResultOptions{
		suppressSuccessfulCompletion: true,
	})...), nil
}

func (c *DirectChannelClient) SubmitAskStream(ctx context.Context, sessionID string, req AskRequest) (ShellRunStream, error) {
	text := strings.TrimSpace(req.Text)
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return ShellRunStream{}, err
	}
	run, err := handle.Submit(ctx, fluxplane.NewSubmission().WithText(text))
	if err != nil {
		return ShellRunStream{}, err
	}
	return c.streamRunWithOptions(ctx, sessionID, run, EventAskOutput, EventCommandComplete, transcriptResultOptions{
		suppressSuccessfulCompletion: true,
	}), nil
}

func (c *DirectChannelClient) SubmitSlash(ctx context.Context, sessionID string, req SlashRequest) ([]TranscriptEvent, error) {
	line := strings.TrimSpace(req.Line)
	start := TranscriptEvent{ID: newEventID("slash"), SessionID: sessionID, Time: time.Now(), Kind: EventSlashSubmitted, Summary: line, Data: map[string]string{"cwd": req.CWD}}
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	run, err := handle.Submit(ctx, fluxplane.NewSubmission().WithCommandLine(line))
	if err != nil {
		return []TranscriptEvent{start}, err
	}
	result, err := run.Wait(ctx)
	events := []TranscriptEvent{start}
	if err != nil {
		return events, err
	}
	return append(events, transcriptEventsForResult(sessionID, result, EventCommandOutput, EventCommandComplete)...), nil
}

func (c *DirectChannelClient) SubmitSlashStream(ctx context.Context, sessionID string, req SlashRequest) (ShellRunStream, error) {
	line := strings.TrimSpace(req.Line)
	handle, err := c.sessionHandle(sessionID)
	if err != nil {
		return ShellRunStream{}, err
	}
	run, err := handle.Submit(ctx, fluxplane.NewSubmission().WithCommandLine(line))
	if err != nil {
		return ShellRunStream{}, err
	}
	return c.streamRun(ctx, sessionID, run, EventCommandOutput, EventCommandComplete), nil
}

func (c *DirectChannelClient) streamRun(ctx context.Context, sessionID string, run fluxplane.Run, outputKind TranscriptKind, completeKind TranscriptKind) ShellRunStream {
	return c.streamRunWithOptions(ctx, sessionID, run, outputKind, completeKind, transcriptResultOptions{})
}

func (c *DirectChannelClient) streamRunWithOptions(ctx context.Context, sessionID string, run fluxplane.Run, outputKind TranscriptKind, completeKind TranscriptKind, opts transcriptResultOptions) ShellRunStream {
	events := make(chan TranscriptEvent, 128)
	done := make(chan ShellRunDone, 1)
	go func() {
		defer close(done)
		defer close(events)
		sawLiveOutput := false
		for {
			select {
			case <-ctx.Done():
				done <- ShellRunDone{Err: ctx.Err()}
				return
			case event, ok := <-run.Events():
				if !ok {
					result, err := run.Wait(ctx)
					finalEvents := transcriptEventsForResultWithOptions(sessionID, result, outputKind, completeKind, opts)
					if sawLiveOutput {
						finalEvents = dropOutputEvents(finalEvents, outputKind)
					}
					done <- ShellRunDone{Events: finalEvents, Err: err}
					return
				}
				for _, transcript := range transcriptEventsForRunEventWithOptions(sessionID, event, opts) {
					if transcript.Kind == EventAskDelta || transcript.Kind == EventProcessOutput {
						sawLiveOutput = true
					}
					events <- transcript
				}
			}
		}
	}()
	return ShellRunStream{Events: events, Done: done}
}

func dropOutputEvents(events []TranscriptEvent, kind TranscriptKind) []TranscriptEvent {
	if len(events) == 0 {
		return nil
	}
	out := events[:0]
	for _, event := range events {
		if event.Kind == kind {
			continue
		}
		out = append(out, event)
	}
	return out
}

func transcriptEventsForRunEvent(sessionID string, event clientapi.Event) []TranscriptEvent {
	return transcriptEventsForRunEventWithOptions(sessionID, event, transcriptResultOptions{})
}

func transcriptEventsForRunEventWithOptions(sessionID string, event clientapi.Event, opts transcriptResultOptions) []TranscriptEvent {
	now := time.Now()
	switch event.Kind {
	case clientapi.EventOperationRequested:
		if event.Operation == nil {
			return nil
		}
		if opts.suppressShellOperationEvents && event.Operation.Operation.Name == "shell_exec" {
			return nil
		}
		return []TranscriptEvent{{
			ID:        newEventID("op-start"),
			SessionID: sessionID,
			Time:      now,
			Kind:      EventOperationStarted,
			Summary:   operationSummary(event.Operation.Operation, event.Operation.Input),
			Data:      map[string]string{"operation": event.Operation.Operation.String(), "call_id": string(event.Operation.CallID)},
		}}
	case clientapi.EventOperationCompleted:
		if event.Operation == nil || event.Operation.Result == nil {
			return nil
		}
		if opts.suppressShellOperationEvents && event.Operation.Operation.Name == "shell_exec" {
			return nil
		}
		summary := operationCompletedSummary(event.Operation.Operation, *event.Operation.Result)
		kind := EventOperationComplete
		if event.Operation.Result.IsError() {
			kind = EventError
		}
		return []TranscriptEvent{{
			ID:        newEventID("op-done"),
			SessionID: sessionID,
			Time:      now,
			Kind:      kind,
			Summary:   summary,
			Data:      map[string]string{"operation": event.Operation.Operation.String(), "call_id": string(event.Operation.CallID)},
		}}
	case clientapi.EventRuntimeEmitted:
		if event.Runtime == nil {
			return nil
		}
		return transcriptEventsForRuntimeEventWithOptions(sessionID, now, *event.Runtime, opts)
	default:
		return nil
	}
}

func transcriptEventsForRuntimeEventWithOptions(sessionID string, now time.Time, runtimeEvent clientapi.RuntimeEvent, opts transcriptResultOptions) []TranscriptEvent {
	switch runtimeEvent.Name {
	case coreusage.EventRecordedName:
		recorded, ok := decodeRuntimePayload[coreusage.Recorded](runtimeEvent.Payload)
		if !ok || recorded.Empty() {
			return nil
		}
		return []TranscriptEvent{usageTranscriptEvent(sessionID, now, recorded)}
	case llmagent.EventModelRequestedName:
		return nil
	case llmagent.EventModelStreamedName:
		streamed, ok := decodeRuntimePayload[llmagent.ModelStreamed](runtimeEvent.Payload)
		if !ok {
			return nil
		}
		switch streamed.Event.Kind {
		case llmagent.StreamContentDelta:
			if streamed.Event.Text == "" {
				return nil
			}
			return []TranscriptEvent{{ID: newEventID("agent-delta"), SessionID: sessionID, Time: now, Kind: EventAskDelta, Summary: streamed.Event.Text}}
		case llmagent.StreamThinkingDelta:
			if streamed.Event.Text == "" {
				return nil
			}
			return []TranscriptEvent{{ID: newEventID("thinking"), SessionID: sessionID, Time: now, Kind: EventThinking, Summary: streamed.Event.Text}}
		case llmagent.StreamToolCallDelta:
			if streamed.Event.Tool == "" || !streamed.Event.Final {
				return nil
			}
			return []TranscriptEvent{{ID: newEventID("tool-call"), SessionID: sessionID, Time: now, Kind: EventOperationStarted, Summary: "tool call " + string(streamed.Event.Tool)}}
		default:
			return nil
		}
	case system.EventProcessStarted, system.EventProcessOutput, system.EventProcessExited:
		processEvent, ok := decodeRuntimePayload[system.ProcessEvent](runtimeEvent.Payload)
		if !ok {
			return nil
		}
		return transcriptEventsForProcessEventWithOptions(sessionID, now, processEvent, opts)
	default:
		return nil
	}
}

func usageTranscriptEvent(sessionID string, now time.Time, recorded coreusage.Recorded) TranscriptEvent {
	data := map[string]string{
		"subject_kind": string(recorded.Subject.Kind),
		"provider":     recorded.Subject.Provider,
		"name":         recorded.Subject.Name,
	}
	for _, measurement := range recorded.Measurements {
		value := fmt.Sprintf("%.0f", measurement.Quantity)
		switch measurement.Metric {
		case coreusage.MetricLLMInputTokens:
			if measurement.Dimensions["cache_creation"] == "true" {
				data["cache_write_tokens"] = addDecimalStrings(data["cache_write_tokens"], value)
				continue
			}
			data["input_tokens"] = addDecimalStrings(data["input_tokens"], value)
		case coreusage.MetricLLMCachedTokens:
			data["cached_tokens"] = addDecimalStrings(data["cached_tokens"], value)
		case coreusage.MetricLLMOutputTokens:
			data["output_tokens"] = addDecimalStrings(data["output_tokens"], value)
		case coreusage.MetricLLMReasoningTokens:
			data["reasoning_tokens"] = addDecimalStrings(data["reasoning_tokens"], value)
		case coreusage.MetricLLMTotalTokens:
			data["total_tokens"] = addDecimalStrings(data["total_tokens"], value)
		case coreusage.MetricCost:
			data["cost"] = addDecimalStrings(data["cost"], fmt.Sprintf("%.6f", measurement.Quantity))
			if currency := strings.TrimSpace(measurement.Dimensions["currency"]); currency != "" {
				data["currency"] = currency
			}
		}
	}
	return TranscriptEvent{
		ID:        newEventID("usage"),
		SessionID: sessionID,
		Time:      now,
		Kind:      EventUsageRecorded,
		Summary:   usageSummaryFromData(data),
		Data:      data,
	}
}

func addDecimalStrings(left, right string) string {
	if strings.TrimSpace(left) == "" {
		return right
	}
	var l, r float64
	_, _ = fmt.Sscanf(left, "%f", &l)
	_, _ = fmt.Sscanf(right, "%f", &r)
	return fmt.Sprintf("%.6f", l+r)
}

func transcriptEventsForProcessEventWithOptions(sessionID string, now time.Time, event system.ProcessEvent, opts transcriptResultOptions) []TranscriptEvent {
	switch event.Kind {
	case "started":
		if opts.suppressProcessLifecycle {
			return nil
		}
		return []TranscriptEvent{{ID: newEventID("proc-start"), SessionID: sessionID, Time: now, Kind: EventProcessStarted, Summary: event.ProcessID}}
	case "output":
		prefix := event.Stream
		if prefix == "" {
			prefix = "stdout"
		}
		var out []TranscriptEvent
		for _, line := range strings.SplitAfter(event.Data, "\n") {
			if line == "" {
				continue
			}
			summary := prefix + ": " + strings.TrimRight(line, "\n")
			data := map[string]string{"process_id": event.ProcessID, "stream": prefix}
			if opts.rawProcessOutput {
				summary = strings.TrimRight(line, "\n")
				data["raw"] = "true"
			}
			out = append(out, TranscriptEvent{
				ID:        newEventID("proc-out"),
				SessionID: sessionID,
				Time:      now,
				Kind:      EventProcessOutput,
				Summary:   summary,
				Data:      data,
			})
		}
		return out
	case "exited":
		if opts.suppressProcessLifecycle {
			return nil
		}
		code := strings.TrimSpace(event.Data)
		if code == "" {
			code = "unknown"
		}
		return []TranscriptEvent{{ID: newEventID("proc-exit"), SessionID: sessionID, Time: now, Kind: EventProcessExited, Summary: event.ProcessID + " code=" + code}}
	default:
		return nil
	}
}

func decodeRuntimePayload[T any](payload any) (T, bool) {
	var zero T
	switch typed := payload.(type) {
	case T:
		return typed, true
	case *T:
		if typed == nil {
			return zero, false
		}
		return *typed, true
	case json.RawMessage:
		var out T
		if err := json.Unmarshal(typed, &out); err != nil {
			return zero, false
		}
		return out, true
	case []byte:
		var out T
		if err := json.Unmarshal(typed, &out); err != nil {
			return zero, false
		}
		return out, true
	case map[string]any:
		data, err := json.Marshal(typed)
		if err != nil {
			return zero, false
		}
		var out T
		if err := json.Unmarshal(data, &out); err != nil {
			return zero, false
		}
		return out, true
	default:
		return zero, false
	}
}

func operationSummary(ref operation.Ref, input any) string {
	summary := ref.String()
	if text := compactValue(input, 120); text != "" {
		summary += " " + text
	}
	return summary
}

func operationCompletedSummary(ref operation.Ref, result operation.Result) string {
	if result.IsError() && result.Error != nil {
		return ref.String() + " " + result.Error.Message
	}
	status := string(result.Status)
	if status == "" {
		status = string(operation.StatusOK)
	}
	if text := compactValue(result.Output, 120); text != "" {
		return ref.String() + " " + text
	}
	return ref.String() + " status=" + status
}

func compactValue(value any, limit int) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(outputText(value))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func (c *DirectChannelClient) sessionHandle(sessionID string) (fluxplane.Session, error) {
	if c == nil {
		return nil, fmt.Errorf("direct channel client unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	handle := c.handles[sessionID]
	if handle == nil {
		return nil, fmt.Errorf("unknown shell session %q", sessionID)
	}
	return handle, nil
}

func transcriptEventsForResult(sessionID string, result fluxplane.Result, outputKind TranscriptKind, completeKind TranscriptKind) []TranscriptEvent {
	return transcriptEventsForResultWithOptions(sessionID, result, outputKind, completeKind, transcriptResultOptions{})
}

type transcriptResultOptions struct {
	suppressSuccessfulCompletion bool
	suppressShellOperationEvents bool
	suppressProcessLifecycle     bool
	rawProcessOutput             bool
}

func shellCommandTranscriptOptions() transcriptResultOptions {
	return transcriptResultOptions{
		suppressSuccessfulCompletion: true,
		suppressShellOperationEvents: true,
		suppressProcessLifecycle:     true,
		rawProcessOutput:             true,
	}
}

func transcriptEventsForResultWithOptions(sessionID string, result fluxplane.Result, outputKind TranscriptKind, completeKind TranscriptKind, opts transcriptResultOptions) []TranscriptEvent {
	now := time.Now()
	events := []TranscriptEvent{}
	hasOutbound := false
	if result.Outbound != nil && result.Outbound.Message != nil {
		events = append(events, TranscriptEvent{ID: newEventID("out"), SessionID: sessionID, Time: now, Kind: outputKind, Summary: outputText(result.Outbound.Message.Content)})
		hasOutbound = true
	}
	if result.Operation != nil {
		summary := string(result.Operation.Status)
		if result.Operation.Error != nil {
			return append(events, TranscriptEvent{ID: newEventID("op-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: result.Operation.Error.Message})
		}
		if !hasOutbound && result.Operation.Effect != nil {
			effect := result.Operation.Effect.Result
			if effect.IsError() && effect.Error != nil {
				return append(events, TranscriptEvent{ID: newEventID("op-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: effect.Error.Message})
			}
			if text := operationResultText(effect); strings.TrimSpace(text) != "" {
				events = append(events, TranscriptEvent{ID: newEventID("op-out"), SessionID: sessionID, Time: now, Kind: outputKind, Summary: text})
			}
		}
		if !opts.suppressSuccessfulCompletion || !isSuccessfulCompletionSummary(summary) {
			events = append(events, TranscriptEvent{ID: newEventID("op-done"), SessionID: sessionID, Time: now, Kind: completeKind, Summary: summary})
		}
		return events
	}
	if result.Command != nil {
		summary := string(result.Command.Status)
		if result.Command.Error != nil {
			return append(events, TranscriptEvent{ID: newEventID("cmd-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: result.Command.Error.Message})
		}
		if !opts.suppressSuccessfulCompletion || !isSuccessfulCompletionSummary(summary) {
			events = append(events, TranscriptEvent{ID: newEventID("cmd-done"), SessionID: sessionID, Time: now, Kind: completeKind, Summary: summary})
		}
		return events
	}
	if result.Input != nil {
		summary := string(result.Input.Status)
		if result.Input.Error != nil {
			return append(events, TranscriptEvent{ID: newEventID("input-error"), SessionID: sessionID, Time: now, Kind: EventError, Summary: result.Input.Error.Message})
		}
		if !opts.suppressSuccessfulCompletion || !isSuccessfulCompletionSummary(summary) {
			events = append(events, TranscriptEvent{ID: newEventID("input-done"), SessionID: sessionID, Time: now, Kind: completeKind, Summary: summary})
		}
	}
	return events
}

func isSuccessfulCompletionSummary(summary string) bool {
	summary = strings.TrimSpace(strings.ToLower(summary))
	return summary == "" || summary == "ok"
}

func operationResultText(result operation.Result) string {
	if result.Output == nil {
		return ""
	}
	return outputText(result.Output)
}

func outputText(value any) string {
	if rendered, ok := value.(operation.ModelRenderable); ok {
		return rendered.ModelText()
	}
	return fmt.Sprint(value)
}

func (c *DirectChannelClient) ChangeCWD(ctx context.Context, sessionID string, path string) (CWDResult, error) {
	_ = ctx
	_ = sessionID
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return CWDResult{}, fmt.Errorf("cd: missing path")
	}
	if cleaned == "-" {
		return CWDResult{}, fmt.Errorf("cd - is not supported yet")
	}
	return CWDResult{CWD: cleaned}, nil
}

func (c *DirectChannelClient) ResourceSearch(ctx context.Context, sessionID string, query ResourceSearchQuery) ([]ResourceSearchResult, error) {
	_ = ctx
	_ = sessionID
	return staticResourceSearch(query, c.commands), nil
}

func newRemoteDirectChannelClient(endpoint string) (ShellClient, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultDirectEndpoint
	}
	cfg := httpsse.ClientConfig{BaseURL: endpoint}
	if parsed, err := url.Parse(endpoint); err == nil && strings.EqualFold(parsed.Scheme, "unix") {
		cfg.BaseURL = "http://unix"
		cfg.UnixSocket = parsed.Path
	}
	client, err := httpsse.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return NewDirectChannelClient(DirectChannelClientOptions{
		Client:  client,
		Session: fluxplane.SessionRef{Name: defaultSessionName},
		Prefix:  "remote",
	}), nil
}

func shellOperationInvocation(line string, cwd string) (fluxplane.OperationInvocation, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return fluxplane.OperationInvocation{}, fmt.Errorf("shell command is empty")
	}
	return fluxplane.OperationInvocation{
		Operation: operation.Ref{Name: "shell_exec"},
		Input: map[string]any{
			"command": line,
			"workdir": strings.TrimSpace(cwd),
		},
	}, nil
}
