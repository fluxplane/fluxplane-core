package session

import (
	"fmt"

	"github.com/fluxplane/engine/core/event"
)

// RegisterEvents registers session event payloads with registry.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("session: event registry is nil")
	}
	for _, sample := range []event.Event{
		InputReceived{},
		CommandReceived{},
		CommandRejected{},
		AgentStepCompleted{},
		OperationRequested{},
		OperationCompleted{},
		OutboundProduced{},
		RuntimeEmitted{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
