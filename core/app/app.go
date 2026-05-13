package app

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	coresession "github.com/fluxplane/agentruntime/core/session"
)

// Name identifies an application manifest.
type Name string

// SourceSpec describes a resource source requested by an app manifest. Loading
// the source is adapter work; this is only the inert declaration.
type SourceSpec struct {
	Location    string            `json:"location"`
	Scope       string            `json:"scope,omitempty"`
	Ecosystem   string            `json:"ecosystem,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// DiscoveryPolicy describes which resource discovery surfaces an app may use.
type DiscoveryPolicy struct {
	IncludeGlobalUserResources bool   `json:"include_global_user_resources,omitempty"`
	IncludeExternalEcosystems  bool   `json:"include_external_ecosystems,omitempty"`
	AllowRemote                bool   `json:"allow_remote,omitempty"`
	TrustStoreDir              string `json:"trust_store_dir,omitempty"`
}

// ModelPolicy describes model selection intent without binding to a provider
// transport or runtime implementation.
type ModelPolicy struct {
	Model         string            `json:"model,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	UseCase       string            `json:"use_case,omitempty"`
	SourceAPI     string            `json:"source_api,omitempty"`
	ApprovedOnly  *bool             `json:"approved_only,omitempty"`
	AllowDegraded *bool             `json:"allow_degraded,omitempty"`
	AllowUntested *bool             `json:"allow_untested,omitempty"`
	EvidencePath  string            `json:"evidence_path,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// SemanticSearchSpec describes app-level semantic datasource indexing defaults.
type SemanticSearchSpec struct {
	Enabled    bool              `json:"enabled,omitempty"`
	Embeddings EmbeddingSpec     `json:"embeddings,omitempty"`
	Store      SemanticStoreSpec `json:"store,omitempty"`
	Defaults   SemanticDefaults  `json:"defaults,omitempty"`
}

// EmbeddingSpec declares the embedding provider/model requested by the app.
type EmbeddingSpec struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// SemanticStoreSpec declares where semantic index state is stored.
type SemanticStoreSpec struct {
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
}

// SemanticDefaults declares default chunking and retrieval behavior.
type SemanticDefaults struct {
	Chunking  SemanticChunkingSpec  `json:"chunking,omitempty"`
	Retrieval SemanticRetrievalSpec `json:"retrieval,omitempty"`
}

// SemanticChunkingSpec configures default semantic chunk planning.
type SemanticChunkingSpec struct {
	Strategy      string `json:"strategy,omitempty"`
	TargetTokens  int    `json:"target_tokens,omitempty"`
	OverlapTokens int    `json:"overlap_tokens,omitempty"`
}

// SemanticRetrievalSpec configures default semantic retrieval behavior.
type SemanticRetrievalSpec struct {
	Mode     string  `json:"mode,omitempty"`
	Limit    int     `json:"limit,omitempty"`
	MinScore float64 `json:"min_score,omitempty"`
}

// PluginRef identifies a requested plugin in an app manifest. Plugin
// instantiation belongs outside core.
type PluginRef struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

// Spec is an inert application manifest. It can be authored by config files,
// embedded apps, or tests, and later composed by orchestration.
type Spec struct {
	Name           Name               `json:"name,omitempty"`
	Description    string             `json:"description,omitempty"`
	DefaultAgent   agent.Ref          `json:"default_agent,omitempty"`
	DefaultSession coresession.Ref    `json:"default_session,omitempty"`
	Sources        []SourceSpec       `json:"sources,omitempty"`
	Discovery      DiscoveryPolicy    `json:"discovery,omitempty"`
	Model          ModelPolicy        `json:"model,omitempty"`
	SemanticSearch SemanticSearchSpec `json:"semantic_search,omitempty"`
	Plugins        []PluginRef        `json:"plugins,omitempty"`
	Annotations    map[string]string  `json:"annotations,omitempty"`
}

// Validate checks the manifest is structurally useful without resolving refs.
func (s Spec) Validate() error {
	for i, source := range s.Sources {
		if strings.TrimSpace(source.Location) == "" {
			return fmt.Errorf("app: sources[%d] location is empty", i)
		}
	}
	for i, plugin := range s.Plugins {
		if strings.TrimSpace(plugin.Name) == "" {
			return fmt.Errorf("app: plugins[%d] name is empty", i)
		}
	}
	return nil
}
