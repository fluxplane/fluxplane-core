package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/command"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coretask "github.com/fluxplane/fluxplane-core/core/task"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	"github.com/fluxplane/fluxplane-core/orchestration/taskexecutor"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimetask "github.com/fluxplane/fluxplane-core/runtime/task"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-policy"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

const (
	Name                      = "task"
	TaskCreateOp              = "task_create"
	TaskModifyOp              = "task_modify"
	TaskGetOp                 = "task_get"
	TaskListOp                = "task_list"
	TaskListArtifactsOp       = "task_list_artifacts"
	TaskGetArtifactOp         = "task_get_artifact"
	TaskReadArtifactOp        = "task_read_artifact"
	TaskValidateOp            = "task_validate"
	ReviewRequestOp           = "review_request"
	TaskRunOp                 = "task_run"
	TaskSchedulerStatusOp     = "task_scheduler_status"
	TaskSchedulerSetEnabledOp = "task_scheduler_set_enabled"
	TaskCommand               = "task"
	PlanCommand               = "plan"
	TaskAgent                 = "task"
	TaskSession               = "task"
	PlanAgent                 = "task-planner"
	PlanSession               = "task-planner"
	WorkerAgent               = "worker"
	WorkerSession             = "worker"
	ExplorerAgent             = "explorer"
	ExplorerSession           = "explorer"
	ReviewerAgent             = "reviewer"
	ReviewerSession           = "reviewer"
	ParallelIntentDeriver     = "task.parallel_intent"
	AssertionParallelWork     = "work.parallel_requested"
	defaultPrefix             = "task_"
	artifactPrefix            = "artifact_"
	taskModifyRetries         = 16
)

// Plugin contributes task creation resources and operations.
type Plugin struct {
	Runner    TaskRunner
	Workspace runtimeworkspace.Workspace
}

// TaskRunner controls asynchronous task execution.
type TaskRunner interface {
	SubmitTask(context.Context, coretask.ID) (taskexecutor.SubmitResult, error)
	Status() coretask.SchedulerStatusResult
	SetEnabled(bool) coretask.SchedulerStatusResult
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}
var _ pluginhost.ReactionContributor = Plugin{}

// New returns the task plugin.
func New() Plugin { return Plugin{} }

// NewWithRunner returns the task plugin with scheduler control operations
// backed by the supplied runner.
func NewWithRunner(runner TaskRunner) Plugin { return Plugin{Runner: runner} }

// Config configures task plugin runtime boundaries.
type Config struct {
	Runner    TaskRunner
	Workspace runtimeworkspace.Workspace
}

// NewWithConfig returns the task plugin with explicit runtime boundaries.
func NewWithConfig(cfg Config) Plugin {
	return Plugin(cfg)
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Task creation commands, agents, and operations."}
}

// Contributions returns task resources.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := []operation.Spec{taskCreateSpec(), taskModifySpec(), taskGetSpec(), taskListSpec(), taskListArtifactsSpec(), taskGetArtifactSpec(), taskReadArtifactSpec(), taskValidateSpec(), reviewRequestSpec(), taskRunSpec(), taskSchedulerStatusSpec(), taskSchedulerSetEnabledSpec()}
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Task creation and management operations.",
			Operations:  operationRefs(specs),
		}},
		Operations:        specs,
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{parallelIntentDeriverSpec()},
		Commands: []command.Spec{
			{
				Path:        command.Path{TaskCommand},
				Description: "Create a structured task from the request.",
				Target: invocation.Target{
					Kind:    invocation.TargetSession,
					Session: TaskSession,
				},
				Policy: policy.InvocationPolicy{
					AllowedCallers: []policy.CallerKind{policy.CallerUser},
					RequiredTrust:  policy.TrustVerified,
				},
			},
			{
				Path:        command.Path{PlanCommand},
				Description: "Plan work as a draft task and mark it ready after approval.",
				Target: invocation.Target{
					Kind:    invocation.TargetSession,
					Session: PlanSession,
				},
				Policy: policy.InvocationPolicy{
					AllowedCallers: []policy.CallerKind{policy.CallerUser},
					RequiredTrust:  policy.TrustVerified,
				},
			},
		},
		Agents: []agent.Spec{taskAgentSpec(), planAgentSpec(), workerAgentSpec(), explorerAgentSpec(), reviewerAgentSpec()},
		Sessions: []coresession.Spec{
			{
				Name:        TaskSession,
				Description: "Dedicated task creator session.",
				Agent:       agent.Ref{Name: TaskAgent},
				Operations:  taskOperationRefs(),
				Metadata:    map[string]string{"role": "task_creator"},
			},
			{
				Name:        PlanSession,
				Description: "Dedicated task planning session.",
				Agent:       agent.Ref{Name: PlanAgent},
				Operations:  taskOperationRefs(),
				Metadata:    map[string]string{"role": "task_planner"},
			},
			{Name: WorkerSession, Agent: agent.Ref{Name: WorkerAgent}, Metadata: map[string]string{"role": "task_worker"}},
			{Name: ExplorerSession, Agent: agent.Ref{Name: ExplorerAgent}, Metadata: map[string]string{"role": "task_explorer"}},
			{Name: ReviewerSession, Agent: agent.Ref{Name: ReviewerAgent}, Metadata: map[string]string{"role": "task_reviewer"}},
		},
	}, nil
}

// AssertionDerivers returns executable task-related assertion derivation.
func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{parallelIntentAssertionDeriver{}}, nil
}

// Reactions returns task-plugin default reaction rules.
func (Plugin) Reactions(context.Context, pluginhost.Context) ([]corereaction.Rule, error) {
	return []corereaction.Rule{parallelIntentReactionRule()}, nil
}

// Operations returns executable task operations.
func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	var store runtimetask.Store
	if ctx.EventStore != nil {
		taskStore, err := runtimetask.NewStore(ctx.EventStore)
		if err != nil {
			return nil, err
		}
		store = taskStore
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[coretask.TaskCreateRequest, coretask.TaskCreateResult](taskCreateSpec(), createTask(store), operationruntime.WithAccessFields[coretask.TaskCreateRequest](
			operationruntime.TaskAccess(func(input coretask.TaskCreateRequest) string { return string(input.ID) }, policy.ActionTaskWrite),
		)),
		operationruntime.NewTypedResult[coretask.TaskModifyRequest, coretask.TaskModifyResult](taskModifySpec(), modifyTask(store), operationruntime.WithAccessFields[coretask.TaskModifyRequest](
			operationruntime.TaskAccess(func(input coretask.TaskModifyRequest) string { return string(input.ID) }, policy.ActionTaskWrite),
		)),
		operationruntime.NewTypedResult[coretask.TaskGetRequest, coretask.TaskGetResult](taskGetSpec(), getTask(store), operationruntime.WithAccessFields[coretask.TaskGetRequest](
			operationruntime.TaskAccess(func(input coretask.TaskGetRequest) string { return string(input.ID) }, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.TaskListRequest, coretask.TaskListResult](taskListSpec(), listTasks(store), operationruntime.WithAccessFields[coretask.TaskListRequest](
			operationruntime.StaticAccess[coretask.TaskListRequest](policy.ResourceRef{Kind: policy.ResourceTask, ID: "*"}, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.TaskArtifactListRequest, coretask.TaskArtifactListResult](taskListArtifactsSpec(), listArtifacts(store), operationruntime.WithAccessFields[coretask.TaskArtifactListRequest](
			operationruntime.TaskAccess(func(input coretask.TaskArtifactListRequest) string { return string(input.ID) }, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.TaskArtifactGetRequest, coretask.TaskArtifactGetResult](taskGetArtifactSpec(), getArtifact(store), operationruntime.WithAccessFields[coretask.TaskArtifactGetRequest](
			operationruntime.TaskAccess(func(input coretask.TaskArtifactGetRequest) string { return string(input.ID) }, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.TaskArtifactReadRequest, coretask.TaskArtifactReadResult](taskReadArtifactSpec(), readArtifact(store, p.Workspace), operationruntime.WithAccessFields[coretask.TaskArtifactReadRequest](
			operationruntime.TaskAccess(func(input coretask.TaskArtifactReadRequest) string { return string(input.ID) }, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.TaskValidateRequest, coretask.TaskValidationResult](taskValidateSpec(), validateTask(store), operationruntime.WithAccessFields[coretask.TaskValidateRequest](
			operationruntime.TaskAccess(func(input coretask.TaskValidateRequest) string { return string(input.ID) }, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.ReviewRequest, coretask.ReviewRequestResult](reviewRequestSpec(), requestReview(store), operationruntime.WithAccessFields[coretask.ReviewRequest](
			operationruntime.TaskAccess(func(input coretask.ReviewRequest) string { return string(input.TaskID) }, policy.ActionTaskWrite),
		)),
		operationruntime.NewTypedResult[coretask.ExecutionRequest, coretask.ExecutionResult](taskRunSpec(), runTask(p.Runner, store), operationruntime.WithAccessFields[coretask.ExecutionRequest](
			operationruntime.TaskAccess(func(input coretask.ExecutionRequest) string { return string(input.TaskID) }, policy.ActionTaskRun),
		)),
		operationruntime.NewTypedResult[coretask.SchedulerStatusRequest, coretask.SchedulerStatusResult](taskSchedulerStatusSpec(), schedulerStatus(p.Runner), operationruntime.WithAccessFields[coretask.SchedulerStatusRequest](
			operationruntime.StaticAccess[coretask.SchedulerStatusRequest](policy.ResourceRef{Kind: policy.ResourceTask, ID: "*"}, policy.ActionTaskRead),
		)),
		operationruntime.NewTypedResult[coretask.SchedulerSetEnabledRequest, coretask.SchedulerStatusResult](taskSchedulerSetEnabledSpec(), schedulerSetEnabled(p.Runner), operationruntime.WithAccessFields[coretask.SchedulerSetEnabledRequest](
			operationruntime.StaticAccess[coretask.SchedulerSetEnabledRequest](policy.ResourceRef{Kind: policy.ResourceTask, ID: "*"}, policy.ActionTaskAdmin),
		)),
	}, nil
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

func taskOperationRefs() []operation.Ref {
	return []operation.Ref{
		{Name: TaskCreateOp},
		{Name: TaskModifyOp},
		{Name: TaskGetOp},
		{Name: TaskListOp},
		{Name: TaskListArtifactsOp},
		{Name: TaskGetArtifactOp},
		{Name: TaskReadArtifactOp},
		{Name: ReviewRequestOp},
		{Name: TaskRunOp},
		{Name: TaskSchedulerStatusOp},
		{Name: TaskValidateOp},
		{Name: "clarify"},
	}
}

func taskCreateSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskCreateRequest, coretask.TaskCreateResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskCreateOp},
		Description: "Create an event-sourced task and return immediately after creation.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskGetSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskGetRequest, coretask.TaskGetResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskGetOp},
		Description: "Load the current projected state for an event-sourced task.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskModifySpec() operation.Spec {
	input := operationruntime.WithArrayItems(
		operationruntime.TypeOf[coretask.TaskModifyRequest]("task_modify_input"),
		"modifications",
		operationruntime.OneOf(
			operationruntime.SchemaFor[modifyMetadata](),
			operationruntime.SchemaFor[modifyAcceptanceCriterion](),
			operationruntime.SchemaFor[modifyOutput](),
			operationruntime.SchemaFor[modifyStep](),
			operationruntime.SchemaFor[modifyStepUpdate](),
			operationruntime.SchemaFor[modifyStepRemove](),
			operationruntime.SchemaFor[modifyStepStatus](),
			operationruntime.SchemaFor[modifyArtifact](),
			operationruntime.SchemaFor[modifyArtifactUpdate](),
			operationruntime.SchemaFor[modifyArtifactRemove](),
			operationruntime.SchemaFor[modifyStatus](),
			operationruntime.SchemaFor[modifyReopen](),
			operationruntime.SchemaFor[modifyStepReopen](),
			operationruntime.SchemaFor[modifyComplete](),
		),
	)
	input.Description = "Batch task modification request. Apply modifications sequentially to one existing task."
	return operation.Spec{
		Ref:         operation.Ref{Name: TaskModifyOp},
		Description: "Apply one or more metadata, step, status, artifact, or completion modifications to an existing task.",
		Input:       input,
		Output:      operationruntime.TypeOf[coretask.TaskModifyResult]("task_modify_output"),
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	}
}

func taskListSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskListRequest, coretask.TaskListResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskListOp},
		Description: "List indexed tasks by status, labels, workspace, project, owner, assignee, or text query. Defaults to current-session tasks when called from a session; use scope=all for previous sessions or global task history. Do not add an assignee filter unless the user explicitly asks for one.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskListArtifactsSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskArtifactListRequest, coretask.TaskArtifactListResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskListArtifactsOp},
		Description: "List task, execution, and step artifacts for one task.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskGetArtifactSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskArtifactGetRequest, coretask.TaskArtifactGetResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskGetArtifactOp},
		Description: "Get one task artifact by artifact id.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskReadArtifactSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskArtifactReadRequest, coretask.TaskArtifactReadResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskReadArtifactOp},
		Description: "Read bounded task artifact content from an inline value or safe workspace reference.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectFilesystem},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func reviewRequestSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.ReviewRequest, coretask.ReviewRequestResult](operation.Spec{
		Ref:         operation.Ref{Name: ReviewRequestOp},
		Description: "Create a reviewer-assigned task to review an existing task and its artifacts.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskValidateSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.TaskValidateRequest, coretask.TaskValidationResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskValidateOp},
		Description: "Validate required outputs, artifacts, and step terminal state for one task.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskRunSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.ExecutionRequest, coretask.ExecutionResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskRunOp},
		Description: "Schedule one ready task for asynchronous worker execution.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskSchedulerStatusSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.SchedulerStatusRequest, coretask.SchedulerStatusResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskSchedulerStatusOp},
		Description: "Report local task scheduler enablement, capacity, and running tasks.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func taskSchedulerSetEnabledSpec() operation.Spec {
	return operationruntime.WithTypedContract[coretask.SchedulerSetEnabledRequest, coretask.SchedulerStatusResult](operation.Spec{
		Ref:         operation.Ref{Name: TaskSchedulerSetEnabledOp},
		Description: "Enable or disable automatic reactive task scheduling. Manual task_run remains available.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

type parallelIntentAssertionDeriver struct{}

func (parallelIntentAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return parallelIntentDeriverSpec()
}

func (parallelIntentAssertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != "channel.message" && observation.Kind != "session.continuation" {
			continue
		}
		if !parallelIntentRequested(observationText(observation.Content)) {
			continue
		}
		out = append(out, coreevidence.Assertion{
			Kind:           AssertionParallelWork,
			Target:         "task-scheduler",
			Scope:          observation.Scope,
			Environment:    observation.Environment,
			Confidence:     1,
			ObservationIDs: taskObservationIDs(observation.ID),
		})
	}
	return out, nil
}

func parallelIntentDeriverSpec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             ParallelIntentDeriver,
		Description:      "Derives parallel-work intent from channel message observations.",
		ObservationKinds: []string{"channel.message", "session.continuation"},
	}
}

func parallelIntentReactionRule() corereaction.Rule {
	return corereaction.Rule{
		Name:        "task.parallel_intent.enable_task_operations",
		Description: "Enable task scheduling tools when the user asks for parallel work.",
		When: corereaction.Matcher{
			Assertion: AssertionParallelWork,
			Target:    "task-scheduler",
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: Name,
		}},
	}
}

func parallelIntentRequested(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, phrase := range []string{
		"at the same time",
		"in parallel",
		"concurrently",
		"simultaneously",
		"work on both",
		"both things",
		"split this up",
		"split the work",
		"have agents look",
		"multiple agents",
		"same time",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	if strings.Contains(text, "while you") && strings.Contains(text, "also") {
		return true
	}
	return false
}

func observationText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(typed))
		}
		return string(data)
	}
}

func taskObservationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

func taskAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        TaskAgent,
		Description: "Narrow task creator agent that turns requests into structured tasks.",
		System: strings.Join([]string{
			"You create structured tasks only.",
			"Given the user's request and available context, produce a complete task.",
			"Prefer creating a task with explicit inputs, outputs, assumptions, and acceptance criteria over asking clarification.",
			"Ask clarification only when the request cannot be represented as a useful draft or ready task.",
			"When enough information exists, call task_create.",
			"If the user asks for immediate execution, create the task with status=ready, call task_run for the created task, and report whether it started, is already running, is not ready, or is waiting for capacity.",
			"Treat phrases such as work on both things at the same time, in parallel, concurrently, split this up, or while you do one thing also investigate another as explicit immediate parallel execution requests.",
			"For independent read-only investigation threads in a parallel request, create one ready explorer-assigned task per distinct thread, call task_run for each created task, and report every task id with its scheduler response.",
			"If the requested threads share a write scope or cannot be separated cleanly, create one task with explicit steps instead of parallel tasks.",
			"Use task_modify for follow-up changes to an existing task.",
			"After task_create succeeds, send a concise final message with each task id, title, status, scheduler response when task_run was called, and expected outputs.",
		}, " "),
		Driver:     agent.DriverSpec{Kind: "llmagent"},
		Turns:      agent.TurnPolicy{MaxSteps: 10},
		Operations: taskOperationRefs(),
	}
}

func planAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        PlanAgent,
		Description: "Narrow planning agent that drafts tasks, clarifies uncertainty, and marks approved plans ready.",
		System: strings.TrimSpace(`
# Role

You are a task planner. Your only job is to turn the user's request into an approved event-sourced task.

# Workflow

1. Understand the user's request and the current context.
2. Identify important unknowns, risks, scope boundaries, required inputs, expected outputs, and acceptance criteria.
3. Use clarify when a missing answer materially changes the task. Do not clarify trivia that can be represented as an assumption.
4. Create or update a task with status draft. Include useful steps as a dependency DAG when the work naturally decomposes.
5. Keep planned tasks visible in the current session. Use the normal developer assignee unless the user explicitly asks for human-only ownership.
6. Present the draft task to the user with task id, status=draft, assignee, objective, steps, required inputs, expected outputs, and assumptions. Say that it is not scheduled yet.
7. Ask for approval or refinement. Continue refining the same draft task until the user cancels or approves.
8. When the user approves, or says to execute/make ready/run this plan, call task_modify on the existing task to set the task status to ready, then call task_run for that task.
9. After approval, report the task id, ready/running state, whether task_run started it or found it already running, and whether it is running in the background or waiting for scheduler capacity.

# Rules

- Use only task management and clarification tools.
- Do not execute the planned work yourself.
- Do not mark a task ready until the user has approved the draft.
- Do not create a second task when the user approves or refines an existing draft.
- Do not stop after setting ready when the user approved execution; call task_run and report the scheduler response.
- Prefer task outputs and acceptance criteria over vague prose.
- If the user cancels, leave the task draft or cancelled and report the task id.
`),
		Driver:     agent.DriverSpec{Kind: "llmagent"},
		Turns:      agent.TurnPolicy{MaxSteps: 20},
		Operations: taskOperationRefs(),
	}
}

func workerAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        WorkerAgent,
		Description: "Focused task worker for implementation and investigation steps.",
		System:      "You are a focused task worker. Complete the assigned task step within scope and summarize exactly what you changed or found. If blocked, report the blocker clearly.",
		Driver:      agent.DriverSpec{Kind: "llmagent"},
		Turns:       agent.TurnPolicy{MaxSteps: 50},
		Operations: []operation.Ref{
			{Name: "project_inventory"}, {Name: "project_files"}, {Name: "project_docs"}, {Name: "project_tasks"}, {Name: "project_task_run"},
			{Name: "dir_list"}, {Name: "dir_tree"}, {Name: "file_read"}, {Name: "file_edit"},
			{Name: "grep"}, {Name: "glob"}, {Name: "git_status"}, {Name: "git_diff"},
			{Name: "shell_exec"}, {Name: "code_execute"}, {Name: "web_search"}, {Name: "web_request"},
			{Name: TaskGetOp}, {Name: TaskModifyOp}, {Name: TaskValidateOp}, {Name: TaskListArtifactsOp}, {Name: TaskGetArtifactOp},
		},
	}
}

func explorerAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        ExplorerAgent,
		Description: "Read-only task exploration worker.",
		System:      "You are a read-only task exploration worker. Inspect the requested context and report concise findings with file paths when relevant. Do not modify files.",
		Driver:      agent.DriverSpec{Kind: "llmagent"},
		Turns:       agent.TurnPolicy{MaxSteps: 30},
		Operations: []operation.Ref{
			{Name: "project_inventory"}, {Name: "project_files"}, {Name: "project_docs"}, {Name: "project_tasks"},
			{Name: "dir_list"}, {Name: "dir_tree"}, {Name: "file_read"}, {Name: "grep"}, {Name: "glob"},
			{Name: "git_status"}, {Name: "git_diff"}, {Name: "web_search"}, {Name: "web_request"},
			{Name: TaskGetOp}, {Name: TaskModifyOp}, {Name: TaskValidateOp}, {Name: TaskListArtifactsOp}, {Name: TaskGetArtifactOp},
		},
	}
}

func reviewerAgentSpec() agent.Spec {
	return agent.Spec{
		Name:        ReviewerAgent,
		Description: "Task review worker for validating completed work.",
		System:      "You are a task reviewer. Review the assigned task evidence against scope, outputs, and acceptance criteria. Report concrete findings first, then residual risk. Do not modify files.",
		Driver:      agent.DriverSpec{Kind: "llmagent"},
		Turns:       agent.TurnPolicy{MaxSteps: 30},
		Operations: []operation.Ref{
			{Name: "project_inventory"}, {Name: "project_files"}, {Name: "project_docs"}, {Name: "project_tasks"},
			{Name: "dir_list"}, {Name: "dir_tree"}, {Name: "file_read"}, {Name: "grep"}, {Name: "glob"},
			{Name: "git_status"}, {Name: "git_diff"}, {Name: "go_test"}, {Name: "go_vet"},
			{Name: TaskGetOp}, {Name: TaskModifyOp}, {Name: TaskValidateOp}, {Name: TaskListArtifactsOp}, {Name: TaskGetArtifactOp},
		},
	}
}

type modifyMetadata struct {
	Op                 string                  `json:"op" jsonschema:"description=Operation discriminator.,enum=update_metadata,required"`
	Title              string                  `json:"title,omitempty" jsonschema:"description=Replacement title."`
	Description        string                  `json:"description,omitempty" jsonschema:"description=Replacement description."`
	Objective          string                  `json:"objective,omitempty" jsonschema:"description=Replacement objective."`
	AcceptanceCriteria []string                `json:"acceptance_criteria,omitempty" jsonschema:"description=Replacement acceptance criteria."`
	Inputs             []coretask.ArtifactSpec `json:"inputs,omitempty" jsonschema:"description=Replacement input artifact declarations."`
	Outputs            []coretask.ArtifactSpec `json:"outputs,omitempty" jsonschema:"description=Replacement output artifact declarations."`
	Labels             []string                `json:"labels,omitempty" jsonschema:"description=Replacement labels."`
	Priority           coretask.Priority       `json:"priority,omitempty" jsonschema:"description=Replacement priority.,enum=low,enum=normal,enum=high,enum=urgent"`
	Assignee           coretask.Role           `json:"assignee,omitempty" jsonschema:"description=Replacement assignee."`
	Owner              coretask.Role           `json:"owner,omitempty" jsonschema:"description=Replacement owner."`
	Metadata           map[string]string       `json:"metadata,omitempty" jsonschema:"description=Metadata keys to merge into task metadata."`
}

type modifyAcceptanceCriterion struct {
	Op        string `json:"op" jsonschema:"description=Operation discriminator.,enum=add_acceptance_criterion,required"`
	Criterion string `json:"criterion" jsonschema:"description=Acceptance criterion to append.,required"`
}

type modifyOutput struct {
	Op     string                `json:"op" jsonschema:"description=Operation discriminator.,enum=add_output,required"`
	Output coretask.ArtifactSpec `json:"output" jsonschema:"description=Expected output artifact declaration.,required"`
}

type modifyStep struct {
	Op   string        `json:"op" jsonschema:"description=Operation discriminator.,enum=add_step,required"`
	Step coretask.Step `json:"step" jsonschema:"description=Step to append.,required"`
}

type modifyStepUpdate struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=update_step,required"`
	StepID coretask.StepID `json:"step_id" jsonschema:"description=Step id to replace.,required"`
	Step   coretask.Step   `json:"step" jsonschema:"description=Replacement step.,required"`
}

type modifyStepRemove struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=remove_step,required"`
	StepID coretask.StepID `json:"step_id" jsonschema:"description=Step id to remove.,required"`
}

type modifyStepStatus struct {
	Op          string               `json:"op" jsonschema:"description=Operation discriminator.,enum=set_step_status,required"`
	StepID      coretask.StepID      `json:"step_id" jsonschema:"description=Step id whose execution status changes.,required"`
	StepStatus  coretask.StepStatus  `json:"step_status" jsonschema:"description=New step status.,enum=waiting,enum=running,enum=blocked,enum=skipped,enum=completed,enum=failed,enum=cancelled,required"`
	ExecutionID coretask.ExecutionID `json:"execution_id,omitempty" jsonschema:"description=Optional execution id. Defaults to current execution or manual."`
	StepOutput  operation.Value      `json:"step_output,omitempty" jsonschema:"description=Optional step output."`
	Reason      string               `json:"reason,omitempty" jsonschema:"description=Reason for status change."`
}

type modifyArtifact struct {
	Op          string                `json:"op" jsonschema:"description=Operation discriminator.,enum=add_artifact,required"`
	ExecutionID coretask.ExecutionID  `json:"execution_id,omitempty" jsonschema:"description=Optional execution id. For step artifacts defaults to current execution or manual."`
	StepID      coretask.StepID       `json:"step_id,omitempty" jsonschema:"description=Optional step id. When set without execution_id the artifact is attached to current execution or manual."`
	Artifact    coretask.ArtifactSpec `json:"artifact" jsonschema:"description=Artifact to add.,required"`
}

type modifyArtifactUpdate struct {
	Op          string                `json:"op" jsonschema:"description=Operation discriminator.,enum=update_artifact,required"`
	ExecutionID coretask.ExecutionID  `json:"execution_id,omitempty" jsonschema:"description=Optional execution id used only to verify the artifact scope."`
	StepID      coretask.StepID       `json:"step_id,omitempty" jsonschema:"description=Optional step id used only to verify the artifact scope."`
	ArtifactID  string                `json:"artifact_id" jsonschema:"description=Artifact id to update.,required"`
	Artifact    coretask.ArtifactSpec `json:"artifact" jsonschema:"description=Replacement artifact.,required"`
}

type modifyArtifactRemove struct {
	Op          string               `json:"op" jsonschema:"description=Operation discriminator.,enum=remove_artifact,required"`
	ExecutionID coretask.ExecutionID `json:"execution_id,omitempty" jsonschema:"description=Optional execution id used only to verify the artifact scope."`
	StepID      coretask.StepID      `json:"step_id,omitempty" jsonschema:"description=Optional step id used only to verify the artifact scope."`
	ArtifactID  string               `json:"artifact_id" jsonschema:"description=Artifact id to remove.,required"`
	Reason      string               `json:"reason,omitempty" jsonschema:"description=Reason for removal."`
}

type modifyStatus struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=set_status,required"`
	Status coretask.Status `json:"status" jsonschema:"description=New task status.,enum=draft,enum=ready,enum=running,enum=blocked,enum=completed,enum=failed,enum=cancelled,enum=interrupted,required"`
	Reason string          `json:"reason,omitempty" jsonschema:"description=Reason for status change."`
}

type modifyReopen struct {
	Op     string          `json:"op" jsonschema:"description=Operation discriminator.,enum=reopen,required"`
	Status coretask.Status `json:"status,omitempty" jsonschema:"description=Active status after reopening. Defaults to ready.,enum=draft,enum=ready,enum=running,enum=blocked,enum=interrupted"`
	Reason string          `json:"reason,omitempty" jsonschema:"description=Reason for reopening the task."`
}

type modifyStepReopen struct {
	Op          string               `json:"op" jsonschema:"description=Operation discriminator.,enum=reopen_step,required"`
	StepID      coretask.StepID      `json:"step_id" jsonschema:"description=Step id to reopen.,required"`
	StepStatus  coretask.StepStatus  `json:"step_status,omitempty" jsonschema:"description=Active step status after reopening. Defaults to waiting.,enum=waiting,enum=running,enum=blocked"`
	ExecutionID coretask.ExecutionID `json:"execution_id,omitempty" jsonschema:"description=Optional execution id. Defaults to current execution or manual."`
	Reason      string               `json:"reason,omitempty" jsonschema:"description=Reason for reopening the step."`
}

type modifyComplete struct {
	Op             string                  `json:"op" jsonschema:"description=Operation discriminator.,enum=complete,required"`
	ForceOverrides []string                `json:"force_overrides,omitempty" jsonschema:"description=Validation check codes to override explicitly. Supported values include required_output and steps_terminal."`
	Reason         string                  `json:"reason,omitempty" jsonschema:"description=Completion reason."`
	Artifacts      []coretask.ArtifactSpec `json:"artifacts,omitempty" jsonschema:"description=Artifacts to add before completion validation."`
}

func createTask(store runtimetask.Store) func(operation.Context, coretask.TaskCreateRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskCreateRequest) operation.Result {
		if store == nil {
			return operation.Failed("task_store_missing", "task_create requires an event store", nil)
		}
		task := taskFromCreateRequest(req)
		if task.ID == "" {
			task.ID = coretask.ID(newID(defaultPrefix))
		}
		if task.Status == "" {
			task.Status = coretask.StatusReady
		}
		attachOriginMetadata(ctx, &task)
		if err := task.Validate(); err != nil {
			return operation.Failed("task_invalid", err.Error(), nil)
		}
		req.ID = task.ID
		if req.Status == "" {
			req.Status = task.Status
		}
		req.Metadata = cloneStringMap(task.Metadata)
		events := []event.Event{
			coretask.CreateRequested{TaskID: task.ID, Request: req},
			coretask.Created{TaskID: task.ID, Task: task},
		}
		if err := store.Create(ctx, task.ID, events...); err != nil {
			if isAppendConflict(err) {
				return operation.Failed("task_already_exists", "task id already exists", map[string]any{"task_id": task.ID})
			}
			return operation.Failed("task_store_append_failed", err.Error(), map[string]any{"task_id": task.ID})
		}
		if err := store.Index(ctx, taskSummary(task)); err != nil {
			return operation.Failed("task_index_append_failed", err.Error(), map[string]any{"task_id": task.ID})
		}
		for _, payload := range events {
			ctx.Events().Emit(payload)
		}
		return operation.OK(coretask.TaskCreateResult{Task: task})
	}
}

func attachOriginMetadata(ctx context.Context, task *coretask.Task) {
	if task == nil {
		return
	}
	scope, ok := sessionenv.ScopeFromContext(ctx)
	if !ok {
		return
	}
	if task.Metadata == nil {
		task.Metadata = map[string]string{}
	}
	originThreadID := string(scope.Thread.ID)
	if parentThreadID := parentThreadID(ctx, scope); parentThreadID != "" {
		originThreadID = parentThreadID
		if task.Metadata[coretask.MetadataOriginDelegateThreadID] == "" {
			task.Metadata[coretask.MetadataOriginDelegateThreadID] = string(scope.Thread.ID)
		}
	}
	if task.Metadata[coretask.MetadataOriginThreadID] == "" {
		task.Metadata[coretask.MetadataOriginThreadID] = originThreadID
	}
	if task.Metadata[coretask.MetadataOriginBranchID] == "" && scope.Thread.BranchID != "" {
		task.Metadata[coretask.MetadataOriginBranchID] = string(scope.Thread.BranchID)
	}
	if task.Metadata[coretask.MetadataOriginRunID] == "" && scope.RunID != "" {
		task.Metadata[coretask.MetadataOriginRunID] = scope.RunID
	}
}

func parentThreadID(ctx context.Context, scope sessionenv.Scope) string {
	if scope.ThreadStore == nil || scope.Thread.ID == "" {
		return ""
	}
	snapshot, err := scope.ThreadStore.Read(ctx, corethread.ReadParams{ID: scope.Thread.ID})
	if err != nil {
		return ""
	}
	return snapshot.Metadata["parent_thread_id"]
}

func isAppendConflict(err error) bool {
	var conflict event.AppendConflict
	return errors.As(err, &conflict)
}

func getTask(store runtimetask.Store) func(operation.Context, coretask.TaskGetRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskGetRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		out := taskGetResult(state)
		out.View = defaultView(req.View)
		return operation.OK(out)
	}
}

func modifyTask(store runtimetask.Store) func(operation.Context, coretask.TaskModifyRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskModifyRequest) operation.Result {
		if len(req.Modifications) == 0 {
			return operation.Failed("task_modify_empty", "task_modify requires at least one modification", nil)
		}
		var (
			state   runtimetask.State
			events  []event.Event
			results []coretask.TaskModificationResult
		)
		for attempt := 0; attempt < taskModifyRetries; attempt++ {
			projected, ok, result := loadTaskWithSequence(ctx, store, req.ID)
			if !ok {
				return result
			}
			state = projected.State
			events = nil
			results = make([]coretask.TaskModificationResult, 0, len(req.Modifications))
			for _, item := range req.Modifications {
				modEvents, err := applyModification(req.ID, req.Reason, item, &state)
				results = append(results, coretask.TaskModificationResult{Op: item.Op, OK: err == nil, Error: errorString(err)})
				if err != nil {
					return operation.Failed("task_modify_invalid", err.Error(), map[string]any{"op": item.Op})
				}
				events = append(events, modEvents...)
			}
			if len(events) == 0 {
				return operation.Failed("task_modify_noop", "task_modify did not produce any changes", nil)
			}
			if err := store.AppendExpected(ctx, req.ID, projected.Sequence, events...); err != nil {
				if isAppendConflict(err) {
					continue
				}
				return operation.Failed("task_store_append_failed", err.Error(), map[string]any{"task_id": req.ID})
			}
			goto appended
		}
		return operation.Failed("task_conflict_retry", "task was modified concurrently; retry task_modify", map[string]any{"task_id": req.ID})

	appended:
		if err := store.Index(ctx, taskSummary(state.Task)); err != nil {
			return operation.Failed("task_index_append_failed", err.Error(), map[string]any{"task_id": req.ID})
		}
		for _, payload := range events {
			ctx.Events().Emit(payload)
		}
		projected, err := store.Project(ctx, req.ID)
		if err != nil {
			return operation.Failed("task_store_project_failed", err.Error(), map[string]any{"task_id": req.ID})
		}
		return operation.OK(coretask.TaskModifyResult{
			Results:          results,
			Task:             projected.Task,
			CurrentExecution: projected.CurrentExecution,
			Executions:       projected.Executions,
			Validation:       validateState(projected),
			Artifacts:        scopedArtifacts(projected),
		})
	}
}

func loadTaskWithSequence(ctx context.Context, store runtimetask.Store, id coretask.ID) (runtimetask.SequencedState, bool, operation.Result) {
	if store == nil {
		return runtimetask.SequencedState{}, false, operation.Failed("task_store_missing", "task operation requires an event store", nil)
	}
	if strings.TrimSpace(string(id)) == "" {
		return runtimetask.SequencedState{}, false, operation.Failed("task_id_required", "task id is required", nil)
	}
	projected, err := store.ProjectWithSequence(ctx, id)
	if err != nil {
		return runtimetask.SequencedState{}, false, operation.Failed("task_store_load_failed", err.Error(), map[string]any{"task_id": id})
	}
	if projected.State.Task.ID == "" {
		return runtimetask.SequencedState{}, false, operation.Failed("task_not_found", "task was not found", map[string]any{"task_id": id})
	}
	return projected, true, operation.Result{}
}

func listTasks(store runtimetask.Store) func(operation.Context, coretask.TaskListRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskListRequest) operation.Result {
		if store == nil {
			return operation.Failed("task_store_missing", "task_list requires an event store", nil)
		}
		if req.Scope != "" && req.Scope != coretask.TaskListScopeCurrentSession && req.Scope != coretask.TaskListScopeAll {
			return operation.Failed("task_list_invalid_scope", fmt.Sprintf("task_list scope %q is invalid", req.Scope), map[string]any{"scope": req.Scope})
		}
		summaries, err := store.List(ctx)
		if err != nil {
			return operation.Failed("task_index_load_failed", err.Error(), nil)
		}
		out, truncated := filterSummaries(summaries, req, sessionThreadID(ctx))
		return operation.OK(coretask.TaskListResult{Tasks: out, Truncated: truncated})
	}
}

func listArtifacts(store runtimetask.Store) func(operation.Context, coretask.TaskArtifactListRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskArtifactListRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		return operation.OK(coretask.TaskArtifactListResult{Artifacts: scopedArtifacts(state)})
	}
}

func getArtifact(store runtimetask.Store) func(operation.Context, coretask.TaskArtifactGetRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskArtifactGetRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		for _, artifact := range scopedArtifacts(state) {
			if artifact.Artifact.ID == req.ArtifactID {
				view := req.View
				if view == "" {
					view = coretask.ViewFull
				}
				out := coretask.TaskArtifactGetResult{Artifact: artifact, View: view}
				if preview, omitted, ok := artifactValuePreview(artifact.Artifact.Value, req.MaxBytes); ok {
					out.ValuePreview = preview
					out.OmittedBytes = omitted
				}
				out.ValueIncluded = req.IncludeValue
				if !req.IncludeValue {
					out.Artifact.Artifact.Value = nil
				}
				return operation.OK(out)
			}
		}
		return operation.Failed("task_artifact_not_found", "artifact was not found", map[string]any{"task_id": req.ID, "artifact_id": req.ArtifactID})
	}
}

func readArtifact(store runtimetask.Store, workspace runtimeworkspace.Workspace) func(operation.Context, coretask.TaskArtifactReadRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskArtifactReadRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		var scoped coretask.ScopedArtifact
		for _, artifact := range scopedArtifacts(state) {
			if artifact.Artifact.ID == req.ArtifactID {
				scoped = artifact
				break
			}
		}
		if scoped.Artifact.ID == "" {
			return operation.Failed("task_artifact_not_found", "artifact was not found", map[string]any{"task_id": req.ID, "artifact_id": req.ArtifactID})
		}
		maxBytes := req.MaxBytes
		if maxBytes <= 0 {
			maxBytes = 4096
		}
		if artifactPrefersRef(scoped.Artifact) {
			return readArtifactRef(ctx, workspace, scoped, maxBytes)
		}
		if text, omitted, ok := artifactValuePreview(scoped.Artifact.Value, int(maxBytes)); ok {
			return operation.OK(coretask.TaskArtifactReadResult{Artifact: scoped, Content: text, Truncated: omitted > 0, Source: "value"})
		}
		if strings.TrimSpace(scoped.Artifact.Ref) == "" {
			return operation.Failed("task_artifact_content_missing", "artifact has no inline value or ref", map[string]any{"task_id": req.ID, "artifact_id": req.ArtifactID})
		}
		return readArtifactRef(ctx, workspace, scoped, maxBytes)
	}
}

func artifactPrefersRef(artifact coretask.ArtifactSpec) bool {
	return strings.TrimSpace(artifact.Ref) != "" && strings.EqualFold(artifact.Metadata["replaced"], "true")
}

func readArtifactRef(ctx operation.Context, workspace runtimeworkspace.Workspace, scoped coretask.ScopedArtifact, maxBytes int64) operation.Result {
	ref := strings.TrimSpace(scoped.Artifact.Ref)
	if ref == "" {
		return operation.Failed("task_artifact_content_missing", "artifact has no ref", map[string]any{"task_id": scoped.TaskID, "artifact_id": scoped.Artifact.ID})
	}
	if strings.EqualFold(scoped.Artifact.Metadata["replaced"], "true") {
		data, truncated, err := operationruntime.ReadReplacementFile(ctx, ref, maxBytes)
		if err != nil {
			return operation.Failed("task_artifact_ref_read_failed", err.Error(), map[string]any{"task_id": scoped.TaskID, "artifact_id": scoped.Artifact.ID, "ref": ref})
		}
		return operation.OK(coretask.TaskArtifactReadResult{
			Artifact:     scoped,
			Content:      strings.ToValidUTF8(string(data), ""),
			Truncated:    truncated,
			Source:       "replacement_ref",
			ResolvedPath: ref,
		})
	}
	if workspace == nil {
		return operation.Failed("task_artifact_reader_missing", "task_read_artifact requires a runtime workspace for ref reads", map[string]any{"task_id": scoped.TaskID, "artifact_id": scoped.Artifact.ID, "ref": ref})
	}
	resolved, err := workspace.ResolveExisting(ctx, ref)
	if err != nil {
		return operation.Failed("task_artifact_ref_read_failed", err.Error(), map[string]any{"task_id": scoped.TaskID, "artifact_id": scoped.Artifact.ID, "ref": ref})
	}
	fsys, err := runtimeworkspace.FileSystem(workspace)
	if err != nil {
		return operation.Failed("task_artifact_ref_read_failed", err.Error(), map[string]any{"task_id": scoped.TaskID, "artifact_id": scoped.Artifact.ID, "ref": ref})
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, runtimeworkspace.PathName(resolved), maxBytes)
	if err != nil {
		return operation.Failed("task_artifact_ref_read_failed", err.Error(), map[string]any{"task_id": scoped.TaskID, "artifact_id": scoped.Artifact.ID, "ref": ref})
	}
	return operation.OK(coretask.TaskArtifactReadResult{
		Artifact:     scoped,
		Content:      strings.ToValidUTF8(string(data), ""),
		Truncated:    truncated,
		Source:       "ref",
		ResolvedPath: resolved.Rel,
	})
}

func requestReview(store runtimetask.Store) func(operation.Context, coretask.ReviewRequest) operation.Result {
	return func(ctx operation.Context, req coretask.ReviewRequest) operation.Result {
		if store == nil {
			return operation.Failed("task_store_missing", "review_request requires an event store", nil)
		}
		source, ok, result := loadTask(ctx, store, req.TaskID)
		if !ok {
			return result
		}
		reviewer := req.Reviewer
		if reviewer == "" {
			reviewer = coretask.RoleReviewer
		}
		status := req.Status
		if status == "" {
			status = coretask.StatusReady
		}
		task := coretask.Task{
			ID:          coretask.ID(newID(defaultPrefix)),
			Title:       "Review: " + firstNonEmpty(source.Task.Title, source.Task.Objective, string(source.Task.ID)),
			Objective:   "Review task " + string(source.Task.ID) + " against its scope, artifacts, and acceptance criteria.",
			Description: strings.TrimSpace(req.Instruction),
			Status:      status,
			Assignee:    reviewer,
			Owner:       source.Task.Owner,
			WorkspaceID: source.Task.WorkspaceID,
			ProjectID:   source.Task.ProjectID,
			Labels:      append([]string{"review", "task-review"}, req.Labels...),
			Inputs: []coretask.ArtifactSpec{{
				ID:          "review-subject",
				Name:        "Review subject task",
				Kind:        coretask.ArtifactReference,
				Description: "Task to review",
				Required:    true,
				Ref:         "task:" + string(source.Task.ID),
			}},
			Outputs: []coretask.ArtifactSpec{{
				ID:          "review-report",
				Name:        "Review report",
				Kind:        coretask.ArtifactReview,
				Description: "Concrete findings, residual risk, and approval/blocking recommendation.",
				Required:    true,
			}},
			Metadata: map[string]string{
				"review_subject_task_id": string(source.Task.ID),
			},
		}
		attachOriginMetadata(ctx, &task)
		if err := task.Validate(); err != nil {
			return operation.Failed("task_invalid", err.Error(), nil)
		}
		createReq := coretask.TaskCreateRequest{
			ID: task.ID, Kind: coretask.TaskCreateKindGeneric, Instruction: req.Instruction,
			Objective: task.Objective, Title: task.Title, Description: task.Description,
			Inputs: task.Inputs, Outputs: task.Outputs, Labels: task.Labels, Assignee: task.Assignee,
			Owner: task.Owner, WorkspaceID: task.WorkspaceID, ProjectID: task.ProjectID, Status: task.Status,
			Metadata: cloneStringMap(task.Metadata),
		}
		events := []event.Event{
			coretask.CreateRequested{TaskID: task.ID, Request: createReq},
			coretask.Created{TaskID: task.ID, Task: task},
		}
		if err := store.Create(ctx, task.ID, events...); err != nil {
			if isAppendConflict(err) {
				return operation.Failed("task_already_exists", "task id already exists", map[string]any{"task_id": task.ID})
			}
			return operation.Failed("task_store_append_failed", err.Error(), map[string]any{"task_id": task.ID})
		}
		if err := store.Index(ctx, taskSummary(task)); err != nil {
			return operation.Failed("task_index_append_failed", err.Error(), map[string]any{"task_id": task.ID})
		}
		for _, payload := range events {
			ctx.Events().Emit(payload)
		}
		return operation.OK(coretask.ReviewRequestResult{Task: task})
	}
}

func artifactValuePreview(value operation.Value, maxBytes int) (string, int64, bool) {
	if value == nil {
		return "", 0, false
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			text = fmt.Sprint(typed)
		} else {
			text = string(data)
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", 0, false
	}
	if maxBytes <= 0 {
		maxBytes = 4096
	}
	data := []byte(text)
	if len(data) <= maxBytes {
		return text, 0, true
	}
	preview := strings.ToValidUTF8(string(data[:maxBytes]), "")
	return preview, int64(len(data) - maxBytes), true
}

func validateTask(store runtimetask.Store) func(operation.Context, coretask.TaskValidateRequest) operation.Result {
	return func(ctx operation.Context, req coretask.TaskValidateRequest) operation.Result {
		state, ok, result := loadTask(ctx, store, req.ID)
		if !ok {
			return result
		}
		return operation.OK(validateState(state))
	}
}

func runTask(runner TaskRunner, store runtimetask.Store) func(operation.Context, coretask.ExecutionRequest) operation.Result {
	return func(ctx operation.Context, req coretask.ExecutionRequest) operation.Result {
		if runner == nil {
			return operation.Failed("task_scheduler_missing", "task_run requires a task scheduler", nil)
		}
		if strings.TrimSpace(string(req.TaskID)) == "" {
			return operation.Failed("task_id_required", "task id is required", nil)
		}
		action := req.Action
		if action == "" {
			action = coretask.ExecutionActionRun
		}
		switch action {
		case coretask.ExecutionActionRun, coretask.ExecutionActionContinue:
		default:
			return operation.Failed("task_run_invalid", fmt.Sprintf("task_run does not support action %q", action), map[string]any{"task_id": req.TaskID})
		}
		if req.DryRun {
			return operation.OK(coretask.ExecutionResult{
				TaskID:             req.TaskID,
				Status:             coretask.StatusReady,
				WaitingForCapacity: false,
				Summary:            fmt.Sprintf("Task %s would be scheduled.", req.TaskID),
			})
		}
		if err := recordTaskRunWatcher(ctx, store, req.TaskID); err != nil {
			return operation.Failed("task_run_watch_failed", err.Error(), map[string]any{"task_id": req.TaskID})
		}
		submitted, err := runner.SubmitTask(ctx, req.TaskID)
		if err != nil {
			return operation.Failed("task_run_failed", err.Error(), map[string]any{"task_id": req.TaskID})
		}
		status := submitted.Status
		if submitted.Started {
			status = coretask.StatusRunning
		}
		waitingForCapacity := !submitted.Started && !submitted.Running && submitted.Status == coretask.StatusReady
		return operation.OK(coretask.ExecutionResult{
			TaskID:             submitted.TaskID,
			Status:             status,
			Started:            submitted.Started,
			Running:            submitted.Running,
			WaitingForCapacity: waitingForCapacity,
			Background:         submitted.Started || submitted.Running,
			Summary:            submitted.Summary,
		})
	}
}

func recordTaskRunWatcher(ctx context.Context, store runtimetask.Store, taskID coretask.ID) error {
	if store == nil || taskID == "" {
		return nil
	}
	scope, ok := sessionenv.ScopeFromContext(ctx)
	if !ok || scope.Thread.ID == "" {
		return nil
	}
	for attempt := 0; attempt < taskModifyRetries; attempt++ {
		projected, err := store.ProjectWithSequence(ctx, taskID)
		if err != nil {
			return err
		}
		task := projected.State.Task
		if task.ID == "" {
			return nil
		}
		if task.ID != taskID {
			return fmt.Errorf("task %q projected as %q", taskID, task.ID)
		}
		if task.Metadata == nil {
			task.Metadata = map[string]string{}
		}
		changed := false
		set := func(key, value string) {
			if value == "" || task.Metadata[key] == value {
				return
			}
			task.Metadata[key] = value
			changed = true
		}
		set(coretask.MetadataWatchThreadID, string(scope.Thread.ID))
		if scope.Thread.BranchID != "" {
			set(coretask.MetadataWatchBranchID, string(scope.Thread.BranchID))
		}
		set(coretask.MetadataWatchRunID, scope.RunID)
		if !changed {
			return nil
		}
		if err := store.AppendExpected(ctx, taskID, projected.Sequence, coretask.Revised{TaskID: taskID, Task: task, Reason: "task_run requested execution from this session"}); errors.Is(err, event.ErrAppendConflict) {
			continue
		} else if err != nil {
			return err
		}
		return store.Index(ctx, taskSummary(task))
	}
	return fmt.Errorf("task %q changed while recording task_run watcher", taskID)
}

func schedulerStatus(runner TaskRunner) func(operation.Context, coretask.SchedulerStatusRequest) operation.Result {
	return func(operation.Context, coretask.SchedulerStatusRequest) operation.Result {
		if runner == nil {
			return operation.Failed("task_scheduler_missing", "task_scheduler_status requires a task scheduler", nil)
		}
		return operation.OK(runner.Status())
	}
}

func schedulerSetEnabled(runner TaskRunner) func(operation.Context, coretask.SchedulerSetEnabledRequest) operation.Result {
	return func(_ operation.Context, req coretask.SchedulerSetEnabledRequest) operation.Result {
		if runner == nil {
			return operation.Failed("task_scheduler_missing", "task_scheduler_set_enabled requires a task scheduler", nil)
		}
		return operation.OK(runner.SetEnabled(req.Enabled))
	}
}

func loadTask(ctx context.Context, store runtimetask.Store, id coretask.ID) (runtimetask.State, bool, operation.Result) {
	if store == nil {
		return runtimetask.State{}, false, operation.Failed("task_store_missing", "task operation requires an event store", nil)
	}
	if strings.TrimSpace(string(id)) == "" {
		return runtimetask.State{}, false, operation.Failed("task_id_required", "task id is required", nil)
	}
	records, err := store.Load(ctx, id)
	if err != nil {
		return runtimetask.State{}, false, operation.Failed("task_store_load_failed", err.Error(), map[string]any{"task_id": id})
	}
	if len(records) == 0 {
		return runtimetask.State{}, false, operation.Failed("task_not_found", "task was not found", map[string]any{"task_id": id})
	}
	state := runtimetask.Project(records)
	if state.Task.ID == "" {
		return runtimetask.State{}, false, operation.Failed("task_not_found", "task was not found", map[string]any{"task_id": id})
	}
	return state, true, operation.Result{}
}

func taskGetResult(state runtimetask.State) coretask.TaskGetResult {
	return coretask.TaskGetResult{
		Task:             state.Task,
		CurrentExecution: state.CurrentExecution,
		Executions:       state.Executions,
	}
}

func taskSummary(task coretask.Task) coretask.TaskSummary {
	return coretask.TaskSummary{
		ID:          task.ID,
		Title:       task.Title,
		Objective:   task.Objective,
		Description: task.Description,
		Status:      task.Status,
		Priority:    task.Priority,
		Assignee:    task.Assignee,
		Owner:       task.Owner,
		WorkspaceID: task.WorkspaceID,
		ProjectID:   task.ProjectID,
		Labels:      append([]string(nil), task.Labels...),
		Metadata:    cloneStringMap(task.Metadata),
	}
}

func filterSummaries(in []coretask.TaskSummary, req coretask.TaskListRequest, currentThread string) ([]coretask.TaskSummary, bool) {
	out := make([]coretask.TaskSummary, 0, len(in))
	query := strings.ToLower(strings.TrimSpace(req.Query))
	scope := req.Scope
	if scope == "" && currentThread != "" {
		scope = coretask.TaskListScopeCurrentSession
	}
	for _, summary := range in {
		if scope == coretask.TaskListScopeCurrentSession && currentThread != "" && summary.Metadata[coretask.MetadataOriginThreadID] != currentThread {
			continue
		}
		if req.Status != "" && summary.Status != req.Status {
			continue
		}
		if req.Label != "" && !stringSliceContains(summary.Labels, req.Label) {
			continue
		}
		if req.ProjectID != "" && summary.ProjectID != req.ProjectID {
			continue
		}
		if req.WorkspaceID != "" && summary.WorkspaceID != req.WorkspaceID {
			continue
		}
		if req.Assignee != "" && summary.Assignee != req.Assignee {
			continue
		}
		if req.Owner != "" && summary.Owner != req.Owner {
			continue
		}
		if query != "" && !summaryMatches(summary, query) {
			continue
		}
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return string(out[i].ID) < string(out[j].ID)
	})
	limit := req.MaxResults
	if limit <= 0 {
		limit = 50
	}
	if len(out) > limit {
		return out[:limit], true
	}
	return out, false
}

func sessionThreadID(ctx context.Context) string {
	scope, ok := sessionenv.ScopeFromContext(ctx)
	if !ok || scope.Thread.ID == "" {
		return ""
	}
	return string(scope.Thread.ID)
}

func summaryMatches(summary coretask.TaskSummary, query string) bool {
	fields := []string{string(summary.ID), summary.Title, summary.Objective, summary.Description, string(summary.Status), string(summary.Assignee), string(summary.Owner)}
	for _, value := range fields {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	for _, label := range summary.Labels {
		if strings.Contains(strings.ToLower(label), query) {
			return true
		}
	}
	for k, v := range summary.Metadata {
		if strings.Contains(strings.ToLower(k), query) || strings.Contains(strings.ToLower(v), query) {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func scopedArtifacts(state runtimetask.State) []coretask.ScopedArtifact {
	var out []coretask.ScopedArtifact
	for _, artifact := range state.Task.Artifacts {
		out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, Artifact: artifact})
	}
	execIDs := make([]string, 0, len(state.Executions))
	for id := range state.Executions {
		execIDs = append(execIDs, string(id))
	}
	sort.Strings(execIDs)
	for _, rawID := range execIDs {
		id := coretask.ExecutionID(rawID)
		exec := state.Executions[id]
		for _, artifact := range exec.Artifacts {
			out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, ExecutionID: id, Artifact: artifact})
		}
		stepIDs := make([]string, 0, len(exec.Steps))
		for stepID := range exec.Steps {
			stepIDs = append(stepIDs, string(stepID))
		}
		sort.Strings(stepIDs)
		for _, rawStepID := range stepIDs {
			stepID := coretask.StepID(rawStepID)
			for _, artifact := range exec.Steps[stepID].Artifacts {
				out = append(out, coretask.ScopedArtifact{TaskID: state.Task.ID, ExecutionID: id, StepID: stepID, Artifact: artifact})
			}
		}
	}
	return out
}

func validateState(state runtimetask.State) coretask.TaskValidationResult {
	result := coretask.TaskValidationResult{TaskID: state.Task.ID, Ready: state.Task.Status == coretask.StatusReady || state.Task.Status == coretask.StatusRunning}
	produced := scopedArtifacts(state)
	completable := true
	for _, output := range state.Task.Outputs {
		if !output.Required {
			continue
		}
		ok := artifactSatisfied(output, produced)
		if !ok {
			completable = false
		}
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "required_output",
			Message: fmt.Sprintf("required output %s", firstNonEmpty(output.ID, output.Name, output.Description, output.Ref)),
			OK:      ok,
			Target:  output.ID,
		})
	}
	if len(state.Task.Steps) > 0 {
		ok := runtimetask.AllStepsTerminal(state)
		if !ok {
			completable = false
		}
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "steps_terminal",
			Message: "all declared task steps have terminal execution state",
			OK:      ok,
		})
	}
	if len(state.Task.AcceptanceCriteria) > 0 {
		result.Checks = append(result.Checks, coretask.TaskCheck{
			Code:    "acceptance_manual",
			Message: "acceptance criteria require reviewer or caller judgment",
			OK:      true,
		})
	}
	if len(result.Checks) == 0 {
		result.Checks = append(result.Checks, coretask.TaskCheck{Code: "task_shape", Message: "task has no required outputs or steps", OK: true})
	}
	result.Completable = completable
	return result
}

func completionAllowed(validation coretask.TaskValidationResult, overrides []string) bool {
	if validation.Completable {
		return true
	}
	allowed := map[string]bool{}
	for _, code := range overrides {
		allowed[strings.TrimSpace(code)] = true
	}
	for _, check := range validation.Checks {
		if check.OK {
			continue
		}
		if !allowed[check.Code] {
			return false
		}
	}
	return len(allowed) > 0
}

func artifactSatisfied(required coretask.ArtifactSpec, produced []coretask.ScopedArtifact) bool {
	for _, scoped := range produced {
		artifact := scoped.Artifact
		if required.ID != "" && artifact.ID == required.ID {
			return true
		}
		if required.Name != "" && strings.EqualFold(artifact.Name, required.Name) {
			return true
		}
	}
	return false
}

func activeTaskStatus(status coretask.Status) bool {
	switch status {
	case coretask.StatusDraft, coretask.StatusReady, coretask.StatusRunning, coretask.StatusBlocked, coretask.StatusInterrupted:
		return true
	default:
		return false
	}
}

func activeStepStatus(status coretask.StepStatus) bool {
	switch status {
	case coretask.StepStatusWaiting, coretask.StepStatusRunning, coretask.StepStatusBlocked:
		return true
	default:
		return false
	}
}

func stepExists(steps []coretask.Step, id coretask.StepID) bool {
	for _, step := range steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

func stepHasExecutionState(state runtimetask.State, id coretask.StepID) bool {
	for _, exec := range state.Executions {
		if _, ok := exec.Steps[id]; ok {
			return true
		}
	}
	return false
}

func artifactScopeForID(state runtimetask.State, artifactID string) (coretask.ScopedArtifact, bool) {
	for _, scoped := range scopedArtifacts(state) {
		if scoped.Artifact.ID == artifactID {
			return scoped, true
		}
	}
	return coretask.ScopedArtifact{}, false
}

func normalizeArtifactScope(state runtimetask.State, executionID coretask.ExecutionID, stepID coretask.StepID) (coretask.ExecutionID, coretask.StepID) {
	if stepID == "" {
		return executionID, stepID
	}
	if executionID != "" {
		return executionID, stepID
	}
	if state.CurrentExecution != "" {
		return state.CurrentExecution, stepID
	}
	return coretask.ExecutionID("manual"), stepID
}

func ensureArtifactID(state runtimetask.State, artifact *coretask.ArtifactSpec) {
	for artifact.ID == "" {
		id := newID(artifactPrefix)
		if artifactIDExists(state, id) {
			continue
		}
		artifact.ID = id
	}
}

func artifactIDExists(state runtimetask.State, artifactID string) bool {
	if artifactID == "" {
		return false
	}
	_, ok := artifactScopeForID(state, artifactID)
	return ok
}

func applyModification(taskID coretask.ID, batchReason string, item coretask.TaskModification, state *runtimetask.State) ([]event.Event, error) {
	reason := firstNonEmpty(item.Reason, batchReason)
	switch strings.TrimSpace(item.Op) {
	case "update_metadata":
		next := cloneTask(state.Task)
		if item.Title != "" {
			next.Title = strings.TrimSpace(item.Title)
		}
		if item.Description != "" {
			next.Description = strings.TrimSpace(item.Description)
		}
		if item.Objective != "" {
			next.Objective = strings.TrimSpace(item.Objective)
		}
		if len(item.AcceptanceCriteria) > 0 {
			next.AcceptanceCriteria = append([]string(nil), item.AcceptanceCriteria...)
		}
		if len(item.Inputs) > 0 {
			next.Inputs = cloneArtifacts(item.Inputs)
		}
		if len(item.Outputs) > 0 {
			next.Outputs = cloneArtifacts(item.Outputs)
		}
		if len(item.Scope) > 0 {
			next.Scope = append([]string(nil), item.Scope...)
		}
		if len(item.Constraints) > 0 {
			next.Constraints = append([]string(nil), item.Constraints...)
		}
		if len(item.Labels) > 0 {
			next.Labels = append([]string(nil), item.Labels...)
		}
		if item.Priority != "" {
			next.Priority = item.Priority
		}
		if item.Assignee != "" {
			next.Assignee = item.Assignee
		}
		if item.Owner != "" {
			next.Owner = item.Owner
		}
		if item.WorkspaceID != "" {
			next.WorkspaceID = item.WorkspaceID
		}
		if item.ProjectID != "" {
			next.ProjectID = item.ProjectID
		}
		if item.WorkflowRef != "" {
			next.WorkflowRef = item.WorkflowRef
		}
		if len(item.Metadata) > 0 {
			if next.Metadata == nil {
				next.Metadata = map[string]string{}
			}
			for k, v := range item.Metadata {
				next.Metadata[k] = v
			}
		}
		return revise(taskID, next, reason, state)
	case "add_acceptance_criterion":
		if strings.TrimSpace(item.Criterion) == "" {
			return nil, fmt.Errorf("acceptance criterion is required")
		}
		next := cloneTask(state.Task)
		next.AcceptanceCriteria = append(append([]string(nil), next.AcceptanceCriteria...), strings.TrimSpace(item.Criterion))
		return revise(taskID, next, reason, state)
	case "add_output":
		if err := validateArtifact(item.Output); err != nil {
			return nil, err
		}
		next := cloneTask(state.Task)
		artifact := item.Output
		if artifact.ID == "" {
			artifact.ID = newID(artifactPrefix)
		}
		next.Outputs = append(append([]coretask.ArtifactSpec(nil), next.Outputs...), artifact)
		return revise(taskID, next, reason, state)
	case "add_step":
		if strings.TrimSpace(string(item.Step.ID)) == "" {
			return nil, fmt.Errorf("step id is required")
		}
		next := cloneTask(state.Task)
		next.Steps = append(cloneSteps(next.Steps), cloneStep(item.Step))
		return revise(taskID, next, reason, state)
	case "update_step":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		next := cloneTask(state.Task)
		steps := cloneSteps(next.Steps)
		found := false
		step := cloneStep(item.Step)
		if step.ID == "" {
			step.ID = item.StepID
		}
		for i := range steps {
			if steps[i].ID == item.StepID {
				steps[i] = step
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		next.Steps = steps
		return revise(taskID, next, reason, state)
	case "remove_step":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		if stepHasExecutionState(*state, item.StepID) {
			return nil, fmt.Errorf("step %q has execution state", item.StepID)
		}
		next := cloneTask(state.Task)
		steps := make([]coretask.Step, 0, len(next.Steps))
		found := false
		for _, step := range next.Steps {
			if step.ID == item.StepID {
				found = true
				continue
			}
			steps = append(steps, cloneStep(step))
		}
		if !found {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		next.Steps = steps
		return revise(taskID, next, reason, state)
	case "set_step_status":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		if !stepExists(state.Task.Steps, item.StepID) {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		if item.StepStatus == "" || !coretask.ValidStepStatus(item.StepStatus) {
			return nil, fmt.Errorf("step status %q is invalid", item.StepStatus)
		}
		executionID := item.ExecutionID
		if executionID == "" {
			executionID = state.CurrentExecution
		}
		if executionID == "" {
			executionID = coretask.ExecutionID("manual")
		}
		previous := coretask.StepStatus("")
		if exec, ok := state.Executions[executionID]; ok {
			previous = exec.Steps[item.StepID].Status
		}
		if coretask.StepTerminal(previous) && activeStepStatus(item.StepStatus) {
			return nil, fmt.Errorf("step %q is terminal; use reopen_step", item.StepID)
		}
		payload := coretask.StepStatusChanged{TaskID: taskID, ExecutionID: executionID, StepID: item.StepID, Previous: previous, Current: item.StepStatus, Reason: reason, Output: item.StepOutput}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "add_artifact":
		executionID, stepID := normalizeArtifactScope(*state, item.ExecutionID, item.StepID)
		if stepID != "" && !stepExists(state.Task.Steps, stepID) {
			return nil, fmt.Errorf("step %q not found", stepID)
		}
		if err := validateArtifact(item.Artifact); err != nil {
			return nil, err
		}
		artifact := item.Artifact
		if artifact.ID != "" && artifactIDExists(*state, artifact.ID) {
			return nil, fmt.Errorf("artifact %q already exists", artifact.ID)
		}
		ensureArtifactID(*state, &artifact)
		payload := coretask.ArtifactAdded{TaskID: taskID, ExecutionID: executionID, StepID: stepID, Artifact: artifact}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "update_artifact":
		if item.ArtifactID == "" {
			return nil, fmt.Errorf("artifact_id is required")
		}
		scoped, found := artifactScopeForID(*state, item.ArtifactID)
		if !found {
			return nil, fmt.Errorf("artifact %q not found", item.ArtifactID)
		}
		if item.ExecutionID != "" && item.ExecutionID != scoped.ExecutionID {
			return nil, fmt.Errorf("artifact %q not found in execution %q", item.ArtifactID, item.ExecutionID)
		}
		if item.StepID != "" && item.StepID != scoped.StepID {
			return nil, fmt.Errorf("artifact %q not found in step %q", item.ArtifactID, item.StepID)
		}
		if err := validateArtifact(item.Artifact); err != nil {
			return nil, err
		}
		artifact := item.Artifact
		if artifact.ID == "" {
			artifact.ID = item.ArtifactID
		} else if artifact.ID != item.ArtifactID && artifactIDExists(*state, artifact.ID) {
			return nil, fmt.Errorf("artifact %q already exists", artifact.ID)
		}
		payload := coretask.ArtifactUpdated{TaskID: taskID, ExecutionID: scoped.ExecutionID, StepID: scoped.StepID, ArtifactID: item.ArtifactID, Artifact: artifact}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "remove_artifact":
		if item.ArtifactID == "" {
			return nil, fmt.Errorf("artifact_id is required")
		}
		scoped, found := artifactScopeForID(*state, item.ArtifactID)
		if !found {
			return nil, fmt.Errorf("artifact %q not found", item.ArtifactID)
		}
		if item.ExecutionID != "" && item.ExecutionID != scoped.ExecutionID {
			return nil, fmt.Errorf("artifact %q not found in execution %q", item.ArtifactID, item.ExecutionID)
		}
		if item.StepID != "" && item.StepID != scoped.StepID {
			return nil, fmt.Errorf("artifact %q not found in step %q", item.ArtifactID, item.StepID)
		}
		payload := coretask.ArtifactRemoved{TaskID: taskID, ExecutionID: scoped.ExecutionID, StepID: scoped.StepID, ArtifactID: item.ArtifactID, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "set_status":
		if item.Status == "" || !coretask.ValidStatus(item.Status) {
			return nil, fmt.Errorf("task status %q is invalid", item.Status)
		}
		if state.Task.Status == item.Status {
			return nil, nil
		}
		if coretask.Terminal(state.Task.Status) && activeTaskStatus(item.Status) {
			return nil, fmt.Errorf("task is terminal; use reopen")
		}
		if state.Task.Status == coretask.StatusRunning && (item.Status == coretask.StatusReady || item.Status == coretask.StatusDraft) {
			return nil, fmt.Errorf("task is running; wait for scheduler completion or interruption before setting %s", item.Status)
		}
		payload := coretask.StatusChanged{TaskID: taskID, Previous: state.Task.Status, Current: item.Status, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "reopen":
		if !coretask.Terminal(state.Task.Status) {
			return nil, fmt.Errorf("task is not terminal")
		}
		status := item.Status
		if status == "" {
			status = coretask.StatusReady
		}
		if !activeTaskStatus(status) {
			return nil, fmt.Errorf("reopen status %q is invalid", status)
		}
		payload := coretask.StatusChanged{TaskID: taskID, Previous: state.Task.Status, Current: status, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "reopen_step":
		if item.StepID == "" {
			return nil, fmt.Errorf("step_id is required")
		}
		if !stepExists(state.Task.Steps, item.StepID) {
			return nil, fmt.Errorf("step %q not found", item.StepID)
		}
		executionID := item.ExecutionID
		if executionID == "" {
			executionID = state.CurrentExecution
		}
		if executionID == "" {
			executionID = coretask.ExecutionID("manual")
		}
		previous := coretask.StepStatus("")
		if exec, ok := state.Executions[executionID]; ok {
			previous = exec.Steps[item.StepID].Status
		}
		if !coretask.StepTerminal(previous) {
			return nil, fmt.Errorf("step %q is not terminal", item.StepID)
		}
		status := item.StepStatus
		if status == "" {
			status = coretask.StepStatusWaiting
		}
		if !activeStepStatus(status) {
			return nil, fmt.Errorf("reopen step status %q is invalid", status)
		}
		payload := coretask.StepStatusChanged{TaskID: taskID, ExecutionID: executionID, StepID: item.StepID, Previous: previous, Current: status, Reason: reason}
		*state = runtimetask.Apply(*state, payload, timeNow())
		return []event.Event{payload}, nil
	case "complete":
		events := make([]event.Event, 0, len(artifactList(item))+1)
		for _, artifact := range artifactList(item) {
			if err := validateArtifact(artifact); err != nil {
				return nil, err
			}
			if artifact.ID != "" && artifactIDExists(*state, artifact.ID) {
				return nil, fmt.Errorf("artifact %q already exists", artifact.ID)
			}
			ensureArtifactID(*state, &artifact)
			payload := coretask.ArtifactAdded{TaskID: taskID, Artifact: artifact}
			*state = runtimetask.Apply(*state, payload, timeNow())
			events = append(events, payload)
		}
		validation := validateState(*state)
		if !completionAllowed(validation, item.ForceOverrides) {
			return nil, fmt.Errorf("task is not completable")
		}
		if state.Task.Status != coretask.StatusCompleted {
			payload := coretask.StatusChanged{TaskID: taskID, Previous: state.Task.Status, Current: coretask.StatusCompleted, Reason: reason}
			*state = runtimetask.Apply(*state, payload, timeNow())
			events = append(events, payload)
		}
		return events, nil
	default:
		return nil, fmt.Errorf("unsupported task modification %q", item.Op)
	}
}

func artifactList(m coretask.TaskModification) []coretask.ArtifactSpec {
	if len(m.Artifacts) > 0 {
		return m.Artifacts
	}
	if len(m.Outputs) > 0 {
		return m.Outputs
	}
	if m.Artifact.Name != "" || m.Artifact.Description != "" || m.Artifact.Ref != "" || m.Artifact.ID != "" {
		return []coretask.ArtifactSpec{m.Artifact}
	}
	return nil
}

func revise(taskID coretask.ID, next coretask.Task, reason string, state *runtimetask.State) ([]event.Event, error) {
	if next.ID == "" {
		next.ID = taskID
	}
	if next.ID != taskID {
		return nil, fmt.Errorf("task id mismatch: %s != %s", next.ID, taskID)
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	payload := coretask.Revised{TaskID: taskID, Task: next, Reason: reason}
	*state = runtimetask.Apply(*state, payload, timeNow())
	return []event.Event{payload}, nil
}

func validateArtifact(artifact coretask.ArtifactSpec) error {
	if strings.TrimSpace(artifact.Name) == "" && strings.TrimSpace(artifact.Description) == "" && strings.TrimSpace(artifact.Ref) == "" {
		return fmt.Errorf("artifact requires name, description, or ref")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func timeNow() time.Time {
	return time.Now().UTC()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func defaultView(view coretask.View) coretask.View {
	if view == coretask.ViewSummary {
		return view
	}
	return coretask.ViewFull
}

func taskFromCreateRequest(req coretask.TaskCreateRequest) coretask.Task {
	title := strings.TrimSpace(req.Title)
	objective := strings.TrimSpace(req.Objective)
	if title == "" && objective == "" {
		objective = strings.TrimSpace(req.Instruction)
	}
	if title == "" {
		title = summaryTitle(objective)
	}
	if objective == "" {
		objective = title
	}
	return coretask.Task{
		ID:                 req.ID,
		Title:              title,
		Description:        strings.TrimSpace(req.Description),
		Objective:          objective,
		AcceptanceCriteria: append([]string(nil), req.AcceptanceCriteria...),
		Inputs:             cloneArtifacts(req.Inputs),
		Outputs:            cloneArtifacts(req.Outputs),
		Scope:              append([]string(nil), req.Scope...),
		Constraints:        append([]string(nil), req.Constraints...),
		Status:             req.Status,
		Priority:           req.Priority,
		Assignee:           req.Assignee,
		Owner:              req.Owner,
		WorkspaceID:        req.WorkspaceID,
		ProjectID:          req.ProjectID,
		WorkflowRef:        req.WorkflowRef,
		Steps:              cloneSteps(req.SuggestedSteps),
		Labels:             append([]string(nil), req.Labels...),
		Metadata:           cloneStringMap(req.Metadata),
	}
}

func summaryTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(text) <= 80 {
		return text
	}
	return strings.TrimSpace(text[:80])
}

func cloneSteps(in []coretask.Step) []coretask.Step {
	if len(in) == 0 {
		return nil
	}
	out := make([]coretask.Step, len(in))
	for i, step := range in {
		out[i] = cloneStep(step)
	}
	return out
}

func cloneTask(task coretask.Task) coretask.Task {
	out := task
	out.AcceptanceCriteria = append([]string(nil), task.AcceptanceCriteria...)
	out.Inputs = cloneArtifacts(task.Inputs)
	out.Outputs = cloneArtifacts(task.Outputs)
	out.Artifacts = cloneArtifacts(task.Artifacts)
	out.Scope = append([]string(nil), task.Scope...)
	out.Constraints = append([]string(nil), task.Constraints...)
	out.Steps = cloneSteps(task.Steps)
	out.Labels = append([]string(nil), task.Labels...)
	out.Metadata = cloneStringMap(task.Metadata)
	return out
}

func cloneStep(step coretask.Step) coretask.Step {
	out := step
	out.AcceptanceCriteria = append([]string(nil), step.AcceptanceCriteria...)
	out.Inputs = cloneArtifacts(step.Inputs)
	out.Outputs = cloneArtifacts(step.Outputs)
	out.DependsOn = append([]coretask.StepID(nil), step.DependsOn...)
	out.Scope = append([]string(nil), step.Scope...)
	out.Metadata = cloneStringMap(step.Metadata)
	return out
}

func cloneArtifacts(in []coretask.ArtifactSpec) []coretask.ArtifactSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]coretask.ArtifactSpec, len(in))
	for i, artifact := range in {
		out[i] = artifact
		out[i].Metadata = cloneStringMap(artifact.Metadata)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%x", prefix, strings.ReplaceAll(err.Error(), " ", "_"))
	}
	return prefix + hex.EncodeToString(b[:])
}
