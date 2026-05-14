package terminalui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
)

// TurnOptions configures terminal execution/rendering for one submitted turn.
type TurnOptions struct {
	Debug     bool
	Usage     bool
	Reasoning ReasoningDisplay
	Out       io.Writer
	Err       io.Writer
}

type turnRenderResult struct {
	Streamed    bool
	ActivePlans map[string]bool
	SeenRuntime map[string]bool
}

// RunTurn submits prompt to a session, treating slash-prefixed prompts as
// command invocations and all other prompts as conversational input.
func RunTurn(ctx context.Context, session clientapi.SessionHandle, prompt string, opts TurnOptions, tracker *usage.Tracker) error {
	if invocation, ok, err := command.ParseSlash(prompt); err != nil {
		return err
	} else if ok {
		return runCommandTurn(ctx, session, invocation, opts, tracker)
	}
	return runInputTurn(ctx, session, prompt, opts, tracker)
}

func runInputTurn(ctx context.Context, session clientapi.SessionHandle, prompt string, opts TurnOptions, tracker *usage.Tracker) error {
	run, err := session.Submit(ctx, clientapi.NewSubmission().WithText(prompt))
	if err != nil {
		return err
	}
	eventsDone := renderTurnEvents(run.Events(), tracker, opts)
	result, err := run.Wait(ctx)
	eventResult := turnRenderResult{}
	if eventsDone != nil {
		eventResult = <-eventsDone
	}
	if len(eventResult.ActivePlans) > 0 {
		followResult := followBackgroundPlans(ctx, session, eventResult, tracker, opts)
		eventResult.Streamed = eventResult.Streamed || followResult.Streamed
	}
	if !eventResult.Streamed {
		renderOutbound(opts.Out, result)
	}
	renderUsage(opts.Err, opts.Usage, tracker)
	if err != nil {
		return err
	}
	return ResultError(result)
}

func runCommandTurn(ctx context.Context, session clientapi.SessionHandle, invocation command.Invocation, opts TurnOptions, tracker *usage.Tracker) error {
	run, err := session.Submit(ctx, clientapi.NewSubmission().WithCommand(invocation))
	if err != nil {
		return err
	}
	eventsDone := renderTurnEvents(run.Events(), tracker, opts)
	result, err := run.Wait(ctx)
	if eventsDone != nil {
		<-eventsDone
	}
	renderOutbound(opts.Out, result)
	renderUsage(opts.Err, opts.Usage, tracker)
	if err != nil {
		return err
	}
	return ResultError(result)
}

// ResultError converts non-OK input/command results into user-facing errors.
func ResultError(result clientapi.Result) error {
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

func renderOutbound(out io.Writer, result clientapi.Result) {
	if out == nil {
		out = io.Discard
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		return
	}
	content := fmt.Sprint(result.Outbound.Message.Content)
	if content == "" {
		return
	}
	_ = RenderMarkdown(out, content)
}

func renderTurnEvents(events <-chan clientapi.Event, tracker *usage.Tracker, opts TurnOptions) <-chan turnRenderResult {
	done := make(chan turnRenderResult, 1)
	go func() {
		renderer := NewRenderer(defaultWriter(opts.Out), defaultWriter(opts.Err), false)
		renderer.Reasoning = opts.Reasoning
		result := turnRenderResult{ActivePlans: map[string]bool{}, SeenRuntime: map[string]bool{}}
		for event := range events {
			trackUsageEvent(tracker, event)
			trackPlanRuntimeEvent(event, result.ActivePlans, result.SeenRuntime)
			if opts.Debug {
				renderer.RenderDebug(event)
			}
			renderer.Render(event)
		}
		renderer.Finish()
		result.Streamed = renderer.HasStreamedContent()
		done <- result
		close(done)
	}()
	return done
}

func followBackgroundPlans(ctx context.Context, session clientapi.SessionHandle, initial turnRenderResult, tracker *usage.Tracker, opts TurnOptions) turnRenderResult {
	if session == nil || len(initial.ActivePlans) == 0 {
		return turnRenderResult{}
	}
	events, cancel, err := session.Events(ctx, clientapi.EventOptions{Buffer: 64, Replay: true})
	if err != nil {
		_, _ = fmt.Fprintf(defaultWriter(opts.Err), "background plan events unavailable: %v\n", err)
		return turnRenderResult{}
	}
	defer cancel()
	renderer := NewRenderer(defaultWriter(opts.Out), defaultWriter(opts.Err), false)
	renderer.Reasoning = opts.Reasoning
	result := turnRenderResult{ActivePlans: cloneBoolMap(initial.ActivePlans), SeenRuntime: cloneBoolMap(initial.SeenRuntime)}
	for len(result.ActivePlans) > 0 {
		select {
		case <-ctx.Done():
			renderer.Finish()
			return result
		case event, ok := <-events:
			if !ok {
				renderer.Finish()
				return result
			}
			if !isPlanRuntimeEvent(event) && !isSubagentRuntimeEvent(event) {
				continue
			}
			key := runtimeEventKey(event)
			if key != "" && result.SeenRuntime[key] {
				continue
			}
			trackUsageEvent(tracker, event)
			trackPlanRuntimeEvent(event, result.ActivePlans, result.SeenRuntime)
			if opts.Debug {
				renderer.RenderDebug(event)
			}
			renderer.Render(event)
		}
	}
	renderer.Finish()
	result.Streamed = renderer.HasStreamedContent()
	return result
}

func renderUsage(out io.Writer, enabled bool, tracker *usage.Tracker) {
	if enabled && tracker != nil {
		RenderUsageSnapshot(defaultWriter(out), tracker.Snapshot())
	}
}

func trackPlanRuntimeEvent(event clientapi.Event, active map[string]bool, seen map[string]bool) {
	if event.Runtime == nil {
		return
	}
	if key := runtimeEventKey(event); key != "" && seen != nil {
		seen[key] = true
	}
	planID := runtimePlanID(event)
	if planID == "" || active == nil {
		return
	}
	switch string(event.Runtime.Name) {
	case "plan.execution_started":
		active[planID] = true
	case "plan.completed", "plan.failed", "plan.cancelled":
		delete(active, planID)
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

func isPlanRuntimeEvent(event clientapi.Event) bool {
	return event.Runtime != nil && strings.HasPrefix(string(event.Runtime.Name), "plan.")
}

func isSubagentRuntimeEvent(event clientapi.Event) bool {
	return event.Runtime != nil && strings.HasPrefix(string(event.Runtime.Name), "subagent.")
}

func runtimeEventKey(event clientapi.Event) string {
	if event.Runtime == nil {
		return ""
	}
	raw, err := json.Marshal(event.Runtime.Payload)
	if err != nil {
		return string(event.Runtime.Name)
	}
	return string(event.Runtime.Name) + ":" + string(raw)
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

func trackUsageEvent(tracker *usage.Tracker, event clientapi.Event) {
	if tracker == nil {
		return
	}
	if recorded, ok := usageFromEvent(event); ok {
		tracker.Add(recorded)
	}
}

func usageFromEvent(event clientapi.Event) (usage.Recorded, bool) {
	if event.Runtime == nil || event.Runtime.Name != usage.EventRecordedName {
		return usage.Recorded{}, false
	}
	recorded, ok := event.Runtime.Payload.(usage.Recorded)
	if !ok || recorded.Empty() {
		return usage.Recorded{}, false
	}
	return recorded, true
}

func defaultWriter(out io.Writer) io.Writer {
	if out == nil {
		return io.Discard
	}
	return out
}
