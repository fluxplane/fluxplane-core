package planexecplugin

import (
	"context"
	"sync"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const (
	Name            = "planexec"
	DelegateOp      = "delegate"
	PlanOp          = "plan"
	WorkerAgent     = "worker"
	ExplorerAgent   = "explorer"
	WorkerSession   = "worker"
	ExplorerSession = "explorer"
)

type Plugin struct {
	mu   sync.Mutex
	plan PlanState
	seq  int
}

var _ pluginhost.Plugin = (*Plugin)(nil)
var _ pluginhost.OperationContributor = (*Plugin)(nil)

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Sub-agent delegation and plan execution operations."}
}

func (p *Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Operations: []operation.Spec{delegateSpec(), planSpec()},
		Agents: []agent.Spec{
			{
				Name:        WorkerAgent,
				Description: "Focused implementation worker for delegated coding tasks.",
				System:      "You are a focused worker agent. Complete the assigned task within scope and summarize exactly what you changed or found. If blocked, report the blocker clearly.",
				Driver:      agent.DriverSpec{Kind: "llmagent"},
				Policy:      agent.Policy{MaxSteps: 50, MaxContinuations: 3},
				Operations: []operation.Ref{
					{Name: "dir_list"}, {Name: "dir_tree"}, {Name: "file_read"}, {Name: "file_patch"},
					{Name: "grep"}, {Name: "glob"}, {Name: "git_status"}, {Name: "git_diff"},
					{Name: "shell_exec"}, {Name: "code_execute"},
				},
			},
			{
				Name:        ExplorerAgent,
				Description: "Read-only exploration worker for delegated investigation tasks.",
				System:      "You are a read-only exploration worker. Inspect the requested context and report concise findings with file paths when relevant. Do not modify files.",
				Driver:      agent.DriverSpec{Kind: "llmagent"},
				Policy:      agent.Policy{MaxSteps: 30, MaxContinuations: 3},
				Operations: []operation.Ref{
					{Name: "dir_list"}, {Name: "dir_tree"}, {Name: "file_read"}, {Name: "grep"}, {Name: "glob"},
					{Name: "git_status"}, {Name: "git_diff"}, {Name: "web_request"},
				},
			},
		},
		Sessions: []coresession.Spec{
			{Name: WorkerSession, Agent: agent.Ref{Name: WorkerAgent}, Metadata: map[string]string{"role": "subagent_worker"}},
			{Name: ExplorerSession, Agent: agent.Ref{Name: ExplorerAgent}, Metadata: map[string]string{"role": "subagent_explorer"}},
		},
	}, nil
}

func (p *Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p == nil {
		p = New()
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[delegateInput, operation.Rendered](delegateSpec(), p.delegate),
		operationruntime.NewTypedResult[planInput, operation.Rendered](planSpec(), p.planOperation),
	}, nil
}

func delegateSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: DelegateOp},
		Description: "Spawn, inspect, fetch results from, or cancel delegated sub-agent tasks.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}

func planSpec() operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: PlanOp},
		Description: "Create, revise, execute, inspect, or cancel a DAG plan backed by delegated sub-agent steps.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectCreate, operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskMedium,
		},
	}
}
