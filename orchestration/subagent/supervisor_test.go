package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

func TestSupervisorSpawnWaitsForChildResultAndEmitsEvents(t *testing.T) {
	var events []event.Event
	supervisor := New(Config{Client: fakeClient{result: "done"}, MaxParallel: 2})
	handle, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Policy:  coresession.DelegationPolicy{AllowedProfiles: []coresession.Ref{{Name: "worker"}}},
		Events:  event.SinkFunc(func(evt event.Event) { events = append(events, evt) }),
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	result, err := supervisor.Wait(context.Background(), handle.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if len(events) < 3 {
		t.Fatalf("events len = %d, want at least spawn/start/complete", len(events))
	}
	if _, ok := events[0].(SpawnRequested); !ok {
		t.Fatalf("first event = %T, want SpawnRequested", events[0])
	}
}

func TestSupervisorRejectsDisallowedProfile(t *testing.T) {
	supervisor := New(Config{Client: fakeClient{result: "done"}})
	_, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "explorer"},
		Task:    "do it",
		Policy:  coresession.DelegationPolicy{AllowedProfiles: []coresession.Ref{{Name: "worker"}}},
	})
	if err == nil {
		t.Fatal("Spawn err = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "allowed profiles: worker") {
		t.Fatalf("Spawn err = %q, want allowed profile hint", err.Error())
	}
}

func TestSupervisorEnforcesAllowedAgent(t *testing.T) {
	resolver := ProfileResolverFunc(func(context.Context, coresession.Ref) (coresession.Spec, error) {
		return coresession.Spec{Name: "worker", Agent: agent.Ref{Name: "worker-agent"}}, nil
	})
	supervisor := New(Config{Client: fakeClient{result: "done"}, ResolveProfile: resolver})
	_, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			AllowedAgents:   []agent.Ref{{Name: "other-agent"}},
		},
	})
	if err == nil {
		t.Fatal("Spawn err = nil, want disallowed agent rejection")
	}
	handle, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			AllowedAgents:   []agent.Ref{{Name: "worker-agent"}},
		},
	})
	if err != nil {
		t.Fatalf("Spawn allowed agent: %v", err)
	}
	if handle.Agent.Name != "worker-agent" {
		t.Fatalf("handle agent = %q, want worker-agent", handle.Agent.Name)
	}
}

func TestSupervisorRequiresResolverForAllowedAgents(t *testing.T) {
	supervisor := New(Config{Client: fakeClient{result: "done"}})
	_, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			AllowedAgents:   []agent.Ref{{Name: "worker-agent"}},
		},
	})
	if err == nil {
		t.Fatal("Spawn err = nil, want missing resolver rejection")
	}
}

func TestSupervisorCapacityCountsPreparedSpawns(t *testing.T) {
	supervisor := New(Config{Client: fakeClient{result: "done"}, MaxParallel: 1})
	req := SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Policy:  coresession.DelegationPolicy{AllowedProfiles: []coresession.Ref{{Name: "worker"}}, MaxParallel: 1},
	}
	if _, err := supervisor.Prepare(context.Background(), req); err != nil {
		t.Fatalf("Prepare first: %v", err)
	}
	if _, err := supervisor.Prepare(context.Background(), req); err == nil {
		t.Fatal("Prepare second err = nil, want capacity error")
	}
}

func TestSupervisorClampsRequestedTimeoutToPolicyMax(t *testing.T) {
	supervisor := New(Config{Client: fakeClient{result: "done"}})
	handle, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Timeout: 30 * time.Minute,
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			DefaultTimeout:  "10m",
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !handle.TimeoutClamped {
		t.Fatal("timeout clamped = false, want true")
	}
	if handle.RequestedTimeout != "30m0s" || handle.EffectiveTimeout != "10m0s" || handle.MaxTimeout != "10m0s" {
		t.Fatalf("timeouts = requested %q effective %q max %q, want 30m/10m/10m", handle.RequestedTimeout, handle.EffectiveTimeout, handle.MaxTimeout)
	}
}

func TestSupervisorAllowsRequestedTimeoutBelowPolicyMax(t *testing.T) {
	supervisor := New(Config{Client: fakeClient{result: "done"}})
	handle, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Timeout: 5 * time.Minute,
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			DefaultTimeout:  "10m",
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if handle.TimeoutClamped {
		t.Fatal("timeout clamped = true, want false")
	}
	if handle.RequestedTimeout != "5m0s" || handle.EffectiveTimeout != "5m0s" || handle.MaxTimeout != "10m0s" {
		t.Fatalf("timeouts = requested %q effective %q max %q, want 5m/5m/10m", handle.RequestedTimeout, handle.EffectiveTimeout, handle.MaxTimeout)
	}
}

func TestSupervisorNarrowsChildProfileWithDelegationPolicy(t *testing.T) {
	client := &recordingClient{result: "done"}
	resolver := ProfileResolverFunc(func(context.Context, coresession.Ref) (coresession.Spec, error) {
		return coresession.Spec{
			Name:  "worker",
			Agent: agent.Ref{Name: "worker-agent"},
			Context: []corecontext.ProviderRef{
				{Name: "docs"},
				{Name: "repo"},
			},
			Commands: []command.Path{
				{"safe"},
				{"danger"},
			},
			Operations: []operation.Ref{
				{Name: "read"},
				{Name: "write"},
			},
		}, nil
	})
	supervisor := New(Config{Client: client, ResolveProfile: resolver})

	if _, err := supervisor.Prepare(context.Background(), SpawnRequest{
		Profile: coresession.Ref{Name: "worker"},
		Task:    "do it",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
			Context:         []corecontext.ProviderRef{{Name: "docs"}},
			Commands:        []command.Path{{"safe"}},
			Operations:      []operation.Ref{{Name: "read"}},
		},
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if len(client.profile.Context) != 1 || client.profile.Context[0].Name != "docs" {
		t.Fatalf("context = %#v, want docs only", client.profile.Context)
	}
	if len(client.profile.Commands) != 1 || client.profile.Commands[0].String() != "/safe" {
		t.Fatalf("commands = %#v, want /safe only", client.profile.Commands)
	}
	if len(client.profile.Operations) != 1 || client.profile.Operations[0].Name != "read" {
		t.Fatalf("operations = %#v, want read only", client.profile.Operations)
	}
}

type fakeClient struct {
	result string
}

func (c fakeClient) Open(context.Context, OpenRequest) (Session, error) {
	return fakeSession(c), nil
}

type fakeSession struct {
	result string
}

func (s fakeSession) Info() SessionInfo {
	return SessionInfo{}
}

func (s fakeSession) SendInput(context.Context, Input) (Run, error) {
	return fakeRun{result: s.result, events: make(chan RunEvent)}, nil
}

type fakeRun struct {
	result string
	events chan RunEvent
}

func (r fakeRun) ID() string { return "run_1" }

func (r fakeRun) Events() <-chan RunEvent {
	go func() {
		time.Sleep(time.Millisecond)
		close(r.events)
	}()
	return r.events
}

func (r fakeRun) Wait(context.Context) (RunResult, error) {
	return RunResult{Text: r.result}, nil
}

func TestSupervisorForwardsApproverToChildSession(t *testing.T) {
	client := &recordingClient{result: "done"}
	supervisor := New(Config{Client: client})
	approver := operationruntime.AutoApprover{}
	_, err := supervisor.Spawn(context.Background(), SpawnRequest{
		Profile:  coresession.Ref{Name: "worker"},
		Task:     "do it",
		Policy:   coresession.DelegationPolicy{AllowedProfiles: []coresession.Ref{{Name: "worker"}}},
		Approver: approver,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, ok := client.approver.(operationruntime.AutoApprover); !ok {
		t.Fatalf("child session approver = %T, want AutoApprover", client.approver)
	}
}

type recordingClient struct {
	result   string
	profile  coresession.Spec
	approver operationruntime.ApprovalGate
}

func (c *recordingClient) Open(_ context.Context, req OpenRequest) (Session, error) {
	c.profile = req.Profile
	c.approver = req.Approver
	return fakeSession{result: c.result}, nil
}
