package workflow

import (
	"fmt"

	"github.com/fluxplane/engine/core/event"
)

// RegisterEvents registers workflow event payloads with registry.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("workflow: event registry is nil")
	}
	for _, sample := range []event.Event{
		Queued{},
		Started{},
		StepStarted{},
		StepCompleted{},
		StepFailed{},
		Completed{},
		Failed{},
		Canceled{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
