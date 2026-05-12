package appconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"gopkg.in/yaml.v3"
)

const DefaultManifestName = "agentsdk.app.json"

var DefaultManifestNames = []string{
	"agentsdk.app.json",
	"agentsdk.app.yaml",
	"agentsdk.app.yml",
}

// LoadDir loads the default app manifest from dir.
func LoadDir(ctx context.Context, dir string) (resource.ContributionBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return resource.ContributionBundle{}, err
	}
	var missing []string
	for _, name := range DefaultManifestNames {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err == nil {
			return DecodeManifest(path, data)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return resource.ContributionBundle{}, fmt.Errorf("appconfig: read manifest %s: %w", path, err)
		}
		missing = append(missing, name)
	}
	return resource.ContributionBundle{}, fmt.Errorf("appconfig: no manifest found in %s (looked for %s)", filepath.Clean(dir), strings.Join(missing, ", "))
}

// DecodeManifest decodes one local app manifest.
func DecodeManifest(path string, data []byte) (resource.ContributionBundle, error) {
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return resource.ContributionBundle{}, fmt.Errorf("appconfig: decode manifest: %w", err)
	}

	spec := manifest.Spec()
	if err := spec.Validate(); err != nil {
		return resource.ContributionBundle{}, fmt.Errorf("appconfig: validate manifest: %w", err)
	}

	source := resource.SourceRef{
		ID:       "appconfig:" + filepath.Clean(path),
		Scope:    resource.ScopeProject,
		Location: filepath.Clean(path),
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	}
	bundle := resource.ContributionBundle{
		Source: source,
		Apps:   []coreapp.Spec{spec},
	}
	for _, plugin := range spec.Plugins {
		bundle.Plugins = append(bundle.Plugins, resource.PluginRef{
			Name:   plugin.Name,
			Config: cloneMap(plugin.Config),
		})
	}
	return bundle, nil
}

// Manifest is the app manifest file shape accepted by this adapter.
type Manifest struct {
	Name         coreapp.Name `json:"name,omitempty" yaml:"name,omitempty"`
	Description  string       `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultAgent agentRef     `json:"default_agent,omitempty" yaml:"default_agent,omitempty"`
	Sources      []sourceSpec `json:"sources,omitempty" yaml:"sources,omitempty"`
	Discovery    discovery    `json:"discovery,omitempty" yaml:"discovery,omitempty"`
	ModelPolicy  modelPolicy  `json:"model_policy,omitempty" yaml:"model_policy,omitempty"`
	Plugins      []pluginRef  `json:"plugins,omitempty" yaml:"plugins,omitempty"`
}

// Spec converts the manifest file shape to the pure app model.
func (m Manifest) Spec() coreapp.Spec {
	spec := coreapp.Spec{
		Name:         m.Name,
		Description:  m.Description,
		DefaultAgent: agent.Ref(m.DefaultAgent),
		Discovery:    m.Discovery.Spec(),
		Model:        m.ModelPolicy.Spec(),
	}
	for _, source := range m.Sources {
		spec.Sources = append(spec.Sources, coreapp.SourceSpec(source))
	}
	for _, plugin := range m.Plugins {
		spec.Plugins = append(spec.Plugins, coreapp.PluginRef(plugin))
	}
	return spec
}

type discovery struct {
	IncludeGlobalUserResources bool   `json:"include_global_user_resources,omitempty" yaml:"include_global_user_resources,omitempty"`
	IncludeExternalEcosystems  bool   `json:"include_external_ecosystems,omitempty" yaml:"include_external_ecosystems,omitempty"`
	AllowRemote                bool   `json:"allow_remote,omitempty" yaml:"allow_remote,omitempty"`
	TrustStoreDir              string `json:"trust_store_dir,omitempty" yaml:"trust_store_dir,omitempty"`
}

func (d discovery) Spec() coreapp.DiscoveryPolicy {
	return coreapp.DiscoveryPolicy{
		IncludeGlobalUserResources: d.IncludeGlobalUserResources,
		IncludeExternalEcosystems:  d.IncludeExternalEcosystems,
		AllowRemote:                d.AllowRemote,
		TrustStoreDir:              d.TrustStoreDir,
	}
}

type modelPolicy struct {
	Model         string            `json:"model,omitempty" yaml:"model,omitempty"`
	Provider      string            `json:"provider,omitempty" yaml:"provider,omitempty"`
	UseCase       string            `json:"use_case,omitempty" yaml:"use_case,omitempty"`
	SourceAPI     string            `json:"source_api,omitempty" yaml:"source_api,omitempty"`
	ApprovedOnly  *bool             `json:"approved_only,omitempty" yaml:"approved_only,omitempty"`
	AllowDegraded *bool             `json:"allow_degraded,omitempty" yaml:"allow_degraded,omitempty"`
	AllowUntested *bool             `json:"allow_untested,omitempty" yaml:"allow_untested,omitempty"`
	EvidencePath  string            `json:"evidence_path,omitempty" yaml:"evidence_path,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (m modelPolicy) Spec() coreapp.ModelPolicy {
	return coreapp.ModelPolicy{
		Model:         m.Model,
		Provider:      m.Provider,
		UseCase:       m.UseCase,
		SourceAPI:     m.SourceAPI,
		ApprovedOnly:  m.ApprovedOnly,
		AllowDegraded: m.AllowDegraded,
		AllowUntested: m.AllowUntested,
		EvidencePath:  m.EvidencePath,
		Annotations:   cloneStringMap(m.Annotations),
	}
}

type agentRef agent.Ref

func (r *agentRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var name string
		if err := node.Decode(&name); err != nil {
			return err
		}
		*r = agentRef{agent.Name(strings.TrimSpace(name))}
		return nil
	}
	var ref agent.Ref
	if err := node.Decode(&ref); err != nil {
		return err
	}
	ref.Name = agent.Name(strings.TrimSpace(string(ref.Name)))
	*r = agentRef(ref)
	return nil
}

func (r *agentRef) UnmarshalJSON(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) == 0 {
		return nil
	}
	return r.UnmarshalYAML(node.Content[0])
}

type sourceSpec coreapp.SourceSpec

func (s *sourceSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var location string
		if err := node.Decode(&location); err != nil {
			return err
		}
		*s = sourceSpec{Location: strings.TrimSpace(location)}
		return nil
	}
	var spec coreapp.SourceSpec
	if err := node.Decode(&spec); err != nil {
		return err
	}
	spec.Location = strings.TrimSpace(spec.Location)
	*s = sourceSpec(spec)
	return nil
}

func (s *sourceSpec) UnmarshalJSON(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) == 0 {
		return nil
	}
	return s.UnmarshalYAML(node.Content[0])
}

type pluginRef coreapp.PluginRef

func (p *pluginRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var name string
		if err := node.Decode(&name); err != nil {
			return err
		}
		*p = pluginRef{Name: strings.TrimSpace(name)}
		return nil
	}
	var ref coreapp.PluginRef
	if err := node.Decode(&ref); err != nil {
		return err
	}
	ref.Name = strings.TrimSpace(ref.Name)
	ref.Config = cloneMap(ref.Config)
	*p = pluginRef(ref)
	return nil
}

func (p *pluginRef) UnmarshalJSON(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) == 0 {
		return nil
	}
	return p.UnmarshalYAML(node.Content[0])
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

var (
	_ json.Unmarshaler = (*agentRef)(nil)
	_ json.Unmarshaler = (*sourceSpec)(nil)
	_ json.Unmarshaler = (*pluginRef)(nil)
)
