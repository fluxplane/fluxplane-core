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
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	coreskill "github.com/fluxplane/agentruntime/core/skill"
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
	Path         string
	Bundle       resource.ContributionBundle
	Distribution coredistribution.Spec
	Daemon       DaemonConfig
	Connectors   map[string]ConnectorDoc
}

// DecodeFile decodes one local app file. It supports both the legacy single
// app document and the rewrite-native multi-document kind-based shape.
func DecodeFile(path string, data []byte) (File, error) {
	source := manifestSource(path)
	bundle := resource.ContributionBundle{Source: source}
	distribution := coredistribution.Spec{}
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
			distribution = manifest.Distribution.Spec()
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
	return File{Path: filepath.Clean(path), Bundle: bundle, Distribution: distribution, Daemon: daemon, Connectors: connectors}, nil
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
	Kind           string                  `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name           coreapp.Name            `json:"name,omitempty" yaml:"name,omitempty"`
	Description    string                  `json:"description,omitempty" yaml:"description,omitempty"`
	DefaultAgent   agentRef                `json:"default_agent,omitempty" yaml:"default_agent,omitempty"`
	Sources        []sourceSpec            `json:"sources,omitempty" yaml:"sources,omitempty"`
	Discovery      discovery               `json:"discovery,omitempty" yaml:"discovery,omitempty"`
	ModelPolicy    modelPolicy             `json:"model_policy,omitempty" yaml:"model_policy,omitempty"`
	SemanticSearch semanticSearchDoc       `json:"semantic_search,omitempty" yaml:"semantic_search,omitempty"`
	Distribution   distributionDoc         `json:"distribution,omitempty" yaml:"distribution,omitempty"`
	Plugins        []pluginRef             `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	Datasources    []DatasourceDoc         `json:"datasources,omitempty" yaml:"datasources,omitempty"`
	Daemon         DaemonConfig            `json:"daemon,omitempty" yaml:"daemon,omitempty"`
	Connectors     map[string]ConnectorDoc `json:"connectors,omitempty" yaml:"connectors,omitempty"`
}

type distributionDoc struct {
	Name                string                   `json:"name,omitempty" yaml:"name,omitempty"`
	Title               string                   `json:"title,omitempty" yaml:"title,omitempty"`
	Description         string                   `json:"description,omitempty" yaml:"description,omitempty"`
	Author              string                   `json:"author,omitempty" yaml:"author,omitempty"`
	Version             string                   `json:"version,omitempty" yaml:"version,omitempty"`
	DefaultSession      string                   `json:"default_session,omitempty" yaml:"default_session,omitempty"`
	DefaultConversation string                   `json:"default_conversation,omitempty" yaml:"default_conversation,omitempty"`
	DefaultModel        modelDefaultDoc          `json:"default_model,omitempty" yaml:"default_model,omitempty"`
	Surfaces            surfacesDoc              `json:"surfaces,omitempty" yaml:"surfaces,omitempty"`
	Build               buildDoc                 `json:"build,omitempty" yaml:"build,omitempty"`
	Commands            []distributionCommandDoc `json:"commands,omitempty" yaml:"commands,omitempty"`
	Metadata            map[string]string        `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

func (d distributionDoc) Spec() coredistribution.Spec {
	spec := coredistribution.Spec{
		Name:                strings.TrimSpace(d.Name),
		Title:               strings.TrimSpace(d.Title),
		Description:         strings.TrimSpace(d.Description),
		Author:              strings.TrimSpace(d.Author),
		Version:             strings.TrimSpace(d.Version),
		DefaultSession:      coresession.Ref{Name: coresession.Name(strings.TrimSpace(d.DefaultSession))},
		DefaultConversation: channel.ConversationRef{ID: strings.TrimSpace(d.DefaultConversation)},
		DefaultModel: coredistribution.ModelDefault{
			Provider: strings.TrimSpace(d.DefaultModel.Provider),
			Model:    strings.TrimSpace(d.DefaultModel.Model),
			UseCase:  strings.TrimSpace(d.DefaultModel.UseCase),
		},
		Surfaces: coredistribution.Surfaces{
			CLI:      d.Surfaces.CLI,
			REPL:     d.Surfaces.REPL,
			OneShot:  d.Surfaces.OneShot,
			Serve:    d.Surfaces.Serve,
			Deploy:   d.Surfaces.Deploy,
			Validate: d.Surfaces.Validate,
			Status:   d.Surfaces.Status,
			Discover: d.Surfaces.Discover,
		},
		Build: coredistribution.BuildSpec{
			Assets: cleaned(d.Build.Assets),
		},
		Metadata: cloneStringMap(d.Metadata),
	}
	if d.Build.Docker != nil {
		spec.Build.Docker = d.Build.Docker.Spec()
	}
	for _, command := range d.Commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		spec.Commands = append(spec.Commands, coredistribution.Command{
			Name:        name,
			Description: strings.TrimSpace(command.Description),
			Metadata:    cloneStringMap(command.Metadata),
		})
	}
	return spec
}

type modelDefaultDoc struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model    string `json:"model,omitempty" yaml:"model,omitempty"`
	UseCase  string `json:"use_case,omitempty" yaml:"use_case,omitempty"`
}

type surfacesDoc struct {
	CLI      bool `json:"cli,omitempty" yaml:"cli,omitempty"`
	REPL     bool `json:"repl,omitempty" yaml:"repl,omitempty"`
	OneShot  bool `json:"one_shot,omitempty" yaml:"one_shot,omitempty"`
	Serve    bool `json:"serve,omitempty" yaml:"serve,omitempty"`
	Deploy   bool `json:"deploy,omitempty" yaml:"deploy,omitempty"`
	Validate bool `json:"validate,omitempty" yaml:"validate,omitempty"`
	Status   bool `json:"status,omitempty" yaml:"status,omitempty"`
	Discover bool `json:"discover,omitempty" yaml:"discover,omitempty"`
}

type buildDoc struct {
	Assets []string        `json:"assets,omitempty" yaml:"assets,omitempty"`
	Docker *dockerBuildDoc `json:"docker,omitempty" yaml:"docker,omitempty"`
}

type dockerBuildDoc struct {
	Image       string            `json:"image,omitempty" yaml:"image,omitempty"`
	Tags        []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Dockerfile  string            `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	Context     string            `json:"context,omitempty" yaml:"context,omitempty"`
	Platforms   []string          `json:"platforms,omitempty" yaml:"platforms,omitempty"`
	BuildArgs   map[string]string `json:"build_args,omitempty" yaml:"build_args,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (d dockerBuildDoc) Spec() *coredistribution.DockerBuildSpec {
	return &coredistribution.DockerBuildSpec{
		Image:       strings.TrimSpace(d.Image),
		Tags:        cleaned(d.Tags),
		Dockerfile:  strings.TrimSpace(d.Dockerfile),
		Context:     strings.TrimSpace(d.Context),
		Platforms:   cleaned(d.Platforms),
		BuildArgs:   cloneStringMap(d.BuildArgs),
		Annotations: cloneStringMap(d.Annotations),
	}
}

type distributionCommandDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
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
	Semantic    semanticDoc       `json:"semantic,omitempty" yaml:"semantic,omitempty"`
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
		Semantic:    d.Semantic.Spec(),
	}
}

type semanticDoc struct {
	Enabled  bool                         `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Entities map[string]entitySemanticDoc `json:"entities,omitempty" yaml:"entities,omitempty"`
}

func (d semanticDoc) Spec() coredatasource.SemanticSpec {
	out := coredatasource.SemanticSpec{Enabled: d.Enabled}
	if len(d.Entities) > 0 {
		out.Entities = map[coredatasource.EntityType]coredatasource.EntitySemantic{}
		for name, entity := range d.Entities {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			out.Entities[coredatasource.EntityType(name)] = entity.Spec()
		}
	}
	return out
}

type entitySemanticDoc struct {
	Corpus      corpusDoc      `json:"corpus,omitempty" yaml:"corpus,omitempty"`
	Chunking    chunkingDoc    `json:"chunking,omitempty" yaml:"chunking,omitempty"`
	Retrieval   retrievalDoc   `json:"retrieval,omitempty" yaml:"retrieval,omitempty"`
	Incremental incrementalDoc `json:"incremental,omitempty" yaml:"incremental,omitempty"`
}

func (d entitySemanticDoc) Spec() coredatasource.EntitySemantic {
	return coredatasource.EntitySemantic{
		Corpus:      d.Corpus.Spec(),
		Chunking:    d.Chunking.Spec(),
		Retrieval:   d.Retrieval.Spec(),
		Incremental: d.Incremental.Spec(),
	}
}

type corpusDoc struct {
	TitleFields    []string `json:"title_fields,omitempty" yaml:"title_fields,omitempty"`
	BodyFields     []string `json:"body_fields,omitempty" yaml:"body_fields,omitempty"`
	MetadataFields []string `json:"metadata_fields,omitempty" yaml:"metadata_fields,omitempty"`
	ExcludeFields  []string `json:"exclude_fields,omitempty" yaml:"exclude_fields,omitempty"`
}

func (d corpusDoc) Spec() coredatasource.CorpusSpec {
	return coredatasource.CorpusSpec{
		TitleFields:    cleaned(d.TitleFields),
		BodyFields:     cleaned(d.BodyFields),
		MetadataFields: cleaned(d.MetadataFields),
		ExcludeFields:  cleaned(d.ExcludeFields),
	}
}

type chunkingDoc struct {
	Strategy      string `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	TargetTokens  int    `json:"target_tokens,omitempty" yaml:"target_tokens,omitempty"`
	OverlapTokens int    `json:"overlap_tokens,omitempty" yaml:"overlap_tokens,omitempty"`
}

func (d chunkingDoc) Spec() coredatasource.ChunkingSpec {
	return coredatasource.ChunkingSpec{
		Strategy:      strings.TrimSpace(d.Strategy),
		TargetTokens:  d.TargetTokens,
		OverlapTokens: d.OverlapTokens,
	}
}

type retrievalDoc struct {
	Mode     string  `json:"mode,omitempty" yaml:"mode,omitempty"`
	Limit    int     `json:"limit,omitempty" yaml:"limit,omitempty"`
	MinScore float64 `json:"min_score,omitempty" yaml:"min_score,omitempty"`
}

func (d retrievalDoc) Spec() coredatasource.RetrievalSpec {
	return coredatasource.RetrievalSpec{
		Mode:     strings.TrimSpace(d.Mode),
		Limit:    d.Limit,
		MinScore: d.MinScore,
	}
}

type incrementalDoc struct {
	UpdatedAtField string `json:"updated_at_field,omitempty" yaml:"updated_at_field,omitempty"`
}

func (d incrementalDoc) Spec() coredatasource.IncrementalSpec {
	return coredatasource.IncrementalSpec{UpdatedAtField: strings.TrimSpace(d.UpdatedAtField)}
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
	Skills           []string `json:"skills,omitempty" yaml:"skills,omitempty"`
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
	for _, name := range raw.Skills {
		name = strings.TrimSpace(name)
		if name != "" {
			spec.Skills = append(spec.Skills, coreskill.Ref{Name: coreskill.Name(name)})
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
		Name:           m.Name,
		Description:    m.Description,
		DefaultAgent:   agent.Ref(m.DefaultAgent),
		Discovery:      m.Discovery.Spec(),
		Model:          m.ModelPolicy.Spec(),
		SemanticSearch: m.SemanticSearch.Spec(),
	}
	for _, source := range m.Sources {
		spec.Sources = append(spec.Sources, coreapp.SourceSpec(source))
	}
	for _, plugin := range m.Plugins {
		spec.Plugins = append(spec.Plugins, coreapp.PluginRef(plugin))
	}
	return spec
}

type semanticSearchDoc struct {
	Enabled    bool                `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Embeddings embeddingDoc        `json:"embeddings,omitempty" yaml:"embeddings,omitempty"`
	Store      semanticStoreDoc    `json:"store,omitempty" yaml:"store,omitempty"`
	Defaults   semanticDefaultsDoc `json:"defaults,omitempty" yaml:"defaults,omitempty"`
}

func (d semanticSearchDoc) Spec() coreapp.SemanticSearchSpec {
	return coreapp.SemanticSearchSpec{
		Enabled: d.Enabled,
		Embeddings: coreapp.EmbeddingSpec{
			Provider: strings.TrimSpace(d.Embeddings.Provider),
			Model:    strings.TrimSpace(d.Embeddings.Model),
		},
		Store: coreapp.SemanticStoreSpec{
			Kind: strings.TrimSpace(d.Store.Kind),
			Path: strings.TrimSpace(d.Store.Path),
		},
		Defaults: coreapp.SemanticDefaults{
			Chunking: coreapp.SemanticChunkingSpec{
				Strategy:      strings.TrimSpace(d.Defaults.Chunking.Strategy),
				TargetTokens:  d.Defaults.Chunking.TargetTokens,
				OverlapTokens: d.Defaults.Chunking.OverlapTokens,
			},
			Retrieval: coreapp.SemanticRetrievalSpec{
				Mode:     strings.TrimSpace(d.Defaults.Retrieval.Mode),
				Limit:    d.Defaults.Retrieval.Limit,
				MinScore: d.Defaults.Retrieval.MinScore,
			},
		},
	}
}

type embeddingDoc struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model    string `json:"model,omitempty" yaml:"model,omitempty"`
}

type semanticStoreDoc struct {
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

type semanticDefaultsDoc struct {
	Chunking  chunkingDoc  `json:"chunking,omitempty" yaml:"chunking,omitempty"`
	Retrieval retrievalDoc `json:"retrieval,omitempty" yaml:"retrieval,omitempty"`
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

func cleaned(values []string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

var (
	_ json.Unmarshaler = (*agentRef)(nil)
	_ json.Unmarshaler = (*sourceSpec)(nil)
	_ json.Unmarshaler = (*pluginRef)(nil)
)
