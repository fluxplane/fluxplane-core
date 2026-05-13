package planexecplugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
)

type planInput struct {
	Actions []planAction `json:"actions" jsonschema:"required,minItems=1"`
}

type planAction struct {
	Action      string     `json:"action" jsonschema:"required,enum=create,enum=revise,enum=execute,enum=status,enum=step_output,enum=cancel"`
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Steps       []StepSpec `json:"steps,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	StepID      string     `json:"step_id,omitempty"`
}

func (p *Plugin) planOperation(ctx operation.Context, input planInput) operation.Result {
	if len(input.Actions) == 0 {
		return operation.Failed("plan_actions_required", "at least one action is required", nil)
	}
	var last any
	for _, action := range input.Actions {
		result := p.applyPlanAction(ctx, action)
		if result.IsError() {
			return result
		}
		last = result.Output
	}
	return operation.OK(last)
}

func (p *Plugin) applyPlanAction(ctx operation.Context, action planAction) operation.Result {
	switch action.Action {
	case "create":
		return p.createPlan(ctx, action)
	case "revise":
		return p.revisePlan(ctx, action)
	case "execute":
		return p.executePlan(ctx)
	case "status":
		return rendered("Plan status.", p.stateForContext(ctx))
	case "step_output":
		return p.stepOutput(ctx, action.StepID)
	case "cancel":
		return p.cancelPlan(ctx, action.Reason)
	default:
		return operation.Failed("plan_action_invalid", "unknown plan action "+action.Action, nil)
	}
}

func (p *Plugin) createPlan(ctx operation.Context, action planAction) operation.Result {
	spec := PlanSpec{Title: action.Title, Description: action.Description, Steps: action.Steps}
	if err := validateSpec(spec); err != nil {
		return operation.Failed("plan_invalid", err.Error(), nil)
	}
	current := p.stateForContext(ctx)
	p.mu.Lock()
	if current.ID != "" && current.Phase != PhaseCompleted && current.Phase != PhaseFailed && current.Phase != PhaseCancelled {
		p.mu.Unlock()
		return operation.Failed("plan_exists", "a plan already exists; use revise, status, execute, cancel, or wait for completion", nil)
	}
	state := PlanState{ID: p.nextPlanIDLocked(), Phase: PhaseDrafting, Spec: cloneSpec(spec), CreatedAt: time.Now().UTC()}
	p.plan = state
	p.mu.Unlock()
	emitEvent(ctx, PlanCreated{PlanID: state.ID, Spec: state.Spec})
	return rendered("Created plan "+state.ID+".", state)
}

func (p *Plugin) revisePlan(ctx operation.Context, action planAction) operation.Result {
	spec := PlanSpec{Title: action.Title, Description: action.Description, Steps: action.Steps}
	if err := validateSpec(spec); err != nil {
		return operation.Failed("plan_invalid", err.Error(), nil)
	}
	current := p.stateForContext(ctx)
	p.mu.Lock()
	p.plan = current
	if p.plan.ID == "" {
		p.mu.Unlock()
		return operation.Failed("plan_missing", "no plan exists", nil)
	}
	if p.plan.Phase == PhaseExecuting {
		p.mu.Unlock()
		return operation.Failed("plan_executing", "cannot revise a plan while it is executing", nil)
	}
	p.plan.Spec = cloneSpec(spec)
	p.plan.Phase = PhaseDrafting
	p.plan.Steps = nil
	p.plan.Error = ""
	state := cloneState(p.plan)
	p.mu.Unlock()
	emitEvent(ctx, PlanRevised{PlanID: state.ID, Spec: state.Spec, Reason: action.Reason})
	return rendered("Revised plan "+state.ID+".", state)
}

func (p *Plugin) executePlan(ctx operation.Context) operation.Result {
	scope, ok := subagent.ScopeFromContext(ctx)
	if !ok {
		return operation.Failed("plan_executor_unavailable", "sub-agent supervisor is not available in this session", nil)
	}
	current := p.stateForContext(ctx)
	p.mu.Lock()
	p.plan = current
	if p.plan.ID == "" {
		p.mu.Unlock()
		return operation.Failed("plan_missing", "no plan exists", nil)
	}
	if p.plan.Phase != PhaseDrafting {
		p.mu.Unlock()
		return operation.Failed("plan_not_drafting", "plan must be in drafting phase to execute", map[string]any{"phase": p.plan.Phase})
	}
	p.plan.Phase = PhaseExecuting
	p.plan.Steps = make(map[string]StepExec, len(p.plan.Spec.Steps))
	for _, step := range p.plan.Spec.Steps {
		p.plan.Steps[step.ID] = StepExec{Status: StepStatusWaiting, Profile: step.Profile}
	}
	state := cloneState(p.plan)
	p.mu.Unlock()
	emitEvent(ctx, PlanExecutionStarted{PlanID: state.ID})
	return p.runPlan(ctx, scope, state.ID)
}

func (p *Plugin) runPlan(ctx operation.Context, scope subagent.Scope, planID string) operation.Result {
	completed := make(chan stepResult, 16)
	running := 0
	for {
		state := p.state()
		if state.ID != planID || state.Phase != PhaseExecuting {
			return rendered("Plan stopped.", state)
		}
		if allSteps(state, StepStatusCompleted) {
			p.finishPlan(ctx, PhaseCompleted, "")
			return rendered("Plan completed.", p.state())
		}
		if anyStepFailed(state) {
			p.cancelWaitingDependents(ctx, state)
			p.finishPlan(ctx, PhaseFailed, "one or more steps failed")
			return operation.Failed("plan_failed", "one or more steps failed", map[string]any{"plan": p.state()})
		}
		dispatched := 0
		for _, step := range readySteps(state) {
			if err := p.dispatchStep(ctx, scope, state.ID, step, completed); err != nil {
				if strings.Contains(err.Error(), "at capacity") {
					break
				}
				p.markStepFailed(ctx, state.ID, step.ID, err.Error())
				continue
			}
			dispatched++
			running++
		}
		if running == 0 && dispatched == 0 {
			p.finishPlan(ctx, PhaseFailed, "no runnable steps remain")
			return operation.Failed("plan_stalled", "no runnable steps remain", map[string]any{"plan": p.state()})
		}
		select {
		case result := <-completed:
			running--
			if state := p.state(); state.ID != planID || state.Phase != PhaseExecuting {
				return rendered("Plan stopped.", state)
			}
			if result.err != "" {
				p.markStepFailed(ctx, planID, result.stepID, result.err)
			} else {
				p.markStepCompleted(ctx, planID, result.stepID, result.output)
			}
		case <-ctx.Done():
			p.cancelActiveSteps(ctx, scope, p.state(), ctx.Err().Error())
			p.finishPlan(ctx, PhaseCancelled, ctx.Err().Error())
			return operation.Canceled(ctx.Err().Error())
		}
	}
}

type stepResult struct {
	stepID string
	output string
	err    string
}

func (p *Plugin) dispatchStep(ctx operation.Context, scope subagent.Scope, planID string, step StepSpec, completed chan<- stepResult) error {
	profile := step.Profile
	if strings.TrimSpace(profile) == "" {
		profile = string(WorkerSession)
	}
	task := planStepTask(step)
	prepared, err := scope.Supervisor.Prepare(ctx, subagent.SpawnRequest{
		ID:             subagent.ID(planID + ":" + step.ID),
		Profile:        coresession.Ref{Name: coresession.Name(profile)},
		Task:           task,
		TaskID:         step.ID,
		Policy:         scope.Policy,
		ParentThreadID: scope.ParentThreadID,
		ParentRunID:    scope.ParentRunID,
		ParentCallID:   scope.ParentCallID,
		Events: event.SinkFunc(func(payload event.Event) {
			scope.Events.Emit(payload)
			if progressed, ok := payload.(subagent.Progressed); ok {
				emitEvent(ctx, StepProgressed{PlanID: planID, StepID: step.ID, Message: progressed.Message})
			}
		}),
	})
	if err != nil {
		return err
	}
	p.mu.Lock()
	exec := p.plan.Steps[step.ID]
	exec.Status = StepStatusRunning
	exec.WorkerID = string(prepared.Handle.ID)
	exec.Profile = profile
	exec.StartedAt = time.Now().UTC()
	p.plan.Steps[step.ID] = exec
	p.mu.Unlock()
	emitEvent(ctx, StepDispatched{
		PlanID: planID, StepID: step.ID, Title: step.Title,
		WorkerID: prepared.Handle.ID, Profile: profile,
		Cause: subagent.CausationFromHandle(prepared.Handle),
	})
	prepared.Start()
	go func() {
		result, err := scope.Supervisor.Wait(ctx, prepared.Handle.ID)
		if err != nil {
			sendStepResult(ctx, completed, stepResult{stepID: step.ID, err: err.Error()})
			return
		}
		if result.Error != "" {
			sendStepResult(ctx, completed, stepResult{stepID: step.ID, err: result.Error})
			return
		}
		sendStepResult(ctx, completed, stepResult{stepID: step.ID, output: result.Output})
	}()
	return nil
}

func sendStepResult(ctx context.Context, completed chan<- stepResult, result stepResult) {
	select {
	case completed <- result:
	case <-ctx.Done():
	}
}

func planStepTask(step StepSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s", step.Title, step.Description)
	if len(step.Scope) > 0 {
		b.WriteString("\n\nScope:\n")
		for _, item := range step.Scope {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	if step.Acceptance != "" {
		fmt.Fprintf(&b, "\n\nAcceptance criteria:\n%s\n", step.Acceptance)
	}
	return strings.TrimSpace(b.String())
}

func (p *Plugin) markStepCompleted(ctx operation.Context, planID, stepID, output string) {
	p.mu.Lock()
	exec := p.plan.Steps[stepID]
	exec.Status = StepStatusCompleted
	exec.Output = output
	exec.DoneAt = time.Now().UTC()
	p.plan.Steps[stepID] = exec
	p.mu.Unlock()
	emitEvent(ctx, StepCompleted{PlanID: planID, StepID: stepID, Output: output})
}

func (p *Plugin) markStepFailed(ctx operation.Context, planID, stepID, message string) {
	p.mu.Lock()
	exec := p.plan.Steps[stepID]
	exec.Status = StepStatusFailed
	exec.Error = message
	exec.DoneAt = time.Now().UTC()
	p.plan.Steps[stepID] = exec
	p.mu.Unlock()
	emitEvent(ctx, StepFailed{PlanID: planID, StepID: stepID, Error: message})
}

func (p *Plugin) markStepCancelled(ctx operation.Context, planID, stepID, reason string) {
	p.mu.Lock()
	exec := p.plan.Steps[stepID]
	exec.Status = StepStatusCancelled
	exec.Error = reason
	exec.DoneAt = time.Now().UTC()
	p.plan.Steps[stepID] = exec
	p.mu.Unlock()
	emitEvent(ctx, StepCancelled{PlanID: planID, StepID: stepID, Reason: reason})
}

func (p *Plugin) cancelWaitingDependents(ctx operation.Context, state PlanState) {
	failed := map[string]bool{}
	for id, exec := range state.Steps {
		if exec.Status == StepStatusFailed {
			failed[id] = true
		}
	}
	for {
		changed := false
		for _, step := range state.Spec.Steps {
			exec := state.Steps[step.ID]
			if exec.Status != StepStatusWaiting {
				continue
			}
			for _, dep := range step.DependsOn {
				if failed[dep] {
					exec.Status = StepStatusCancelled
					exec.Error = "dependency failed: " + dep
					state.Steps[step.ID] = exec
					failed[step.ID] = true
					changed = true
					emitEvent(ctx, StepCancelled{PlanID: state.ID, StepID: step.ID, Reason: exec.Error})
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	p.mu.Lock()
	for id, exec := range state.Steps {
		p.plan.Steps[id] = exec
	}
	p.mu.Unlock()
}

func (p *Plugin) finishPlan(ctx operation.Context, phase PlanPhase, reason string) {
	p.mu.Lock()
	p.plan.Phase = phase
	p.plan.Error = reason
	state := cloneState(p.plan)
	p.mu.Unlock()
	switch phase {
	case PhaseCompleted:
		emitEvent(ctx, PlanCompleted{PlanID: state.ID, Summary: state.Spec.Title})
	case PhaseFailed:
		emitEvent(ctx, PlanFailed{PlanID: state.ID, Reason: reason})
	case PhaseCancelled:
		emitEvent(ctx, PlanCancelled{PlanID: state.ID, Reason: reason})
	}
}

func (p *Plugin) stepOutput(ctx operation.Context, stepID string) operation.Result {
	state := p.stateForContext(ctx)
	if stepID == "" {
		return operation.Failed("plan_step_id_required", "step_id is required", nil)
	}
	exec, ok := state.Steps[stepID]
	if !ok {
		return operation.Failed("plan_step_missing", "step not found", map[string]any{"step_id": stepID})
	}
	return rendered(firstNonEmpty(exec.Output, exec.Error, string(exec.Status)), exec)
}

func (p *Plugin) cancelPlan(ctx operation.Context, reason string) operation.Result {
	if reason == "" {
		reason = "cancelled by plan operation"
	}
	state := p.stateForContext(ctx)
	if state.ID == "" {
		return operation.Failed("plan_missing", "no plan exists", nil)
	}
	p.mu.Lock()
	p.plan = state
	p.mu.Unlock()
	scope, _ := subagent.ScopeFromContext(ctx)
	p.cancelActiveSteps(ctx, scope, state, reason)
	p.finishPlan(ctx, PhaseCancelled, reason)
	return rendered("Plan cancelled.", p.state())
}

func (p *Plugin) cancelActiveSteps(ctx operation.Context, scope subagent.Scope, state PlanState, reason string) {
	if reason == "" {
		reason = "cancelled"
	}
	for _, step := range state.Spec.Steps {
		exec := state.Steps[step.ID]
		switch exec.Status {
		case StepStatusWaiting, StepStatusRunning:
			if exec.WorkerID != "" && scope.Supervisor != nil {
				_ = scope.Supervisor.Cancel(subagent.ID(exec.WorkerID), reason)
			}
			p.markStepCancelled(ctx, state.ID, step.ID, reason)
		}
	}
}

func (p *Plugin) stateForContext(ctx operation.Context) PlanState {
	scope, ok := subagent.ScopeFromContext(ctx)
	if !ok {
		return p.state()
	}
	records, available, err := runtimeRecords(ctx, scope)
	if err != nil || !available {
		return p.state()
	}
	state, seq := projectPlan(records)
	if state.ID == "" {
		return p.state()
	}
	p.mu.Lock()
	if seq > p.seq {
		p.seq = seq
	}
	p.plan = state
	p.mu.Unlock()
	return state
}

func (p *Plugin) state() PlanState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneState(p.plan)
}

func (p *Plugin) nextPlanIDLocked() string {
	p.seq++
	return fmt.Sprintf("plan_%d", p.seq)
}

func readySteps(state PlanState) []StepSpec {
	var ready []StepSpec
	for _, step := range state.Spec.Steps {
		if state.Steps[step.ID].Status != StepStatusWaiting {
			continue
		}
		ok := true
		for _, dep := range step.DependsOn {
			if state.Steps[dep].Status != StepStatusCompleted {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, step)
		}
	}
	return ready
}

func allSteps(state PlanState, status StepStatus) bool {
	if len(state.Steps) == 0 {
		return false
	}
	for _, exec := range state.Steps {
		if exec.Status != status {
			return false
		}
	}
	return true
}

func anyStepFailed(state PlanState) bool {
	for _, exec := range state.Steps {
		if exec.Status == StepStatusFailed {
			return true
		}
	}
	return false
}

func emitEvent(ctx operation.Context, payload event.Event) {
	if ctx == nil || payload == nil {
		return
	}
	ctx.Events().Emit(payload)
}
