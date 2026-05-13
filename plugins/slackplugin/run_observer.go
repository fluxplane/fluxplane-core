package slackplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/slack-go/slack"
)

const (
	streamFlushInterval = 300 * time.Millisecond
	statusMinInterval   = 800 * time.Millisecond
	slackAppendRetries  = 2
)

type runSummary struct {
	Events          int
	ModelEvents     int
	OperationEvents int
	Streamed        bool
	ContentStreamed bool
	ActivePlans     map[string]bool
	LastCursor      clientapi.EventCursor
}

type runObserver struct {
	channel *SlackChannel
	target  Target

	mu         sync.Mutex
	streamTS   string
	streamed   bool
	streamFail bool
	buffer     strings.Builder
	appended   strings.Builder
	timer      *time.Timer

	status        string
	statusUpdated time.Time
	summary       runSummary
	activePlans   map[string]bool
	appendFailed  bool
}

func newRunObserver(channel *SlackChannel, target Target) *runObserver {
	return &runObserver{channel: channel, target: target}
}

func (o *runObserver) Observe(events <-chan clientapi.Event) <-chan runSummary {
	done := make(chan runSummary, 1)
	go func() {
		for event := range events {
			o.Handle(event)
		}
		_ = o.Flush()
		o.mu.Lock()
		o.summary.Streamed = o.streamed
		o.summary.ActivePlans = cloneBoolMap(o.activePlans)
		summary := o.summary
		o.mu.Unlock()
		done <- summary
		close(done)
	}()
	return done
}

func (o *runObserver) Handle(event clientapi.Event) {
	if o == nil || o.channel == nil {
		return
	}
	o.mu.Lock()
	o.summary.Events++
	if event.Cursor.Sequence != 0 {
		o.summary.LastCursor = event.Cursor
	}
	o.mu.Unlock()
	switch event.Kind {
	case clientapi.EventOperationRequested:
		o.handleOperationRequested(event)
	case clientapi.EventOperationCompleted:
		o.handleOperationCompleted(event)
	case clientapi.EventRuntimeEmitted:
		o.handleRuntime(event)
	case clientapi.EventRunCompleted:
		slog.Info("slack run completed", "channel", o.channel.name, "run", event.RunID)
	case clientapi.EventRunFailed:
		slog.Warn("slack run failed", "channel", o.channel.name, "run", event.RunID, "error", event.Error)
	}
}

func (o *runObserver) FollowPlans(ctx context.Context, session clientapi.SessionHandle, active map[string]bool) runSummary {
	if o == nil || session == nil || len(active) == 0 {
		return runSummary{}
	}
	o.mu.Lock()
	if o.activePlans == nil {
		o.activePlans = map[string]bool{}
	}
	for planID := range active {
		o.activePlans[planID] = true
	}
	o.mu.Unlock()
	after := o.snapshotSummary().LastCursor
	events, cancel, err := session.Events(ctx, clientapi.EventOptions{Buffer: 64, Replay: true, After: after})
	if err != nil {
		slog.Warn("slack background plan events unavailable", "channel", o.channel.name, "error", err)
		return runSummary{}
	}
	defer cancel()
	for {
		o.mu.Lock()
		remaining := len(o.activePlans)
		o.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return o.snapshotSummary()
		case event, ok := <-events:
			if !ok {
				return o.snapshotSummary()
			}
			if event.Runtime == nil || (!strings.HasPrefix(string(event.Runtime.Name), "plan.") && !strings.HasPrefix(string(event.Runtime.Name), "subagent.")) {
				continue
			}
			o.Handle(event)
		}
	}
	_ = o.Flush()
	return o.snapshotSummary()
}

func (o *runObserver) snapshotSummary() runSummary {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.summary.Streamed = o.streamed
	o.summary.ActivePlans = cloneBoolMap(o.activePlans)
	return o.summary
}

func (o *runObserver) Finish(ctx context.Context, finalMarkdown string) {
	flushErr := o.Flush()
	if o.started() {
		if err := o.stopStream(ctx, o.finalMarkdownPatch(finalMarkdown, flushErr)); err != nil {
			slog.Warn("slack stream stop failed", "channel", o.channel.name, "slack_channel", o.target.ChannelID, "thread_ts", o.target.ThreadTS, "error", err)
		}
	}
	o.setStatus(ctx, "")
}

func (o *runObserver) handleOperationRequested(event clientapi.Event) {
	if event.Operation == nil {
		return
	}
	o.mu.Lock()
	o.summary.OperationEvents++
	o.mu.Unlock()
	name := event.Operation.Operation.String()
	slog.Info("slack run tool start", "channel", o.channel.name, "run", event.RunID, "tool", name, "input", compactValue(event.Operation.Input, 320))
	title := toolLabel(name)
	o.setStatus(context.Background(), "is "+title+"...")
}

func (o *runObserver) handleOperationCompleted(event clientapi.Event) {
	if event.Operation == nil {
		return
	}
	o.mu.Lock()
	o.summary.OperationEvents++
	o.mu.Unlock()
	name := event.Operation.Operation.String()
	status := operation.StatusOK
	if event.Operation.Result != nil && event.Operation.Result.Status != "" {
		status = event.Operation.Result.Status
	}
	attrs := []any{"channel", o.channel.name, "run", event.RunID, "tool", name, "status", status}
	if event.Operation.Result != nil && event.Operation.Result.Error != nil {
		attrs = append(attrs, "error", event.Operation.Result.Error.Message)
	}
	slog.Info("slack run tool end", attrs...)
	o.setStatus(context.Background(), "is thinking...")
}

func (o *runObserver) handleRuntime(event clientapi.Event) {
	if event.Runtime == nil {
		return
	}
	o.trackPlanRuntime(event)
	o.mu.Lock()
	o.summary.ModelEvents++
	o.mu.Unlock()
	switch payload := event.Runtime.Payload.(type) {
	case llmagent.ModelRequested:
		slog.Info("slack run model start", "channel", o.channel.name, "run", event.RunID, "provider", payload.Provider, "model", payload.Model)
		o.setStatus(context.Background(), "is thinking...")
	case llmagent.ModelCompleted:
		slog.Info("slack run model end", "channel", o.channel.name, "run", event.RunID, "provider", payload.Provider, "model", payload.Model, "decision", payload.Decision)
	case llmagent.ModelFailed:
		slog.Warn("slack run model failed", "channel", o.channel.name, "run", event.RunID, "provider", payload.Provider, "model", payload.Model, "error", payload.Error)
	case llmagent.ModelStreamed:
		o.handleModelStream(event.RunID, payload.Event)
	case planexecplugin.PlanCreated:
		o.handlePlanCreated(payload)
	case planexecplugin.StepDispatched:
		o.handlePlanStepDispatched(payload)
	case planexecplugin.StepProgressed:
		o.appendTaskUpdate(planTaskID(payload.PlanID, payload.StepID), "Step "+payload.StepID, slack.TaskCardStatusInProgress, payload.Message, "")
	case planexecplugin.StepCompleted:
		o.appendTaskUpdate(planTaskID(payload.PlanID, payload.StepID), "Step "+payload.StepID, slack.TaskCardStatusComplete, "", payload.Output)
	case planexecplugin.StepFailed:
		o.appendTaskUpdate(planTaskID(payload.PlanID, payload.StepID), "Step "+payload.StepID, slack.TaskCardStatusError, "", payload.Error)
	case planexecplugin.PlanCompleted:
		o.appendTaskUpdate("plan:"+payload.PlanID, "Plan completed", slack.TaskCardStatusComplete, "", payload.Summary)
	case planexecplugin.PlanFailed:
		o.appendTaskUpdate("plan:"+payload.PlanID, "Plan failed", slack.TaskCardStatusError, "", payload.Reason)
	case planexecplugin.PlanCancelled:
		o.appendTaskUpdate("plan:"+payload.PlanID, "Plan cancelled", slack.TaskCardStatusError, "", payload.Reason)
	case subagent.Started:
		o.appendTaskUpdate(delegateTaskID(payload.WorkerID), "Delegate "+string(payload.WorkerID), slack.TaskCardStatusInProgress, payload.Task, "")
	case subagent.Completed:
		o.appendTaskUpdate(delegateTaskID(payload.WorkerID), "Delegate "+string(payload.WorkerID), slack.TaskCardStatusComplete, "", payload.Output)
	case subagent.Failed:
		o.appendTaskUpdate(delegateTaskID(payload.WorkerID), "Delegate "+string(payload.WorkerID), slack.TaskCardStatusError, "", payload.Error)
	case subagent.Cancelled:
		o.appendTaskUpdate(delegateTaskID(payload.WorkerID), "Delegate "+string(payload.WorkerID), slack.TaskCardStatusError, "", payload.Reason)
	case map[string]any:
		o.handleRuntimeMap(event, payload)
	}
}

func (o *runObserver) handleRuntimeMap(event clientapi.Event, payload map[string]any) {
	switch string(event.Runtime.Name) {
	case string(llmagent.EventModelStreamedName):
		raw, ok := payload["event"]
		if !ok {
			return
		}
		data, err := json.Marshal(raw)
		if err != nil {
			return
		}
		var streamEvent llmagent.StreamEvent
		if err := json.Unmarshal(data, &streamEvent); err != nil {
			return
		}
		o.handleModelStream(event.RunID, streamEvent)
	case string(planexecplugin.EventPlanCreated):
		var typed planexecplugin.PlanCreated
		if decodeRuntimeMap(payload, &typed) == nil {
			o.handlePlanCreated(typed)
		}
	case string(planexecplugin.EventStepDispatched):
		var typed planexecplugin.StepDispatched
		if decodeRuntimeMap(payload, &typed) == nil {
			o.handlePlanStepDispatched(typed)
		}
	case string(planexecplugin.EventStepProgressed):
		var typed planexecplugin.StepProgressed
		if decodeRuntimeMap(payload, &typed) == nil {
			o.appendTaskUpdate(planTaskID(typed.PlanID, typed.StepID), "Step "+typed.StepID, slack.TaskCardStatusInProgress, typed.Message, "")
		}
	case string(planexecplugin.EventStepCompleted):
		var typed planexecplugin.StepCompleted
		if decodeRuntimeMap(payload, &typed) == nil {
			o.appendTaskUpdate(planTaskID(typed.PlanID, typed.StepID), "Step "+typed.StepID, slack.TaskCardStatusComplete, "", typed.Output)
		}
	case string(planexecplugin.EventStepFailed):
		var typed planexecplugin.StepFailed
		if decodeRuntimeMap(payload, &typed) == nil {
			o.appendTaskUpdate(planTaskID(typed.PlanID, typed.StepID), "Step "+typed.StepID, slack.TaskCardStatusError, "", typed.Error)
		}
	case string(planexecplugin.EventPlanCompleted):
		var typed planexecplugin.PlanCompleted
		if decodeRuntimeMap(payload, &typed) == nil {
			o.appendTaskUpdate("plan:"+typed.PlanID, "Plan completed", slack.TaskCardStatusComplete, "", typed.Summary)
		}
	case string(planexecplugin.EventPlanFailed):
		var typed planexecplugin.PlanFailed
		if decodeRuntimeMap(payload, &typed) == nil {
			o.appendTaskUpdate("plan:"+typed.PlanID, "Plan failed", slack.TaskCardStatusError, "", typed.Reason)
		}
	case string(planexecplugin.EventPlanCancelled):
		var typed planexecplugin.PlanCancelled
		if decodeRuntimeMap(payload, &typed) == nil {
			o.appendTaskUpdate("plan:"+typed.PlanID, "Plan cancelled", slack.TaskCardStatusError, "", typed.Reason)
		}
	}
}

func (o *runObserver) trackPlanRuntime(event clientapi.Event) {
	planID := runtimePlanID(event)
	if planID == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.activePlans == nil {
		o.activePlans = map[string]bool{}
	}
	switch string(event.Runtime.Name) {
	case string(planexecplugin.EventPlanExecutionStarted):
		o.activePlans[planID] = true
	case string(planexecplugin.EventPlanCompleted), string(planexecplugin.EventPlanFailed), string(planexecplugin.EventPlanCancelled):
		delete(o.activePlans, planID)
	}
}

func runtimePlanID(event clientapi.Event) string {
	if event.Runtime == nil {
		return ""
	}
	payload := runtimePayloadMap(event.Runtime.Payload)
	if value, ok := payload["plan_id"].(string); ok {
		return value
	}
	return ""
}

func runtimePayloadMap(payload any) map[string]any {
	switch typed := payload.(type) {
	case map[string]any:
		return typed
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil
		}
		return out
	}
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (o *runObserver) handlePlanCreated(event planexecplugin.PlanCreated) {
	o.appendTaskUpdate("plan:"+event.PlanID, event.Spec.Title, slack.TaskCardStatusInProgress, event.Spec.Description, "")
	for _, step := range event.Spec.Steps {
		o.appendTaskUpdate(planTaskID(event.PlanID, step.ID), step.Title, slack.TaskCardStatusPending, "", "")
	}
}

func (o *runObserver) handlePlanStepDispatched(event planexecplugin.StepDispatched) {
	title := event.Title
	if title == "" {
		title = "Step " + event.StepID
	}
	detail := event.Profile
	if event.WorkerID != "" {
		detail = strings.TrimSpace(detail + " " + string(event.WorkerID))
	}
	o.appendTaskUpdate(planTaskID(event.PlanID, event.StepID), title, slack.TaskCardStatusInProgress, detail, "")
}

func decodeRuntimeMap(payload map[string]any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func planTaskID(planID, stepID string) string {
	return "plan:" + planID + ":" + stepID
}

func delegateTaskID(workerID subagent.ID) string {
	return "delegate:" + string(workerID)
}

func (o *runObserver) handleModelStream(runID clientapi.RunID, event llmagent.StreamEvent) {
	switch event.Kind {
	case llmagent.StreamContentDelta:
		if event.Text != "" {
			o.Append(event.Text)
		}
	case llmagent.StreamThinkingDelta:
		if o.channel.debug && strings.TrimSpace(event.Text) != "" {
			slog.Debug("slack run thinking delta", "channel", o.channel.name, "run", runID, "bytes", len(event.Text))
		}
	case llmagent.StreamToolCallDelta:
		if event.Final {
			slog.Info("slack run model tool call", "channel", o.channel.name, "run", runID, "tool", event.Tool)
			o.setStatus(context.Background(), "is "+toolLabel(string(event.Tool))+"...")
		} else if o.channel.debug && strings.TrimSpace(event.Text) != "" {
			slog.Debug("slack run tool args delta", "channel", o.channel.name, "run", runID, "tool", event.Tool, "args", compactText(event.Text, 240))
		}
	}
}

func (o *runObserver) Append(text string) {
	if text == "" || o == nil {
		return
	}
	if !o.ensureStarted(context.Background()) {
		return
	}
	o.mu.Lock()
	o.buffer.WriteString(text)
	o.summary.ContentStreamed = true
	if o.timer == nil {
		o.timer = time.AfterFunc(streamFlushInterval, func() { _ = o.Flush() })
	}
	o.mu.Unlock()
}

func (o *runObserver) Flush() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	text := o.buffer.String()
	o.buffer.Reset()
	o.mu.Unlock()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if err := o.appendMarkdown(context.Background(), text); err != nil {
		o.mu.Lock()
		var restored strings.Builder
		restored.WriteString(text)
		restored.WriteString(o.buffer.String())
		o.buffer = restored
		o.appendFailed = true
		o.mu.Unlock()
		slog.Warn("slack stream append failed; buffered text will be retried", "channel", o.channel.name, "slack_channel", o.target.ChannelID, "thread_ts", o.target.ThreadTS, "error", err)
		return err
	}
	o.mu.Lock()
	o.appended.WriteString(text)
	o.mu.Unlock()
	return nil
}

func (o *runObserver) ensureStarted(ctx context.Context) bool {
	o.mu.Lock()
	if o.streamed {
		o.mu.Unlock()
		return true
	}
	if o.streamFail {
		o.mu.Unlock()
		return false
	}
	o.mu.Unlock()
	if err := o.startStream(ctx); err != nil {
		o.mu.Lock()
		o.streamFail = true
		o.mu.Unlock()
		slog.Debug("slack stream start failed", "channel", o.channel.name, "slack_channel", o.target.ChannelID, "thread_ts", o.target.ThreadTS, "error", err)
		return false
	}
	return true
}

func (o *runObserver) started() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.streamed
}

func (o *runObserver) startStream(ctx context.Context) error {
	if o.channel == nil || o.channel.api == nil {
		return fmt.Errorf("slack api is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	options := []slack.MsgOption{
		slack.MsgOptionStartStream(),
		slack.MsgOptionTS(o.target.ThreadTS),
	}
	if o.target.TeamID != "" {
		options = append(options, slack.MsgOptionRecipientTeamID(o.target.TeamID))
	}
	if o.target.UserID != "" {
		options = append(options, slack.MsgOptionRecipientUserID(o.target.UserID))
	}
	_, ts, err := o.channel.api.PostMessageContext(ctx, o.target.ChannelID, options...)
	if err != nil {
		return err
	}
	if ts == "" {
		return fmt.Errorf("startStream returned empty ts")
	}
	o.mu.Lock()
	o.streamTS = ts
	o.streamed = true
	o.summary.Streamed = true
	o.mu.Unlock()
	slog.Info("slack stream started", "channel", o.channel.name, "slack_channel", o.target.ChannelID, "thread_ts", o.target.ThreadTS, "stream_ts", ts)
	return nil
}

func (o *runObserver) appendMarkdown(ctx context.Context, text string) error {
	return o.appendStream(ctx, slack.MsgOptionMarkdownText(text))
}

func (o *runObserver) appendTaskUpdate(taskID, title string, status slack.TaskCardStatus, details, output string) {
	if taskID == "" || title == "" || o == nil {
		return
	}
	if !o.ensureStarted(context.Background()) {
		return
	}
	chunk := slack.NewTaskUpdateChunk(taskID, title)
	chunk.Status = status
	chunk.Details = compactText(details, 240)
	chunk.Output = compactText(output, 240)
	if err := o.appendChunks(context.Background(), chunk); err != nil {
		o.mu.Lock()
		o.appendFailed = true
		o.mu.Unlock()
		slog.Warn("slack task update append failed", "channel", o.channel.name, "slack_channel", o.target.ChannelID, "thread_ts", o.target.ThreadTS, "error", err)
	}
}

func (o *runObserver) appendChunks(ctx context.Context, chunks ...slack.StreamChunk) error {
	return o.appendStream(ctx, slack.MsgOptionChunks(chunks...))
}

func (o *runObserver) appendStream(ctx context.Context, options ...slack.MsgOption) error {
	o.mu.Lock()
	ts := o.streamTS
	o.mu.Unlock()
	if ts == "" || o.channel == nil || o.channel.api == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt <= slackAppendRetries; attempt++ {
		callOptions := append([]slack.MsgOption{slack.MsgOptionAppendStream(ts)}, options...)
		_, _, err = o.channel.api.PostMessageContext(ctx, o.target.ChannelID, callOptions...)
		if err == nil {
			return nil
		}
		if rateLimited, ok := err.(*slack.RateLimitedError); ok && rateLimited.RetryAfter > 0 && attempt < slackAppendRetries {
			timer := time.NewTimer(rateLimited.RetryAfter)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		break
	}
	return err
}

func (o *runObserver) stopStream(ctx context.Context, markdown string) error {
	o.mu.Lock()
	ts := o.streamTS
	o.mu.Unlock()
	if ts == "" || o.channel == nil || o.channel.api == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	options := []slack.MsgOption{slack.MsgOptionStopStream(ts)}
	if strings.TrimSpace(markdown) != "" {
		options = append(options, slack.MsgOptionMarkdownText(markdown))
	}
	_, _, err := o.channel.api.PostMessageContext(ctx, o.target.ChannelID, options...)
	return err
}

func (o *runObserver) finalMarkdownPatch(finalMarkdown string, flushErr error) string {
	if strings.TrimSpace(finalMarkdown) == "" {
		return ""
	}
	o.mu.Lock()
	appended := o.appended.String()
	failed := o.appendFailed || flushErr != nil
	o.mu.Unlock()
	if appended == "" {
		return finalMarkdown
	}
	if strings.HasPrefix(finalMarkdown, appended) {
		return finalMarkdown[len(appended):]
	}
	if failed {
		return "\n\n" + finalMarkdown
	}
	return ""
}

func (o *runObserver) setStatus(ctx context.Context, status string) {
	if o == nil || o.channel == nil || o.channel.api == nil || o.target.ChannelID == "" || o.target.ThreadTS == "" {
		return
	}
	now := time.Now()
	o.mu.Lock()
	if status == o.status {
		o.mu.Unlock()
		return
	}
	if status != "" && !o.statusUpdated.IsZero() && now.Sub(o.statusUpdated) < statusMinInterval {
		o.mu.Unlock()
		return
	}
	o.status = status
	o.statusUpdated = now
	o.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := o.channel.api.SetAssistantThreadsStatusContext(ctx, slack.AssistantThreadsSetStatusParameters{
		ChannelID: o.target.ChannelID,
		ThreadTS:  o.target.ThreadTS,
		Status:    status,
	})
	if err != nil {
		slog.Debug("slack setStatus failed", "channel", o.channel.name, "slack_channel", o.target.ChannelID, "thread_ts", o.target.ThreadTS, "status", status, "error", err)
	}
}

func (c *SlackChannel) postError(ctx context.Context, target Target, err error) error {
	if err == nil {
		return nil
	}
	message := compactText(err.Error(), 600)
	if message == "" {
		message = "unknown error"
	}
	_, postErr := c.dispatcher.Post(ctx, target, "Something went wrong while processing this request:\n```"+sanitizeCodeFence(message)+"```")
	return postErr
}

func slackResultError(result clientapi.Result) error {
	if result.Input != nil && result.Input.Status != sessionruntime.InputStatusOK {
		if result.Input.Error != nil {
			return fmt.Errorf("%s: %s", result.Input.Error.Code, result.Input.Error.Message)
		}
		return fmt.Errorf("input failed: %s", result.Input.Status)
	}
	if result.Command != nil && result.Command.Status != sessionruntime.CommandStatusOK {
		if result.Command.Error != nil {
			return fmt.Errorf("%s: %s", result.Command.Error.Code, result.Command.Error.Message)
		}
		return fmt.Errorf("command failed: %s", result.Command.Status)
	}
	return nil
}

func toolLabel(name string) string {
	switch name {
	case "channel_send":
		return "sending a message"
	case "datasource_search":
		return "searching datasources"
	case "datasource_get":
		return "reading a datasource record"
	case "slack_search", "slack_bot_search":
		return "searching Slack"
	case "gitlab_project_search":
		return "searching GitLab projects"
	case "jira_issue_search":
		return "searching Jira issues"
	case "web_request":
		return "reading the web"
	default:
		switch {
		case strings.Contains(name, "slack") && strings.Contains(name, "search"):
			return "searching Slack"
		case strings.Contains(name, "gitlab") && strings.Contains(name, "search"):
			return "searching GitLab"
		case strings.Contains(name, "jira") && strings.Contains(name, "search"):
			return "searching Jira"
		case strings.Contains(name, "search"):
			return "searching"
		default:
			return "using " + name
		}
	}
}

func compactValue(value any, max int) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return compactText(fmt.Sprint(value), max)
	}
	return compactText(string(data), max)
}

func compactText(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", "\\n"))
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func sanitizeCodeFence(text string) string {
	return strings.ReplaceAll(text, "```", "` ` `")
}
