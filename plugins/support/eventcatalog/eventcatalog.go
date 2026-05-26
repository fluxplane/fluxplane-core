// Package eventcatalog lists first-party plugin event payload types linked into
// this module.
package eventcatalog

import (
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/plugins/native/human"
)

// All returns every first-party plugin event payload type known to this binary.
func All() []event.Event {
	return []event.Event{
		resource.LoadError{},
		human.ClarificationRequested{},
		human.ClarificationCompleted{},
		human.NotificationSent{},
	}
}
