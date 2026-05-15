package planexecplugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
)

type delegateInput struct {
	Action   string            `json:"action" jsonschema:"required,enum=spawn,enum=status,enum=result,enum=cancel"`
	Profile  string            `json:"profile,omitempty"`
	Task     string            `json:"task,omitempty"`
	Scope    []string          `json:"scope,omitempty"`
	Timeout  string            `json:"timeout,omitempty"`
	WorkerID string            `json:"worker_id,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (p *Plugin) delegate(ctx operation.Context, input delegateInput) operation.Result {
	scope, ok := subagent.ScopeFromContext(ctx)
	if !ok {
		return operation.Failed("delegate_unavailable", "sub-agent supervisor is not available in this session", nil)
	}
	switch input.Action {
	case "spawn":
		return p.delegateSpawn(ctx, scope, input)
	case "status":
		return rendered("Sub-agent status.", map[string]any{"workers": p.delegateWorkers(ctx, scope)})
	case "result":
		return p.delegateResult(ctx, scope, input)
	case "cancel":
		if strings.TrimSpace(input.WorkerID) == "" {
			return operation.Failed("delegate_worker_id_required", "worker_id is required", nil)
		}
		if err := scope.Supervisor.Cancel(subagent.ID(input.WorkerID), "cancelled by delegate operation"); err != nil {
			return operation.Failed("delegate_cancel_failed", err.Error(), nil)
		}
		return rendered(fmt.Sprintf("Cancelled %s.", input.WorkerID), map[string]any{"worker_id": input.WorkerID})
	default:
		return operation.Failed("delegate_action_invalid", "action must be spawn, status, result, or cancel", nil)
	}
}

func (p *Plugin) delegateResult(ctx operation.Context, scope subagent.Scope, input delegateInput) operation.Result {
	if strings.TrimSpace(input.WorkerID) == "" {
		return operation.Failed("delegate_worker_id_required", "worker_id is required", nil)
	}
	id := subagent.ID(input.WorkerID)
	if result, err := scope.Supervisor.Result(id); err == nil {
		return rendered(renderDelegateResult(result), result)
	}
	timeout, err := parseDuration(input.Timeout)
	if err != nil {
		return operation.Failed("delegate_timeout_invalid", err.Error(), nil)
	}
	worker, ok := p.delegateWorkerMap(ctx, scope)[id]
	if !ok {
		return operation.Failed("delegate_result_failed", fmt.Sprintf("subagent: worker %q not found", input.WorkerID), nil)
	}
	if worker.Status == subagent.StatusPrepared || worker.Status == subagent.StatusRunning {
		if timeout > 0 {
			waitCtx, cancel := context.WithTimeout(ctx, timeout)
			result, waitErr := scope.Supervisor.Wait(waitCtx, id)
			cancel()
			if waitErr == nil {
				return rendered(renderDelegateResult(result), result)
			}
			if !errors.Is(waitErr, context.DeadlineExceeded) && !errors.Is(waitErr, context.Canceled) {
				return operation.Failed("delegate_result_failed", waitErr.Error(), nil)
			}
			if refreshed, ok := p.delegateWorkerMap(ctx, scope)[id]; ok {
				worker = refreshed
			}
		}
		return rendered(string(worker.Status), subagent.Result{Handle: worker, Output: worker.Output, Error: worker.Error})
	}
	result := subagent.Result{Handle: worker, Output: worker.Output, Error: worker.Error}
	return rendered(renderDelegateResult(result), result)
}

func (p *Plugin) delegateWorkers(ctx operation.Context, scope subagent.Scope) []subagent.Handle {
	return sortedWorkers(p.delegateWorkerMap(ctx, scope))
}

func (p *Plugin) delegateWorkerMap(ctx operation.Context, scope subagent.Scope) map[subagent.ID]subagent.Handle {
	workers := map[subagent.ID]subagent.Handle{}
	records, available, err := runtimeRecords(ctx, scope)
	if err == nil && available {
		workers = projectWorkers(records)
	}
	for _, active := range scope.Supervisor.Status() {
		workers[active.ID] = active
	}
	return workers
}

func (p *Plugin) delegateSpawn(ctx operation.Context, scope subagent.Scope, input delegateInput) operation.Result {
	if strings.TrimSpace(input.Task) == "" {
		return operation.Failed("delegate_task_required", "task is required", nil)
	}
	timeout, err := parseDuration(input.Timeout)
	if err != nil {
		return operation.Failed("delegate_timeout_invalid", err.Error(), nil)
	}
	task := input.Task
	if len(input.Scope) > 0 {
		task += "\n\nScope:\n"
		for _, item := range input.Scope {
			task += "- " + item + "\n"
		}
	}
	handle, err := scope.Supervisor.Spawn(ctx, subagent.SpawnRequest{
		Profile:        coresession.Ref{Name: coresession.Name(input.Profile)},
		Task:           task,
		Timeout:        timeout,
		Policy:         scope.Policy,
		ParentThreadID: scope.ParentThreadID,
		ParentRunID:    scope.ParentRunID,
		ParentCallID:   scope.ParentCallID,
		Metadata:       input.Metadata,
		Events:         scope.Events,
		Approver:       scope.Approver,
	})
	if err != nil {
		return operation.Failed("delegate_spawn_failed", err.Error(), delegateSpawnFailureDetails(input, scope.Policy))
	}
	return rendered(renderDelegateSpawn(handle), handle)
}

func renderDelegateSpawn(handle subagent.Handle) string {
	text := fmt.Sprintf("Spawned %s using %s.", handle.ID, handle.Profile.Name)
	if handle.TimeoutClamped && handle.MaxTimeout != "" {
		text += " Timeout clamped to " + handle.MaxTimeout + "."
	}
	return text
}

func renderDelegateResult(result subagent.Result) string {
	switch result.Handle.Status {
	case subagent.StatusFailed:
		return "Sub-agent failed: " + firstNonEmpty(result.Error, result.Handle.Error, "unknown error")
	case subagent.StatusCancelled:
		return "Sub-agent cancelled: " + firstNonEmpty(result.Error, result.Handle.Error, "cancelled")
	default:
		return firstNonEmpty(result.Output, result.Error, "done")
	}
}

func delegateSpawnFailureDetails(input delegateInput, policy coresession.DelegationPolicy) map[string]any {
	details := map[string]any{
		"requested_profile": strings.TrimSpace(input.Profile),
	}
	if profiles := allowedProfileNames(policy.AllowedProfiles); len(profiles) > 0 {
		details["allowed_profiles"] = profiles
	}
	return details
}

func allowedProfileNames(profiles []coresession.Ref) []string {
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Name != "" {
			out = append(out, string(profile.Name))
		}
	}
	return out
}

func parseDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func rendered(text string, data any) operation.Result {
	return operation.OK(operation.Rendered{Text: text, Data: data})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
