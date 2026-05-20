// Package eventcatalog lists first-party plugin event payload types linked into
// this module.
package eventcatalog

import (
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/plugins/native/human"
)

// All returns every first-party plugin event payload type known to this binary.
func All() []event.Event {
	return []event.Event{
		human.ClarificationRequested{},
		human.ClarificationCompleted{},
	}
}
