// Package terminal renders runtime events and human prompts for terminal apps.
package terminal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	coreactivation "github.com/fluxplane/fluxplane-core/core/activation"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/skill"
	coretask "github.com/fluxplane/fluxplane-core/core/task"
	"github.com/fluxplane/fluxplane-core/core/testrun"
	"github.com/fluxplane/fluxplane-core/core/usage"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionagent"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

const (
	ansiReset  = "\x1b[0m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiDim    = "\x1b[2m"
)

// Renderer renders client events for humans.
type Renderer struct {
	Out       io.Writer
	Err       io.Writer
	ShowUsage bool
	Reasoning ReasoningDisplay

	mu     sync.Mutex
	starts map[operation.CallID]time.Time
	tasks  map[string]*terminalTaskView

	content *markdownLiveRenderer
	debug   *markdownLiveRenderer

	streamedContent bool
	reasoningOpen   bool
}

// NewRenderer returns a terminal event renderer.
func NewRenderer(out, err io.Writer, showUsage bool) *Renderer {
	return &Renderer{
		Out:       out,
		Err:       err,
		ShowUsage: showUsage,
		starts:    map[operation.CallID]time.Time{},
		tasks:     map[string]*terminalTaskView{},
		content:   newMarkdownRenderer(out),
		debug:     newMarkdownRenderer(err),
	}
}

// Render renders one event.
func (r *Renderer) Render(event clientapi.Event) {
	if r == nil {
		return
	}
	out := r.Err
	if out == nil {
		out = io.Discard
	}
	switch event.Kind {
	case clientapi.EventOperationRequested:
		r.flushContent()
		if event.Operation == nil {
			return
		}
		r.mu.Lock()
		r.starts[event.Operation.CallID] = time.Now()
		r.mu.Unlock()
		_, _ = fmt.Fprintf(out, "%s●%s %s\n", ansiCyan, ansiReset, event.Operation.Operation.String())
		if summary := operationStartSummary(*event.Operation); summary != "" {
			_, _ = fmt.Fprintf(out, "  ↳ %s\n", summary)
		}
	case clientapi.EventOperationCompleted:
		r.flushContent()
		if event.Operation == nil || event.Operation.Result == nil {
			return
		}
		duration := r.duration(event.Operation.CallID)
		status := event.Operation.Result.Status
		if status == "" {
			status = operation.StatusOK
		}
		marker := "✓"
		color := ansiGreen
		if event.Operation.Result.Error != nil || status != operation.StatusOK {
			marker = "✕"
			color = ansiRed
		}
		if event, ok := resultTestRunEvent(*event.Operation.Result); ok {
			_, _ = fmt.Fprintf(out, "  %s%s%s %s %sduration=%s%s\n", color, marker, ansiReset, firstLine(RenderTestRunEvent(event)), ansiDim, duration.Round(time.Millisecond), ansiReset)
			if details := testRunEventDetails(event); details != "" {
				_, _ = fmt.Fprintln(out, details)
			}
			return
		}
		if event.Operation.Operation.Name == "code_execute" {
			if rendered, ok := renderCodeExecuteResult(*event.Operation.Result, duration); ok {
				_, _ = fmt.Fprint(out, rendered)
				return
			}
		}
		_, _ = fmt.Fprintf(out, "  %s%s%s ", color, marker, ansiReset)
		if event.Operation.Result.Error != nil {
			_, _ = fmt.Fprint(out, failureSummary(status, event.Operation.Result.Error))
		} else if summary := resultSummary(*event.Operation.Result); summary != "" {
			_, _ = fmt.Fprint(out, summary)
		} else {
			_, _ = fmt.Fprintf(out, "status=%s", status)
		}
		details := operationResultMarkdown(*event.Operation.Result)
		_, _ = fmt.Fprintf(out, " %sduration=%s%s\n", ansiDim, duration.Round(time.Millisecond), ansiReset)
		if details != "" {
			_ = RenderMarkdown(out, details)
		}
	case clientapi.EventRuntimeEmitted:
		r.renderRuntime(out, event)
	}
}

// Finish flushes streaming markdown state.
func (r *Renderer) Finish() {
	r.flushContent()
	r.flushReasoning()
	if r.debug != nil {
		_ = r.debug.Flush()
		r.debug = newMarkdownRenderer(r.Err)
	}
}

// HasStreamedContent reports whether assistant content was rendered as deltas.
func (r *Renderer) HasStreamedContent() bool {
	if r == nil {
		return false
	}
	return r.streamedContent
}

// RenderDebug renders a client event as syntax-highlighted fenced JSON.
func (r *Renderer) RenderDebug(event clientapi.Event) {
	if r == nil || r.debug == nil {
		return
	}
	data, err := json.MarshalIndent(redactedDebugEvent(event), "", "  ")
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", event))
	}
	_, _ = r.debug.Write([]byte("```json\n"))
	_, _ = r.debug.Write(data)
	_, _ = r.debug.Write([]byte("\n```\n\n"))
	_ = r.debug.Flush()
	r.debug = newMarkdownRenderer(r.Err)
}

func (r *Renderer) duration(callID operation.CallID) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	start, ok := r.starts[callID]
	if !ok {
		return 0
	}
	delete(r.starts, callID)
	return time.Since(start)
}

func (r *Renderer) renderRuntime(out io.Writer, event clientapi.Event) {
	if event.Runtime == nil {
		return
	}
	if r.renderActivationRuntime(out, event.Runtime.Name, event.Runtime.Payload) {
		return
	}
	if r.renderTaskRuntime(out, string(event.Runtime.Name), event.Runtime.Payload) {
		return
	}
	switch payload := event.Runtime.Payload.(type) {
	case llmagent.ModelStreamed:
		r.renderModelStream(payload.Event)
	case system.ProcessEvent:
		r.flushContent()
		renderProcessEvent(out, payload)
	case policy.AuthorizationDecision:
		r.flushContent()
		renderAuthorizationDecision(out, payload)
	case operationruntime.ApprovalRequested:
		r.flushContent()
		renderApprovalRequested(out, payload)
	case operationruntime.ApprovalGranted:
		r.flushContent()
		renderApprovalGranted(out, payload)
	case operationruntime.ApprovalDenied:
		r.flushContent()
		renderApprovalDenied(out, payload)
	case usage.Recorded:
		r.flushContent()
		if r.ShowUsage {
			RenderUsageRequest(out, payload)
		}
	case sessionagent.Started:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%ssession agent start:%s %s %s[%s]%s\n", ansiCyan, ansiReset, payload.ID, ansiDim, payload.Profile.Name, ansiReset)
	case sessionagent.Completed:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%ssession agent done:%s %s %s\n", ansiGreen, ansiReset, payload.ID, compact(payload.Output, 160))
	case sessionagent.Failed:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%ssession agent failed:%s %s %s\n", ansiRed, ansiReset, payload.ID, payload.Error)
	case sessionrun.Started:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%s%s start:%s %s", ansiCyan, sessionRunLabel(payload.Causation), ansiReset, compact(payload.Input, 160))
		renderSessionRunScope(out, payload.Causation)
		_, _ = fmt.Fprintln(out)
	case sessionrun.Progressed:
		if !sessionRunProgressVisible(payload.Message) {
			return
		}
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%s%s progress:%s %s", ansiCyan, sessionRunLabel(payload.Causation), ansiReset, compact(payload.Message, 160))
		renderSessionRunScope(out, payload.Causation)
		_, _ = fmt.Fprintln(out)
	case sessionrun.Completed:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%s%s done:%s %s", ansiGreen, sessionRunLabel(payload.Causation), ansiReset, compact(payload.Output, 160))
		renderSessionRunScope(out, payload.Causation)
		_, _ = fmt.Fprintln(out)
	case sessionrun.Failed:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%s%s failed:%s %s", ansiRed, sessionRunLabel(payload.Causation), ansiReset, payload.Error)
		renderSessionRunScope(out, payload.Causation)
		_, _ = fmt.Fprintln(out)
	case sessionrun.Cancelled:
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%s%s cancelled:%s %s", ansiYellow, sessionRunLabel(payload.Causation), ansiReset, payload.Reason)
		renderSessionRunScope(out, payload.Causation)
		_, _ = fmt.Fprintln(out)
	default:
		r.flushContent()
		if string(event.Runtime.Name) == "human.clarification.requested" {
			return
		}
		if string(event.Runtime.Name) == "human.clarification.completed" {
			_, _ = fmt.Fprintf(out, "clarify answer: %s\n", field(payload, "Answer"))
		}
	}
}

func (r *Renderer) renderActivationRuntime(out io.Writer, name coreevent.Name, payload any) bool {
	switch name {
	case coreactivation.EventFocusDetected:
		var typed coreactivation.FocusDetected
		if decodeFlexiblePayload(payload, &typed) != nil {
			return false
		}
		r.flushContent()
		labels := append([]string(nil), typed.Intents...)
		for _, subject := range typed.Subjects {
			if subject.Name != "" {
				labels = append(labels, subject.Name)
			}
		}
		_, _ = fmt.Fprintf(out, "%sfocus:%s %s", ansiCyan, ansiReset, compact(firstNonEmptyString(typed.Objective, typed.Summary), 160))
		if len(labels) > 0 {
			_, _ = fmt.Fprintf(out, " %s[%s]%s", ansiDim, strings.Join(uniqueCompactStrings(labels, 8), ", "), ansiReset)
		}
		if typed.Source != "" || typed.Confidence > 0 {
			_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, sourceConfidenceSummary(typed.Source, typed.Confidence), ansiReset)
		}
		_, _ = fmt.Fprintln(out)
		return true
	case coreactivation.EventSurfacePrepareRequested:
		var typed coreactivation.SurfacePrepareRequested
		if decodeFlexiblePayload(payload, &typed) != nil {
			return false
		}
		r.flushContent()
		terms := append([]string(nil), typed.Terms...)
		terms = append(terms, typed.ActivationSets...)
		_, _ = fmt.Fprintf(out, "%ssurface requested:%s %s", ansiCyan, ansiReset, compact(firstNonEmptyString(strings.Join(terms, " "), typed.Objective), 160))
		if typed.Lifetime != "" || typed.Source != "" {
			_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, lifetimeSourceSummary(typed.Lifetime, typed.Source), ansiReset)
		}
		_, _ = fmt.Fprintln(out)
		return true
	case coreactivation.EventSurfaceResolved:
		var typed coreactivation.SurfaceResolved
		if decodeFlexiblePayload(payload, &typed) != nil {
			return false
		}
		r.flushContent()
		summary := firstNonEmptyString(strings.Join(typed.ActivationSets, ", "), resolvedResourceSummary(typed.Resources))
		if summary == "" && len(typed.UnmatchedTerms) > 0 {
			summary = "unmatched " + strings.Join(typed.UnmatchedTerms, ", ")
		}
		_, _ = fmt.Fprintf(out, "%ssurface resolved:%s %s\n", ansiCyan, ansiReset, compact(summary, 180))
		if skipped := activationDiagnosticsSummary(append(typed.Skipped, typed.Diagnostics...)); skipped != "" {
			_, _ = fmt.Fprintf(out, "  %sskipped:%s %s\n", ansiDim, ansiReset, skipped)
		}
		return true
	case coreactivation.EventSurfacePrepared:
		var typed coreactivation.SurfacePrepared
		if decodeFlexiblePayload(payload, &typed) != nil {
			return false
		}
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%ssurface prepared:%s %s", ansiGreen, ansiReset, compact(firstNonEmptyString(strings.Join(typed.ActivationSets, ", "), preparedCountsSummary(typed)), 180))
		if typed.Lifetime != "" || typed.Source != "" {
			_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, lifetimeSourceSummary(typed.Lifetime, typed.Source), ansiReset)
		}
		_, _ = fmt.Fprintln(out)
		renderActivationDetail(out, "operations", operationRefsToStrings(typed.Operations))
		renderActivationDetail(out, "operation sets", typed.OperationSets)
		renderActivationDetail(out, "context", contextRefsToStrings(typed.ContextProviders))
		renderActivationDetail(out, "datasources", datasourceRefsToStrings(typed.Datasources))
		renderActivationDetail(out, "skills", skillRefsToStrings(typed.Skills))
		renderActivationDetail(out, "references", referenceTargetsToStrings(typed.References))
		renderActivationDetail(out, "inline context", typed.InlineContexts)
		if skipped := activationDiagnosticsSummary(typed.Diagnostics); skipped != "" {
			_, _ = fmt.Fprintf(out, "  %sskipped:%s %s\n", ansiDim, ansiReset, skipped)
		}
		return true
	case coreactivation.EventSurfacePrepareSkipped:
		var typed coreactivation.SurfacePrepareSkipped
		if decodeFlexiblePayload(payload, &typed) != nil {
			return false
		}
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%ssurface skipped:%s %s", ansiYellow, ansiReset, compact(firstNonEmptyString(typed.ActivationSet, typed.Resource, typed.Term), 140))
		if reason := firstNonEmptyString(typed.Reason, typed.Diagnostic.Reason, typed.Diagnostic.Message); reason != "" {
			_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, compact(reason, 120), ansiReset)
		}
		_, _ = fmt.Fprintln(out)
		return true
	case coreactivation.EventSurfaceExpired:
		var typed coreactivation.SurfaceExpired
		if decodeFlexiblePayload(payload, &typed) != nil {
			return false
		}
		r.flushContent()
		_, _ = fmt.Fprintf(out, "%ssurface expired:%s %s", ansiYellow, ansiReset, compact(firstNonEmptyString(strings.Join(typed.ActivationSets, ", "), expiredCountsSummary(typed)), 180))
		if typed.Lifetime != "" || typed.Reason != "" {
			_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, lifetimeReasonSummary(typed.Lifetime, typed.Reason), ansiReset)
		}
		_, _ = fmt.Fprintln(out)
		return true
	default:
		return false
	}
}

func (r *Renderer) renderTaskRuntime(out io.Writer, name string, payload any) bool {
	switch name {
	case string(coretask.EventCreatedName):
		var typed coretask.Created
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.storeTask(typed.Task)
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventRevisedName):
		var typed coretask.Revised
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.storeTask(typed.Task)
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventStatusChangedName):
		var typed coretask.StatusChanged
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStatus(string(typed.TaskID), typed.Current)
			if typed.Reason != "" && typed.Current == coretask.StatusBlocked {
				r.updateTaskDetail(string(typed.TaskID), "", typed.Reason)
			}
			_, _ = fmt.Fprintf(out, "%stask status:%s %s %s", ansiCyan, ansiReset, typed.TaskID, typed.Current)
			if typed.Reason != "" {
				_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, compact(typed.Reason, 120), ansiReset)
			}
			_, _ = fmt.Fprintln(out)
			if typed.Current == coretask.StatusBlocked {
				r.renderTask(out, string(typed.TaskID))
			}
			return true
		}
	case string(coretask.EventExecutionStartedName):
		var typed coretask.ExecutionStarted
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStatus(string(typed.TaskID), coretask.StatusRunning)
			_, _ = fmt.Fprintf(out, "%stask execution:%s %s started %s%s%s\n", ansiCyan, ansiReset, typed.TaskID, ansiDim, typed.ExecutionID, ansiReset)
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventExecutionInterruptedName):
		var typed coretask.ExecutionInterrupted
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStatus(string(typed.TaskID), coretask.StatusBlocked)
			r.updateTaskDetail(string(typed.TaskID), "", typed.Reason)
			_, _ = fmt.Fprintf(out, "%stask blocked:%s %s", ansiYellow, ansiReset, typed.TaskID)
			if typed.Reason != "" {
				_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, compact(typed.Reason, 120), ansiReset)
			}
			_, _ = fmt.Fprintln(out)
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventStepDispatchedName):
		var typed coretask.StepDispatched
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStep(string(typed.TaskID), typed.StepID, firstNonEmptyString(typed.Title, string(typed.StepID)), coretask.StepStatusRunning, "", typed.Profile, typed.Assignee)
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventStepStatusChangedName):
		var typed coretask.StepStatusChanged
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStep(string(typed.TaskID), typed.StepID, string(typed.StepID), typed.Current, typed.Reason, "", "")
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventStepProgressedName):
		var typed coretask.StepProgressed
		if decodeTypedPayload(payload, &typed) == nil {
			if ignoreRuntimeProgress(typed.Message) {
				return true
			}
			r.flushContent()
			r.updateTaskStep(string(typed.TaskID), typed.StepID, string(typed.StepID), "", typed.Message, "", "")
			_, _ = fmt.Fprintf(out, "%stask progress:%s %s/%s %s%s%s\n", ansiCyan, ansiReset, typed.TaskID, typed.StepID, ansiDim, typed.Message, ansiReset)
			return true
		}
	case string(coretask.EventStepCompletedName):
		var typed coretask.StepCompleted
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStep(string(typed.TaskID), typed.StepID, string(typed.StepID), coretask.StepStatusCompleted, "", "", "")
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventStepFailedName):
		var typed coretask.StepFailed
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStep(string(typed.TaskID), typed.StepID, string(typed.StepID), coretask.StepStatusFailed, operationErrorText(typed.Error), "", "")
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventStepCancelledName):
		var typed coretask.StepCancelled
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStep(string(typed.TaskID), typed.StepID, string(typed.StepID), coretask.StepStatusCancelled, typed.Reason, "", "")
			r.renderTask(out, string(typed.TaskID))
			return true
		}
	case string(coretask.EventArtifactAddedName):
		var typed coretask.ArtifactAdded
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			_, _ = fmt.Fprintf(out, "%stask artifact:%s %s %s\n", ansiCyan, ansiReset, taskArtifactScope(typed), artifactSummary(typed.Artifact))
			return true
		}
	case string(coretask.EventExecutionCompletedName):
		var typed coretask.ExecutionCompleted
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStatus(string(typed.TaskID), coretask.StatusCompleted)
			_, _ = fmt.Fprintf(out, "%stask completed:%s %s\n", ansiGreen, ansiReset, typed.TaskID)
			return true
		}
	case string(coretask.EventExecutionFailedName):
		var typed coretask.ExecutionFailed
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStatus(string(typed.TaskID), coretask.StatusFailed)
			_, _ = fmt.Fprintf(out, "%stask failed:%s %s %s\n", ansiRed, ansiReset, typed.TaskID, operationErrorText(typed.Error))
			return true
		}
	case string(coretask.EventExecutionCancelledName):
		var typed coretask.ExecutionCancelled
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			r.updateTaskStatus(string(typed.TaskID), coretask.StatusCancelled)
			_, _ = fmt.Fprintf(out, "%stask cancelled:%s %s %s%s%s\n", ansiYellow, ansiReset, typed.TaskID, ansiDim, compact(typed.Reason, 120), ansiReset)
			return true
		}
	case string(coretask.EventSchedulerDiagnosticName):
		var typed coretask.SchedulerDiagnostic
		if decodeTypedPayload(payload, &typed) == nil {
			r.flushContent()
			if typed.Diagnostic.Code == "task_finalizing_outputs" {
				r.updateTaskDetail(string(typed.TaskID), "finalizing", typed.Diagnostic.Message)
				_, _ = fmt.Fprintf(out, "%stask finalizing:%s %s %s%s%s\n", ansiCyan, ansiReset, typed.TaskID, ansiDim, compact(typed.Diagnostic.Message, 160), ansiReset)
				r.renderTask(out, string(typed.TaskID))
				return true
			}
			if typed.Diagnostic.Code == "task_auto_schedule_deferred" {
				r.updateTaskDetail(string(typed.TaskID), "queued", typed.Diagnostic.Message)
				r.renderTask(out, string(typed.TaskID))
			}
			if typed.Diagnostic.Code == "task_auto_schedule_disabled" {
				r.updateTaskDetail(string(typed.TaskID), "waiting", typed.Diagnostic.Message)
				r.renderTask(out, string(typed.TaskID))
			}
			target := firstNonEmptyString(string(typed.StepID), string(typed.ExecutionID), string(typed.TaskID))
			_, _ = fmt.Fprintf(out, "%stask scheduler:%s %s %s%s%s\n", ansiYellow, ansiReset, target, ansiDim, compact(typed.Diagnostic.Message, 160), ansiReset)
			return true
		}
	}
	return false
}

type terminalStepStatus string

const (
	terminalStepWaiting   terminalStepStatus = "waiting"
	terminalStepRunning   terminalStepStatus = "running"
	terminalStepCompleted terminalStepStatus = "completed"
	terminalStepFailed    terminalStepStatus = "failed"
	terminalStepCancelled terminalStepStatus = "cancelled"
)

type terminalTaskView struct {
	ID     string
	Title  string
	Status coretask.Status
	Phase  string
	Detail string
	Steps  []terminalStepView
}

type terminalStepView struct {
	ID      string
	Title   string
	Profile string
	Status  terminalStepStatus
	Detail  string
}

func (r *Renderer) storeTask(task coretask.Task) {
	if task.ID == "" {
		task.ID = "task"
	}
	view := &terminalTaskView{
		ID:     string(task.ID),
		Title:  firstNonEmptyString(task.Title, task.Objective, string(task.ID)),
		Status: task.Status,
		Steps:  make([]terminalStepView, 0, len(task.Steps)),
	}
	existing := r.taskSnapshot(string(task.ID))
	existingSteps := map[string]terminalStepView{}
	if existing != nil {
		view.Phase = existing.Phase
		view.Detail = existing.Detail
		for _, step := range existing.Steps {
			existingSteps[step.ID] = step
		}
	}
	for _, step := range task.Steps {
		stepView := terminalStepView{
			ID:      string(step.ID),
			Title:   firstNonEmptyString(step.Title, step.Objective, step.Description, string(step.ID)),
			Profile: firstNonEmptyString(step.Profile, string(step.Assignee), "worker"),
			Status:  terminalStepWaiting,
		}
		if existing, ok := existingSteps[string(step.ID)]; ok {
			stepView.Status = existing.Status
			stepView.Detail = existing.Detail
			if existing.Profile != "" {
				stepView.Profile = existing.Profile
			}
		}
		view.Steps = append(view.Steps, stepView)
	}
	r.mu.Lock()
	if r.tasks == nil {
		r.tasks = map[string]*terminalTaskView{}
	}
	r.tasks[string(task.ID)] = view
	r.mu.Unlock()
}

func (r *Renderer) updateTaskStatus(taskID string, status coretask.Status) {
	if taskID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	view := r.taskLocked(taskID)
	if view == nil {
		if r.tasks == nil {
			r.tasks = map[string]*terminalTaskView{}
		}
		view = &terminalTaskView{ID: taskID, Title: taskID}
		r.tasks[taskID] = view
	}
	if status != "" {
		view.Status = status
		switch status {
		case coretask.StatusCompleted, coretask.StatusFailed, coretask.StatusCancelled:
			view.Phase = ""
			view.Detail = ""
		case coretask.StatusReady, coretask.StatusRunning:
			if view.Phase == "queued" || view.Phase == "waiting" {
				view.Phase = ""
				view.Detail = ""
			}
		}
	}
}

func (r *Renderer) updateTaskDetail(taskID, phase, detail string) {
	if taskID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	view := r.taskLocked(taskID)
	if view == nil {
		if r.tasks == nil {
			r.tasks = map[string]*terminalTaskView{}
		}
		view = &terminalTaskView{ID: taskID, Title: taskID}
		r.tasks[taskID] = view
	}
	if phase != "" {
		view.Phase = phase
	}
	if detail != "" {
		view.Detail = detail
	}
}

func (r *Renderer) updateTaskStep(taskID string, stepID coretask.StepID, title string, status coretask.StepStatus, detail, profile string, assignee coretask.Role) {
	if taskID == "" || stepID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	view := r.taskLocked(taskID)
	if view == nil {
		if r.tasks == nil {
			r.tasks = map[string]*terminalTaskView{}
		}
		view = &terminalTaskView{ID: taskID, Title: taskID}
		r.tasks[taskID] = view
	}
	for i := range view.Steps {
		if view.Steps[i].ID != string(stepID) {
			continue
		}
		if title != "" && view.Steps[i].Title == view.Steps[i].ID {
			view.Steps[i].Title = title
		}
		if mapped := terminalTaskStepStatus(status); mapped != "" {
			view.Steps[i].Status = mapped
		}
		view.Steps[i].Detail = detail
		if profile != "" {
			view.Steps[i].Profile = profile
		} else if assignee != "" {
			view.Steps[i].Profile = string(assignee)
		}
		return
	}
	view.Steps = append(view.Steps, terminalStepView{
		ID:      string(stepID),
		Title:   firstNonEmptyString(title, string(stepID)),
		Profile: firstNonEmptyString(profile, string(assignee), "worker"),
		Status:  terminalTaskStepStatus(status),
		Detail:  detail,
	})
	if view.Steps[len(view.Steps)-1].Status == "" {
		view.Steps[len(view.Steps)-1].Status = terminalStepWaiting
	}
}

func (r *Renderer) renderTask(out io.Writer, taskID string) {
	view := r.taskSnapshot(taskID)
	if view == nil {
		return
	}
	status := string(view.Status)
	if status == "" {
		status = string(coretask.StatusDraft)
	}
	labels := []string{status}
	if view.Phase != "" {
		labels = append(labels, view.Phase)
	}
	_, _ = fmt.Fprintf(out, "\n%stask:%s %s %s[%s", ansiCyan, ansiReset, view.Title, ansiDim, strings.Join(labels, ", "))
	if len(view.Steps) > 0 {
		_, _ = fmt.Fprintf(out, ", %d steps", len(view.Steps))
	}
	_, _ = fmt.Fprintf(out, "]%s\n", ansiReset)
	if view.Detail != "" {
		_, _ = fmt.Fprintf(out, "  ! %s%s%s\n", ansiDim, compact(view.Detail, 160), ansiReset)
	}
	for _, step := range view.Steps {
		_, _ = fmt.Fprintf(out, "  %s %s %s[%s]%s", terminalStepMarker(step.Status), step.Title, ansiDim, firstNonEmptyString(step.Profile, "worker"), ansiReset)
		if step.Detail != "" && step.Status != terminalStepCompleted {
			_, _ = fmt.Fprintf(out, " %s%s%s", ansiDim, compact(step.Detail, 120), ansiReset)
		}
		_, _ = fmt.Fprintln(out)
	}
}

func (r *Renderer) taskSnapshot(taskID string) *terminalTaskView {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneTerminalTaskView(r.taskLocked(taskID))
}

func (r *Renderer) taskLocked(taskID string) *terminalTaskView {
	if len(r.tasks) == 0 {
		return nil
	}
	if taskID != "" {
		return r.tasks[taskID]
	}
	for _, view := range r.tasks {
		return view
	}
	return nil
}

func cloneTerminalTaskView(in *terminalTaskView) *terminalTaskView {
	if in == nil {
		return nil
	}
	out := *in
	out.Steps = append([]terminalStepView(nil), in.Steps...)
	return &out
}

func terminalStepMarker(status terminalStepStatus) string {
	switch status {
	case terminalStepRunning:
		return ansiCyan + "●" + ansiReset
	case terminalStepCompleted:
		return ansiGreen + "●" + ansiReset
	case terminalStepFailed:
		return ansiRed + "✕" + ansiReset
	case terminalStepCancelled:
		return ansiYellow + "–" + ansiReset
	default:
		return ansiDim + "◌" + ansiReset
	}
}

func terminalTaskStepStatus(status coretask.StepStatus) terminalStepStatus {
	switch status {
	case coretask.StepStatusRunning:
		return terminalStepRunning
	case coretask.StepStatusCompleted:
		return terminalStepCompleted
	case coretask.StepStatusFailed:
		return terminalStepFailed
	case coretask.StepStatusCancelled, coretask.StepStatusSkipped, coretask.StepStatusBlocked:
		return terminalStepCancelled
	case coretask.StepStatusWaiting:
		return terminalStepWaiting
	default:
		return ""
	}
}

func taskArtifactScope(event coretask.ArtifactAdded) string {
	if event.StepID != "" {
		return fmt.Sprintf("%s/%s", event.TaskID, event.StepID)
	}
	if event.ExecutionID != "" {
		return fmt.Sprintf("%s/%s", event.TaskID, event.ExecutionID)
	}
	return string(event.TaskID)
}

func artifactSummary(artifact coretask.ArtifactSpec) string {
	label := firstNonEmptyString(artifact.ID, artifact.Name, artifact.Description, string(artifact.Kind), "artifact")
	kind := string(artifact.Kind)
	if kind == "" {
		kind = "artifact"
	}
	suffix := ""
	if artifact.Required {
		suffix = " required"
	}
	return fmt.Sprintf("%s [%s]%s", label, kind, suffix)
}

func operationErrorText(err *operation.Error) string {
	if err == nil {
		return ""
	}
	if err.Message != "" && err.Code != "" {
		return err.Code + ": " + err.Message
	}
	if err.Message != "" {
		return err.Message
	}
	return err.Code
}

func decodeTypedPayload(payload any, out any) error {
	switch payload.(type) {
	case json.RawMessage, map[string]any, []byte:
		return fmt.Errorf("terminalui: untyped runtime payload")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func decodeFlexiblePayload(payload any, out any) error {
	switch typed := payload.(type) {
	case json.RawMessage:
		return json.Unmarshal(typed, out)
	case []byte:
		return json.Unmarshal(typed, out)
	default:
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	}
}

func sourceConfidenceSummary(source coreactivation.Source, confidence float64) string {
	parts := []string{}
	if source != "" {
		parts = append(parts, "source="+string(source))
	}
	if confidence > 0 {
		parts = append(parts, fmt.Sprintf("confidence=%.2f", confidence))
	}
	return strings.Join(parts, " ")
}

func lifetimeSourceSummary(lifetime coreactivation.Lifetime, source coreactivation.Source) string {
	parts := []string{}
	if lifetime != "" {
		parts = append(parts, "duration="+string(lifetime))
	}
	if source != "" {
		parts = append(parts, "source="+string(source))
	}
	return strings.Join(parts, " ")
}

func lifetimeReasonSummary(lifetime coreactivation.Lifetime, reason string) string {
	parts := []string{}
	if lifetime != "" {
		parts = append(parts, "duration="+string(lifetime))
	}
	if reason != "" {
		parts = append(parts, "reason="+reason)
	}
	return strings.Join(parts, " ")
}

func preparedCountsSummary(event coreactivation.SurfacePrepared) string {
	count := len(event.Operations) + len(event.OperationSets) + len(event.ContextProviders) + len(event.Datasources) + len(event.Skills) + len(event.InlineContexts)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%d resources", count)
}

func expiredCountsSummary(event coreactivation.SurfaceExpired) string {
	count := len(event.Operations) + len(event.OperationSets) + len(event.ContextProviders) + len(event.Datasources) + len(event.Skills) + len(event.InlineContexts)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%d resources", count)
}

func renderActivationDetail(out io.Writer, label string, values []string) {
	values = uniqueCompactStrings(values, 8)
	if len(values) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "  %s: %s\n", label, strings.Join(values, ", "))
}

func operationRefsToStrings(refs []operation.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if value := ref.String(); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func contextRefsToStrings(refs []corecontext.ProviderRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" {
			out = append(out, string(ref.Name))
		}
	}
	return out
}

func datasourceRefsToStrings(refs []coredatasource.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" {
			out = append(out, string(ref.Name))
		}
	}
	return out
}

func skillRefsToStrings(refs []skill.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" {
			out = append(out, string(ref.Name))
		}
	}
	return out
}

func referenceTargetsToStrings(refs []coreactivation.ReferenceTarget) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Skill.Name != "" && ref.Path != "" {
			out = append(out, string(ref.Skill.Name)+":"+ref.Path)
		}
	}
	return out
}

func resolvedResourceSummary(resources []coreactivation.ResolvedResource) string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		value := firstNonEmptyString(resource.Alias, resource.Name, resource.Address)
		if value != "" {
			out = append(out, string(resource.Kind)+":"+value)
		}
	}
	return strings.Join(uniqueCompactStrings(out, 8), ", ")
}

func activationDiagnosticsSummary(diagnostics []coreactivation.Diagnostic) string {
	out := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		value := firstNonEmptyString(diagnostic.Message, diagnostic.Reason, diagnostic.Target, diagnostic.Term)
		if value != "" {
			out = append(out, compact(value, 80))
		}
	}
	return strings.Join(uniqueCompactStrings(out, 4), "; ")
}

func uniqueCompactStrings(values []string, limit int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		remaining := len(out) - limit
		out = out[:limit]
		out = append(out, fmt.Sprintf("+%d more", remaining))
	}
	return out
}

func (r *Renderer) renderModelStream(event llmagent.StreamEvent) {
	switch event.Kind {
	case llmagent.StreamThinkingDelta:
		r.writeReasoningDelta(event)
	case llmagent.StreamContentDelta:
		if event.Text == "" {
			return
		}
		r.writeContentDelta(event.Text)
	case llmagent.StreamToolCallDelta:
		if event.Tool != "" && event.Final {
			r.flushContent()
			_, _ = fmt.Fprintf(r.Err, "%stool call:%s %s\n", ansiYellow, ansiReset, event.Tool)
		}
	}
}

func (r *Renderer) flushContent() {
	if r == nil {
		return
	}
	r.flushReasoning()
	r.flushMarkdownContent()
}

func (r *Renderer) flushMarkdownContent() {
	if r == nil {
		return
	}
	if r.content != nil {
		_ = r.content.Flush()
		r.content = newMarkdownRenderer(r.out())
	}
}

func (r *Renderer) writeContentDelta(text string) {
	r.flushReasoning()
	if r.content == nil {
		r.content = newMarkdownRenderer(r.out())
	}
	if _, err := r.content.Write([]byte(text)); err == nil {
		r.streamedContent = true
	}
}

func (r *Renderer) writeReasoningDelta(event llmagent.StreamEvent) {
	if r == nil || r.Reasoning == "" || r.Reasoning == ReasoningDisplayOff || event.Text == "" {
		return
	}
	r.flushMarkdownContent()
	out := r.Err
	if out == nil {
		out = io.Discard
	}
	if !r.reasoningOpen {
		label := "◇ reasoning"
		if r.Reasoning == ReasoningDisplayRaw {
			label = "◇ raw reasoning"
		}
		_, _ = fmt.Fprintf(out, "%s%s%s\n", ansiDim, label, ansiReset)
		r.reasoningOpen = true
	}
	_, _ = fmt.Fprintf(out, "%s%s%s", ansiDim, event.Text, ansiReset)
	if event.Final {
		r.flushReasoning()
	}
}

func (r *Renderer) flushReasoning() {
	if r == nil || !r.reasoningOpen {
		return
	}
	out := r.Err
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintln(out)
	r.reasoningOpen = false
}

func (r *Renderer) out() io.Writer {
	if r == nil || r.Out == nil {
		return io.Discard
	}
	return r.Out
}

func redactedDebugEvent(event clientapi.Event) clientapi.Event {
	if event.Runtime == nil || event.Runtime.Name != llmagent.EventModelStreamedName {
		return event
	}
	out := event
	runtimeEvent := *event.Runtime
	out.Runtime = &runtimeEvent
	if payload, ok := event.Runtime.Payload.(llmagent.ModelStreamed); ok {
		if payload.Event.Kind == llmagent.StreamThinkingDelta && payload.Event.Text != "" {
			payload.Event.Redaction = fmt.Sprintf("thinking_delta:%d_bytes", len(payload.Event.Text))
			payload.Event.Text = ""
			runtimeEvent.Payload = payload
		}
	}
	return out
}

func field(value any, name string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ""
	}
	if found, ok := decoded[strings.ToLower(name)]; ok {
		return compact(found, 320)
	}
	if found, ok := decoded[name]; ok {
		return compact(found, 320)
	}
	return ""
}

func renderProcessEvent(out io.Writer, event system.ProcessEvent) {
	switch event.Kind {
	case "started":
		_, _ = fmt.Fprintf(out, "process start: %s\n", event.ProcessID)
	case "exited":
		_, _ = fmt.Fprintf(out, "process exit: %s code=%s\n", event.ProcessID, strings.TrimSpace(event.Data))
	case "output":
		for _, line := range strings.SplitAfter(event.Data, "\n") {
			if line == "" {
				continue
			}
			_, _ = fmt.Fprintf(out, "%s", line)
			if !strings.HasSuffix(line, "\n") {
				_, _ = fmt.Fprintln(out)
			}
		}
	}
}

func renderAuthorizationDecision(out io.Writer, decision policy.AuthorizationDecision) {
	color := ansiYellow
	switch decision.Decision {
	case policy.DecisionDeny:
		color = ansiRed
	case policy.DecisionAllow:
		color = ansiDim
	}
	_, _ = fmt.Fprintf(out, "%sauthz:%s %s %s %s", color, ansiReset, decision.Decision, decision.Action, authorizationResourceLabel(decision.Resource))
	if subject := authorizationSubjectLabel(decision.Subjects); subject != "" {
		_, _ = fmt.Fprintf(out, " %ssubject=%s%s", ansiDim, subject, ansiReset)
	}
	if decision.Trust != "" {
		_, _ = fmt.Fprintf(out, " %strust=%s%s", ansiDim, decision.Trust, ansiReset)
	}
	if decision.Reason != "" {
		_, _ = fmt.Fprintf(out, " %sreason=%s%s", ansiDim, decision.Reason, ansiReset)
	}
	_, _ = fmt.Fprintln(out)
}

func renderApprovalRequested(out io.Writer, event operationruntime.ApprovalRequested) {
	_, _ = fmt.Fprintf(out, "%sapproval requested:%s %s", ansiYellow, ansiReset, event.Operation.String())
	renderApprovalTail(out, event.Resource, event.Action, event.Risk, event.Reason, "")
}

func renderApprovalGranted(out io.Writer, event operationruntime.ApprovalGranted) {
	_, _ = fmt.Fprintf(out, "%sapproval granted:%s %s", ansiGreen, ansiReset, event.Operation.String())
	renderApprovalTail(out, event.Resource, event.Action, event.Risk, event.Reason, "")
}

func renderApprovalDenied(out io.Writer, event operationruntime.ApprovalDenied) {
	_, _ = fmt.Fprintf(out, "%sapproval denied:%s %s", ansiRed, ansiReset, event.Operation.String())
	renderApprovalTail(out, event.Resource, event.Action, event.Risk, event.Reason, event.Error)
}

func renderApprovalTail(out io.Writer, resource policy.ResourceRef, action policy.Action, risk operationruntime.CommandRisk, reason, errText string) {
	if resource.Kind != "" {
		_, _ = fmt.Fprintf(out, " %sresource=%s%s", ansiDim, authorizationResourceLabel(resource), ansiReset)
	}
	if action != "" {
		_, _ = fmt.Fprintf(out, " %saction=%s%s", ansiDim, action, ansiReset)
	}
	if risk.Level != "" {
		_, _ = fmt.Fprintf(out, " %srisk=%s%s", ansiDim, risk.Level, ansiReset)
	}
	if strings.TrimSpace(firstNonEmptyString(reason, risk.Reason)) != "" {
		_, _ = fmt.Fprintf(out, " %sreason=%s%s", ansiDim, compact(firstNonEmptyString(reason, risk.Reason), 140), ansiReset)
	}
	if errText != "" {
		_, _ = fmt.Fprintf(out, " %serror=%s%s", ansiDim, compact(errText, 140), ansiReset)
	}
	_, _ = fmt.Fprintln(out)
}

func authorizationResourceLabel(resource policy.ResourceRef) string {
	switch {
	case resource.Name != "":
		return string(resource.Kind) + ":" + resource.Name
	case resource.Path != "":
		return string(resource.Kind) + ":" + resource.Path
	case resource.ID != "":
		return string(resource.Kind) + ":" + resource.ID
	default:
		return string(resource.Kind)
	}
}

func authorizationSubjectLabel(subjects []policy.SubjectRef) string {
	if len(subjects) == 0 {
		return ""
	}
	labels := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		if subject.ID == "" {
			continue
		}
		labels = append(labels, string(subject.Kind)+":"+subject.ID)
	}
	return strings.Join(labels, ",")
}

func resultSummary(result operation.Result) string {
	switch value := result.Output.(type) {
	case operation.Rendered:
		if summary := dataSummary(value.Data); summary != "" && len(value.ModelText()) > 240 {
			return summary
		}
		if text := strings.TrimSpace(value.ModelText()); text != "" {
			return compact(text, 240)
		}
		if summary := dataSummary(value.Data); summary != "" {
			return summary
		}
		return paramSummary(value.Data, 240)
	case map[string]any:
		if text, ok := value["text"].(string); ok && text != "" {
			return compact(text, 240)
		}
		if summary := dataSummary(value); summary != "" {
			return summary
		}
		return paramSummary(value, 240)
	default:
		return compact(value, 240)
	}
}

func operationResultMarkdown(result operation.Result) string {
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		return ""
	}
	data, ok := valueAsMap(rendered.Data)
	if !ok {
		return ""
	}
	if diff, ok := data["diff"].(string); ok {
		return fencedCodeBlock("diff", diff)
	}
	diffs := terminalStringSlice(data["atomic_diffs"])
	if len(diffs) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(diffs))
	for _, diff := range diffs {
		if block := fencedCodeBlock("diff", diff); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n")
}

func resultTestRunEvent(result operation.Result) (testrun.Event, bool) {
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		return testrun.Event{}, false
	}
	data, ok := valueAsMap(rendered.Data)
	if !ok {
		return testrun.Event{}, false
	}
	value, ok := testRunEventValue(data)
	if !ok {
		return testrun.Event{}, false
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return testrun.Event{}, false
	}
	var event testrun.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		return testrun.Event{}, false
	}
	if event.Kind == "" && event.Status == "" && event.Target == "" && event.Command == "" {
		return testrun.Event{}, false
	}
	return event, true
}

func testRunEventValue(data map[string]any) (any, bool) {
	if value, ok := data["test_run_event"]; ok && !emptySummaryValue(value) {
		return value, true
	}
	test, ok := data["test"]
	if !ok {
		return nil, false
	}
	testData, ok := valueAsMap(test)
	if !ok {
		return nil, false
	}
	value, ok := testData["test_run_event"]
	if !ok || emptySummaryValue(value) {
		return nil, false
	}
	return value, true
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return text
}

func testRunEventDetails(event testrun.Event) string {
	var lines []string
	if len(event.Failures) > 0 {
		failure := event.Failures[0]
		if failure.Test != "" {
			lines = append(lines, "  "+failure.Test)
		}
		if line := renderTestRunFailureLine(failure); line != "" {
			lines = append(lines, "  "+line)
		}
	}
	if summary := renderTestRunSummary(event); summary != "" {
		lines = append(lines, "  "+summary)
	}
	return strings.Join(lines, "\n")
}

func terminalStringSlice(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func failureSummary(status operation.Status, err *operation.Error) string {
	if err == nil {
		return string(status)
	}
	parts := []string{}
	if status != "" {
		parts = append(parts, string(status))
	}
	if err.Code != "" && err.Code != string(status) {
		parts = append(parts, "code="+terminalValue(err.Code))
	}
	if err.Message != "" {
		parts = append(parts, "reason="+terminalValue(err.Message))
	}
	if details := paramSummary(err.Details, 120); details != "" {
		parts = append(parts, details)
	}
	return strings.Join(parts, " ")
}

func dataSummary(value any) string {
	data, ok := valueAsMap(value)
	if !ok || len(data) == 0 {
		return ""
	}
	keys := []string{"path", "bytes", "asset", "url", "process_id", "status", "status_code", "truncated"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		found, ok := data[key]
		if !ok || emptySummaryValue(found) {
			continue
		}
		parts = append(parts, key+"="+terminalValue(found))
	}
	if len(parts) == 0 {
		return ""
	}
	return compact(strings.Join(parts, " "), 240)
}

func operationStartSummary(event clientapi.OperationEvent) string {
	return paramSummary(event.Input, 320)
}

func ignoreRuntimeProgress(message string) bool {
	switch strings.TrimSpace(message) {
	case "llmagent.model_streamed",
		"llmagent.model_completed",
		"usage.recorded",
		"conversation.items.appended",
		"conversation.continuation.stored":
		return true
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func paramSummary(value any, limit int) string {
	if emptySummaryValue(value) {
		return ""
	}
	if data, ok := valueAsMap(value); ok {
		keys := make([]string, 0, len(data))
		for key, item := range data {
			if strings.TrimSpace(key) == "" || emptySummaryValue(item) {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+"="+terminalValue(data[key]))
		}
		return compact(strings.Join(parts, " "), limit)
	}
	return compact(terminalValue(value), limit)
}

func valueAsMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return typed, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func terminalValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return quoteTerminalString(typed)
	case json.RawMessage:
		return compactJSON(typed)
	case []byte:
		return quoteTerminalString(string(typed))
	case fmt.Stringer:
		return quoteTerminalString(typed.String())
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return quoteTerminalString(fmt.Sprint(typed))
		}
		return compactJSON(data)
	}
}

func quoteTerminalString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\r\"'=") {
		data, err := json.Marshal(value)
		if err == nil {
			return string(data)
		}
	}
	return value
}

func compactJSON(data []byte) string {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return quoteTerminalString(string(data))
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return quoteTerminalString(string(data))
	}
	return string(encoded)
}

func emptySummaryValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []byte:
		return len(typed) == 0
	case json.RawMessage:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func sessionRunLabel(c sessionrun.Causation) string {
	iteration := strings.TrimSpace(c.Metadata["loop_iteration"])
	count := strings.TrimSpace(c.Metadata["loop_count"])
	if iteration != "" && count != "" {
		return "loop " + iteration + "/" + count
	}
	if iteration != "" {
		return "loop " + iteration
	}
	if c.ID != "" {
		return "session run " + string(c.ID)
	}
	return "session run"
}

func renderSessionRunScope(out io.Writer, c sessionrun.Causation) {
	parts := []string{}
	if c.Profile.Name != "" {
		parts = append(parts, "session="+string(c.Profile.Name))
	}
	if c.ChildThreadID != "" {
		parts = append(parts, "thread="+string(c.ChildThreadID))
	}
	if len(parts) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, " %s[%s]%s", ansiDim, strings.Join(parts, " "), ansiReset)
}

func sessionRunProgressVisible(message string) bool {
	message = strings.TrimSpace(message)
	return strings.HasPrefix(message, "calling ") || strings.HasPrefix(message, "completed ")
}

func compact(value any, limit int) string {
	if value == nil {
		return ""
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			text = fmt.Sprint(typed)
		} else {
			text = string(data)
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return text
}

// RenderUsageRequest renders a compact single-line context summary for one
// LLM request. It is printed inline after each model response when --usage is
// active, giving immediate feedback on the input context size submitted to the
// provider for that specific request. Non-LLM subjects are silently skipped.
func RenderUsageRequest(w io.Writer, recorded usage.Recorded) {
	if w == nil || recorded.Subject.Kind != usage.SubjectLLM || recorded.Empty() {
		return
	}
	var inputTok, cachedTok, cacheWriteTok, outputTok float64
	for _, m := range recorded.Measurements {
		switch m.Metric {
		case usage.MetricLLMInputTokens:
			inputTok += m.Quantity
		case usage.MetricLLMCachedTokens:
			cachedTok += m.Quantity
		case usage.MetricLLMCacheWriteTokens:
			cacheWriteTok += m.Quantity
		case usage.MetricLLMOutputTokens:
			outputTok += m.Quantity
		}
	}
	contextTok := inputTok + cachedTok + cacheWriteTok
	if contextTok == 0 && outputTok == 0 {
		return
	}
	label := subjectLabel(recorded.Subject)
	parts := []string{}
	if contextTok > 0 {
		parts = append(parts, "↑ ctx "+formatHumanNumber(contextTok))
	}
	if cachedTok > 0 {
		parts = append(parts, "↻ cached "+formatHumanNumber(cachedTok))
	}
	if cacheWriteTok > 0 {
		parts = append(parts, "↥ cache write "+formatHumanNumber(cacheWriteTok))
	}
	if outputTok > 0 {
		parts = append(parts, "↓ out "+formatHumanNumber(outputTok))
	}
	_, _ = fmt.Fprintf(w, "%s🧠 %s%s  %s%s\n", ansiDim, label, ansiReset, strings.Join(parts, "  "), ansiReset)
}

// RenderUsageSnapshot renders grouped usage totals.
func RenderUsageSnapshot(w io.Writer, snapshot usage.Snapshot) {
	if w == nil || snapshot.Empty() {
		return
	}
	_, _ = fmt.Fprintf(w, "%sTotal usage%s\n", ansiCyan, ansiReset)
	for _, subject := range snapshot.Subjects {
		if len(subject.Totals) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s %s%s%s\n", subjectIcon(subject.Subject.Kind), ansiCyan, subjectLabel(subject.Subject), ansiReset)
		for _, measurement := range subject.Totals {
			if line := measurementLine(subject.Subject, subject.Totals, measurement); line != "" {
				_, _ = fmt.Fprintf(w, "  %s\n", line)
			}
		}
	}
}

func subjectIcon(kind usage.SubjectKind) string {
	switch kind {
	case usage.SubjectLLM:
		return "🧠"
	case usage.SubjectNetwork:
		return "🌐"
	case usage.SubjectFile:
		return "📄"
	case usage.SubjectProcess:
		return "⚙"
	case usage.SubjectMoney:
		return "💵"
	default:
		return "•"
	}
}

func subjectLabel(subject usage.Subject) string {
	if subject.Provider != "" && subject.Name != "" {
		return subject.Provider + "/" + subject.Name
	}
	if subject.Name != "" {
		return subject.Name
	}
	if subject.Provider != "" {
		return subject.Provider
	}
	if subject.Kind != "" {
		return string(subject.Kind)
	}
	return "usage"
}

func measurementLine(subject usage.Subject, totals []usage.Measurement, measurement usage.Measurement) string {
	switch measurement.Metric {
	case usage.MetricLLMInputTokens:
		return "↑ input tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMCachedTokens:
		line := "↻ cached input tokens " + formatHumanNumber(measurement.Quantity)
		if rate, ok := cacheRate(subject, totals, measurement); ok {
			line += " | cached " + formatPercent(rate)
		}
		return line
	case usage.MetricLLMCacheWriteTokens:
		return "↥ cache write tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMOutputTokens:
		return "↓ output tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMReasoningTokens:
		return "✦ reasoning tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricLLMTotalTokens:
		return "∑ total tokens " + formatHumanNumber(measurement.Quantity)
	case usage.MetricNetworkBytes:
		return networkBytesLine(measurement)
	case usage.MetricFileBytes:
		return "↔ file bytes " + formatBytes(measurement.Quantity)
	case usage.MetricRequests:
		return "• requests " + formatHumanNumber(measurement.Quantity)
	case usage.MetricWallTime:
		return "◷ wall time " + formatDurationQuantity(measurement.Quantity, measurement.Unit)
	case usage.MetricCost:
		return "💵 estimated cost " + formatCost(measurement)
	default:
		return string(measurement.Metric) + " " + formatHumanQuantity(measurement)
	}
}

func cacheRate(subject usage.Subject, totals []usage.Measurement, cached usage.Measurement) (float64, bool) {
	if subject.Kind != usage.SubjectLLM || cached.Quantity <= 0 {
		return 0, false
	}
	var input float64
	for _, measurement := range totals {
		switch measurement.Metric {
		case usage.MetricLLMInputTokens, usage.MetricLLMCachedTokens, usage.MetricLLMCacheWriteTokens:
			input += measurement.Quantity
		}
	}
	if input <= 0 {
		return 0, false
	}
	rate := cached.Quantity / input * 100
	if rate < 0 {
		rate = 0
	}
	if rate > 100 {
		rate = 100
	}
	return rate, true
}

func networkBytesLine(measurement usage.Measurement) string {
	switch measurement.Direction {
	case usage.DirectionUpload, usage.DirectionInput, usage.DirectionWrite:
		return "↑ uploaded " + formatBytes(measurement.Quantity)
	case usage.DirectionDownload, usage.DirectionOutput, usage.DirectionRead:
		return "↓ downloaded " + formatBytes(measurement.Quantity)
	default:
		return "↔ transferred " + formatBytes(measurement.Quantity)
	}
}

func formatHumanQuantity(measurement usage.Measurement) string {
	switch measurement.Unit {
	case usage.UnitByte:
		return formatBytes(measurement.Quantity)
	case usage.UnitCurrency:
		return formatCost(measurement)
	default:
		return formatHumanNumber(measurement.Quantity)
	}
}

func formatHumanNumber(quantity float64) string {
	if quantity != float64(int64(quantity)) {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", quantity), "0"), ".")
	}
	value := int64(quantity)
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return sign + text
}

func formatBytes(quantity float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := quantity
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return formatHumanNumber(value) + " " + units[unit]
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".") + " " + units[unit]
}

func formatCost(measurement usage.Measurement) string {
	currency := "USD"
	if measurement.Dimensions != nil && measurement.Dimensions["currency"] != "" {
		currency = measurement.Dimensions["currency"]
	}
	prefix := currency + " "
	if currency == "USD" {
		prefix = "$"
	}
	quantity := measurement.Quantity
	switch {
	case quantity >= 1:
		return prefix + fmt.Sprintf("%.2f", quantity)
	case quantity >= 0.01:
		return prefix + strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", quantity), "0"), ".")
	default:
		return prefix + strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", quantity), "0"), ".")
	}
}

func formatPercent(quantity float64) string {
	return formatHumanNumber(math.Round(quantity)) + "%"
}

func formatDurationQuantity(quantity float64, unit usage.Unit) string {
	if unit == usage.UnitMillisecond {
		return time.Duration(quantity * float64(time.Millisecond)).Round(time.Millisecond).String()
	}
	return formatHumanNumber(quantity)
}

// Prompter collects clarify answers from a terminal.
type Prompter struct {
	In  io.Reader
	Out io.Writer
}

// Clarify implements system.Clarifier.
func (p Prompter) Clarify(ctx context.Context, req system.ClarifyRequest) (system.ClarifyResult, error) {
	if p.In == nil {
		return system.ClarifyResult{}, fmt.Errorf("terminalui: input is nil")
	}
	out := p.Out
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintf(out, "\nclarify: %s\n", req.Prompt)
	fields := schemaFields(req.Schema)
	reader := bufio.NewReader(p.In)
	if len(fields) == 0 {
		_, _ = fmt.Fprint(out, "> ")
		text, err := readLine(ctx, reader)
		if err != nil {
			return system.ClarifyResult{}, err
		}
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err == nil {
			return system.ClarifyResult{Answer: decoded}, nil
		}
		return system.ClarifyResult{Answer: text}, nil
	}
	answer := map[string]any{}
	for _, field := range fields {
		prompt := field.Name
		if field.Enum != "" {
			prompt += " " + field.Enum
		}
		if value, ok := req.Defaults[field.Name]; ok {
			prompt += fmt.Sprintf(" [%v]", value)
		}
		_, _ = fmt.Fprintf(out, "%s: ", prompt)
		text, err := readLine(ctx, reader)
		if err != nil {
			return system.ClarifyResult{}, err
		}
		if strings.TrimSpace(text) == "" {
			if value, ok := req.Defaults[field.Name]; ok {
				answer[field.Name] = value
			}
			continue
		}
		answer[field.Name] = text
	}
	return system.ClarifyResult{Answer: answer}, nil
}

// Approver collects y/N approval decisions from a terminal.
type Approver struct {
	In  io.Reader
	Out io.Writer
}

// Approve implements operationruntime.ApprovalGate.
func (a Approver) Approve(ctx operation.Context, req operationruntime.ApprovalRequest) error {
	if a.In == nil {
		return fmt.Errorf("terminalui: approval input is nil")
	}
	out := a.Out
	if out == nil {
		out = io.Discard
	}
	_, _ = fmt.Fprintf(out, "\napproval required: %s\n", req.Spec.Ref.String())
	if req.Risk.Level != "" {
		_, _ = fmt.Fprintf(out, "risk: %s\n", req.Risk.Level)
	}
	if strings.TrimSpace(req.Risk.Reason) != "" {
		_, _ = fmt.Fprintf(out, "reason: %s\n", req.Risk.Reason)
	}
	if summary := approvalInputSummary(req.Input); summary != "" {
		_, _ = fmt.Fprintf(out, "input: %s\n", summary)
	}
	_, _ = fmt.Fprint(out, "Approve? [y/N] ")
	text, err := readLine(ctx, bufio.NewReader(a.In))
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("user denied approval")
	}
}

func approvalInputSummary(input any) string {
	if input == nil {
		return ""
	}
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Sprint(input)
	}
	const max = 600
	if len(data) > max {
		return string(data[:max]) + "...(truncated)"
	}
	return string(data)
}

type schemaField struct {
	Name string
	Enum string
}

func schemaFields(raw json.RawMessage) []schemaField {
	if len(raw) == 0 {
		return nil
	}
	var schema struct {
		Properties map[string]struct {
			Enum []any `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	names := append([]string(nil), schema.Required...)
	for name := range schema.Properties {
		if !contains(names, name) {
			names = append(names, name)
		}
	}
	fields := make([]schemaField, 0, len(names))
	for _, name := range names {
		var enum string
		if prop, ok := schema.Properties[name]; ok && len(prop.Enum) > 0 {
			values := make([]string, 0, len(prop.Enum))
			for _, value := range prop.Enum {
				values = append(values, fmt.Sprint(value))
			}
			enum = "(" + strings.Join(values, "|") + ")"
		}
		fields = append(fields, schemaField{Name: name, Enum: enum})
	}
	return fields
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func readLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := reader.ReadString('\n')
		done <- result{text: strings.TrimSpace(text), err: err}
	}()
	select {
	case result := <-done:
		if result.err != nil && result.err != io.EOF {
			return "", result.err
		}
		return result.text, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
