package planexecplugin

import (
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
		if strings.TrimSpace(input.WorkerID) == "" {
			return operation.Failed("delegate_worker_id_required", "worker_id is required", nil)
		}
		if result, err := scope.Supervisor.Result(subagent.ID(input.WorkerID)); err == nil {
			return rendered(firstNonEmpty(result.Output, result.Error, "done"), result)
		}
		workers := p.delegateWorkerMap(ctx, scope)
		worker, ok := workers[subagent.ID(input.WorkerID)]
		if !ok {
			return operation.Failed("delegate_result_failed", fmt.Sprintf("subagent: worker %q not found", input.WorkerID), nil)
		}
		if worker.Status == subagent.StatusPrepared || worker.Status == subagent.StatusRunning {
			return operation.Failed("delegate_result_failed", fmt.Sprintf("subagent: worker %q is %s", input.WorkerID, worker.Status), nil)
		}
		result := subagent.Result{Handle: worker, Output: worker.Output, Error: worker.Error}
		return rendered(firstNonEmpty(result.Output, result.Error, "done"), result)
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
	})
	if err != nil {
		return operation.Failed("delegate_spawn_failed", err.Error(), nil)
	}
	return rendered(fmt.Sprintf("Spawned %s using %s.", handle.ID, handle.Profile.Name), handle)
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
