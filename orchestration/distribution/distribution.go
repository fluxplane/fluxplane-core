// Package distribution assembles runnable distribution declarations.
package distribution

import (
	"context"

	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/resource"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

// Distribution is a runnable package declaration plus its local runtime hook.
type Distribution struct {
	Spec    coredistribution.Spec
	Bundles []resource.ContributionBundle
	Runtime Runtime
}

// Runtime opens a local session for a distribution.
type Runtime interface {
	OpenSession(context.Context, OpenRequest) (clientapi.SessionHandle, error)
}

// OpenRequest carries launcher-selected runtime options.
type OpenRequest struct {
	Provider string
	Model    string
	Debug    bool
}
