package contrib

import (
	"github.com/fluxplane/fluxplane-core/contrib/discovery"
	goalcontrib "github.com/fluxplane/fluxplane-core/contrib/goal"
	"github.com/fluxplane/fluxplane-core/contrib/identity"
	"github.com/fluxplane/fluxplane-core/contrib/loop"
	"github.com/fluxplane/fluxplane-core/contrib/memory"
	"github.com/fluxplane/fluxplane-core/contrib/skills"
	taskcontrib "github.com/fluxplane/fluxplane-core/contrib/task"
	"github.com/fluxplane/fluxplane-core/contrib/text"
	"github.com/fluxplane/fluxplane-core/contrib/workspace"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
)

type RuntimeOptions struct {
	Workspace  runtimeworkspace.Workspace
	TaskRunner taskcontrib.TaskRunner
}

func Runtime(opts RuntimeOptions) []contributions.Provider {
	return []contributions.Provider{
		workspace.New(opts.Workspace),
		discovery.New(),
		identity.New(),
		goalcontrib.New(),
		loop.New(),
		memory.New(),
		taskcontrib.NewWithConfig(taskcontrib.Config{Runner: opts.TaskRunner, Workspace: opts.Workspace}),
		skills.New(),
		text.New(),
	}
}
