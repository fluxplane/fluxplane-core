// Package eventcatalog lists first-party plugin event payload types linked into
// this module.
package eventcatalog

import (
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
)

// All returns every first-party plugin event payload type known to this binary.
func All() []event.Event {
	return []event.Event{
		humanplugin.ClarificationRequested{},
		humanplugin.ClarificationCompleted{},
		planexecplugin.PlanCreated{},
		planexecplugin.PlanRevised{},
		planexecplugin.PlanExecutionStarted{},
		planexecplugin.StepDispatched{},
		planexecplugin.StepProgressed{},
		planexecplugin.StepCompleted{},
		planexecplugin.StepFailed{},
		planexecplugin.StepCancelled{},
		planexecplugin.PlanCompleted{},
		planexecplugin.PlanFailed{},
		planexecplugin.PlanCancelled{},
	}
}
