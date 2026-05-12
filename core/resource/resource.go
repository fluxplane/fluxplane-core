package resource

import (
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
)

// ID identifies a resource contribution.
type ID string

// Scope describes where a resource originated.
type Scope string

const (
	ScopeProject  Scope = "project"
	ScopeUser     Scope = "user"
	ScopeEmbedded Scope = "embedded"
	ScopeRemote   Scope = "remote"
	ScopeExplicit Scope = "explicit"
)

// SourceRef describes a resource source without implying how it was loaded.
type SourceRef struct {
	ID        string       `json:"id,omitempty"`
	Ecosystem string       `json:"ecosystem,omitempty"`
	Scope     Scope        `json:"scope,omitempty"`
	Location  string       `json:"location,omitempty"`
	Ref       string       `json:"ref,omitempty"`
	Trust     policy.Trust `json:"trust,omitempty"`
}

// Severity classifies diagnostics produced during resource loading.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Diagnostic describes a resource loading or validation issue.
type Diagnostic struct {
	Severity Severity  `json:"severity"`
	Source   SourceRef `json:"source,omitempty"`
	Message  string    `json:"message"`
}

// PluginRef identifies a plugin requested by resources or app configuration.
type PluginRef struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

// ContributionBundle is the normalized pure resource contribution shape.
type ContributionBundle struct {
	Source SourceRef `json:"source,omitempty"`

	Apps             []coreapp.Spec             `json:"apps,omitempty"`
	Agents           []agent.Spec               `json:"agents,omitempty"`
	OperationSets    []operation.Set            `json:"operation_sets,omitempty"`
	Operations       []operation.Spec           `json:"operations,omitempty"`
	Commands         []command.Spec             `json:"commands,omitempty"`
	Sessions         []coresession.Spec         `json:"sessions,omitempty"`
	Skills           []skill.Spec               `json:"skills,omitempty"`
	ContextProviders []corecontext.ProviderSpec `json:"context_providers,omitempty"`
	Workflows        []workflow.Spec            `json:"workflows,omitempty"`
	Plugins          []PluginRef                `json:"plugins,omitempty"`
	Diagnostics      []Diagnostic               `json:"diagnostics,omitempty"`
}

// Append appends another bundle into b while preserving b.Source.
func (b *ContributionBundle) Append(other ContributionBundle) {
	if b == nil {
		return
	}
	b.Apps = append(b.Apps, other.Apps...)
	b.Agents = append(b.Agents, other.Agents...)
	b.OperationSets = append(b.OperationSets, other.OperationSets...)
	b.Operations = append(b.Operations, other.Operations...)
	b.Commands = append(b.Commands, other.Commands...)
	b.Sessions = append(b.Sessions, other.Sessions...)
	b.Skills = append(b.Skills, other.Skills...)
	b.ContextProviders = append(b.ContextProviders, other.ContextProviders...)
	b.Workflows = append(b.Workflows, other.Workflows...)
	b.Plugins = append(b.Plugins, other.Plugins...)
	b.Diagnostics = append(b.Diagnostics, other.Diagnostics...)
}
