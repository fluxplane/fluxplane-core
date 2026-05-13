package planexecplugin

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
)

type runtimeRecord struct {
	Name    event.Name
	Payload any
	At      time.Time
}

func runtimeRecords(ctx context.Context, scope subagent.Scope) ([]runtimeRecord, bool, error) {
	if scope.ThreadStore == nil || scope.ParentThreadID == "" {
		return nil, false, nil
	}
	snapshot, err := scope.ThreadStore.Read(ctx, corethread.ReadParams{ID: scope.ParentThreadID})
	if err != nil {
		return nil, true, err
	}
	records, err := snapshot.EventsForBranch(snapshot.BranchID)
	if err != nil {
		return nil, true, err
	}
	out := make([]runtimeRecord, 0, len(records))
	for _, record := range records {
		payload := record.Event.Payload
		switch typed := payload.(type) {
		case coresession.RuntimeEmitted:
			out = append(out, runtimeRecord{Name: typed.Name, Payload: typed.Payload, At: record.Event.Time})
		case *coresession.RuntimeEmitted:
			if typed != nil {
				out = append(out, runtimeRecord{Name: typed.Name, Payload: typed.Payload, At: record.Event.Time})
			}
		default:
			if payload != nil {
				out = append(out, runtimeRecord{Name: payload.EventName(), Payload: payload, At: record.Event.Time})
			}
		}
	}
	return out, true, nil
}

func projectPlan(records []runtimeRecord) (PlanState, int) {
	var state PlanState
	maxSeq := 0
	for _, record := range records {
		if !strings.HasPrefix(string(record.Name), "plan.") {
			continue
		}
		switch record.Name {
		case EventPlanCreated:
			var payload PlanCreated
			if !decodePayload(record.Payload, &payload) {
				continue
			}
			state = PlanState{ID: payload.PlanID, Phase: PhaseDrafting, Spec: cloneSpec(payload.Spec), CreatedAt: record.At}
			maxSeq = max(maxSeq, planSeq(payload.PlanID))
		case EventPlanRevised:
			var payload PlanRevised
			if !decodePayload(record.Payload, &payload) || payload.PlanID != state.ID {
				continue
			}
			state.Spec = cloneSpec(payload.Spec)
			state.Phase = PhaseDrafting
			state.Steps = nil
			state.Error = ""
		case EventPlanExecutionStarted:
			var payload PlanExecutionStarted
			if !decodePayload(record.Payload, &payload) || payload.PlanID != state.ID {
				continue
			}
			state.Phase = PhaseExecuting
			state.Error = ""
			if state.Steps == nil {
				state.Steps = make(map[string]StepExec, len(state.Spec.Steps))
			}
			for _, step := range state.Spec.Steps {
				exec := state.Steps[step.ID]
				if exec.Status != StepStatusCompleted {
					exec = StepExec{Status: StepStatusWaiting, Profile: step.Profile}
				}
				state.Steps[step.ID] = exec
			}
		case EventStepDispatched:
			var payload StepDispatched
			if !decodePayload(record.Payload, &payload) || payload.PlanID != state.ID {
				continue
			}
			ensureStepMap(&state)
			exec := state.Steps[payload.StepID]
			exec.Status = StepStatusRunning
			exec.WorkerID = string(payload.WorkerID)
			exec.Profile = payload.Profile
			exec.StartedAt = record.At
			state.Steps[payload.StepID] = exec
		case EventStepCompleted:
			var payload StepCompleted
			if !decodePayload(record.Payload, &payload) || payload.PlanID != state.ID {
				continue
			}
			ensureStepMap(&state)
			exec := state.Steps[payload.StepID]
			exec.Status = StepStatusCompleted
			exec.Output = payload.Output
			exec.DoneAt = record.At
			state.Steps[payload.StepID] = exec
		case EventStepFailed:
			var payload StepFailed
			if !decodePayload(record.Payload, &payload) || payload.PlanID != state.ID {
				continue
			}
			ensureStepMap(&state)
			exec := state.Steps[payload.StepID]
			exec.Status = StepStatusFailed
			exec.Error = payload.Error
			exec.DoneAt = record.At
			state.Steps[payload.StepID] = exec
		case EventStepCancelled:
			var payload StepCancelled
			if !decodePayload(record.Payload, &payload) || payload.PlanID != state.ID {
				continue
			}
			ensureStepMap(&state)
			exec := state.Steps[payload.StepID]
			exec.Status = StepStatusCancelled
			exec.Error = payload.Reason
			exec.DoneAt = record.At
			state.Steps[payload.StepID] = exec
		case EventPlanCompleted:
			var payload PlanCompleted
			if decodePayload(record.Payload, &payload) && payload.PlanID == state.ID {
				state.Phase = PhaseCompleted
				state.Error = ""
			}
		case EventPlanFailed:
			var payload PlanFailed
			if decodePayload(record.Payload, &payload) && payload.PlanID == state.ID {
				state.Phase = PhaseFailed
				state.Error = payload.Reason
			}
		case EventPlanCancelled:
			var payload PlanCancelled
			if decodePayload(record.Payload, &payload) && payload.PlanID == state.ID {
				state.Phase = PhaseCancelled
				state.Error = payload.Reason
			}
		}
	}
	return cloneState(state), maxSeq
}

func projectWorkers(records []runtimeRecord) map[subagent.ID]subagent.Handle {
	workers := map[subagent.ID]subagent.Handle{}
	for _, record := range records {
		if !strings.HasPrefix(string(record.Name), "subagent.") {
			continue
		}
		switch record.Name {
		case subagent.EventSpawnRequested:
			var payload subagent.SpawnRequested
			if !decodePayload(record.Payload, &payload) || payload.WorkerID == "" {
				continue
			}
			h := workers[payload.WorkerID]
			applyCausation(&h, payload.Causation)
			h.ID = payload.WorkerID
			h.Status = subagent.StatusPrepared
			h.Task = payload.Task
			workers[h.ID] = h
		case subagent.EventStarted:
			var payload subagent.Started
			if !decodePayload(record.Payload, &payload) || payload.WorkerID == "" {
				continue
			}
			h := workers[payload.WorkerID]
			applyCausation(&h, payload.Causation)
			h.ID = payload.WorkerID
			h.Status = subagent.StatusRunning
			h.Task = payload.Task
			h.StartedAt = record.At
			workers[h.ID] = h
		case subagent.EventProgressed:
			var payload subagent.Progressed
			if !decodePayload(record.Payload, &payload) || payload.WorkerID == "" {
				continue
			}
			h := workers[payload.WorkerID]
			applyCausation(&h, payload.Causation)
			h.ID = payload.WorkerID
			h.Progress = payload.Message
			workers[h.ID] = h
		case subagent.EventCompleted:
			var payload subagent.Completed
			if !decodePayload(record.Payload, &payload) || payload.WorkerID == "" {
				continue
			}
			h := workers[payload.WorkerID]
			applyCausation(&h, payload.Causation)
			h.ID = payload.WorkerID
			h.Status = subagent.StatusCompleted
			h.Output = payload.Output
			h.Progress = ""
			h.DoneAt = record.At
			workers[h.ID] = h
		case subagent.EventFailed:
			var payload subagent.Failed
			if !decodePayload(record.Payload, &payload) || payload.WorkerID == "" {
				continue
			}
			h := workers[payload.WorkerID]
			applyCausation(&h, payload.Causation)
			h.ID = payload.WorkerID
			h.Status = subagent.StatusFailed
			h.Error = payload.Error
			h.Progress = ""
			h.DoneAt = record.At
			workers[h.ID] = h
		case subagent.EventCancelled:
			var payload subagent.Cancelled
			if !decodePayload(record.Payload, &payload) || payload.WorkerID == "" {
				continue
			}
			h := workers[payload.WorkerID]
			applyCausation(&h, payload.Causation)
			h.ID = payload.WorkerID
			h.Status = subagent.StatusCancelled
			h.Error = payload.Reason
			h.Progress = ""
			h.DoneAt = record.At
			workers[h.ID] = h
		}
	}
	return workers
}

func sortedWorkers(workers map[subagent.ID]subagent.Handle) []subagent.Handle {
	out := make([]subagent.Handle, 0, len(workers))
	for _, worker := range workers {
		out = append(out, worker)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func applyCausation(h *subagent.Handle, cause subagent.Causation) {
	h.ParentThread = cause.ParentThreadID
	h.ParentRunID = cause.ParentRunID
	h.ParentCallID = cause.ParentCallID
	h.ChildThreadID = cause.ChildThreadID
	h.ChildRunID = cause.ChildRunID
	h.Profile = cause.Profile
	h.Agent = cause.Agent
	h.TaskID = cause.TaskID
	h.Metadata = cloneStringMap(cause.Metadata)
}

func ensureStepMap(state *PlanState) {
	if state.Steps == nil {
		state.Steps = map[string]StepExec{}
	}
}

func decodePayload[T any](in any, out *T) bool {
	if out == nil || in == nil {
		return false
	}
	if typed, ok := in.(T); ok {
		*out = typed
		return true
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

func planSeq(id string) int {
	value := strings.TrimPrefix(id, "plan_")
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
