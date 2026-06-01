package appconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/user"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

// NewManifest starts an in-process builder for the app manifest shape accepted
// by this package.
func NewManifest(name string) *ManifestBuilder {
	return &ManifestBuilder{
		source: resource.SourceRef{ID: name, Scope: resource.ScopeEmbedded, Location: name},
		manifest: Manifest{
			Name: coreapp.Name(name),
		},
		plugins: map[string]map[string]any{},
	}
}

// ManifestBuilder builds a contribution bundle through the same document
// decoders used by appconfig manifest files.
type ManifestBuilder struct {
	source   resource.SourceRef
	manifest Manifest
	plugins  map[string]map[string]any
	agents   []agentDoc
	sessions []sessionDoc
	errs     []error
}

// WithSource sets the contribution source used for the built bundle.
func (b *ManifestBuilder) WithSource(source resource.SourceRef) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.source = source
	return b
}

// WithDescription sets the app manifest description.
func (b *ManifestBuilder) WithDescription(description string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Description = description
	return b
}

// WithDefaults sets manifest defaults for profile, agent, and model. Empty
// values leave the corresponding default unset.
func (b *ManifestBuilder) WithDefaults(profile, agentName, model string) *ManifestBuilder {
	if b == nil {
		return b
	}
	if strings.TrimSpace(profile) != "" {
		b.manifest.Defaults.Profile = profile
	}
	if strings.TrimSpace(agentName) != "" {
		b.manifest.Defaults.Agent = agentRef{agent.Name(agentName)}
	}
	if strings.TrimSpace(model) != "" {
		b.manifest.Defaults.Model = ModelSelector(model)
	}
	return b
}

// WithDefaultProfile sets defaults.profile.
func (b *ManifestBuilder) WithDefaultProfile(profile string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Defaults.Profile = profile
	return b
}

// WithDefaultAgentName sets defaults.agent without adding an agent document.
func (b *ManifestBuilder) WithDefaultAgentName(name string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Defaults.Agent = agentRef{agent.Name(name)}
	return b
}

// WithDefaultModel sets defaults.model.
func (b *ManifestBuilder) WithDefaultModel(model string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Defaults.Model = ModelSelector(model)
	return b
}

// WithModel sets the app-level model_policy fields.
func (b *ManifestBuilder) WithModel(provider, model, useCase string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.ModelPolicy.Provider = provider
	b.manifest.ModelPolicy.Model = ModelSelector(model)
	b.manifest.ModelPolicy.UseCase = useCase
	return b
}

// WithModelAlias declares one provider model and its manifest aliases under
// models.available.
func (b *ManifestBuilder) WithModelAlias(provider, model string, aliases ...string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Models.Available = append(b.manifest.Models.Available, modelAvailableDoc{
		Provider: provider,
		Model:    model,
		Aliases:  append([]string(nil), aliases...),
	})
	return b
}

// WithPlugin declares an enabled plugin instance whose kind equals its
// instance name.
func (b *ManifestBuilder) WithPlugin(kind string) *ManifestBuilder {
	return b.WithPluginConfig(kind, kind, nil)
}

// WithPluginConfig declares an enabled plugin instance and encodes cfg into the
// raw manifest config map. The instance is the manifest map key; kind defaults
// to instance when empty.
func (b *ManifestBuilder) WithPluginConfig(instance, kind string, cfg any) *ManifestBuilder {
	if b == nil {
		return b
	}
	instance = strings.TrimSpace(instance)
	kind = strings.TrimSpace(kind)
	if instance == "" {
		instance = kind
	}
	if kind == "" {
		kind = instance
	}
	if instance == "" {
		b.errs = append(b.errs, fmt.Errorf("appconfig: plugin instance is empty"))
		return b
	}
	raw, err := pluginConfigMap(cfg)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("appconfig: plugin %q config: %w", instance, err))
		return b
	}
	if b.plugins == nil {
		b.plugins = map[string]map[string]any{}
	}
	entry := cloneMap(raw)
	if kind != "" && kind != instance {
		if entry == nil {
			entry = map[string]any{}
		}
		entry["kind"] = kind
	}
	b.plugins[instance] = entry
	return b
}

// WithIdentityGroup appends an app-local identity group declaration.
func (b *ManifestBuilder) WithIdentityGroup(group user.Group) *ManifestBuilder {
	if b == nil {
		return b
	}
	doc := identityGroupDoc{
		ID:          string(group.ID),
		DisplayName: group.DisplayName,
		Trust:       group.Trust,
		Annotations: cloneStringMap(group.Annotations),
	}
	for _, member := range group.Members {
		doc.Members = append(doc.Members, string(member))
	}
	b.manifest.Identity.Groups = append(b.manifest.Identity.Groups, doc)
	return b
}

// WithIdentityRule appends an app-local identity group rule declaration.
func (b *ManifestBuilder) WithIdentityRule(rule user.GroupRule) *ManifestBuilder {
	if b == nil {
		return b
	}
	doc := identityRuleDoc{
		Match: identityMatchDoc{
			Provider:   rule.Match.Provider,
			ProviderID: rule.Match.ProviderID,
			Resolution: rule.Match.Resolution,
		},
	}
	for _, group := range rule.Groups {
		doc.Groups = append(doc.Groups, string(group))
	}
	b.manifest.Identity.Rules = append(b.manifest.Identity.Rules, doc)
	return b
}

// WithDatasourceIndex sets app-level datasource index defaults.
func (b *ManifestBuilder) WithDatasourceIndex(concurrency int, freshness string) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Datasource.Index = datasourceIndexDefaultsDoc{
		Concurrency: concurrency,
		Freshness:   DurationString(freshness),
	}
	return b
}

// WithDatasource appends a manifest-shaped datasource declaration.
func (b *ManifestBuilder) WithDatasource(doc DatasourceDoc) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Datasource.Datasources = append(b.manifest.Datasource.Datasources, doc)
	return b
}

// WithDatasourceSpec appends a core datasource spec using the fields supported
// by app manifest datasource documents.
func (b *ManifestBuilder) WithDatasourceSpec(spec coredatasource.Spec) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.manifest.Datasource.Datasources = append(b.manifest.Datasource.Datasources, datasourceDocFromSpec(spec))
	return b
}

// WithAgent appends an agent document using the fields supported by appconfig
// agent manifests.
func (b *ManifestBuilder) WithAgent(spec agent.Spec) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.agents = append(b.agents, agentDocFromSpec(spec))
	return b
}

// WithDefaultAgent appends an agent document and sets it as defaults.agent.
func (b *ManifestBuilder) WithDefaultAgent(spec agent.Spec) *ManifestBuilder {
	if b == nil {
		return b
	}
	return b.WithAgent(spec).WithDefaultAgentName(string(spec.Name))
}

// WithSession appends a session document using the fields supported by
// appconfig session manifests.
func (b *ManifestBuilder) WithSession(spec coresession.Spec) *ManifestBuilder {
	if b == nil {
		return b
	}
	b.sessions = append(b.sessions, sessionDocFromSpec(spec))
	return b
}

// Build decodes the accumulated manifest documents into a contribution bundle.
func (b *ManifestBuilder) Build() (resource.ContributionBundle, error) {
	if b == nil {
		return resource.ContributionBundle{}, fmt.Errorf("appconfig: nil manifest builder")
	}
	if err := errors.Join(b.errs...); err != nil {
		return resource.ContributionBundle{}, err
	}
	bundle := resource.ContributionBundle{Source: b.source}
	distribution := coredistribution.Spec{}
	runtime := RuntimeConfig{}
	daemon := DaemonConfig{}
	profiles := ProfileSet(nil)
	state := manifestDecodeState{
		Bundle:       &bundle,
		Distribution: &distribution,
		Runtime:      &runtime,
		Daemon:       &daemon,
		Profiles:     &profiles,
	}
	appDoc, err := b.appDocument()
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	if err := decodeAppDocument(appDoc, &state); err != nil {
		return resource.ContributionBundle{}, err
	}
	for i, doc := range b.agents {
		if err := decodeAgentDocument(doc, &state); err != nil {
			return resource.ContributionBundle{}, fmt.Errorf("appconfig: build agent[%d]: %w", i, err)
		}
	}
	for i, doc := range b.sessions {
		if err := decodeSessionDocument(doc, &state); err != nil {
			return resource.ContributionBundle{}, fmt.Errorf("appconfig: build session[%d]: %w", i, err)
		}
	}
	inferSingleAgentDefault(&bundle)
	return bundle, nil
}

func (b *ManifestBuilder) appDocument() (map[string]any, error) {
	data, err := json.Marshal(b.manifest)
	if err != nil {
		return nil, fmt.Errorf("appconfig: marshal manifest builder app document: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("appconfig: decode manifest builder app document: %w", err)
	}
	if len(b.plugins) > 0 {
		plugins := make(map[string]any, len(b.plugins))
		for instance, cfg := range b.plugins {
			if len(cfg) == 0 {
				plugins[instance] = nil
				continue
			}
			plugins[instance] = cloneMap(cfg)
		}
		doc["plugins"] = plugins
	}
	return doc, nil
}

func pluginConfigMap(cfg any) (map[string]any, error) {
	if cfg == nil {
		return nil, nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode plugin config: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("encode plugin config: %w", err)
	}
	return cloneMap(raw), nil
}

func agentDocFromSpec(spec agent.Spec) agentDoc {
	doc := agentDoc{
		Name:        string(spec.Name),
		Description: spec.Description,
		Model:       ModelSelector(spec.Inference.Model),
		MaxTokens:   spec.Inference.MaxOutputTokens,
		Thinking:    ThinkingMode(spec.Inference.Thinking),
		Effort:      ReasoningEffort(spec.Inference.ReasoningEffort),
		System:      spec.System,
		Turns: turnsDoc{
			MaxSteps: spec.Turns.MaxSteps,
			Continuation: continuationDoc{
				MaxContinuations: spec.Turns.Continuation.MaxContinuations,
				ContextPolicy:    spec.Turns.Continuation.ContextPolicy,
				StopCondition:    stopConditionDocFromSpec(spec.Turns.Continuation.StopCondition),
			},
		},
		Uses: append([]string(nil), spec.ActivationSets...),
	}
	for _, ref := range spec.Operations {
		if ref.Name != "" {
			doc.Operations = append(doc.Operations, string(ref.Name))
		}
	}
	for _, ref := range spec.Tools {
		if strings.TrimSpace(ref.Name) != "" {
			doc.Tools = append(doc.Tools, ref.Name)
		}
	}
	for _, ref := range spec.Context {
		if ref.Name != "" {
			doc.Context = append(doc.Context, string(ref.Name))
		}
	}
	for _, ref := range spec.Datasources {
		if ref.Name != "" {
			doc.Datasources = append(doc.Datasources, string(ref.Name))
		}
	}
	for _, ref := range spec.Skills {
		if ref.Name != "" {
			doc.Skills = append(doc.Skills, string(ref.Name))
		}
	}
	return doc
}

func stopConditionDocFromSpec(spec agent.StopConditionSpec) stopConditionDoc {
	doc := stopConditionDoc{
		Type:        spec.Type,
		Max:         spec.Max,
		Prompt:      spec.Prompt,
		Session:     spec.Session,
		Annotations: cloneStringMap(spec.Annotations),
	}
	for _, condition := range spec.Conditions {
		child := stopConditionDocFromSpec(condition)
		doc.Conditions = append(doc.Conditions, &child)
	}
	return doc
}

func sessionDocFromSpec(spec coresession.Spec) sessionDoc {
	return sessionDoc{
		Name:        string(spec.Name),
		Description: spec.Description,
		Agent:       string(spec.Agent.Name),
		Channel:     string(spec.Channel.Name),
		Metadata:    cloneStringMap(spec.Metadata),
	}
}

func datasourceDocFromSpec(spec coredatasource.Spec) DatasourceDoc {
	doc := DatasourceDoc{
		Name:        string(spec.Name),
		Description: spec.Description,
		Kind:        spec.Kind,
		Config:      cloneStringMap(spec.Config),
		Index: datasourceIndexDoc{
			Enabled:   spec.Index.Enabled,
			Freshness: DurationString(spec.Index.Freshness),
		},
		Semantic: semanticDocFromSpec(spec.Semantic),
	}
	for _, entity := range spec.Entities {
		doc.Entities = append(doc.Entities, string(entity))
	}
	return doc
}

func semanticDocFromSpec(spec coredatasource.SemanticSpec) semanticDoc {
	doc := semanticDoc{Enabled: spec.Enabled}
	if len(spec.Entities) == 0 {
		return doc
	}
	doc.Entities = map[string]entitySemanticDoc{}
	for name, entity := range spec.Entities {
		doc.Entities[string(name)] = entitySemanticDoc{
			Corpus: corpusDoc{
				TitleFields:    append([]string(nil), entity.Corpus.TitleFields...),
				BodyFields:     append([]string(nil), entity.Corpus.BodyFields...),
				MetadataFields: append([]string(nil), entity.Corpus.MetadataFields...),
				ExcludeFields:  append([]string(nil), entity.Corpus.ExcludeFields...),
			},
			Chunking: chunkingDoc{
				Strategy:      entity.Chunking.Strategy,
				TargetTokens:  entity.Chunking.TargetTokens,
				OverlapTokens: entity.Chunking.OverlapTokens,
			},
			Retrieval: retrievalDoc{
				Mode:     entity.Retrieval.Mode,
				Limit:    entity.Retrieval.Limit,
				MinScore: entity.Retrieval.MinScore,
			},
			Incremental: incrementalDoc{
				UpdatedAtField: entity.Incremental.UpdatedAtField,
			},
		}
	}
	return doc
}
