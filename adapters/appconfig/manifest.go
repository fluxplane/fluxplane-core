package appconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
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
	file, err := LoadDirFile(ctx, dir)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	return file.Bundle, nil
}

// LoadDirFile loads the default app manifest from dir and returns both pure
// resource contributions and serve/daemon configuration.
func LoadDirFile(ctx context.Context, dir string) (File, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	var missing []string
	for _, name := range DefaultManifestNames {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err == nil {
			return DecodeFile(path, data)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return File{}, fmt.Errorf("appconfig: read manifest %s: %w", path, err)
		}
		missing = append(missing, name)
	}
	return File{}, fmt.Errorf("appconfig: no manifest found in %s (looked for %s)", filepath.Clean(dir), strings.Join(missing, ", "))
}

// DecodeManifest decodes one local app manifest.
func DecodeManifest(path string, data []byte) (resource.ContributionBundle, error) {
	file, err := DecodeFile(path, data)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	return file.Bundle, nil
}

// File is the complete app configuration file shape after decoding.
type File struct {
	Path       string
	Bundle     resource.ContributionBundle
	Daemon     DaemonConfig
	Connectors map[string]ConnectorDoc
}

// DecodeFile decodes one local app file. It supports both the legacy single
// app document and the rewrite-native multi-document kind-based shape.
func DecodeFile(path string, data []byte) (File, error) {
	source := manifestSource(path)
	bundle := resource.ContributionBundle{Source: source}
	daemon := DaemonConfig{}
	connectors := map[string]ConnectorDoc{}

	docs, err := decodeDocuments(data)
	if err != nil {
		return File{}, fmt.Errorf("appconfig: decode manifest: %w", err)
	}
	if len(docs) == 0 {
		return File{}, fmt.Errorf("appconfig: manifest is empty")
	}
	for i, doc := range docs {
		kind := strings.TrimSpace(documentKind(doc))
		if i == 0 && (kind == "" || kind == "app") {
			var manifest Manifest
			if err := doc.Decode(&manifest); err != nil {
				return File{}, fmt.Errorf("appconfig: decode app document: %w", err)
			}
			spec := manifest.Spec()
			if err := spec.Validate(); err != nil {
				return File{}, fmt.Errorf("appconfig: validate manifest: %w", err)
			}
			bundle.Apps = append(bundle.Apps, spec)
			for _, plugin := range spec.Plugins {
				bundle.Plugins = append(bundle.Plugins, resource.PluginRef{
					Name:   plugin.Name,
					Config: cloneMap(plugin.Config),
				})
			}
			for _, ds := range manifest.Datasources {
				bundle.Datasources = append(bundle.Datasources, ds.Spec())
			}
			daemon = manifest.Daemon
			connectors = cloneConnectorMap(manifest.Connectors)
			continue
		}
		switch kind {
		case "agent":
			spec, err := decodeAgentDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Agents = append(bundle.Agents, spec)
		case "session":
			spec, err := decodeSessionDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Sessions = append(bundle.Sessions, spec)
		case "datasource":
			spec, err := decodeDatasourceDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Datasources = append(bundle.Datasources, spec)
		case "":
			return File{}, fmt.Errorf("appconfig: document %d kind is empty", i+1)
		default:
			return File{}, fmt.Errorf("appconfig: unsupported document kind %q", kind)
		}
	}
	return File{Path: filepath.Clean(path), Bundle: bundle, Daemon: daemon, Connectors: connectors}, nil
}

func manifestSource(path string) resource.SourceRef {
	return resource.SourceRef{
		ID:       "appconfig:" + filepath.Clean(path),
		Scope:    resource.ScopeProject,
		Location: filepath.Clean(path),
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	}
}

func decodeDocuments(data []byte) ([]yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var docs []yaml.Node
	for {
		var doc yaml.Node
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			continue
		}
		docs = append(docs, *doc.Content[0])
	}
	return docs, nil
}

func (f File) Validate() error {
	for i, spec := range f.Bundle.Datasources {
		if err := spec.Validate(); err != nil {
			return fmt.Errorf("appconfig: datasources[%d]: %w", i, err)
		}
	}
	for name, connector := range f.Connectors {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("appconfig: connectors contains an empty instance name")
		}
		if strings.TrimSpace(connector.Kind) == "" {
			return fmt.Errorf("appconfig: connectors[%q].kind is empty", name)
		}
	}
	for i, listener := range f.Daemon.Listeners {
		if strings.TrimSpace(listener.Name) == "" {
			return fmt.Errorf("appconfig: daemon.listeners[%d] name is empty", i)
		}
		if strings.TrimSpace(listener.Type) == "" {
			return fmt.Errorf("appconfig: daemon.listeners[%d] type is empty", i)
		}
	}
	for i, ch := range f.Daemon.Channels {
		if strings.TrimSpace(ch.Name) == "" {
			return fmt.Errorf("appconfig: daemon.channels[%d] name is empty", i)
		}
		if strings.TrimSpace(ch.Type) == "" {
			return fmt.Errorf("appconfig: daemon.channels[%d] type is empty", i)
		}
	}
	return nil
}

type kindDoc struct {
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
}

// Manifest is the app manifest file shape accepted by this adapter.
type Manifest struct {
	Kind         string                  `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name         coreapp.Name            `json:"name,omitempty" yaml:"name,omitempty"`
	Description  string                  `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultAgent agentRef                `json:"default_agent,omitempty" yaml:"default_agent,omitempty"`
	Sources      []sourceSpec            `json:"sources,omitempty" yaml:"sources,omitempty"`
	Discovery    discovery               `json:"discovery,omitempty" yaml:"discovery,omitempty"`
	ModelPolicy  modelPolicy             `json:"model_policy,omitempty" yaml:"model_policy,omitempty"`
	Plugins      []pluginRef             `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	Datasources  []DatasourceDoc         `json:"datasources,omitempty" yaml:"datasources,omitempty"`
	Daemon       DaemonConfig            `json:"daemon,omitempty" yaml:"daemon,omitempty"`
	Connectors   map[string]ConnectorDoc `json:"connectors,omitempty" yaml:"connectors,omitempty"`
}

// DatasourceDoc declares one configured datasource instance.
type DatasourceDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Entities    []string          `json:"entities,omitempty" yaml:"entities,omitempty"`
	Connector   string            `json:"connector,omitempty" yaml:"connector,omitempty"`
	Kind        string            `json:"kind,omitempty" yaml:"kind,omitempty"`
	Type        string            `json:"type,omitempty" yaml:"type,omitempty"`
	Path        string            `json:"path,omitempty" yaml:"path,omitempty"`
	Include     []string          `json:"include,omitempty" yaml:"include,omitempty"`
	Config      map[string]string `json:"config,omitempty" yaml:"config,omitempty"`
}

func (d DatasourceDoc) Spec() coredatasource.Spec {
	cfg := cloneStringMap(d.Config)
	if cfg == nil {
		cfg = map[string]string{}
	}
	if strings.TrimSpace(d.Path) != "" {
		cfg["path"] = strings.TrimSpace(d.Path)
	}
	if len(d.Include) > 0 {
		var include []string
		for _, pattern := range d.Include {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				include = append(include, pattern)
			}
		}
		if len(include) > 0 {
			cfg["include"] = strings.Join(include, ",")
		}
	}
	if len(cfg) == 0 {
		cfg = nil
	}
	var entities []coredatasource.EntityType
	for _, entity := range d.Entities {
		if entity = strings.TrimSpace(entity); entity != "" {
			entities = append(entities, coredatasource.EntityType(entity))
		}
	}
	return coredatasource.Spec{
		Name:        coredatasource.Name(strings.TrimSpace(d.Name)),
		Description: strings.TrimSpace(d.Description),
		Entities:    entities,
		Connector:   strings.TrimSpace(d.Connector),
		Kind:        datasourceKind(d),
		Config:      cfg,
	}
}

func datasourceKind(d DatasourceDoc) string {
	kind := strings.TrimSpace(d.Kind)
	if kind == "datasource" || kind == "" {
		if typ := strings.TrimSpace(d.Type); typ != "" {
			return typ
		}
	}
	return kind
}

// ConnectorDoc declares one connector instance required by agentsdk serve.
type ConnectorDoc struct {
	Kind string `json:"kind" yaml:"kind"`
}

// DaemonConfig contains process wiring consumed by agentsdk serve.
type DaemonConfig struct {
	Listeners []ListenerDoc `json:"listeners,omitempty" yaml:"listeners,omitempty"`
	Channels  []ChannelDoc  `json:"channels,omitempty" yaml:"channels,omitempty"`
}

type ListenerDoc struct {
	Name string         `json:"name" yaml:"name"`
	Type string         `json:"type" yaml:"type"`
	Addr string         `json:"addr,omitempty" yaml:"addr,omitempty"`
	Auth map[string]any `json:"auth,omitempty" yaml:"auth,omitempty"`
}

type ChannelDoc struct {
	Name      string    `json:"name" yaml:"name"`
	Type      string    `json:"type" yaml:"type"`
	Connector string    `json:"connector,omitempty" yaml:"connector,omitempty"`
	Listener  string    `json:"listener,omitempty" yaml:"listener,omitempty"`
	Session   string    `json:"session,omitempty" yaml:"session,omitempty"`
	Access    AccessDoc `json:"access,omitempty" yaml:"access,omitempty"`
}

type AccessDoc struct {
	Mode             string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	AllowUsers       []string `json:"allow_users,omitempty" yaml:"allow_users,omitempty"`
	DenyUsers        []string `json:"deny_users,omitempty" yaml:"deny_users,omitempty"`
	AllowChannels    []string `json:"allow_channels,omitempty" yaml:"allow_channels,omitempty"`
	DenyChannels     []string `json:"deny_channels,omitempty" yaml:"deny_channels,omitempty"`
	AllowKinds       []string `json:"allow_kinds,omitempty" yaml:"allow_kinds,omitempty"`
	DefaultTrust     string   `json:"default_trust,omitempty" yaml:"default_trust,omitempty"`
	Operators        []string `json:"operators,omitempty" yaml:"operators,omitempty"`
	InternalUsers    []string `json:"internal_users,omitempty" yaml:"internal_users,omitempty"`
	InternalChannels []string `json:"internal_channels,omitempty" yaml:"internal_channels,omitempty"`
	Sharing          string   `json:"sharing,omitempty" yaml:"sharing,omitempty"`
}

type agentDoc struct {
	Kind             string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name             string   `json:"name" yaml:"name"`
	Description      string   `json:"description,omitempty" yaml:"description,omitempty"`
	Model            string   `json:"model,omitempty" yaml:"model,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	MaxSteps         int      `json:"max_steps,omitempty" yaml:"max_steps,omitempty"`
	MaxContinuations int      `json:"max_continuations,omitempty" yaml:"max_continuations,omitempty"`
	Thinking         string   `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	Effort           string   `json:"effort,omitempty" yaml:"effort,omitempty"`
	Tools            []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	Context          []string `json:"context,omitempty" yaml:"context,omitempty"`
	Datasources      []string `json:"datasources,omitempty" yaml:"datasources,omitempty"`
	System           string   `json:"system,omitempty" yaml:"system,omitempty"`
}

func decodeAgentDoc(node yaml.Node) (agent.Spec, error) {
	var raw agentDoc
	if err := node.Decode(&raw); err != nil {
		return agent.Spec{}, fmt.Errorf("appconfig: decode agent document: %w", err)
	}
	spec := agent.Spec{
		Name:        agent.Name(strings.TrimSpace(raw.Name)),
		Description: strings.TrimSpace(raw.Description),
		System:      strings.TrimSpace(raw.System),
		Inference: agent.InferenceSpec{
			Model:           strings.TrimSpace(raw.Model),
			MaxOutputTokens: raw.MaxTokens,
			Thinking:        strings.TrimSpace(raw.Thinking),
			ReasoningEffort: strings.TrimSpace(raw.Effort),
		},
		Policy: agent.Policy{MaxSteps: raw.MaxSteps, MaxContinuations: raw.MaxContinuations},
	}
	for _, name := range raw.Tools {
		name = strings.TrimSpace(name)
		if name != "" {
			spec.Tools = append(spec.Tools, agent.ToolRef{Name: name})
		}
	}
	for _, name := range raw.Context {
		name = strings.TrimSpace(name)
		if name != "" {
			spec.Context = append(spec.Context, corecontext.ProviderRef{Name: corecontext.ProviderName(name)})
		}
	}
	for _, name := range raw.Datasources {
		name = strings.TrimSpace(name)
		if name != "" {
			spec.Datasources = append(spec.Datasources, coredatasource.Ref{Name: coredatasource.Name(name)})
		}
	}
	if err := spec.Validate(); err != nil {
		return agent.Spec{}, fmt.Errorf("appconfig: validate agent document: %w", err)
	}
	return spec, nil
}

func decodeDatasourceDoc(node yaml.Node) (coredatasource.Spec, error) {
	var raw DatasourceDoc
	if err := node.Decode(&raw); err != nil {
		return coredatasource.Spec{}, fmt.Errorf("appconfig: decode datasource document: %w", err)
	}
	spec := raw.Spec()
	if err := spec.Validate(); err != nil {
		return coredatasource.Spec{}, fmt.Errorf("appconfig: validate datasource document: %w", err)
	}
	return spec, nil
}

type sessionDoc struct {
	Kind        string            `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Agent       string            `json:"agent,omitempty" yaml:"agent,omitempty"`
	Channel     string            `json:"channel,omitempty" yaml:"channel,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

func decodeSessionDoc(node yaml.Node) (coresession.Spec, error) {
	var raw sessionDoc
	if err := node.Decode(&raw); err != nil {
		return coresession.Spec{}, fmt.Errorf("appconfig: decode session document: %w", err)
	}
	spec := coresession.Spec{
		Name:        coresession.Name(strings.TrimSpace(raw.Name)),
		Description: strings.TrimSpace(raw.Description),
		Agent:       agent.Ref{Name: agent.Name(strings.TrimSpace(raw.Agent))},
		Metadata:    cloneStringMap(raw.Metadata),
	}
	if raw.Channel != "" {
		spec.Channel = channel.Ref{Name: channel.Name(strings.TrimSpace(raw.Channel))}
	}
	if err := spec.Validate(); err != nil {
		return coresession.Spec{}, fmt.Errorf("appconfig: validate session document: %w", err)
	}
	return spec, nil
}

func documentKind(node yaml.Node) string {
	var raw kindDoc
	_ = node.Decode(&raw)
	return raw.Kind
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

func cloneConnectorMap(in map[string]ConnectorDoc) map[string]ConnectorDoc {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ConnectorDoc, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value.Kind = strings.TrimSpace(value.Kind)
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
