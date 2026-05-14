package app

import (
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
)

// ResourceBinding binds an inert resource spec to its canonical resource
// identity.
type ResourceBinding[T any] struct {
	ID     resource.ResourceID `json:"id"`
	Source resource.SourceRef  `json:"source,omitempty"`
	Spec   T                   `json:"spec"`
}

// AppCatalog indexes application manifests by canonical resource ID address.
type AppCatalog map[string]ResourceBinding[coreapp.Spec]

// AgentCatalog indexes agent specs by canonical resource ID address.
type AgentCatalog map[string]ResourceBinding[agent.Spec]

// SkillCatalog indexes skill specs by canonical resource ID address.
type SkillCatalog map[string]ResourceBinding[skill.Spec]

// ContextProviderCatalog indexes context provider specs by canonical resource
// ID address.
type ContextProviderCatalog map[string]ResourceBinding[corecontext.ProviderSpec]

// DatasourceCatalog indexes datasource specs by canonical resource ID address.
type DatasourceCatalog map[string]ResourceBinding[coredatasource.Spec]

// LLMProviderCatalog indexes LLM provider specs by canonical resource ID
// address.
type LLMProviderCatalog map[string]ResourceBinding[corellm.ProviderSpec]

// LLMModelAliasCatalog indexes model alias specs by canonical resource ID
// address.
type LLMModelAliasCatalog map[string]ResourceBinding[corellm.ModelAliasSpec]

// WorkflowCatalog indexes workflow specs by canonical resource ID address.
type WorkflowCatalog map[string]ResourceBinding[workflow.Spec]

// OperationSetCatalog indexes named operation capability sets.
type OperationSetCatalog map[string]ResourceBinding[operation.Set]
