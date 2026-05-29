package launch

import (
	"fmt"

	"github.com/fluxplane/fluxplane-core/orchestration/eventregistry"
	"github.com/fluxplane/fluxplane-core/plugins/support/eventcatalog"
	coreevent "github.com/fluxplane/fluxplane-event"
)

// MustTerminalEventRegistry returns the terminal event registry used by local
// distribution CLIs.
func MustTerminalEventRegistry() *coreevent.Registry {
	registry, err := TerminalEventRegistry()
	if err != nil {
		panic(fmt.Sprintf("launch: build terminal event registry: %v", err))
	}
	return registry
}

// TerminalEventRegistry builds an event registry for first-party terminal
// renderers.
func TerminalEventRegistry() (*coreevent.Registry, error) {
	return eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
}
