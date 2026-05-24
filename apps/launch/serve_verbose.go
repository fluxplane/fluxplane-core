package launch

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	fluxplane "github.com/fluxplane/fluxplane-core"
	coretrigger "github.com/fluxplane/fluxplane-core/core/trigger"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/plugins/native/human"
)

type serveEventWatcher interface {
	OnEvent(context.Context, func(clientapi.Event)) (func(), error)
}

func startServeEventLogger(ctx context.Context, client fluxplane.ChannelClient, out io.Writer) (func(), error) {
	if out == nil {
		out = io.Discard
	}
	watcher, ok := client.(serveEventWatcher)
	if !ok {
		return nil, fmt.Errorf("serve: --verbose is unavailable for this channel client")
	}
	var mu sync.Mutex
	return watcher.OnEvent(ctx, func(event clientapi.Event) {
		line := formatServeEvent(time.Now(), event)
		if line == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintln(out, line)
	})
}

func logServeVerboseReady(out io.Writer, root string) {
	if out == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "%s serve verbose=enabled root=%s\n", time.Now().Format(time.RFC3339), quoteLogValue(root))
}

func logServeTriggerStart(out io.Writer, specs []coretrigger.Spec) {
	if out == nil {
		return
	}
	for _, spec := range specs {
		if spec.Disabled {
			continue
		}
		parts := []string{
			time.Now().Format(time.RFC3339),
			"serve",
			"trigger=" + spec.Name,
			"kind=" + string(spec.Kind),
			"session=" + spec.Session,
			"status=starting",
		}
		if spec.Kind == coretrigger.KindSchedule && strings.TrimSpace(spec.Schedule.Every) != "" {
			parts = append(parts, "schedule_every="+strings.TrimSpace(spec.Schedule.Every))
		}
		_, _ = fmt.Fprintln(out, strings.Join(parts, " "))
	}
}

func formatServeEvent(at time.Time, event clientapi.Event) string {
	detail := serveEventDetail(event)
	if detail == "" {
		return ""
	}
	parts := []string{at.Format(time.RFC3339), "serve", "event=" + string(event.Kind)}
	if event.Session.Session.Name != "" {
		parts = append(parts, "session="+string(event.Session.Session.Name))
	}
	if event.Session.Thread.ID != "" {
		parts = append(parts, "thread="+string(event.Session.Thread.ID))
	}
	if event.RunID != "" {
		parts = append(parts, "run="+string(event.RunID))
	}
	parts = append(parts, detail)
	return strings.Join(parts, " ")
}

func serveEventDetail(event clientapi.Event) string {
	switch event.Kind {
	case clientapi.EventSubmissionReceived:
		if event.Submission == nil {
			return "submission=unknown"
		}
		return serveSubmissionDetail(*event.Submission)
	case clientapi.EventInputCompleted:
		if event.Input == nil {
			return "input=completed"
		}
		return "input_status=" + string(event.Input.Status)
	case clientapi.EventCommandCompleted:
		if event.Command == nil {
			return "command=completed"
		}
		parts := []string{"command_status=" + string(event.Command.Status)}
		if path := event.Command.Spec.Path.String(); path != "" {
			parts = append(parts, "command="+path)
		}
		if event.Command.Error != nil {
			parts = append(parts, "error="+quoteLogValue(event.Command.Error.Message))
		}
		return strings.Join(parts, " ")
	case clientapi.EventTriggerCompleted:
		if event.Trigger == nil {
			return "trigger=completed"
		}
		parts := []string{
			"trigger=" + event.Trigger.Trigger.Name,
			"trigger_status=" + string(event.Trigger.Status),
		}
		if len(event.Trigger.Effects) > 0 {
			parts = append(parts, fmt.Sprintf("effects=%d", len(event.Trigger.Effects)))
		}
		if event.Trigger.Error != nil {
			parts = append(parts, "error="+quoteLogValue(event.Trigger.Error.Message))
		}
		return strings.Join(parts, " ")
	case clientapi.EventAgentStepCompleted:
		if event.Agent == nil {
			return "agent_step=completed"
		}
		parts := []string{
			"agent_status=" + string(event.Agent.Status),
			"decision=" + string(event.Agent.Decision.Kind),
		}
		if len(event.Agent.Decision.Operations) > 0 {
			parts = append(parts, fmt.Sprintf("operations=%d", len(event.Agent.Decision.Operations)))
		}
		if event.Agent.Error != nil {
			parts = append(parts, "error="+quoteLogValue(event.Agent.Error.Message))
		}
		return strings.Join(parts, " ")
	case clientapi.EventOperationRequested:
		if event.Operation == nil {
			return "operation=requested"
		}
		parts := []string{"operation=" + event.Operation.Operation.String(), "operation_status=requested"}
		if event.Operation.CallID != "" {
			parts = append(parts, "call="+string(event.Operation.CallID))
		}
		return strings.Join(parts, " ")
	case clientapi.EventOperationCompleted:
		if event.Operation == nil {
			return "operation=completed"
		}
		parts := []string{"operation=" + event.Operation.Operation.String(), "operation_status=completed"}
		if event.Operation.CallID != "" {
			parts = append(parts, "call="+string(event.Operation.CallID))
		}
		if event.Operation.Result != nil {
			parts = append(parts, "result="+string(event.Operation.Result.Status))
			if event.Operation.Result.Error != nil {
				parts = append(parts, "error="+quoteLogValue(event.Operation.Result.Error.Message))
			}
		}
		return strings.Join(parts, " ")
	case clientapi.EventOutboundProduced:
		if event.Outbound == nil || event.Outbound.Message == nil {
			return "outbound=produced"
		}
		return "outbound=" + quoteLogValue(shortLogValue(event.Outbound.Message.Content))
	case clientapi.EventRuntimeEmitted:
		if event.Runtime == nil {
			return "runtime=emitted"
		}
		if event.Runtime.Name == human.EventNotificationSent {
			return serveNotificationDetail(event.Runtime.Payload)
		}
		return "runtime_event=" + string(event.Runtime.Name)
	case clientapi.EventRunCompleted:
		return "run_status=completed"
	case clientapi.EventRunFailed:
		if event.Error != nil {
			return "run_status=failed error=" + quoteLogValue(event.Error.Error())
		}
		return "run_status=failed"
	default:
		return ""
	}
}

func serveNotificationDetail(payload any) string {
	notification, ok := payload.(human.NotificationSent)
	if !ok {
		return "notification=sent"
	}
	parts := []string{"notification=sent"}
	if notification.Level != "" {
		parts = append(parts, "level="+notification.Level)
	}
	if notification.Title != "" {
		parts = append(parts, "title="+quoteLogValue(notification.Title))
	}
	if notification.Message != "" {
		parts = append(parts, "message="+quoteLogValue(notification.Message))
	}
	return strings.Join(parts, " ")
}

func serveSubmissionDetail(submission clientapi.Submission) string {
	parts := []string{"submission=" + string(submission.Kind)}
	switch submission.Kind {
	case clientapi.SubmissionCommand:
		if submission.Command != nil {
			parts = append(parts, "command="+submission.Command.Path.String())
		} else if submission.CommandLine != "" {
			parts = append(parts, "command="+quoteLogValue(submission.CommandLine))
		}
	case clientapi.SubmissionOperation:
		if submission.Operation != nil {
			parts = append(parts, "operation="+submission.Operation.Operation.String())
		}
	case clientapi.SubmissionTrigger:
		if submission.Trigger != nil {
			parts = append(parts, "trigger="+submission.Trigger.Name)
		}
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func quoteLogValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	return fmt.Sprintf("%q", shortLogValue(value))
}

func shortLogValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	const limit = 120
	if len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
