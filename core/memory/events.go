package memory

import (
	"fmt"

	"github.com/fluxplane/agentruntime/core/event"
)

func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("memory: event registry is nil")
	}
	for _, sample := range []event.Event{
		Memorized{},
		Forgotten{},
		Organized{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
