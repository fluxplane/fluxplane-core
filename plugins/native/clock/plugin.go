package clock

import (
	"context"
	"time"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
)

const (
	Name                = "clock"
	ContextProviderName = "time"
)

// Plugin contributes a `time` context provider that injects the current
// wall-clock time on each turn, cached for ~60s, plus the process uptime
// since the plugin was constructed.
type Plugin struct {
	Now     func() time.Time
	TZ      string
	startAt time.Time
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

func New() Plugin {
	now := time.Now
	return Plugin{Now: now, startAt: now()}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Current wall-clock time context provider."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		ContextProviders: []corecontext.ProviderSpec{{
			Name:             ContextProviderName,
			Description:      "Current wall-clock time.",
			Kinds:            []corecontext.BlockKind{corecontext.BlockData},
			DefaultPlacement: corecontext.PlacementSystem,
		}},
	}, nil
}

func (p Plugin) ContextProviders(_ context.Context, _ pluginhost.Context) ([]corecontext.Provider, error) {
	now := p.Now
	if now == nil {
		now = time.Now
	}
	startAt := p.startAt
	if startAt.IsZero() {
		startAt = now()
	}
	return []corecontext.Provider{&ContextProvider{Now: now, TZ: p.TZ, StartAt: startAt}}, nil
}
