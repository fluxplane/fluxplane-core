package session

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

// Name identifies a configured session profile.
type Name string

// Ref identifies a configured session profile by local, qualified, or
// canonical resource name.
type Ref struct {
	Name Name `json:"name"`
}

// DelegationPolicy describes which helper session profiles and agents a parent
// session may run. It does not enable worker execution by itself.
type DelegationPolicy struct {
	AllowedProfiles []Ref                     `json:"allowed_profiles,omitempty"`
	AllowedAgents   []agent.Ref               `json:"allowed_agents,omitempty"`
	MaxParallel     int                       `json:"max_parallel,omitempty"`
	DefaultTimeout  string                    `json:"default_timeout,omitempty"`
	Context         []corecontext.ProviderRef `json:"context,omitempty"`
	Commands        []command.Path            `json:"commands,omitempty"`
	Operations      []operation.Ref           `json:"operations,omitempty"`
	Policy          policy.InvocationPolicy   `json:"policy,omitempty"`
	Annotations     map[string]string         `json:"annotations,omitempty"`
}

// Spec is an inert configured session profile. It describes a reusable entry
// point into an app, not a live session instance or durable state.
type Spec struct {
	Name         Name                      `json:"name"`
	Description  string                    `json:"description,omitempty"`
	Agent        agent.Ref                 `json:"agent,omitempty"`
	Channel      channel.Ref               `json:"channel,omitempty"`
	Conversation channel.ConversationRef   `json:"conversation,omitempty"`
	Context      []corecontext.ProviderRef `json:"context,omitempty"`
	Commands     []command.Path            `json:"commands,omitempty"`
	Operations   []operation.Ref           `json:"operations,omitempty"`
	Policy       policy.InvocationPolicy   `json:"policy,omitempty"`
	Delegation   DelegationPolicy          `json:"delegation,omitempty"`
	Metadata     map[string]string         `json:"metadata,omitempty"`
	Annotations  map[string]string         `json:"annotations,omitempty"`
}

// Validate checks that the configured session profile has a stable name and
// usable refs. It intentionally does not validate that refs are bound.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("session: spec name is empty")
	}
	for i, path := range s.Commands {
		if path.String() == "" {
			return fmt.Errorf("session: command[%d] path is empty", i)
		}
	}
	for i, ref := range s.Operations {
		if ref.Name == "" {
			return fmt.Errorf("session: operation[%d] ref is empty", i)
		}
	}
	for i, ref := range s.Delegation.Operations {
		if ref.Name == "" {
			return fmt.Errorf("session: delegation operation[%d] ref is empty", i)
		}
	}
	for i, profile := range s.Delegation.AllowedProfiles {
		if strings.TrimSpace(string(profile.Name)) == "" {
			return fmt.Errorf("session: delegation allowed_profiles[%d] name is empty", i)
		}
	}
	for i, ref := range s.Delegation.AllowedAgents {
		if strings.TrimSpace(string(ref.Name)) == "" {
			return fmt.Errorf("session: delegation allowed_agents[%d] name is empty", i)
		}
	}
	return nil
}
