package task

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-event"
)

func TestTaskEventNames(t *testing.T) {
	checks := []struct {
		name string
		got  event.Name
		want event.Name
	}{
		{"CreateRequested", CreateRequested{}.EventName(), EventCreateRequestedName},
		{"Created", Created{}.EventName(), EventCreatedName},
		{"Revised", Revised{}.EventName(), EventRevisedName},
		{"StatusChanged", StatusChanged{}.EventName(), EventStatusChangedName},
		{"ArtifactAdded", ArtifactAdded{}.EventName(), EventArtifactAddedName},
		{"ArtifactUpdated", ArtifactUpdated{}.EventName(), EventArtifactUpdatedName},
		{"ArtifactRemoved", ArtifactRemoved{}.EventName(), EventArtifactRemovedName},
		{"StepStatusChanged", StepStatusChanged{}.EventName(), EventStepStatusChangedName},
		{"Indexed", Indexed{}.EventName(), EventIndexedName},
		{"ExecutionStarted", ExecutionStarted{}.EventName(), EventExecutionStartedName},
		{"ExecutionLeaseRenewed", ExecutionLeaseRenewed{}.EventName(), EventExecutionLeaseRenewedName},
		{"ExecutionInterrupted", ExecutionInterrupted{}.EventName(), EventExecutionInterruptedName},
		{"StepDispatched", StepDispatched{}.EventName(), EventStepDispatchedName},
		{"StepProgressed", StepProgressed{}.EventName(), EventStepProgressedName},
		{"StepCompleted", StepCompleted{}.EventName(), EventStepCompletedName},
		{"StepFailed", StepFailed{}.EventName(), EventStepFailedName},
		{"StepCancelled", StepCancelled{}.EventName(), EventStepCancelledName},
		{"ExecutionCompleted", ExecutionCompleted{}.EventName(), EventExecutionCompletedName},
		{"ExecutionFailed", ExecutionFailed{}.EventName(), EventExecutionFailedName},
		{"ExecutionCancelled", ExecutionCancelled{}.EventName(), EventExecutionCancelledName},
		{"SchedulerDiagnostic", SchedulerDiagnostic{}.EventName(), EventSchedulerDiagnosticName},
		{"WorkerRegistered", WorkerRegistered{}.EventName(), EventWorkerRegisteredName},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s EventName = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestRegisterEvents(t *testing.T) {
	registry := event.NewRegistry()
	if err := RegisterEvents(registry); err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
	decoded, ok, err := registry.TryDecode(EventStepCompletedName, []byte(`{"task_id":"task_1","execution_id":"exec_1","step_id":"inspect","output":"ok"}`))
	if err != nil || !ok {
		t.Fatalf("step completed event not registered: ok=%v err=%v", ok, err)
	}
	completed, ok := decoded.(StepCompleted)
	if !ok {
		t.Fatalf("decoded = %T, want StepCompleted", decoded)
	}
	if completed.TaskID != "task_1" || completed.ExecutionID != "exec_1" || completed.StepID != "inspect" || completed.Output != operation.Value("ok") {
		t.Fatalf("completed = %#v, want decoded ids and output", completed)
	}
}
