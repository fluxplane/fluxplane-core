package planexecplugin

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
)

type runnerKey struct {
	ThreadID corethread.ID
	PlanID   string
}

type planRunner struct {
	key    runnerKey
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *Plugin) startRunner(ctx operation.Context, scope subagent.Scope, planID string) (*planRunner, bool) {
	key := runnerKey{ThreadID: scope.ParentThreadID, PlanID: planID}
	p.mu.Lock()
	if p.runners == nil {
		p.runners = map[runnerKey]*planRunner{}
	}
	if existing := p.runners[key]; existing != nil {
		select {
		case <-existing.done:
		default:
			p.mu.Unlock()
			return existing, false
		}
	}
	base := context.Background()
	events := event.Discard()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
		events = ctx.Events()
	}
	runCtx, cancel := context.WithCancel(base)
	runner := &planRunner{key: key, cancel: cancel, done: make(chan struct{})}
	p.runners[key] = runner
	p.mu.Unlock()

	runOpCtx := operation.NewContext(runCtx, events)
	go func() {
		defer close(runner.done)
		defer p.removeRunner(key, runner)
		_ = p.runPlan(runOpCtx, scope, planID)
	}()
	return runner, true
}

func (p *Plugin) runner(scope subagent.Scope, planID string) *planRunner {
	key := runnerKey{ThreadID: scope.ParentThreadID, PlanID: planID}
	p.mu.Lock()
	defer p.mu.Unlock()
	runner := p.runners[key]
	if runner == nil {
		return nil
	}
	select {
	case <-runner.done:
		return nil
	default:
		return runner
	}
}

func (p *Plugin) removeRunner(key runnerKey, runner *planRunner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runners[key] == runner {
		delete(p.runners, key)
	}
}

func (p *Plugin) isRunning(scope subagent.Scope, planID string) bool {
	return p.runner(scope, planID) != nil
}

func (p *Plugin) waitRunner(ctx context.Context, scope subagent.Scope, planID string) error {
	runner := p.runner(scope, planID)
	if runner == nil {
		return fmt.Errorf("plan is not running")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runner.done:
		return nil
	}
}

func (p *Plugin) cancelRunner(scope subagent.Scope, planID string) {
	if runner := p.runner(scope, planID); runner != nil && runner.cancel != nil {
		runner.cancel()
	}
}
