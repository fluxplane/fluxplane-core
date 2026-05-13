package planexecplugin

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func TestDelegateSpawnUsesSubagentSupervisor(t *testing.T) {
	plugin := New()
	ctx := scopedOperationContext(t, "worker done")
	result := plugin.delegate(ctx, delegateInput{Action: "spawn", Profile: "worker", Task: "do it"})
	if result.IsError() {
		t.Fatalf("delegate failed: %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want operation.Rendered", result.Output)
	}
	if rendered.Text == "" {
		t.Fatal("rendered text is empty")
	}
}

func TestPlanExecuteDispatchesDAGSteps(t *testing.T) {
	plugin := New()
	var events []event.Event
	ctx := scopedOperationContextWithEvents(t, "worker done", func(evt event.Event) {
		events = append(events, evt)
	})
	result := plugin.planOperation(ctx, planInput{Actions: []planAction{
		{Action: "create", Title: "T", Steps: []StepSpec{
			{ID: "inspect", Title: "Inspect", Profile: "worker"},
			{ID: "patch", Title: "Patch", DependsOn: []string{"inspect"}, Profile: "worker"},
		}},
		{Action: "execute"},
		{Action: "wait"},
	}})
	if result.IsError() {
		t.Fatalf("plan failed: %#v", result.Error)
	}
	state := plugin.state()
	if state.Phase != PhaseCompleted {
		t.Fatalf("phase = %s, want completed", state.Phase)
	}
	if state.Steps["inspect"].Status != StepStatusCompleted || state.Steps["patch"].Status != StepStatusCompleted {
		t.Fatalf("steps = %#v, want completed", state.Steps)
	}
	if !hasEvent(events, EventStepDispatched) || !hasEvent(events, EventPlanCompleted) {
		t.Fatalf("events missing dispatch/completed: %#v", events)
	}
}

func TestPlanRejectsInvalidDAG(t *testing.T) {
	plugin := New()
	result := plugin.planOperation(operation.NewContext(context.Background(), nil), planInput{Actions: []planAction{
		{Action: "create", Title: "T", Steps: []StepSpec{
			{ID: "a", Title: "A", DependsOn: []string{"missing"}},
		}},
	}})
	if !result.IsError() {
		t.Fatal("plan create succeeded, want invalid DAG error")
	}
}

func TestPlanStatusReplaysPersistedEventsWithFreshPlugin(t *testing.T) {
	store, threadID := testThreadStore(t)
	ctx, _ := persistentOperationContext(t, store, threadID, "worker done")
	plugin := New()
	result := plugin.planOperation(ctx, planInput{Actions: []planAction{
		{Action: "create", Title: "Replay", Steps: []StepSpec{{ID: "one", Title: "One", Profile: "worker"}}},
		{Action: "execute"},
		{Action: "wait"},
	}})
	if result.IsError() {
		t.Fatalf("plan execute failed: %#v", result.Error)
	}

	freshCtx, _ := persistentOperationContext(t, store, threadID, "unused")
	status := New().planOperation(freshCtx, planInput{Actions: []planAction{{Action: "status"}}})
	if status.IsError() {
		t.Fatalf("plan status failed: %#v", status.Error)
	}
	rendered := status.Output.(operation.Rendered)
	state := rendered.Data.(PlanState)
	if state.Phase != PhaseCompleted || state.Steps["one"].Output != "worker done" {
		t.Fatalf("projected state = %#v, want completed output", state)
	}
}

func TestPlanExecuteReturnsBeforeBlockingStepFinishes(t *testing.T) {
	store, threadID := testThreadStore(t)
	client := newBlockingPlanClient()
	ctx, _ := persistentOperationContextWithClient(t, store, threadID, client)
	plugin := New()
	create := plugin.planOperation(ctx, planInput{Actions: []planAction{
		{Action: "create", Title: "Background", Steps: []StepSpec{{ID: "run", Title: "Run", Profile: "worker"}}},
	}})
	if create.IsError() {
		t.Fatalf("plan create failed: %#v", create.Error)
	}
	resultCh := make(chan operation.Result, 1)
	go func() {
		resultCh <- plugin.planOperation(ctx, planInput{Actions: []planAction{{Action: "execute"}}})
	}()
	select {
	case result := <-resultCh:
		if result.IsError() {
			t.Fatalf("execute failed: %#v", result.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("execute did not return while worker was blocked")
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	state := plugin.state()
	if state.Phase != PhaseExecuting {
		t.Fatalf("phase = %s, want executing", state.Phase)
	}
	client.finish()
	wait := plugin.planOperation(ctx, planInput{Actions: []planAction{{Action: "wait"}}})
	if wait.IsError() {
		t.Fatalf("wait failed: %#v", wait.Error)
	}
}

func TestPlanStatusReportsInterruptedWithoutActiveRunner(t *testing.T) {
	store, threadID := testThreadStore(t)
	sink := persistentSink(t, store, threadID)
	spec := PlanSpec{Title: "Interrupted", Steps: []StepSpec{{ID: "run", Title: "Run", Profile: "worker"}}}
	sink.Emit(PlanCreated{PlanID: "plan_1", Spec: spec})
	sink.Emit(PlanExecutionStarted{PlanID: "plan_1"})
	sink.Emit(StepDispatched{PlanID: "plan_1", StepID: "run", WorkerID: "plan_1:run", Profile: "worker"})

	ctx, _ := persistentOperationContext(t, store, threadID, "unused")
	status := New().planOperation(ctx, planInput{Actions: []planAction{{Action: "status"}}})
	if status.IsError() {
		t.Fatalf("status failed: %#v", status.Error)
	}
	state := status.Output.(operation.Rendered).Data.(PlanState)
	if state.Phase != PhaseInterrupted {
		t.Fatalf("phase = %s, want interrupted", state.Phase)
	}
}

func TestPlanExecuteResumesInterruptedIncompleteSteps(t *testing.T) {
	store, threadID := testThreadStore(t)
	sink := persistentSink(t, store, threadID)
	spec := PlanSpec{Title: "Resume", Steps: []StepSpec{
		{ID: "done", Title: "Done", Profile: "worker"},
		{ID: "run", Title: "Run", DependsOn: []string{"done"}, Profile: "worker"},
	}}
	sink.Emit(PlanCreated{PlanID: "plan_1", Spec: spec})
	sink.Emit(PlanExecutionStarted{PlanID: "plan_1"})
	sink.Emit(StepDispatched{PlanID: "plan_1", StepID: "done", WorkerID: "plan_1:done", Profile: "worker"})
	sink.Emit(StepCompleted{PlanID: "plan_1", StepID: "done", Output: "already done"})
	sink.Emit(StepDispatched{PlanID: "plan_1", StepID: "run", WorkerID: "plan_1:run", Profile: "worker"})

	ctx, _ := persistentOperationContext(t, store, threadID, "resumed")
	plugin := New()
	result := plugin.planOperation(ctx, planInput{Actions: []planAction{{Action: "execute"}, {Action: "wait"}}})
	if result.IsError() {
		t.Fatalf("resume execute failed: %#v", result.Error)
	}
	state := plugin.stateForContext(ctx)
	if state.Phase != PhaseCompleted {
		t.Fatalf("phase = %s, want completed", state.Phase)
	}
	if state.Steps["done"].Output != "already done" || state.Steps["run"].Output != "resumed" {
		t.Fatalf("steps = %#v, want completed resume outputs", state.Steps)
	}
}

func TestDelegateResultReplaysPersistedSubagentEventsWithFreshPlugin(t *testing.T) {
	store, threadID := testThreadStore(t)
	ctx, supervisor := persistentOperationContext(t, store, threadID, "delegated done")
	plugin := New()
	result := plugin.delegate(ctx, delegateInput{Action: "spawn", Profile: "worker", Task: "do it"})
	if result.IsError() {
		t.Fatalf("delegate spawn failed: %#v", result.Error)
	}
	handle := result.Output.(operation.Rendered).Data.(subagent.Handle)
	if _, err := supervisor.Wait(context.Background(), handle.ID); err != nil {
		t.Fatalf("wait worker: %v", err)
	}

	freshCtx, _ := persistentOperationContext(t, store, threadID, "unused")
	replayed := New().delegate(freshCtx, delegateInput{Action: "result", WorkerID: string(handle.ID)})
	if replayed.IsError() {
		t.Fatalf("delegate result failed: %#v", replayed.Error)
	}
	delegateResult := replayed.Output.(operation.Rendered).Data.(subagent.Result)
	if delegateResult.Output != "delegated done" {
		t.Fatalf("output = %q, want delegated done", delegateResult.Output)
	}
}

func TestPlanCancelCancelsRunningStep(t *testing.T) {
	store, threadID := testThreadStore(t)
	client := newBlockingPlanClient()
	ctx, supervisor := persistentOperationContextWithClient(t, store, threadID, client)
	plugin := New()
	create := plugin.planOperation(ctx, planInput{Actions: []planAction{
		{Action: "create", Title: "Cancelable", Steps: []StepSpec{{ID: "run", Title: "Run", Profile: "worker"}}},
	}})
	if create.IsError() {
		t.Fatalf("plan create failed: %#v", create.Error)
	}
	done := make(chan operation.Result, 1)
	go func() {
		done <- plugin.planOperation(ctx, planInput{Actions: []planAction{{Action: "execute"}}})
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancelled := plugin.planOperation(ctx, planInput{Actions: []planAction{{Action: "cancel", Reason: "stop"}}})
	if cancelled.IsError() {
		t.Fatalf("plan cancel failed: %#v", cancelled.Error)
	}
	client.finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("execute did not finish after cancel")
	}
	if handles := supervisor.Status(); len(handles) != 1 || handles[0].Status != subagent.StatusCancelled {
		t.Fatalf("handles = %#v, want one cancelled worker", handles)
	}
	state := plugin.state()
	if state.Phase != PhaseCancelled || state.Steps["run"].Status != StepStatusCancelled {
		t.Fatalf("state = %#v, want cancelled plan and step", state)
	}
}

func scopedOperationContext(t *testing.T, result string) operation.Context {
	t.Helper()
	return scopedOperationContextWithEvents(t, result, func(event.Event) {})
}

func scopedOperationContextWithEvents(t *testing.T, result string, sink func(event.Event)) operation.Context {
	t.Helper()
	supervisor := subagent.New(subagent.Config{Client: planFakeClient{result: result}})
	events := event.SinkFunc(sink)
	base := subagent.ContextWithScope(context.Background(), subagent.Scope{
		Supervisor: supervisor,
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			MaxParallel:     2,
		},
		Events: events,
	})
	return operation.NewContext(base, events)
}

func testThreadStore(t *testing.T) (corethread.Store, corethread.ID) {
	t.Helper()
	store, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	threadID := corethread.ID(strings.ReplaceAll(t.Name(), "/", "_"))
	if _, err := store.Create(context.Background(), corethread.CreateParams{ID: threadID}); err != nil {
		t.Fatalf("Create thread: %v", err)
	}
	return store, threadID
}

func persistentOperationContext(t *testing.T, store corethread.Store, threadID corethread.ID, result string) (operation.Context, *subagent.Supervisor) {
	t.Helper()
	return persistentOperationContextWithClient(t, store, threadID, planFakeClient{result: result})
}

func persistentOperationContextWithClient(t *testing.T, store corethread.Store, threadID corethread.ID, client subagent.Client) (operation.Context, *subagent.Supervisor) {
	t.Helper()
	events := persistentSink(t, store, threadID)
	supervisor := subagent.New(subagent.Config{Client: client})
	base := subagent.ContextWithScope(context.Background(), subagent.Scope{
		Supervisor:     supervisor,
		ParentThreadID: threadID,
		ParentRunID:    "run-1",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			MaxParallel:     2,
		},
		Events:      events,
		ThreadStore: store,
	})
	return operation.NewContext(base, events), supervisor
}

func persistentSink(t *testing.T, store corethread.Store, threadID corethread.ID) event.Sink {
	t.Helper()
	return event.SinkFunc(func(payload event.Event) {
		if payload == nil {
			return
		}
		name := payload.EventName()
		if !strings.HasPrefix(string(name), "plan.") && !strings.HasPrefix(string(name), "subagent.") {
			return
		}
		_, err := store.Append(context.Background(), corethread.Ref{ID: threadID}, corethread.AppendRecord{
			Event: event.Record{
				Name:    coresession.EventRuntimeEmitted,
				Payload: coresession.RuntimeEmitted{RunID: "run-1", Name: name, Payload: payload},
			},
		})
		if err != nil {
			t.Fatalf("append runtime event: %v", err)
		}
	})
}

func hasEvent(events []event.Event, name event.Name) bool {
	for _, evt := range events {
		if evt.EventName() == name {
			return true
		}
	}
	return false
}

type planFakeClient struct {
	result string
}

func (c planFakeClient) Open(context.Context, subagent.OpenRequest) (subagent.Session, error) {
	return planFakeSession(c), nil
}

type planFakeSession struct {
	result string
}

func (s planFakeSession) Info() subagent.SessionInfo { return subagent.SessionInfo{} }

func (s planFakeSession) SendInput(context.Context, subagent.Input) (subagent.Run, error) {
	ch := make(chan subagent.RunEvent)
	close(ch)
	return planFakeRun{result: s.result, events: ch}, nil
}

type planFakeRun struct {
	result string
	events <-chan subagent.RunEvent
}

func (r planFakeRun) ID() string { return "run_1" }

func (r planFakeRun) Events() <-chan subagent.RunEvent { return r.events }

func (r planFakeRun) Wait(context.Context) (subagent.RunResult, error) {
	return subagent.RunResult{Text: r.result}, nil
}

type blockingPlanClient struct {
	once    sync.Once
	started chan struct{}
	done    chan struct{}
}

func newBlockingPlanClient() *blockingPlanClient {
	return &blockingPlanClient{started: make(chan struct{}), done: make(chan struct{})}
}

func (c *blockingPlanClient) Open(context.Context, subagent.OpenRequest) (subagent.Session, error) {
	return blockingPlanSession{client: c}, nil
}

func (c *blockingPlanClient) finish() {
	close(c.done)
}

type blockingPlanSession struct {
	client *blockingPlanClient
}

func (s blockingPlanSession) Info() subagent.SessionInfo { return subagent.SessionInfo{} }

func (s blockingPlanSession) SendInput(context.Context, subagent.Input) (subagent.Run, error) {
	s.client.once.Do(func() { close(s.client.started) })
	events := make(chan subagent.RunEvent)
	return blockingPlanRun{client: s.client, events: events}, nil
}

type blockingPlanRun struct {
	client *blockingPlanClient
	events chan subagent.RunEvent
}

func (r blockingPlanRun) ID() string { return "run_1" }

func (r blockingPlanRun) Events() <-chan subagent.RunEvent {
	go func() {
		<-r.client.done
		close(r.events)
	}()
	return r.events
}

func (r blockingPlanRun) Wait(ctx context.Context) (subagent.RunResult, error) {
	select {
	case <-r.client.done:
		return subagent.RunResult{Text: "finished"}, nil
	case <-ctx.Done():
		return subagent.RunResult{}, ctx.Err()
	}
}
