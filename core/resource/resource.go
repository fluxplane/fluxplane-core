package resource

import (
	"strings"

	"github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/event"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/language"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/reaction"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/workflow"
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
	Name     string         `json:"name"`
	Instance string         `json:"instance,omitempty"`
	Config   map[string]any `json:"config,omitempty"`
}

// InstanceName returns the declared plugin instance name. Unnamed refs keep the
// plugin type name as their instance identity.
func (r PluginRef) InstanceName() string {
	if instance := strings.TrimSpace(r.Instance); instance != "" {
		return instance
	}
	return strings.TrimSpace(r.Name)
}

// Key returns the stable declaration key for de-duplicating plugin refs.
func (r PluginRef) Key() string {
	name := strings.TrimSpace(r.Name)
	instance := r.InstanceName()
	if instance == "" || instance == name {
		return name
	}
	return name + "/" + instance
}

// ContributionBundle is the normalized pure resource contribution shape.
type ContributionBundle struct {
	Source SourceRef `json:"source,omitempty"`

	Apps              []coreapp.Spec                      `json:"apps,omitempty"`
	Agents            []agent.Spec                        `json:"agents,omitempty"`
	ActivationSets    []activation.Set                    `json:"activation_sets,omitempty"`
	OperationSets     []operation.Set                     `json:"operation_sets,omitempty"`
	Toolchains        []language.ToolchainSpec            `json:"toolchains,omitempty"`
	ToolSets          []tool.Set                          `json:"tool_sets,omitempty"`
	Operations        []operation.Spec                    `json:"operations,omitempty"`
	Commands          []command.Spec                      `json:"commands,omitempty"`
	Datasources       []coredatasource.Spec               `json:"datasources,omitempty"`
	DataSources       []coredata.SourceSpec               `json:"data_sources,omitempty"`
	LLMProviders      []corellm.ProviderSpec              `json:"llm_providers,omitempty"`
	LLMModelAliases   []corellm.ModelAliasSpec            `json:"llm_model_aliases,omitempty"`
	Sessions          []coresession.Spec                  `json:"sessions,omitempty"`
	PostEditChecks    []coresession.PostEditCheckSpec     `json:"post_edit_checks,omitempty"`
	Skills            []skill.Spec                        `json:"skills,omitempty"`
	ContextProviders  []corecontext.ProviderSpec          `json:"context_providers,omitempty"`
	Workflows         []workflow.Spec                     `json:"workflows,omitempty"`
	Observers         []coreevidence.ObserverSpec         `json:"observers,omitempty"`
	AssertionDerivers []coreevidence.AssertionDeriverSpec `json:"assertion_derivers,omitempty"`
	Reactions         []reaction.Rule                     `json:"reactions,omitempty"`
	EventTypes        []event.Event                       `json:"-"`
	Plugins           []PluginRef                         `json:"plugins,omitempty"`
	Diagnostics       []Diagnostic                        `json:"diagnostics,omitempty"`
}

// Append appends another bundle into b while preserving b.Source.
func (b *ContributionBundle) Append(other ContributionBundle) {
	if b == nil {
		return
	}
	b.Apps = append(b.Apps, other.Apps...)
	b.Agents = append(b.Agents, other.Agents...)
	b.ActivationSets = append(b.ActivationSets, other.ActivationSets...)
	b.OperationSets = append(b.OperationSets, other.OperationSets...)
	b.Toolchains = append(b.Toolchains, other.Toolchains...)
	b.ToolSets = append(b.ToolSets, other.ToolSets...)
	b.Operations = append(b.Operations, other.Operations...)
	b.Commands = append(b.Commands, other.Commands...)
	b.Datasources = append(b.Datasources, other.Datasources...)
	b.DataSources = append(b.DataSources, other.DataSources...)
	b.LLMProviders = append(b.LLMProviders, other.LLMProviders...)
	b.LLMModelAliases = append(b.LLMModelAliases, other.LLMModelAliases...)
	b.Sessions = append(b.Sessions, other.Sessions...)
	b.PostEditChecks = append(b.PostEditChecks, other.PostEditChecks...)
	b.Skills = append(b.Skills, other.Skills...)
	b.ContextProviders = append(b.ContextProviders, other.ContextProviders...)
	b.Workflows = append(b.Workflows, other.Workflows...)
	b.Observers = append(b.Observers, other.Observers...)
	b.AssertionDerivers = append(b.AssertionDerivers, other.AssertionDerivers...)
	b.Reactions = append(b.Reactions, other.Reactions...)
	b.EventTypes = append(b.EventTypes, other.EventTypes...)
	b.Plugins = append(b.Plugins, other.Plugins...)
	b.Diagnostics = append(b.Diagnostics, other.Diagnostics...)
}
