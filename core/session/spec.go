package session

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
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

// PostEditCheckMode describes whether a post-edit check may change files or
// only report diagnostics.
type PostEditCheckMode string

const (
	PostEditCheckModeDiagnostic PostEditCheckMode = "diagnostic"
	PostEditCheckModeFix        PostEditCheckMode = "fix"
)

// PostEditCheckSpec declares an operation to run after matching file edits.
// Plugins contribute these inert descriptors; session runtime owns execution.
type PostEditCheckSpec struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	MatchPaths  []string          `json:"match_paths,omitempty" yaml:"match_paths,omitempty"`
	Operation   operation.Ref     `json:"operation" yaml:"operation"`
	Input       operation.Value   `json:"input,omitempty" yaml:"input,omitempty"`
	Mode        PostEditCheckMode `json:"mode,omitempty" yaml:"mode,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// Validate checks that the post-edit check can be matched and executed.
func (c PostEditCheckSpec) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("session: post_edit_check name is empty")
	}
	if c.Operation.Name == "" {
		return fmt.Errorf("session: post_edit_check %q operation is empty", c.Name)
	}
	switch c.Mode {
	case "", PostEditCheckModeDiagnostic, PostEditCheckModeFix:
	default:
		return fmt.Errorf("session: post_edit_check %q mode %q is invalid", c.Name, c.Mode)
	}
	return nil
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
