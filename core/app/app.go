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

// PluginRef identifies a requested plugin in an app manifest. Plugin
// instantiation belongs outside core.
type PluginRef struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

// Spec is an inert application manifest. It can be authored by config files,
// embedded apps, or tests, and later composed by orchestration.
type Spec struct {
	Name           Name              `json:"name,omitempty"`
	Description    string            `json:"description,omitempty"`
	DefaultAgent   agent.Ref         `json:"default_agent,omitempty"`
	DefaultSession coresession.Ref   `json:"default_session,omitempty"`
	Sources        []SourceSpec      `json:"sources,omitempty"`
	Discovery      DiscoveryPolicy   `json:"discovery,omitempty"`
	Model          ModelPolicy       `json:"model,omitempty"`
	Plugins        []PluginRef       `json:"plugins,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
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
