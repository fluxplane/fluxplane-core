package task

import (
	"fmt"

	"github.com/fluxplane/fluxplane-event"
)

// RegisterEvents registers task event payloads with registry.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("task: event registry is nil")
	}
	for _, sample := range []event.Event{
		CreateRequested{},
		Created{},
		Revised{},
		StatusChanged{},
		ArtifactAdded{},
		ArtifactUpdated{},
		ArtifactRemoved{},
		StepStatusChanged{},
		Indexed{},
		ExecutionStarted{},
		ExecutionLeaseRenewed{},
		ExecutionInterrupted{},
		StepDispatched{},
		StepProgressed{},
		StepCompleted{},
		StepFailed{},
		StepCancelled{},
		ExecutionCompleted{},
		ExecutionFailed{},
		ExecutionCancelled{},
		SchedulerDiagnostic{},
		WorkerRegistered{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
