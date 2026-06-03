package appconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	coretrigger "github.com/fluxplane/fluxplane-core/core/trigger"
	"github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
	invjsonschema "github.com/invopop/jsonschema"
	santhoshjsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const DefaultManifestName = "fluxplane.yaml"

var DefaultManifestNames = []string{
	"fluxplane.yaml",
}

// DecodeOptions controls profile-sensitive manifest decoding.
type DecodeOptions struct {
	Profile  string
	Profiles []string
}

// LoadDir loads the default app manifest from dir.
func LoadDir(ctx context.Context, dir string) (resource.ContributionBundle, error) {
	file, err := LoadDirFile(ctx, dir)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	return file.Bundle, nil
}

// LoadFS loads one app/resource manifest from fsys at path.
func LoadFS(ctx context.Context, fsys fs.FS, path string) (resource.ContributionBundle, error) {
	file, err := LoadFSFile(ctx, fsys, path)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	return file.Bundle, nil
}

// LoadFSFile loads one app/resource manifest from fsys at path and returns both
// pure resource contributions and serve/daemon configuration.
func LoadFSFile(ctx context.Context, fsys fs.FS, path string) (File, error) {
	return LoadFSFileWithOptions(ctx, fsys, path, DecodeOptions{})
}

// LoadFSFileWithOptions loads one app/resource manifest from fsys at path and
// applies the selected profile before decoding resources.
func LoadFSFileWithOptions(ctx context.Context, fsys fs.FS, path string, opts DecodeOptions) (File, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	if fsys == nil {
		return File{}, fmt.Errorf("appconfig: nil filesystem")
	}
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
	data, err := fs.ReadFile(fsys, clean)
	if err != nil {
		return File{}, fmt.Errorf("appconfig: read manifest %s: %w", clean, err)
	}
	return DecodeFileWithOptions(clean, data, opts)
}

// LoadDirFile loads the default app manifest from dir and returns both pure
// resource contributions and serve/daemon configuration.
func LoadDirFile(ctx context.Context, dir string) (File, error) {
	return LoadDirFileWithOptions(ctx, dir, DecodeOptions{})
}

// LoadDirFileWithOptions loads the default app manifest from dir and applies
// the selected profile before decoding resources.
func LoadDirFileWithOptions(ctx context.Context, dir string, opts DecodeOptions) (File, error) {
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
			return DecodeFileWithOptions(path, data, opts)
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

// DecodeResourceFragment decodes reusable resource lists from a product-owned
// configuration file. Unknown top-level fields are ignored so product-specific
// settings can live beside commands, observations, and reactions.
func DecodeResourceFragment(path string, data []byte) (resource.ContributionBundle, error) {
	bundle := resource.ContributionBundle{Source: manifestSource(path)}
	docs, err := decodeDocuments(data)
	if err != nil {
		return resource.ContributionBundle{}, fmt.Errorf("appconfig: decode resource fragment %s: %w", filepath.Clean(path), err)
	}
	for _, doc := range docs {
		raw, ok := documentPayloadValue(doc.Value).(map[string]any)
		if !ok {
			continue
		}
		if value, ok := raw["commands"]; ok {
			commands, err := decodeCommandList(value)
			if err != nil {
				return resource.ContributionBundle{}, err
			}
			bundle.Commands = append(bundle.Commands, commands...)
		}
		if value, ok := raw["observations"]; ok {
			observations, err := decodeObservations(value)
			if err != nil {
				return resource.ContributionBundle{}, err
			}
			bundle.Observers = append(bundle.Observers, observations.Observers...)
			bundle.AssertionDerivers = append(bundle.AssertionDerivers, observations.AssertionDerivers...)
		}
		if value, ok := raw["reactions"]; ok {
			reactions, err := decodeReactionList(value)
			if err != nil {
				return resource.ContributionBundle{}, err
			}
			bundle.Reactions = append(bundle.Reactions, reactions...)
		}
	}
	return bundle, nil
}

// File is the complete app configuration file shape after decoding.
type File struct {
	Path           string
	Bundle         resource.ContributionBundle
	Distribution   coredistribution.Spec
	Runtime        RuntimeConfig
	Daemon         DaemonConfig
	Profile        string
	ActiveProfiles []string
	Profiles       ProfileSet
}

// DecodeFile decodes one local app file. It supports both the legacy single
// app document and the rewrite-native multi-document kind-based shape.
func DecodeFile(path string, data []byte) (File, error) {
	return DecodeFileWithOptions(path, data, DecodeOptions{})
}

// DecodeFileWithOptions decodes one local app file after selecting the requested
// profile. Unprofiled documents apply to every profile.
func DecodeFileWithOptions(path string, data []byte, opts DecodeOptions) (File, error) {
	source := manifestSource(path)
	bundle := resource.ContributionBundle{Source: source}
	distribution := coredistribution.Spec{}
	runtime := RuntimeConfig{}
	daemon := DaemonConfig{}

	docs, err := decodeDocuments(data)
	if err != nil {
		return File{}, fmt.Errorf("appconfig: decode manifest: %w", err)
	}
	if len(docs) == 0 {
		return File{}, fmt.Errorf("appconfig: manifest is empty")
	}
	defaultProfile, profiles, err := manifestProfileDefaults(docs)
	if err != nil {
		return File{}, err
	}
	selectedProfiles := selectedProfileList(opts)
	explicitProfile := len(selectedProfiles) > 0
	if len(selectedProfiles) == 0 && strings.TrimSpace(defaultProfile) != "" {
		selectedProfiles = []string{strings.TrimSpace(defaultProfile)}
	}
	if len(selectedProfiles) == 0 {
		selectedProfiles = []string{"dev"}
	}
	if err := validateSelectedProfiles(selectedProfiles, profiles, explicitProfile); err != nil {
		return File{}, err
	}
	selectedProfile := strings.Join(selectedProfiles, ",")
	state := manifestDecodeState{
		Bundle:       &bundle,
		Distribution: &distribution,
		Runtime:      &runtime,
		Daemon:       &daemon,
		Profile:      selectedProfile,
		Profiles:     &profiles,
	}
	registry := manifestDocumentRegistry()
	for i, doc := range docs {
		kind := strings.TrimSpace(doc.Kind)
		if i == 0 && kind == "" {
			kind = "app"
		}
		if kind == "" {
			return File{}, fmt.Errorf("appconfig: document %d kind is empty", doc.Index)
		}
		if kind == "app" && i != 0 {
			return File{}, fmt.Errorf("appconfig: app document must be first")
		}
		if !doc.AppliesTo(selectedProfiles) {
			continue
		}
		decoder, ok := registry[kind]
		if !ok {
			return File{}, fmt.Errorf("appconfig: unsupported document kind %q", kind)
		}
		if err := decoder(doc.Value, &state); err != nil {
			return File{}, err
		}
	}
	inferSingleAgentDefault(&bundle)
	if len(bundle.Apps) == 0 && len(bundle.Sessions) == 0 {
		if spec, ok := implicitDefaultSessionForAgentTriggers(bundle.Agents, daemon.Triggers); ok {
			bundle.Sessions = append(bundle.Sessions, spec)
		}
	}
	return File{Path: filepath.Clean(path), Bundle: bundle, Distribution: distribution, Runtime: runtime, Daemon: daemon, Profile: selectedProfile, ActiveProfiles: selectedProfiles, Profiles: profiles}, nil
}

func inferSingleAgentDefault(bundle *resource.ContributionBundle) {
	if bundle == nil || len(bundle.Apps) != 1 || len(bundle.Agents) != 1 {
		return
	}
	if strings.TrimSpace(string(bundle.Apps[0].DefaultAgent.Name)) != "" {
		return
	}
	bundle.Apps[0].DefaultAgent = agent.Ref{Name: bundle.Agents[0].Name}
}

type manifestDecodeState struct {
	Bundle       *resource.ContributionBundle
	Distribution *coredistribution.Spec
	Runtime      *RuntimeConfig
	Daemon       *DaemonConfig
	Profile      string
	Profiles     *ProfileSet
}

type manifestDocumentDecoder func(any, *manifestDecodeState) error

func manifestDocumentRegistry() map[string]manifestDocumentDecoder {
	return map[string]manifestDocumentDecoder{
		"app":               decodeAppDocument,
		"agent":             decodeAgentDocument,
		"session":           decodeSessionDocument,
		"command":           decodeCommandDocument,
		"workflow":          decodeWorkflowDocument,
		"operation":         decodeOperationDocument,
		"datasource":        decodeDatasourceDocument,
		"observer":          decodeObserverDocument,
		"assertion_deriver": decodeAssertionDeriverDocument,
		"reaction":          decodeReactionDocument,
		"llm_provider":      decodeLLMProviderDocument,
		"runtime":           decodeRuntimeDocument,
	}
}

func decodeAppDocument(value any, state *manifestDecodeState) error {
	var manifest Manifest
	if err := decodeDocumentValue(value, &manifest); err != nil {
		return fmt.Errorf("appconfig: decode app document: %w", err)
	}
	spec := manifest.Spec()
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("appconfig: validate manifest: %w", err)
	}
	state.Bundle.Apps = append(state.Bundle.Apps, spec)
	for _, plugin := range spec.Plugins {
		state.Bundle.Plugins = append(state.Bundle.Plugins, resource.PluginRef{
			Name:     plugin.Kind,
			Instance: plugin.Instance,
			Config:   cloneMap(plugin.Config),
		})
	}
	for _, ds := range manifest.Datasource.Datasources {
		state.Bundle.Datasources = append(state.Bundle.Datasources, ds.Spec())
	}
	for i, raw := range manifest.Commands {
		spec, err := raw.Spec()
		if err != nil {
			return fmt.Errorf("appconfig: validate commands[%d]: %w", i, err)
		}
		state.Bundle.Commands = append(state.Bundle.Commands, spec)
	}
	for i, raw := range manifest.Workflows {
		spec, err := raw.Spec()
		if err != nil {
			return fmt.Errorf("appconfig: validate workflows[%d]: %w", i, err)
		}
		state.Bundle.Workflows = append(state.Bundle.Workflows, spec)
	}
	for i, raw := range manifest.Operations {
		spec, err := raw.Spec()
		if err != nil {
			return fmt.Errorf("appconfig: validate operations[%d]: %w", i, err)
		}
		state.Bundle.Operations = append(state.Bundle.Operations, spec)
	}
	state.Bundle.Observers = append(state.Bundle.Observers, manifest.Observations.Observers...)
	state.Bundle.AssertionDerivers = append(state.Bundle.AssertionDerivers, manifest.Observations.AssertionDerivers...)
	for i, rule := range manifest.Reactions {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("appconfig: validate reactions[%d]: %w", i, err)
		}
		state.Bundle.Reactions = append(state.Bundle.Reactions, rule)
	}
	models, err := manifest.Models.Contributions()
	if err != nil {
		return err
	}
	if err := validateModelReference(strings.TrimSpace(string(manifest.Models.Default)), models, "models.default"); err != nil {
		return err
	}
	if err := validateModelReference(strings.TrimSpace(string(manifest.Defaults.Model)), models, "defaults.model"); err != nil {
		return err
	}
	if err := validateModelReference(strings.TrimSpace(string(manifest.Distribution.Deploy.Model)), models, "distribution.deploy.model"); err != nil {
		return err
	}
	for name, target := range manifest.Distribution.Build.Targets {
		if err := validateModelReference(strings.TrimSpace(string(target.Model)), models, "distribution.build.targets."+name+".model"); err != nil {
			return err
		}
	}
	modelProviders := mergeLLMProviders(manifest.LLMProviders, models.Providers)
	for i, provider := range modelProviders {
		if err := provider.Validate(); err != nil {
			return fmt.Errorf("appconfig: validate llm_providers[%d]: %w", i, err)
		}
	}
	state.Bundle.LLMProviders = append(state.Bundle.LLMProviders, modelProviders...)
	state.Bundle.LLMModelAliases = append(state.Bundle.LLMModelAliases, models.Aliases...)
	*state.Distribution = manifest.Distribution.Spec()
	mergeRuntimeConfig(state.Runtime, manifest.Runtime)
	*state.Daemon = manifest.Daemon
	return nil
}

func decodeAgentDocument(value any, state *manifestDecodeState) error {
	spec, triggerResources, err := decodeAgentDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Agents = append(state.Bundle.Agents, spec)
	state.Daemon.Triggers = append(state.Daemon.Triggers, triggerResources.Triggers...)
	state.Bundle.Workflows = append(state.Bundle.Workflows, triggerResources.Workflows...)
	return nil
}

func decodeSessionDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeSessionDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Sessions = append(state.Bundle.Sessions, spec)
	return nil
}

func decodeCommandDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeCommandDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Commands = append(state.Bundle.Commands, spec)
	return nil
}

func decodeWorkflowDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeWorkflowDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Workflows = append(state.Bundle.Workflows, spec)
	return nil
}

func decodeOperationDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeOperationDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Operations = append(state.Bundle.Operations, spec)
	return nil
}

func decodeDatasourceDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeDatasourceDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Datasources = append(state.Bundle.Datasources, spec)
	return nil
}

func decodeObserverDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeObserverDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Observers = append(state.Bundle.Observers, spec)
	return nil
}

func decodeAssertionDeriverDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeAssertionDeriverDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.AssertionDerivers = append(state.Bundle.AssertionDerivers, spec)
	return nil
}

func decodeReactionDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeReactionDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.Reactions = append(state.Bundle.Reactions, spec)
	return nil
}

func decodeCommandList(value any) ([]command.Spec, error) {
	var docs []commandDoc
	if err := decodeDocumentValue(value, &docs); err != nil {
		return nil, fmt.Errorf("appconfig: decode commands: %w", err)
	}
	commands := make([]command.Spec, 0, len(docs))
	for i, doc := range docs {
		spec, err := doc.Spec()
		if err != nil {
			return nil, fmt.Errorf("appconfig: validate commands[%d]: %w", i, err)
		}
		commands = append(commands, spec)
	}
	return commands, nil
}

func decodeObservations(value any) (observationsDoc, error) {
	var doc observationsDoc
	if err := decodeDocumentValue(value, &doc); err != nil {
		return observationsDoc{}, fmt.Errorf("appconfig: decode observations: %w", err)
	}
	for i, observer := range doc.Observers {
		if strings.TrimSpace(observer.Name) == "" {
			return observationsDoc{}, fmt.Errorf("appconfig: validate observations.observers[%d]: name is empty", i)
		}
	}
	for i, deriver := range doc.AssertionDerivers {
		if strings.TrimSpace(deriver.Name) == "" {
			return observationsDoc{}, fmt.Errorf("appconfig: validate observations.assertion_derivers[%d]: name is empty", i)
		}
	}
	return doc, nil
}

func decodeReactionList(value any) ([]corereaction.Rule, error) {
	var docs []reactionDoc
	if err := decodeDocumentValue(value, &docs); err != nil {
		return nil, fmt.Errorf("appconfig: decode reactions: %w", err)
	}
	reactions := make([]corereaction.Rule, 0, len(docs))
	for i, doc := range docs {
		rule := doc.Spec()
		if err := rule.Validate(); err != nil {
			return nil, fmt.Errorf("appconfig: validate reactions[%d]: %w", i, err)
		}
		reactions = append(reactions, rule)
	}
	return reactions, nil
}

func decodeLLMProviderDocument(value any, state *manifestDecodeState) error {
	spec, err := decodeLLMProviderDoc(value)
	if err != nil {
		return err
	}
	state.Bundle.LLMProviders = append(state.Bundle.LLMProviders, spec)
	return nil
}

func decodeRuntimeDocument(value any, state *manifestDecodeState) error {
	var runtime RuntimeConfig
	if err := decodeDocumentValue(value, &runtime); err != nil {
		return fmt.Errorf("appconfig: decode runtime document: %w", err)
	}
	mergeRuntimeConfig(state.Runtime, runtime)
	return nil
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

type manifestDocument struct {
	Index    int
	Kind     string
	Profiles []string
	Value    any
}

func (d manifestDocument) AppliesTo(activeProfiles []string) bool {
	if len(d.Profiles) == 0 {
		return true
	}
	active := map[string]bool{}
	for _, profile := range activeProfiles {
		if profile = strings.TrimSpace(profile); profile != "" {
			active[profile] = true
		}
	}
	for _, candidate := range d.Profiles {
		if active[strings.TrimSpace(candidate)] {
			return true
		}
	}
	return false
}

func decodeDocuments(data []byte) ([]manifestDocument, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var docs []manifestDocument
	index := 0
	for {
		var value any
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if value == nil {
			continue
		}
		index++
		normalized, err := normalizeJSONValue(value)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", index, err)
		}
		docs = append(docs, manifestDocument{
			Index:    index,
			Kind:     documentKind(normalized),
			Profiles: documentProfiles(normalized),
			Value:    normalized,
		})
	}
	return docs, nil
}

func manifestProfileDefaults(docs []manifestDocument) (string, ProfileSet, error) {
	if len(docs) == 0 {
		return "", nil, nil
	}
	first := docs[0]
	kind := strings.TrimSpace(first.Kind)
	if kind == "" {
		kind = "app"
	}
	if kind != "app" {
		return "", nil, nil
	}
	var manifest Manifest
	if err := decodeDocumentValue(first.Value, &manifest); err != nil {
		return "", nil, fmt.Errorf("appconfig: decode app document: %w", err)
	}
	return strings.TrimSpace(manifest.Defaults.Profile), manifest.Profiles.normalized(), nil
}

func selectedProfileList(opts DecodeOptions) []string {
	out := profileNamesFromValues(opts.Profiles)
	if strings.TrimSpace(opts.Profile) != "" {
		out = append(out, profileNamesFromValues([]string{opts.Profile})...)
	}
	return cleaned(out)
}

func validateSelectedProfiles(selected []string, profiles ProfileSet, explicit bool) error {
	if len(selected) == 0 || len(profiles) == 0 {
		return nil
	}
	for _, profile := range selected {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			continue
		}
		if _, ok := profiles[profile]; ok {
			continue
		}
		if explicit {
			return fmt.Errorf("appconfig: profile %q is not declared", profile)
		}
		return fmt.Errorf("appconfig: default profile %q is not declared", profile)
	}
	return nil
}

func documentProfiles(value any) []string {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return profileList(raw["profile"])
}

func profileList(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return cleaned([]string{typed})
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				out = append(out, text)
			}
		}
		return cleaned(out)
	default:
		return nil
	}
}

func profileNamesFromValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// ValidateManifestWithSchema validates every non-empty YAML document in a
// manifest against a generated manifest JSON Schema.
func ValidateManifestWithSchema(schemaData, manifestData []byte) error {
	compiled, err := compileSchemaData(schemaData)
	if err != nil {
		return err
	}
	docs, err := decodeDocuments(manifestData)
	if err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	for _, doc := range docs {
		if err := compiled.Validate(doc.Value); err != nil {
			return fmt.Errorf("document %d schema validation failed: %w", doc.Index, err)
		}
	}
	return nil
}

func decodeDocumentValue[T any](value any, out *T) error {
	payload := documentPayloadValue(value)
	if err := validateJSONValue[T](payload); err != nil {
		return fmt.Errorf("validate schema: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal JSON value: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

func documentPayloadValue(value any) any {
	raw, ok := value.(map[string]any)
	if !ok {
		return value
	}
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		if key == "kind" || key == "profile" {
			continue
		}
		out[key] = value
	}
	return out
}

func validateJSONValue[T any](value any) error {
	value, err := normalizeJSONValue(value)
	if err != nil {
		return err
	}
	schemaData, err := schemaDataFor[T]()
	if err != nil {
		return err
	}
	compiled, err := compileSchemaData(schemaData)
	if err != nil {
		return err
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

func compileSchemaData(schemaData []byte) (*santhoshjsonschema.Schema, error) {
	var schemaValue any
	if err := json.Unmarshal(schemaData, &schemaValue); err != nil {
		return nil, fmt.Errorf("decode schema resource: %w", err)
	}
	compiler := santhoshjsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return compiled, nil
}

func normalizeJSONValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON value: %w", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal JSON value: %w", err)
	}
	return out, nil
}

func schemaDataFor[T any]() ([]byte, error) {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	reflector := invjsonschema.Reflector{
		DoNotReference:             false,
		ExpandedStruct:             true,
		AllowAdditionalProperties:  false,
		RequiredFromJSONSchemaTags: true,
		Mapper:                     schemaEnumMapper,
	}
	ptr := reflect.New(typ)
	if typ.Kind() == reflect.Ptr {
		ptr = reflect.New(typ.Elem())
	}
	schema := reflector.Reflect(ptr.Interface())
	if schema == nil {
		return nil, fmt.Errorf("schema is nil")
	}
	schema.Version = invjsonschema.Version
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return data, nil
}

func (f File) Validate() error {
	for i, spec := range f.Bundle.Datasources {
		if err := spec.Validate(); err != nil {
			return fmt.Errorf("appconfig: datasources[%d]: %w", i, err)
		}
	}
	for i, root := range f.Runtime.Workspace.Roots {
		if strings.TrimSpace(root.Name) == "" {
			return fmt.Errorf("appconfig: runtime.workspace.roots[%d].name is empty", i)
		}
		if strings.ContainsAny(strings.TrimSpace(root.Name), `/\`) || strings.HasPrefix(strings.TrimSpace(root.Name), "@") {
			return fmt.Errorf("appconfig: runtime.workspace.roots[%d].name is invalid", i)
		}
		if strings.TrimSpace(root.Path) == "" {
			return fmt.Errorf("appconfig: runtime.workspace.roots[%d].path is empty", i)
		}
		switch strings.TrimSpace(string(root.Access)) {
		case "", "read_only", "read_write":
		default:
			return fmt.Errorf("appconfig: runtime.workspace.roots[%d].access must be read_only or read_write", i)
		}
		for j, envFile := range root.EnvFiles {
			if strings.TrimSpace(envFile) == "" {
				return fmt.Errorf("appconfig: runtime.workspace.roots[%d].env_files[%d] is empty", i, j)
			}
		}
	}
	for i, envFile := range f.Runtime.Workspace.EnvFiles {
		if strings.TrimSpace(envFile) == "" {
			return fmt.Errorf("appconfig: runtime.workspace.env_files[%d] is empty", i)
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
	for i, trigger := range f.Daemon.Triggers {
		if err := trigger.Validate(); err != nil {
			return fmt.Errorf("appconfig: validate daemon.triggers[%d]: %w", i, err)
		}
	}
	return nil
}

// Manifest is the app manifest file shape accepted by this adapter.
type Manifest struct {
	Name           coreapp.Name               `json:"name,omitempty" yaml:"name,omitempty"`
	Description    string                     `json:"description,omitempty" yaml:"description,omitempty"`
	Defaults       defaultsDoc                `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Profiles       ProfileSet                 `json:"profiles,omitempty" yaml:"profiles,omitempty"`
	DefaultAgent   agentRef                   `json:"default_agent,omitempty" yaml:"default_agent,omitempty"`
	Sources        []sourceSpec               `json:"sources,omitempty" yaml:"sources,omitempty"`
	Discovery      discovery                  `json:"discovery,omitempty" yaml:"discovery,omitempty"`
	ModelPolicy    modelPolicy                `json:"model_policy,omitempty" yaml:"model_policy,omitempty"`
	Datasource     datasourceConfigDoc        `json:"datasource,omitempty" yaml:"datasource,omitempty"`
	SemanticSearch semanticSearchDoc          `json:"semantic_search,omitempty" yaml:"semantic_search,omitempty"`
	Security       policy.AuthorizationPolicy `json:"security,omitempty" yaml:"security,omitempty"`
	Identity       identityDoc                `json:"identity,omitempty" yaml:"identity,omitempty"`
	Models         modelConfigDoc             `json:"models,omitempty" yaml:"models,omitempty"`
	Distribution   distributionDoc            `json:"distribution,omitempty" yaml:"distribution,omitempty"`
	Plugins        pluginRefs                 `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	Commands       []commandDoc               `json:"commands,omitempty" yaml:"commands,omitempty"`
	Workflows      []workflowDoc              `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	Operations     []operationDoc             `json:"operations,omitempty" yaml:"operations,omitempty"`
	Observations   observationsDoc            `json:"observations,omitempty" yaml:"observations,omitempty"`
	Reactions      []corereaction.Rule        `json:"reactions,omitempty" yaml:"reactions,omitempty"`
	LLMProviders   []corellm.ProviderSpec     `json:"llm_providers,omitempty" yaml:"llm_providers,omitempty"`
	Runtime        RuntimeConfig              `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Daemon         DaemonConfig               `json:"daemon,omitempty" yaml:"daemon,omitempty"`
}

type defaultsDoc struct {
	Profile string        `json:"profile,omitempty" yaml:"profile,omitempty"`
	Agent   agentRef      `json:"agent,omitempty" yaml:"agent,omitempty"`
	Model   ModelSelector `json:"model,omitempty" yaml:"model,omitempty"`
}

// ProfileSet describes named app profiles. Profiles are metadata plus optional
// JSON Patch operations for exceptional profile-specific mutations.
type ProfileSet map[string]ProfileDoc

func (p ProfileSet) normalized() ProfileSet {
	if len(p) == 0 {
		return nil
	}
	out := make(ProfileSet, len(p))
	for name, profile := range p {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = profile
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ProfileDoc is descriptive metadata for one app profile.
type ProfileDoc struct {
	Description string               `json:"description,omitempty" yaml:"description,omitempty"`
	Patches     []JSONPatchOperation `json:"patches,omitempty" yaml:"patches,omitempty"`
}

// JSONPatchOperation is one RFC 6902-style JSON Patch operation.
type JSONPatchOperation struct {
	Op    string          `json:"op" yaml:"op" jsonschema:"enum=add,enum=remove,enum=replace,enum=move,enum=copy,enum=test"`
	Path  string          `json:"path,omitempty" yaml:"path,omitempty"`
	From  string          `json:"from,omitempty" yaml:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty" yaml:"value,omitempty"`
}

type observationsDoc struct {
	Observers         []coreevidence.ObserverSpec         `json:"observers,omitempty" yaml:"observers,omitempty"`
	AssertionDerivers []coreevidence.AssertionDeriverSpec `json:"assertion_derivers,omitempty" yaml:"assertion_derivers,omitempty"`
}

type identityDoc struct {
	Users  []identityUserDoc  `json:"users,omitempty" yaml:"users,omitempty"`
	Groups []identityGroupDoc `json:"groups,omitempty" yaml:"groups,omitempty"`
	Rules  []identityRuleDoc  `json:"rules,omitempty" yaml:"rules,omitempty"`
}

type identityUserDoc struct {
	ID          string                `json:"id" yaml:"id"`
	Username    string                `json:"username,omitempty" yaml:"username,omitempty"`
	DisplayName string                `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Trust       user.TrustLevel       `json:"trust,omitempty" yaml:"trust,omitempty"`
	Groups      []string              `json:"groups,omitempty" yaml:"groups,omitempty"`
	Emails      []identityEmailDoc    `json:"emails,omitempty" yaml:"emails,omitempty"`
	Identities  []identityIdentityDoc `json:"identities,omitempty" yaml:"identities,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type identityEmailDoc struct {
	Address     string            `json:"address" yaml:"address"`
	Verified    *bool             `json:"verified,omitempty" yaml:"verified,omitempty"`
	Primary     bool              `json:"primary,omitempty" yaml:"primary,omitempty"`
	Source      string            `json:"source,omitempty" yaml:"source,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type identityIdentityDoc struct {
	Provider    string            `json:"provider" yaml:"provider"`
	ProviderID  string            `json:"provider_id" yaml:"provider_id"`
	Email       string            `json:"email,omitempty" yaml:"email,omitempty"`
	DisplayName string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Claims      map[string]string `json:"claims,omitempty" yaml:"claims,omitempty"`
}

type identityGroupDoc struct {
	ID          string            `json:"id" yaml:"id"`
	DisplayName string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Members     []string          `json:"members,omitempty" yaml:"members,omitempty"`
	Trust       user.TrustLevel   `json:"trust,omitempty" yaml:"trust,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type identityRuleDoc struct {
	Match  identityMatchDoc `json:"match,omitempty" yaml:"match,omitempty"`
	Groups []string         `json:"groups,omitempty" yaml:"groups,omitempty"`
}

type identityMatchDoc struct {
	Provider   string               `json:"provider,omitempty" yaml:"provider,omitempty"`
	ProviderID string               `json:"provider_id,omitempty" yaml:"provider_id,omitempty"`
	Resolution user.ResolutionState `json:"resolution,omitempty" yaml:"resolution,omitempty"`
}

func (d identityDoc) Spec() coreapp.IdentitySpec {
	spec := coreapp.IdentitySpec{}
	for _, raw := range d.Users {
		configured := user.User{
			ID:          user.ID(strings.TrimSpace(raw.ID)),
			Username:    strings.TrimSpace(raw.Username),
			DisplayName: strings.TrimSpace(raw.DisplayName),
			Trust:       raw.Trust,
			Annotations: cloneStringMap(raw.Annotations),
		}
		for _, group := range raw.Groups {
			if group = strings.TrimSpace(group); group != "" {
				configured.Groups = append(configured.Groups, user.ID(group))
			}
		}
		for _, email := range raw.Emails {
			address := strings.ToLower(strings.TrimSpace(email.Address))
			if address == "" {
				continue
			}
			verified := true
			if email.Verified != nil {
				verified = *email.Verified
			}
			configured.Emails = append(configured.Emails, user.Email{
				Address:     address,
				Verified:    verified,
				Primary:     email.Primary,
				Source:      strings.TrimSpace(email.Source),
				Annotations: cloneStringMap(email.Annotations),
			})
		}
		for _, identity := range raw.Identities {
			configured.Identities = append(configured.Identities, user.Identity{
				Provider:    strings.TrimSpace(identity.Provider),
				ProviderID:  strings.TrimSpace(identity.ProviderID),
				Email:       strings.TrimSpace(identity.Email),
				DisplayName: strings.TrimSpace(identity.DisplayName),
				Claims:      cloneStringMap(identity.Claims),
			})
		}
		spec.Users = append(spec.Users, configured)
	}
	for _, raw := range d.Groups {
		group := user.Group{
			ID:          user.ID(strings.TrimSpace(raw.ID)),
			DisplayName: strings.TrimSpace(raw.DisplayName),
			Trust:       raw.Trust,
			Annotations: cloneStringMap(raw.Annotations),
		}
		for _, member := range raw.Members {
			if member = strings.TrimSpace(member); member != "" {
				group.Members = append(group.Members, user.ID(member))
			}
		}
		spec.Groups = append(spec.Groups, group)
	}
	for _, raw := range d.Rules {
		rule := user.GroupRule{
			Match: user.IdentityMatch{
				Provider:   strings.TrimSpace(raw.Match.Provider),
				ProviderID: strings.TrimSpace(raw.Match.ProviderID),
				Resolution: raw.Match.Resolution,
			},
		}
		for _, group := range raw.Groups {
			if group = strings.TrimSpace(group); group != "" {
				rule.Groups = append(rule.Groups, user.ID(group))
			}
		}
		spec.Rules = append(spec.Rules, rule)
	}
	return spec
}

// RuntimeConfig contains local runtime wiring consumed by launch adapters.
type RuntimeConfig struct {
	Workspace WorkspaceConfig  `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	Data      RuntimeDataDoc   `json:"data,omitempty" yaml:"data,omitempty"`
	Events    RuntimeEventsDoc `json:"events,omitempty" yaml:"events,omitempty"`
}

func mergeRuntimeConfig(dst *RuntimeConfig, src RuntimeConfig) {
	if dst == nil {
		return
	}
	mergeWorkspaceConfig(&dst.Workspace, src.Workspace)
	mergeRuntimeDataDoc(&dst.Data, src.Data)
	mergeRuntimeEventsDoc(&dst.Events, src.Events)
}

func mergeWorkspaceConfig(dst *WorkspaceConfig, src WorkspaceConfig) {
	if strings.TrimSpace(src.ScratchRoot) != "" {
		dst.ScratchRoot = src.ScratchRoot
	}
	if len(src.Roots) > 0 {
		dst.Roots = append(dst.Roots, src.Roots...)
	}
	if len(src.EnvFiles) > 0 {
		dst.EnvFiles = append(dst.EnvFiles, src.EnvFiles...)
	}
}

func mergeRuntimeDataDoc(dst *RuntimeDataDoc, src RuntimeDataDoc) {
	if strings.TrimSpace(string(src.Store.Kind)) != "" {
		dst.Store.Kind = src.Store.Kind
	}
	if strings.TrimSpace(src.Store.DSN) != "" {
		dst.Store.DSN = src.Store.DSN
	}
	if strings.TrimSpace(src.Store.DSNEnv) != "" {
		dst.Store.DSNEnv = src.Store.DSNEnv
	}
}

func mergeRuntimeEventsDoc(dst *RuntimeEventsDoc, src RuntimeEventsDoc) {
	if strings.TrimSpace(string(src.Store.Kind)) != "" {
		dst.Store.Kind = src.Store.Kind
	}
	if strings.TrimSpace(src.Store.DSN) != "" {
		dst.Store.DSN = src.Store.DSN
	}
	if strings.TrimSpace(src.Store.DSNEnv) != "" {
		dst.Store.DSNEnv = src.Store.DSNEnv
	}
	if strings.TrimSpace(src.Store.Stream) != "" {
		dst.Store.Stream = src.Store.Stream
	}
	if strings.TrimSpace(src.Store.Subject) != "" {
		dst.Store.Subject = src.Store.Subject
	}
	if src.Store.CreateStream {
		dst.Store.CreateStream = true
	}
}

type RuntimeDataStoreKind string

const (
	RuntimeDataStoreMemory RuntimeDataStoreKind = "memory"
	RuntimeDataStoreMem    RuntimeDataStoreKind = "mem"
	RuntimeDataStoreMySQL  RuntimeDataStoreKind = "mysql"
)

func (RuntimeDataStoreKind) JSONSchema() *invjsonschema.Schema {
	return stringEnumSchema("memory", string(RuntimeDataStoreMem), string(RuntimeDataStoreMemory), string(RuntimeDataStoreMySQL))
}

// RuntimeDataDoc contains runtime-owned durable data store settings.
type RuntimeDataDoc struct {
	Store RuntimeDataStoreDoc `json:"store,omitempty" yaml:"store,omitempty"`
}

type RuntimeDataStoreDoc struct {
	Kind   RuntimeDataStoreKind `json:"kind,omitempty" yaml:"kind,omitempty"`
	DSN    string               `json:"dsn,omitempty" yaml:"dsn,omitempty"`
	DSNEnv string               `json:"dsn_env,omitempty" yaml:"dsn_env,omitempty"`
}

type RuntimeEventStoreKind string

const (
	RuntimeEventStoreSQLite        RuntimeEventStoreKind = "sqlite"
	RuntimeEventStoreLocal         RuntimeEventStoreKind = "local"
	RuntimeEventStoreNATS          RuntimeEventStoreKind = "nats"
	RuntimeEventStoreJetstream     RuntimeEventStoreKind = "jetstream"
	RuntimeEventStoreNATSJetstream RuntimeEventStoreKind = "nats-jetstream"
)

func (RuntimeEventStoreKind) JSONSchema() *invjsonschema.Schema {
	return stringEnumSchema("sqlite", string(RuntimeEventStoreJetstream), string(RuntimeEventStoreLocal), string(RuntimeEventStoreNATS), string(RuntimeEventStoreNATSJetstream), string(RuntimeEventStoreSQLite))
}

// RuntimeEventsDoc contains runtime-owned durable event store settings.
type RuntimeEventsDoc struct {
	Store RuntimeEventStoreDoc `json:"store,omitempty" yaml:"store,omitempty"`
}

type RuntimeEventStoreDoc struct {
	Kind         RuntimeEventStoreKind `json:"kind,omitempty" yaml:"kind,omitempty"`
	DSN          string                `json:"dsn,omitempty" yaml:"dsn,omitempty"`
	DSNEnv       string                `json:"dsn_env,omitempty" yaml:"dsn_env,omitempty"`
	Stream       string                `json:"stream,omitempty" yaml:"stream,omitempty" jsonschema:"default=FLUXPLANE_EVENTS"`
	Subject      string                `json:"subject,omitempty" yaml:"subject,omitempty" jsonschema:"default=fluxplane.events.log"`
	CreateStream bool                  `json:"create_stream,omitempty" yaml:"create_stream,omitempty" jsonschema:"default=false"`
}

type WorkspaceAccess string

const (
	WorkspaceAccessReadOnly  WorkspaceAccess = "read_only"
	WorkspaceAccessReadWrite WorkspaceAccess = "read_write"
)

func (WorkspaceAccess) JSONSchema() *invjsonschema.Schema {
	return stringEnumSchema("read_only", string(WorkspaceAccessReadOnly), string(WorkspaceAccessReadWrite))
}

type DurationString string

func (DurationString) JSONSchema() *invjsonschema.Schema {
	return &invjsonschema.Schema{
		Type:     "string",
		Pattern:  `^[-+]?(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h))+$`,
		Examples: []any{"15m", "1.5h", "24h"},
	}
}

func (d *DurationString) UnmarshalYAML(node *yaml.Node) error {
	var value string
	if err := node.Decode(&value); err != nil {
		return err
	}
	return d.set(value)
}

func (d *DurationString) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return d.set(value)
}

func (d *DurationString) set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("invalid duration %q: %w", value, err)
		}
	}
	*d = DurationString(value)
	return nil
}

func stringEnumSchema(defaultValue string, values ...string) *invjsonschema.Schema {
	enum := make([]any, 0, len(values))
	for _, value := range values {
		enum = append(enum, value)
	}
	schema := &invjsonschema.Schema{Type: "string", Enum: enum}
	if defaultValue != "" {
		schema.Default = defaultValue
	}
	return schema
}

// WorkspaceConfig contains additional local filesystem workspace roots.
type WorkspaceConfig struct {
	Roots       []WorkspaceRootDoc `json:"roots,omitempty" yaml:"roots,omitempty"`
	ScratchRoot string             `json:"scratch_root,omitempty" yaml:"scratch_root,omitempty"`
	EnvFiles    []string           `json:"env_files,omitempty" yaml:"env_files,omitempty"`
}

type WorkspaceRootDoc struct {
	Name     string          `json:"name" yaml:"name"`
	Path     string          `json:"path" yaml:"path"`
	Access   WorkspaceAccess `json:"access,omitempty" yaml:"access,omitempty"`
	Create   bool            `json:"create,omitempty" yaml:"create,omitempty"`
	EnvFiles []string        `json:"env_files,omitempty" yaml:"env_files,omitempty"`
}

type ModelSelector string

func (ModelSelector) JSONSchema() *invjsonschema.Schema {
	return &invjsonschema.Schema{Type: "string"}
}

type modelConfigDoc struct {
	Default   ModelSelector       `json:"default,omitempty" yaml:"default,omitempty"`
	Available []modelAvailableDoc `json:"available,omitempty" yaml:"available,omitempty"`
}

type modelAvailableDoc struct {
	Provider string         `json:"provider" yaml:"provider"`
	Model    string         `json:"model" yaml:"model"`
	Aliases  []string       `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Params   modelParamsDoc `json:"params,omitempty" yaml:"params,omitempty"`
}

type ThinkingMode string

const (
	ThinkingDisabled ThinkingMode = "disabled"
	ThinkingAuto     ThinkingMode = "auto"
	ThinkingEnabled  ThinkingMode = "enabled"
)

func (ThinkingMode) JSONSchema() *invjsonschema.Schema {
	return stringEnumSchema("", string(ThinkingDisabled), string(ThinkingAuto), string(ThinkingEnabled))
}

type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortMax    ReasoningEffort = "max"
)

func (ReasoningEffort) JSONSchema() *invjsonschema.Schema {
	return stringEnumSchema("medium", string(ReasoningEffortLow), string(ReasoningEffortMedium), string(ReasoningEffortHigh), string(ReasoningEffortMax))
}

type modelParamsDoc struct {
	Thinking ThinkingMode    `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	Effort   ReasoningEffort `json:"effort,omitempty" yaml:"effort,omitempty"`
}

type modelContributions struct {
	Providers []corellm.ProviderSpec
	Aliases   []corellm.ModelAliasSpec
}

func (d modelConfigDoc) Contributions() (modelContributions, error) {
	providers := map[corellm.ProviderName]corellm.ProviderSpec{}
	aliasTargets := map[string]string{}
	for i, raw := range d.Available {
		providerName := corellm.ProviderName(strings.TrimSpace(raw.Provider))
		modelName := corellm.ModelName(strings.TrimSpace(raw.Model))
		if providerName == "" {
			return modelContributions{}, fmt.Errorf("appconfig: models.available[%d].provider is empty", i)
		}
		if modelName == "" {
			return modelContributions{}, fmt.Errorf("appconfig: models.available[%d].model is empty", i)
		}
		provider := providers[providerName]
		provider.Name = providerName
		model := corellm.ModelSpec{
			Ref: corellm.ModelRef{
				Provider: providerName,
				Name:     modelName,
			},
			Aliases: cleanedModelNames(raw.Aliases),
			Params: corellm.ModelParams{
				Thinking:        strings.TrimSpace(string(raw.Params.Thinking)),
				ReasoningEffort: strings.TrimSpace(string(raw.Params.Effort)),
			},
		}
		provider.Models = append(provider.Models, model)
		providers[providerName] = provider
		target := corellm.ModelRef{Provider: providerName, Name: modelName}.String()
		for _, alias := range cleaned(raw.Aliases) {
			if previous, ok := aliasTargets[alias]; ok && previous != target {
				return modelContributions{}, fmt.Errorf("appconfig: models.available[%d].aliases contains duplicate alias %q for %s and %s", i, alias, previous, target)
			}
			aliasTargets[alias] = target
		}
	}
	keys := make([]corellm.ProviderName, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := modelContributions{Providers: make([]corellm.ProviderSpec, 0, len(keys))}
	for _, key := range keys {
		provider := providers[key]
		if err := provider.Validate(); err != nil {
			return modelContributions{}, fmt.Errorf("appconfig: models.available provider %q: %w", key, err)
		}
		out.Providers = append(out.Providers, provider)
	}
	aliasKeys := make([]string, 0, len(aliasTargets))
	for alias := range aliasTargets {
		aliasKeys = append(aliasKeys, alias)
	}
	sort.Strings(aliasKeys)
	for _, alias := range aliasKeys {
		spec, err := corellm.NewModelAliasSpec(alias, aliasTargets[alias])
		if err != nil {
			return modelContributions{}, fmt.Errorf("appconfig: models.available alias %q: %w", alias, err)
		}
		out.Aliases = append(out.Aliases, spec)
	}
	return out, nil
}

func validateModelReference(value string, models modelContributions, field string) error {
	if value == "" || len(models.Providers) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, alias := range models.Aliases {
		known[strings.TrimSpace(alias.Name)] = true
	}
	for _, provider := range models.Providers {
		for _, model := range provider.Models {
			known[model.Ref.String()] = true
		}
	}
	if known[value] {
		return nil
	}
	return fmt.Errorf("appconfig: %s %q is not declared in models.available", field, value)
}

func cleanedModelNames(values []string) []corellm.ModelName {
	clean := cleaned(values)
	out := make([]corellm.ModelName, 0, len(clean))
	for _, value := range clean {
		out = append(out, corellm.ModelName(value))
	}
	return out
}

func mergeLLMProviders(groups ...[]corellm.ProviderSpec) []corellm.ProviderSpec {
	byName := map[corellm.ProviderName]corellm.ProviderSpec{}
	for _, group := range groups {
		for _, provider := range group {
			name := provider.Name
			existing := byName[name]
			if existing.Name == "" {
				byName[name] = provider
				continue
			}
			existing.Models = append(existing.Models, provider.Models...)
			if provider.DisplayName != "" {
				existing.DisplayName = provider.DisplayName
			}
			if provider.Description != "" {
				existing.Description = provider.Description
			}
			if len(provider.Annotations) > 0 {
				existing.Annotations = cloneStringMap(provider.Annotations)
			}
			byName[name] = existing
		}
	}
	keys := make([]corellm.ProviderName, 0, len(byName))
	for key := range byName {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]corellm.ProviderSpec, 0, len(keys))
	for _, key := range keys {
		provider := byName[key]
		provider.Models = mergeLLMModels(provider.Models)
		out = append(out, provider)
	}
	return out
}

func mergeLLMModels(models []corellm.ModelSpec) []corellm.ModelSpec {
	byName := map[corellm.ModelName]corellm.ModelSpec{}
	var order []corellm.ModelName
	for _, model := range models {
		name := model.Ref.Name
		if _, ok := byName[name]; !ok {
			order = append(order, name)
			byName[name] = model
			continue
		}
		byName[name] = mergeLLMModel(byName[name], model)
	}
	out := make([]corellm.ModelSpec, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

func mergeLLMModel(base, overlay corellm.ModelSpec) corellm.ModelSpec {
	if overlay.Ref.Provider != "" {
		base.Ref.Provider = overlay.Ref.Provider
	}
	if overlay.Ref.Name != "" {
		base.Ref.Name = overlay.Ref.Name
	}
	if overlay.DisplayName != "" {
		base.DisplayName = overlay.DisplayName
	}
	if overlay.Description != "" {
		base.Description = overlay.Description
	}
	base.Aliases = mergeModelNames(base.Aliases, overlay.Aliases)
	if overlay.Params.Thinking != "" {
		base.Params.Thinking = overlay.Params.Thinking
	}
	if overlay.Params.ReasoningEffort != "" {
		base.Params.ReasoningEffort = overlay.Params.ReasoningEffort
	}
	if len(overlay.Annotations) > 0 {
		base.Annotations = cloneStringMap(overlay.Annotations)
	}
	return base
}

func mergeModelNames(groups ...[]corellm.ModelName) []corellm.ModelName {
	seen := map[corellm.ModelName]bool{}
	var out []corellm.ModelName
	for _, group := range groups {
		for _, value := range group {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
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
	Deploy              deployDoc                `json:"deploy,omitempty" yaml:"deploy,omitempty"`
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
			Model:    strings.TrimSpace(string(d.DefaultModel.Model)),
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
			Assets:  cleaned(d.Build.Assets),
			Targets: buildTargetSpecs(d.Build.Targets),
		},
		Deploy: coredistribution.DeploySpec{
			Model:   strings.TrimSpace(string(d.Deploy.Model)),
			Targets: deployTargetSpecs(d.Deploy.Targets),
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
	Provider string        `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model    ModelSelector `json:"model,omitempty" yaml:"model,omitempty"`
	UseCase  string        `json:"use_case,omitempty" yaml:"use_case,omitempty"`
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
	Assets  []string                  `json:"assets,omitempty" yaml:"assets,omitempty"`
	Docker  *dockerBuildDoc           `json:"docker,omitempty" yaml:"docker,omitempty"`
	Targets map[string]buildTargetDoc `json:"targets,omitempty" yaml:"targets,omitempty"`
}

type deployDoc struct {
	Model   ModelSelector              `json:"model,omitempty" yaml:"model,omitempty"`
	Targets map[string]deployTargetDoc `json:"targets,omitempty" yaml:"targets,omitempty"`
}

type buildTargetDoc struct {
	Kind               string            `json:"kind,omitempty" yaml:"kind,omitempty" jsonschema:"enum=binary,enum=dockerfile,enum=docker-image,enum=docker-compose,enum=kubernetes-manifest,enum=helm-chart,enum=documentation,enum=runtime-stack"`
	Description        string            `json:"description,omitempty" yaml:"description,omitempty"`
	Output             string            `json:"output,omitempty" yaml:"output,omitempty"`
	Dockerfile         string            `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	Image              string            `json:"image,omitempty" yaml:"image,omitempty"`
	Tags               []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Platforms          []string          `json:"platforms,omitempty" yaml:"platforms,omitempty"`
	Push               bool              `json:"push,omitempty" yaml:"push,omitempty"`
	BaseImage          string            `json:"base_image,omitempty" yaml:"base_image,omitempty"`
	AuthPath           string            `json:"auth_path,omitempty" yaml:"auth_path,omitempty"`
	AllowPluginAuthEnv bool              `json:"allow_plugin_auth_env,omitempty" yaml:"allow_plugin_auth_env,omitempty"`
	Provider           string            `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model              ModelSelector     `json:"model,omitempty" yaml:"model,omitempty"`
	Effort             string            `json:"effort,omitempty" yaml:"effort,omitempty"`
	Namespace          string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	ImagePullPolicy    string            `json:"image_pull_policy,omitempty" yaml:"image_pull_policy,omitempty" jsonschema:"enum=Always,enum=IfNotPresent,enum=Never"`
	EnvSecretName      string            `json:"env_secret_name,omitempty" yaml:"env_secret_name,omitempty"`
	RuntimeSecretName  string            `json:"runtime_secret_name,omitempty" yaml:"runtime_secret_name,omitempty"`
	Backend            string            `json:"backend,omitempty" yaml:"backend,omitempty" jsonschema:"enum=kubernetes"`
	NodeSelectors      []string          `json:"node_selectors,omitempty" yaml:"node_selectors,omitempty"`
	Release            string            `json:"release,omitempty" yaml:"release,omitempty"`
	Values             map[string]string `json:"values,omitempty" yaml:"values,omitempty"`
}

type deployTargetDoc struct {
	Kind        string            `json:"kind,omitempty" yaml:"kind,omitempty" jsonschema:"enum=docker-compose,enum=kubectl,enum=helm"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Build       []string          `json:"build,omitempty" yaml:"build,omitempty"`
	ComposeFile string            `json:"compose_file,omitempty" yaml:"compose_file,omitempty"`
	Manifest    string            `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Chart       string            `json:"chart,omitempty" yaml:"chart,omitempty"`
	Release     string            `json:"release,omitempty" yaml:"release,omitempty"`
	Namespace   string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Detach      bool              `json:"detach,omitempty" yaml:"detach,omitempty"`
	Values      map[string]string `json:"values,omitempty" yaml:"values,omitempty"`
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

func buildTargetSpecs(input map[string]buildTargetDoc) map[string]coredistribution.BuildTargetSpec {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]coredistribution.BuildTargetSpec, len(input))
	for name, target := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = coredistribution.BuildTargetSpec{
			Kind:               strings.TrimSpace(target.Kind),
			Description:        strings.TrimSpace(target.Description),
			Output:             strings.TrimSpace(target.Output),
			Dockerfile:         strings.TrimSpace(target.Dockerfile),
			Image:              strings.TrimSpace(target.Image),
			Tags:               cleaned(target.Tags),
			Platforms:          cleaned(target.Platforms),
			Push:               target.Push,
			BaseImage:          strings.TrimSpace(target.BaseImage),
			AuthPath:           strings.TrimSpace(target.AuthPath),
			AllowPluginAuthEnv: target.AllowPluginAuthEnv,
			Provider:           strings.TrimSpace(target.Provider),
			Model:              strings.TrimSpace(string(target.Model)),
			Effort:             strings.TrimSpace(target.Effort),
			Namespace:          strings.TrimSpace(target.Namespace),
			ImagePullPolicy:    strings.TrimSpace(target.ImagePullPolicy),
			EnvSecretName:      strings.TrimSpace(target.EnvSecretName),
			RuntimeSecretName:  strings.TrimSpace(target.RuntimeSecretName),
			Backend:            strings.TrimSpace(target.Backend),
			NodeSelectors:      cleaned(target.NodeSelectors),
			Release:            strings.TrimSpace(target.Release),
			Values:             cloneStringMap(target.Values),
		}
	}
	return out
}

func deployTargetSpecs(input map[string]deployTargetDoc) map[string]coredistribution.DeployTargetSpec {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]coredistribution.DeployTargetSpec, len(input))
	for name, target := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = coredistribution.DeployTargetSpec{
			Kind:        strings.TrimSpace(target.Kind),
			Description: strings.TrimSpace(target.Description),
			Build:       cleaned(target.Build),
			ComposeFile: strings.TrimSpace(target.ComposeFile),
			Manifest:    strings.TrimSpace(target.Manifest),
			Chart:       strings.TrimSpace(target.Chart),
			Release:     strings.TrimSpace(target.Release),
			Namespace:   strings.TrimSpace(target.Namespace),
			Detach:      target.Detach,
			Values:      cloneStringMap(target.Values),
		}
	}
	return out
}

type distributionCommandDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type datasourceConfigDoc struct {
	Index       datasourceIndexDefaultsDoc `json:"index,omitempty" yaml:"index,omitempty"`
	Datasources []DatasourceDoc            `json:"datasources,omitempty" yaml:"datasources,omitempty"`
}

func (d datasourceConfigDoc) Spec() coreapp.DatasourceSpec {
	out := coreapp.DatasourceSpec{
		Index: coreapp.DatasourceIndexSpec{
			Concurrency: d.Index.Concurrency,
			Freshness:   strings.TrimSpace(string(d.Index.Freshness)),
		},
	}
	for _, ds := range d.Datasources {
		out.Datasources = append(out.Datasources, ds.Spec())
	}
	return out
}

type datasourceIndexDefaultsDoc struct {
	Concurrency int            `json:"concurrency,omitempty" yaml:"concurrency,omitempty" jsonschema:"default=1"`
	Freshness   DurationString `json:"freshness,omitempty" yaml:"freshness,omitempty" jsonschema:"default=15m"`
}

// DatasourceDoc declares one configured datasource instance.
type DatasourceDoc struct {
	Name        string             `json:"name" yaml:"name"`
	Description string             `json:"description,omitempty" yaml:"description,omitempty"`
	Entities    []string           `json:"entities,omitempty" yaml:"entities,omitempty"`
	Kind        string             `json:"kind,omitempty" yaml:"kind,omitempty"`
	Type        string             `json:"type,omitempty" yaml:"type,omitempty"`
	Path        string             `json:"path,omitempty" yaml:"path,omitempty"`
	Include     []string           `json:"include,omitempty" yaml:"include,omitempty"`
	Config      map[string]string  `json:"config,omitempty" yaml:"config,omitempty"`
	Index       datasourceIndexDoc `json:"index,omitempty" yaml:"index,omitempty"`
	Semantic    semanticDoc        `json:"semantic,omitempty" yaml:"semantic,omitempty"`
}

type datasourceIndexDoc struct {
	Enabled   bool           `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Freshness DurationString `json:"freshness,omitempty" yaml:"freshness,omitempty" jsonschema:"default=15m"`
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
		Kind:        datasourceKind(d),
		Config:      cfg,
		Index: coredatasource.IndexSpec{
			Enabled:   d.Index.Enabled,
			Freshness: strings.TrimSpace(string(d.Index.Freshness)),
		},
		Semantic: d.Semantic.Spec(),
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

// DaemonConfig contains process wiring consumed by app serving.
type DaemonConfig struct {
	Listeners []ListenerDoc      `json:"listeners,omitempty" yaml:"listeners,omitempty"`
	Channels  []ChannelDoc       `json:"channels,omitempty" yaml:"channels,omitempty"`
	Triggers  []coretrigger.Spec `json:"triggers,omitempty" yaml:"triggers,omitempty"`
}

type ListenerDoc struct {
	Name string         `json:"name" yaml:"name"`
	Type string         `json:"type" yaml:"type"`
	Addr string         `json:"addr,omitempty" yaml:"addr,omitempty"`
	Auth map[string]any `json:"auth,omitempty" yaml:"auth,omitempty"`
}

type ChannelDoc struct {
	Name     string    `json:"name" yaml:"name"`
	Type     string    `json:"type" yaml:"type"`
	Instance string    `json:"instance,omitempty" yaml:"instance,omitempty"`
	Listener string    `json:"listener,omitempty" yaml:"listener,omitempty"`
	Session  string    `json:"session,omitempty" yaml:"session,omitempty"`
	Access   AccessDoc `json:"access,omitempty" yaml:"access,omitempty"`
}

type AccessDoc struct {
	Mode             string      `json:"mode,omitempty" yaml:"mode,omitempty"`
	AllowUsers       []string    `json:"allow_users,omitempty" yaml:"allow_users,omitempty"`
	DenyUsers        []string    `json:"deny_users,omitempty" yaml:"deny_users,omitempty"`
	AllowChannels    []string    `json:"allow_channels,omitempty" yaml:"allow_channels,omitempty"`
	DenyChannels     []string    `json:"deny_channels,omitempty" yaml:"deny_channels,omitempty"`
	AllowKinds       []string    `json:"allow_kinds,omitempty" yaml:"allow_kinds,omitempty"`
	DefaultTrust     string      `json:"default_trust,omitempty" yaml:"default_trust,omitempty"`
	Operators        []string    `json:"operators,omitempty" yaml:"operators,omitempty"`
	InternalUsers    []string    `json:"internal_users,omitempty" yaml:"internal_users,omitempty"`
	InternalChannels []string    `json:"internal_channels,omitempty" yaml:"internal_channels,omitempty"`
	Sharing          SharingMode `json:"sharing,omitempty" yaml:"sharing,omitempty"`
}

type SharingMode string

const (
	SharingStrict SharingMode = "strict"
)

func (SharingMode) JSONSchema() *invjsonschema.Schema {
	return stringEnumSchema("strict", string(SharingStrict))
}

type agentDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Model       ModelSelector     `json:"model,omitempty" yaml:"model,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	Turns       turnsDoc          `json:"turns,omitempty" yaml:"turns,omitempty"`
	Thinking    ThinkingMode      `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	Effort      ReasoningEffort   `json:"effort,omitempty" yaml:"effort,omitempty"`
	Operations  []string          `json:"operations,omitempty" yaml:"operations,omitempty"`
	Tools       []string          `json:"tools,omitempty" yaml:"tools,omitempty"`
	Context     []string          `json:"context,omitempty" yaml:"context,omitempty"`
	Datasources []string          `json:"datasources,omitempty" yaml:"datasources,omitempty"`
	Skills      []string          `json:"skills,omitempty" yaml:"skills,omitempty"`
	Uses        []string          `json:"uses,omitempty" yaml:"uses,omitempty"`
	Triggers    []agentTriggerDoc `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	System      string            `json:"system,omitempty" yaml:"system,omitempty"`
}

type agentTriggerDoc struct {
	Name        string                  `json:"name,omitempty" yaml:"name,omitempty"`
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Every       DurationString          `json:"every,omitempty" yaml:"every,omitempty"`
	Startup     *agentStartupTriggerDoc `json:"startup,omitempty" yaml:"startup,omitempty"`
	Session     string                  `json:"session,omitempty" yaml:"session,omitempty"`
	Prompt      string                  `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Workflow    string                  `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Do          agentTriggerDoDoc       `json:"do,omitempty" yaml:"do,omitempty"`
	Actions     []corereaction.Action   `json:"actions,omitempty" yaml:"actions,omitempty"`
	Disabled    bool                    `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Metadata    map[string]string       `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type agentStartupTriggerDoc struct {
	Prompt   string                `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Workflow string                `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Do       agentTriggerDoDoc     `json:"do,omitempty" yaml:"do,omitempty"`
	Actions  []corereaction.Action `json:"actions,omitempty" yaml:"actions,omitempty"`
}

type agentTriggerDoDoc struct {
	Workflow string                `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Actions  []corereaction.Action `json:"actions,omitempty" yaml:"actions,omitempty"`
}

type agentTriggerResources struct {
	Triggers  []coretrigger.Spec
	Workflows []workflow.Spec
}

type turnsDoc struct {
	MaxSteps     int             `json:"max_steps,omitempty" yaml:"max_steps,omitempty"`
	Continuation continuationDoc `json:"continuation,omitempty" yaml:"continuation,omitempty"`
}

type continuationDoc struct {
	MaxContinuations int              `json:"max_continuations,omitempty" yaml:"max_continuations,omitempty"`
	ContextPolicy    string           `json:"context_policy,omitempty" yaml:"context_policy,omitempty"`
	StopCondition    stopConditionDoc `json:"stop_condition,omitempty" yaml:"stop_condition,omitempty"`
}

type stopConditionDoc struct {
	Type        string              `json:"type,omitempty" yaml:"type,omitempty"`
	Max         int                 `json:"max,omitempty" yaml:"max,omitempty"`
	Prompt      string              `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Session     string              `json:"session,omitempty" yaml:"session,omitempty"`
	Conditions  []*stopConditionDoc `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (d stopConditionDoc) Spec() agent.StopConditionSpec {
	out := agent.StopConditionSpec{
		Type:        strings.TrimSpace(d.Type),
		Max:         d.Max,
		Prompt:      strings.TrimSpace(d.Prompt),
		Session:     strings.TrimSpace(d.Session),
		Annotations: cloneStringMap(d.Annotations),
	}
	for _, condition := range d.Conditions {
		if condition == nil {
			continue
		}
		out.Conditions = append(out.Conditions, condition.Spec())
	}
	return out
}

func decodeAgentDoc(value any) (agent.Spec, agentTriggerResources, error) {
	var raw agentDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return agent.Spec{}, agentTriggerResources{}, fmt.Errorf("appconfig: decode agent document: %w", err)
	}
	spec := agent.Spec{
		Name:        agent.Name(strings.TrimSpace(raw.Name)),
		Description: strings.TrimSpace(raw.Description),
		System:      strings.TrimSpace(raw.System),
		Inference: agent.InferenceSpec{
			Model:           strings.TrimSpace(string(raw.Model)),
			MaxOutputTokens: raw.MaxTokens,
			Thinking:        strings.TrimSpace(string(raw.Thinking)),
			ReasoningEffort: strings.TrimSpace(string(raw.Effort)),
		},
		Turns: agent.TurnPolicy{
			MaxSteps: raw.Turns.MaxSteps,
			Continuation: agent.ContinuationPolicy{
				MaxContinuations: raw.Turns.Continuation.MaxContinuations,
				ContextPolicy:    strings.TrimSpace(raw.Turns.Continuation.ContextPolicy),
				StopCondition:    raw.Turns.Continuation.StopCondition.Spec(),
			},
		},
	}
	for _, name := range raw.Uses {
		name = strings.TrimSpace(name)
		if name != "" {
			spec.ActivationSets = append(spec.ActivationSets, name)
		}
	}
	for _, name := range raw.Operations {
		name = strings.TrimSpace(name)
		if name != "" {
			spec.Operations = append(spec.Operations, operation.Ref{Name: operation.Name(name)})
		}
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
		return agent.Spec{}, agentTriggerResources{}, fmt.Errorf("appconfig: validate agent document: %w", err)
	}
	triggerResources, err := agentTriggerSpecs(raw)
	if err != nil {
		return agent.Spec{}, agentTriggerResources{}, fmt.Errorf("appconfig: validate agent triggers for %q: %w", raw.Name, err)
	}
	return spec, triggerResources, nil
}

func agentTriggerSpecs(raw agentDoc) (agentTriggerResources, error) {
	agentName := strings.TrimSpace(raw.Name)
	if agentName == "" || len(raw.Triggers) == 0 {
		return agentTriggerResources{}, nil
	}
	var out agentTriggerResources
	seen := map[string]int{}
	for i, trigger := range raw.Triggers {
		spec, generatedWorkflow, err := agentTriggerSpec(agentName, i, trigger, seen)
		if err != nil {
			return agentTriggerResources{}, err
		}
		out.Triggers = append(out.Triggers, spec)
		if generatedWorkflow.Name != "" {
			out.Workflows = append(out.Workflows, generatedWorkflow)
		}
	}
	return out, nil
}

func agentTriggerSpec(agentName string, index int, raw agentTriggerDoc, seen map[string]int) (coretrigger.Spec, workflow.Spec, error) {
	kind := coretrigger.KindSchedule
	every := strings.TrimSpace(string(raw.Every))
	prompt := strings.TrimSpace(raw.Prompt)
	workflowName := firstNonEmpty(strings.TrimSpace(raw.Workflow), strings.TrimSpace(raw.Do.Workflow))
	actions := append([]corereaction.Action(nil), raw.Actions...)
	actions = append(actions, raw.Do.Actions...)
	if raw.Startup != nil {
		if every != "" {
			return coretrigger.Spec{}, workflow.Spec{}, fmt.Errorf("triggers[%d]: startup and every are mutually exclusive", index)
		}
		kind = coretrigger.KindStartup
		prompt = firstNonEmpty(strings.TrimSpace(raw.Startup.Prompt), prompt)
		workflowName = firstNonEmpty(strings.TrimSpace(raw.Startup.Workflow), strings.TrimSpace(raw.Startup.Do.Workflow), workflowName)
		actions = append(actions, raw.Startup.Actions...)
		actions = append(actions, raw.Startup.Do.Actions...)
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = defaultAgentTriggerName(agentName, index, kind, every)
	}
	name = uniqueAgentTriggerName(name, seen)
	if kind == coretrigger.KindSchedule && every == "" {
		return coretrigger.Spec{}, workflow.Spec{}, fmt.Errorf("triggers[%d]: every is empty", index)
	}
	var generated workflow.Spec
	if workflowName == "" && prompt != "" {
		workflowName = generatedTriggerWorkflowName(name)
		generated = workflow.Spec{
			Name: workflow.Name(workflowName),
			Steps: []workflow.Step{{
				ID:    "prompt",
				Kind:  workflow.StepAgent,
				Agent: agent.Ref{Name: agent.Name(agentName)},
				Input: prompt,
			}},
			Annotations: map[string]string{
				"generated_by": "agent.trigger",
				"agent":        agentName,
				"trigger":      name,
			},
		}
		if err := generated.Validate(); err != nil {
			return coretrigger.Spec{}, workflow.Spec{}, err
		}
	}
	if workflowName != "" {
		actions = append(actions, corereaction.Action{
			Kind: corereaction.ActionRunWorkflow,
			Workflow: corereaction.WorkflowAction{
				Name: workflow.Name(workflowName),
			},
		})
	}
	if len(actions) == 0 {
		return coretrigger.Spec{}, workflow.Spec{}, fmt.Errorf("triggers[%d]: prompt, workflow, do.workflow, or actions is required", index)
	}
	sessionName := strings.TrimSpace(raw.Session)
	if sessionName == "" {
		sessionName = "default"
	}
	spec := coretrigger.Spec{
		Name:        name,
		Description: strings.TrimSpace(raw.Description),
		Kind:        kind,
		Session:     sessionName,
		Actions:     actions,
		Disabled:    raw.Disabled,
		Metadata:    cloneStringMap(raw.Metadata),
	}
	if kind == coretrigger.KindSchedule {
		spec.Schedule.Every = every
	}
	if spec.Metadata == nil {
		spec.Metadata = map[string]string{}
	}
	spec.Metadata["agent"] = agentName
	if err := spec.Validate(); err != nil {
		return coretrigger.Spec{}, workflow.Spec{}, err
	}
	return spec, generated, nil
}

func defaultAgentTriggerName(agentName string, index int, kind coretrigger.Kind, every string) string {
	switch kind {
	case coretrigger.KindStartup:
		return sanitizeResourcePart(agentName) + "-startup"
	case coretrigger.KindSchedule:
		if every = sanitizeResourcePart(every); every != "" {
			return sanitizeResourcePart(agentName) + "-every-" + every
		}
	}
	return fmt.Sprintf("%s-trigger-%d", sanitizeResourcePart(agentName), index+1)
}

func uniqueAgentTriggerName(name string, seen map[string]int) string {
	if seen == nil {
		return name
	}
	seen[name]++
	if seen[name] == 1 {
		return name
	}
	return fmt.Sprintf("%s-%d", name, seen[name])
}

func generatedTriggerWorkflowName(triggerName string) string {
	return "__trigger_" + sanitizeResourcePart(triggerName)
}

func sanitizeResourcePart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "trigger"
	}
	return out
}

func implicitDefaultSessionForAgentTriggers(agents []agent.Spec, triggers []coretrigger.Spec) (coresession.Spec, bool) {
	if len(triggers) == 0 {
		return coresession.Spec{}, false
	}
	var agentName agent.Name
	for _, trigger := range triggers {
		if trigger.Session != "default" {
			continue
		}
		name := agentNameForTrigger(trigger, agents)
		if name == "" {
			continue
		}
		if agentName != "" && agentName != name {
			return coresession.Spec{}, false
		}
		agentName = name
	}
	if agentName == "" {
		return coresession.Spec{}, false
	}
	return coresession.Spec{
		Name:  "default",
		Agent: agent.Ref{Name: agentName},
		Metadata: map[string]string{
			"generated_by": "agent.trigger",
		},
	}, true
}

func agentNameForTrigger(trigger coretrigger.Spec, agents []agent.Spec) agent.Name {
	if trigger.Metadata != nil {
		if name := strings.TrimSpace(trigger.Metadata["agent"]); name != "" {
			return agent.Name(name)
		}
	}
	if len(agents) == 1 {
		return agents[0].Name
	}
	return ""
}

type commandDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Path        []string          `json:"path,omitempty" yaml:"path,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Policy      commandPolicyDoc  `json:"policy,omitempty" yaml:"policy,omitempty"`
	InputSchema any               `json:"input_schema,omitempty" yaml:"input_schema,omitempty"`
	Target      commandTargetDoc  `json:"target" yaml:"target"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type commandPolicyDoc struct {
	AgentCallable *bool `json:"agent_callable,omitempty" yaml:"agent_callable,omitempty"`
}

type commandTargetDoc struct {
	Operation string `json:"operation,omitempty" yaml:"operation,omitempty"`
	Workflow  string `json:"workflow,omitempty" yaml:"workflow,omitempty"`
	Input     any    `json:"input,omitempty" yaml:"input,omitempty"`
}

func decodeCommandDoc(value any) (command.Spec, error) {
	var raw commandDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return command.Spec{}, fmt.Errorf("appconfig: decode command document: %w", err)
	}
	spec, err := raw.Spec()
	if err != nil {
		return command.Spec{}, fmt.Errorf("appconfig: validate command document: %w", err)
	}
	return spec, nil
}

func (d commandDoc) Spec() (command.Spec, error) {
	if err := d.Target.Validate(); err != nil {
		return command.Spec{}, err
	}
	path := commandPath(d)
	annotations := cloneStringMap(d.Annotations)
	if name := commandPathResourceName(path); name != "" && strings.TrimSpace(annotations["name"]) == "" {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["name"] = name
	}
	var input operation.Type
	if d.InputSchema != nil {
		schema, err := json.Marshal(d.InputSchema)
		if err != nil {
			return command.Spec{}, fmt.Errorf("marshal input_schema: %w", err)
		}
		input.Schema = operation.Schema{Format: "json-schema", Data: schema}
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["input_schema"] = string(schema)
	}
	if d.Policy.AgentCallable != nil {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["policy.agent_callable"] = fmt.Sprintf("%t", *d.Policy.AgentCallable)
	}
	spec := command.Spec{
		Path:        path,
		Description: strings.TrimSpace(d.Description),
		Target:      d.Target.Target(),
		Input:       input,
		Annotations: annotations,
	}
	if d.Policy.AgentCallable != nil && *d.Policy.AgentCallable {
		spec.Policy.AllowedCallers = []policy.CallerKind{policy.CallerUser, policy.CallerAgent}
	}
	if err := validateCommandSpec(spec); err != nil {
		return command.Spec{}, err
	}
	return spec, nil
}

func commandPath(d commandDoc) command.Path {
	if len(d.Path) > 0 {
		out := make(command.Path, 0, len(d.Path))
		for _, part := range d.Path {
			if part = strings.Trim(strings.TrimSpace(part), "/"); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	name := strings.Trim(strings.TrimSpace(d.Name), "/")
	if name == "" {
		return nil
	}
	if strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		out := make(command.Path, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return command.Path{name}
}

func commandPathResourceName(path command.Path) string {
	if len(path) <= 1 {
		return ""
	}
	parts := make([]string, 0, len(path))
	for _, part := range path {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts, ":")
}

func (d commandTargetDoc) Validate() error {
	targets := 0
	if strings.TrimSpace(d.Operation) != "" {
		targets++
	}
	if strings.TrimSpace(d.Workflow) != "" {
		targets++
	}
	if targets == 0 {
		return fmt.Errorf("command target is empty")
	}
	if targets > 1 {
		return fmt.Errorf("command target must specify exactly one of operation or workflow")
	}
	return nil
}

func (d commandTargetDoc) Target() invocation.Target {
	switch {
	case strings.TrimSpace(d.Operation) != "":
		return invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: operation.Name(strings.TrimSpace(d.Operation))},
			Input:     d.Input,
		}
	case strings.TrimSpace(d.Workflow) != "":
		return invocation.Target{
			Kind:     invocation.TargetWorkflow,
			Workflow: workflow.Name(strings.TrimSpace(d.Workflow)),
			Input:    d.Input,
		}
	default:
		return invocation.Target{}
	}
}

func validateCommandSpec(spec command.Spec) error {
	if len(spec.Path) == 0 {
		return fmt.Errorf("command path is empty")
	}
	for i, part := range spec.Path {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("command path[%d] is empty", i)
		}
	}
	switch spec.Target.Kind {
	case invocation.TargetOperation:
		if spec.Target.Operation.Name == "" {
			return fmt.Errorf("command target operation is empty")
		}
	case invocation.TargetWorkflow:
		if spec.Target.Workflow == "" {
			return fmt.Errorf("command target workflow is empty")
		}
	default:
		return fmt.Errorf("command target kind %q is unsupported by appconfig", spec.Target.Kind)
	}
	return nil
}

type workflowDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string            `json:"version,omitempty" yaml:"version,omitempty"`
	Inputs      operation.Type    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Outputs     operation.Type    `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Steps       []workflowStepDoc `json:"steps,omitempty" yaml:"steps,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type workflowStepDoc struct {
	ID             string                   `json:"id" yaml:"id"`
	Kind           workflow.StepKind        `json:"kind,omitempty" yaml:"kind,omitempty"`
	Operation      string                   `json:"operation,omitempty" yaml:"operation,omitempty"`
	Agent          string                   `json:"agent,omitempty" yaml:"agent,omitempty"`
	Input          operation.Value          `json:"input,omitempty" yaml:"input,omitempty"`
	InputMap       map[string]string        `json:"input_map,omitempty" yaml:"input_map,omitempty"`
	DependsOn      []string                 `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	When           workflow.Condition       `json:"when,omitempty" yaml:"when,omitempty"`
	Retry          workflow.RetryPolicy     `json:"retry,omitempty" yaml:"retry,omitempty"`
	Timeout        DurationString           `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	ErrorPolicy    workflow.StepErrorPolicy `json:"error_policy,omitempty" yaml:"error_policy,omitempty"`
	IdempotencyKey string                   `json:"idempotency_key,omitempty" yaml:"idempotency_key,omitempty"`
}

func decodeWorkflowDoc(value any) (workflow.Spec, error) {
	var raw workflowDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return workflow.Spec{}, fmt.Errorf("appconfig: decode workflow document: %w", err)
	}
	spec, err := raw.Spec()
	if err != nil {
		return workflow.Spec{}, fmt.Errorf("appconfig: validate workflow document: %w", err)
	}
	return spec, nil
}

func (d workflowDoc) Spec() (workflow.Spec, error) {
	spec := workflow.Spec{
		Name:        workflow.Name(strings.TrimSpace(d.Name)),
		Description: strings.TrimSpace(d.Description),
		Version:     strings.TrimSpace(d.Version),
		Inputs:      d.Inputs,
		Outputs:     d.Outputs,
		Annotations: cloneStringMap(d.Annotations),
	}
	for _, raw := range d.Steps {
		step, err := raw.Step()
		if err != nil {
			return workflow.Spec{}, err
		}
		spec.Steps = append(spec.Steps, step)
	}
	if err := spec.Validate(); err != nil {
		return workflow.Spec{}, err
	}
	return spec, nil
}

func (d workflowStepDoc) Step() (workflow.Step, error) {
	step := workflow.Step{
		ID:             workflow.StepID(strings.TrimSpace(d.ID)),
		Kind:           workflow.StepKind(strings.TrimSpace(string(d.Kind))),
		Input:          d.Input,
		InputMap:       cloneStringMap(d.InputMap),
		DependsOn:      stepIDs(d.DependsOn),
		When:           d.When,
		Retry:          d.Retry,
		ErrorPolicy:    workflow.StepErrorPolicy(strings.TrimSpace(string(d.ErrorPolicy))),
		IdempotencyKey: strings.TrimSpace(d.IdempotencyKey),
	}
	if operationName := strings.TrimSpace(d.Operation); operationName != "" {
		step.Operation = operation.Ref{Name: operation.Name(operationName)}
		if step.Kind == "" {
			step.Kind = workflow.StepOperation
		}
	}
	if agentName := strings.TrimSpace(d.Agent); agentName != "" {
		step.Agent = agent.Ref{Name: agent.Name(agentName)}
		if step.Kind == "" {
			step.Kind = workflow.StepAgent
		}
	}
	if timeout := strings.TrimSpace(string(d.Timeout)); timeout != "" {
		parsed, err := parseDuration(timeout)
		if err != nil {
			return workflow.Step{}, err
		}
		step.Timeout = parsed
	}
	return step, nil
}

type operationDoc struct {
	Name        string              `json:"name,omitempty" yaml:"name,omitempty"`
	Ref         operation.Ref       `json:"ref,omitempty" yaml:"ref,omitempty"`
	Description string              `json:"description,omitempty" yaml:"description,omitempty"`
	Input       operation.Type      `json:"input,omitempty" yaml:"input,omitempty"`
	Output      operation.Type      `json:"output,omitempty" yaml:"output,omitempty"`
	Semantics   operation.Semantics `json:"semantics,omitempty" yaml:"semantics,omitempty"`
	Examples    []operation.Example `json:"examples,omitempty" yaml:"examples,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func decodeOperationDoc(value any) (operation.Spec, error) {
	var raw operationDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return operation.Spec{}, fmt.Errorf("appconfig: decode operation document: %w", err)
	}
	spec, err := raw.Spec()
	if err != nil {
		return operation.Spec{}, fmt.Errorf("appconfig: validate operation document: %w", err)
	}
	return spec, nil
}

func (d operationDoc) Spec() (operation.Spec, error) {
	ref := d.Ref
	if ref.Name == "" {
		ref.Name = operation.Name(strings.TrimSpace(d.Name))
	}
	spec := operation.Spec{
		Ref:         ref,
		Description: strings.TrimSpace(d.Description),
		Input:       d.Input,
		Output:      d.Output,
		Semantics:   d.Semantics,
		Examples:    append([]operation.Example(nil), d.Examples...),
		Annotations: cloneStringMap(d.Annotations),
	}
	if strings.TrimSpace(string(spec.Ref.Name)) == "" {
		return operation.Spec{}, fmt.Errorf("operation name is empty")
	}
	return spec, nil
}

func decodeDatasourceDoc(value any) (coredatasource.Spec, error) {
	var raw DatasourceDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return coredatasource.Spec{}, fmt.Errorf("appconfig: decode datasource document: %w", err)
	}
	spec := raw.Spec()
	if err := spec.Validate(); err != nil {
		return coredatasource.Spec{}, fmt.Errorf("appconfig: validate datasource document: %w", err)
	}
	return spec, nil
}

func decodeObserverDoc(value any) (coreevidence.ObserverSpec, error) {
	var raw observerDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return coreevidence.ObserverSpec{}, fmt.Errorf("appconfig: decode observer document: %w", err)
	}
	spec := raw.Spec()
	if strings.TrimSpace(spec.Name) == "" {
		return coreevidence.ObserverSpec{}, fmt.Errorf("appconfig: observer document name is empty")
	}
	return spec, nil
}

func decodeAssertionDeriverDoc(value any) (coreevidence.AssertionDeriverSpec, error) {
	var raw assertionDeriverDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return coreevidence.AssertionDeriverSpec{}, fmt.Errorf("appconfig: decode assertion_deriver document: %w", err)
	}
	spec := raw.Spec()
	if strings.TrimSpace(spec.Name) == "" {
		return coreevidence.AssertionDeriverSpec{}, fmt.Errorf("appconfig: assertion_deriver document name is empty")
	}
	return spec, nil
}

func decodeReactionDoc(value any) (corereaction.Rule, error) {
	var raw reactionDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return corereaction.Rule{}, fmt.Errorf("appconfig: decode reaction document: %w", err)
	}
	spec := raw.Spec()
	if err := spec.Validate(); err != nil {
		return corereaction.Rule{}, fmt.Errorf("appconfig: validate reaction document: %w", err)
	}
	return spec, nil
}

type observerDoc struct {
	Name            string                        `json:"name" yaml:"name"`
	Description     string                        `json:"description,omitempty" yaml:"description,omitempty"`
	Environment     coreevidence.Ref              `json:"environment,omitempty" yaml:"environment,omitempty"`
	Phase           coreevidence.ObservationPhase `json:"phase,omitempty" yaml:"phase,omitempty"`
	ObservableKinds []string                      `json:"observable_kinds,omitempty" yaml:"observable_kinds,omitempty"`
	Dynamic         bool                          `json:"dynamic,omitempty" yaml:"dynamic,omitempty"`
	Disabled        bool                          `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Annotations     map[string]string             `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (d observerDoc) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            strings.TrimSpace(d.Name),
		Description:     strings.TrimSpace(d.Description),
		Environment:     d.Environment,
		Phase:           d.Phase,
		ObservableKinds: append([]string(nil), d.ObservableKinds...),
		Dynamic:         d.Dynamic,
		Disabled:        d.Disabled,
		Annotations:     cloneStringMap(d.Annotations),
	}
}

type assertionDeriverDoc struct {
	Name             string                           `json:"name" yaml:"name"`
	Description      string                           `json:"description,omitempty" yaml:"description,omitempty"`
	ObservationKinds []string                         `json:"observation_kinds,omitempty" yaml:"observation_kinds,omitempty"`
	Assertions       []coreevidence.AssertionTemplate `json:"assertions,omitempty" yaml:"assertions,omitempty"`
	Annotations      map[string]string                `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (d assertionDeriverDoc) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             strings.TrimSpace(d.Name),
		Description:      strings.TrimSpace(d.Description),
		ObservationKinds: append([]string(nil), d.ObservationKinds...),
		Assertions:       append([]coreevidence.AssertionTemplate(nil), d.Assertions...),
		Annotations:      cloneStringMap(d.Annotations),
	}
}

type reactionDoc struct {
	Name        string                `json:"name" yaml:"name"`
	Mode        corereaction.Mode     `json:"mode,omitempty" yaml:"mode,omitempty"`
	When        corereaction.Matcher  `json:"when" yaml:"when"`
	Actions     []corereaction.Action `json:"actions,omitempty" yaml:"actions,omitempty"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (d reactionDoc) Spec() corereaction.Rule {
	return corereaction.Rule{
		Name:        strings.TrimSpace(d.Name),
		Mode:        d.Mode,
		When:        d.When,
		Actions:     append([]corereaction.Action(nil), d.Actions...),
		Description: strings.TrimSpace(d.Description),
		Annotations: cloneStringMap(d.Annotations),
	}
}

type llmProviderDoc struct {
	Name        corellm.ProviderName `json:"name" yaml:"name"`
	DisplayName string               `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description string               `json:"description,omitempty" yaml:"description,omitempty"`
	Models      []corellm.ModelSpec  `json:"models,omitempty" yaml:"models,omitempty"`
	Annotations map[string]string    `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func (d llmProviderDoc) Spec() corellm.ProviderSpec {
	return corellm.ProviderSpec{
		Name:        d.Name,
		DisplayName: d.DisplayName,
		Description: d.Description,
		Models:      d.Models,
		Annotations: cloneStringMap(d.Annotations),
	}
}

func decodeLLMProviderDoc(value any) (corellm.ProviderSpec, error) {
	var raw llmProviderDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
		return corellm.ProviderSpec{}, fmt.Errorf("appconfig: decode llm provider document: %w", err)
	}
	spec := raw.Spec()
	if err := spec.Validate(); err != nil {
		return corellm.ProviderSpec{}, fmt.Errorf("appconfig: validate llm provider document: %w", err)
	}
	return spec, nil
}

type sessionDoc struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Agent       string            `json:"agent,omitempty" yaml:"agent,omitempty"`
	Channel     string            `json:"channel,omitempty" yaml:"channel,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

func decodeSessionDoc(value any) (coresession.Spec, error) {
	var raw sessionDoc
	if err := decodeDocumentValue(value, &raw); err != nil {
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

func documentKind(value any) string {
	raw, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	kind, _ := raw["kind"].(string)
	return kind
}

// Spec converts the manifest file shape to the pure app model.
func (m Manifest) Spec() coreapp.Spec {
	model := m.ModelPolicy.Spec()
	if defaultModel := strings.TrimSpace(string(m.Models.Default)); defaultModel != "" {
		model.Model = defaultModel
		model.Provider = ""
	}
	if defaultModel := strings.TrimSpace(string(m.Defaults.Model)); defaultModel != "" {
		model.Model = defaultModel
		model.Provider = ""
	}
	defaultAgent := agent.Ref(m.DefaultAgent)
	if strings.TrimSpace(string(m.Defaults.Agent.Name)) != "" {
		defaultAgent = agent.Ref(m.Defaults.Agent)
	}
	spec := coreapp.Spec{
		Name:           m.Name,
		Description:    m.Description,
		DefaultAgent:   defaultAgent,
		Discovery:      m.Discovery.Spec(),
		Model:          model,
		Datasource:     m.Datasource.Spec(),
		SemanticSearch: m.SemanticSearch.Spec(),
		Security:       m.Security,
		Identity:       m.Identity.Spec(),
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
	Model         ModelSelector     `json:"model,omitempty" yaml:"model,omitempty"`
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
		Model:         string(m.Model),
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

func (agentRef) JSONSchema() *invjsonschema.Schema {
	properties := invjsonschema.NewProperties()
	properties.Set("name", &invjsonschema.Schema{Type: "string"})
	return &invjsonschema.Schema{OneOf: []*invjsonschema.Schema{
		{Type: "string"},
		{
			Type:                 "object",
			Properties:           properties,
			AdditionalProperties: invjsonschema.FalseSchema,
		},
	}}
}

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

func (sourceSpec) JSONSchema() *invjsonschema.Schema {
	properties := invjsonschema.NewProperties()
	properties.Set("location", &invjsonschema.Schema{Type: "string"})
	properties.Set("scope", stringEnumSchema("", "app", "project", "user", "embedded", "remote", "explicit"))
	properties.Set("ecosystem", &invjsonschema.Schema{Type: "string"})
	properties.Set("annotations", &invjsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: &invjsonschema.Schema{Type: "string"},
	})
	return &invjsonschema.Schema{OneOf: []*invjsonschema.Schema{
		{Type: "string"},
		{
			Type:                 "object",
			Properties:           properties,
			AdditionalProperties: invjsonschema.FalseSchema,
		},
	}}
}

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

type pluginRefs []pluginRef

func (pluginRefs) JSONSchema() *invjsonschema.Schema {
	properties := invjsonschema.NewProperties()
	properties.Set("kind", &invjsonschema.Schema{Type: "string"})
	properties.Set("instance", &invjsonschema.Schema{Type: "string"})
	properties.Set("enabled", &invjsonschema.Schema{Type: "boolean"})
	entry := &invjsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		AdditionalProperties: invjsonschema.TrueSchema,
	}
	return &invjsonschema.Schema{
		Type: "object",
		AdditionalProperties: &invjsonschema.Schema{OneOf: []*invjsonschema.Schema{
			{Type: "null"},
			entry,
		}},
	}
}

func (p *pluginRefs) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("plugins must be a map of plugin instances")
	}
	byInstance := map[string]pluginRef{}
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		var instance string
		if err := keyNode.Decode(&instance); err != nil {
			return err
		}
		instance = strings.TrimSpace(instance)
		if instance == "" {
			return fmt.Errorf("plugins contains an empty instance name")
		}
		ref, enabled, err := decodePluginMapEntry(instance, valueNode)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
		byInstance[instance] = ref
	}
	instances := make([]string, 0, len(byInstance))
	for instance := range byInstance {
		instances = append(instances, instance)
	}
	sort.Strings(instances)
	out := make([]pluginRef, 0, len(instances))
	for _, instance := range instances {
		out = append(out, byInstance[instance])
	}
	*p = out
	return nil
}

func (p *pluginRefs) UnmarshalJSON(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) == 0 {
		return nil
	}
	return p.UnmarshalYAML(node.Content[0])
}

type pluginRef coreapp.PluginRef

func decodePluginMapEntry(instance string, node *yaml.Node) (pluginRef, bool, error) {
	ref := coreapp.PluginRef{
		Kind:     instance,
		Instance: instance,
	}
	enabled := true
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return pluginRef(ref), enabled, nil
	}
	if node.Kind != yaml.MappingNode {
		return pluginRef{}, false, fmt.Errorf("plugins.%s must be null or an object", instance)
	}
	config := map[string]any{}
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		var key string
		if err := keyNode.Decode(&key); err != nil {
			return pluginRef{}, false, err
		}
		key = strings.TrimSpace(key)
		switch key {
		case "":
			return pluginRef{}, false, fmt.Errorf("plugins.%s contains an empty config key", instance)
		case "kind":
			var kind string
			if err := valueNode.Decode(&kind); err != nil {
				return pluginRef{}, false, fmt.Errorf("plugins.%s.kind: %w", instance, err)
			}
			if kind = strings.TrimSpace(kind); kind != "" {
				ref.Kind = kind
			}
		case "instance":
			var configured string
			if err := valueNode.Decode(&configured); err != nil {
				return pluginRef{}, false, fmt.Errorf("plugins.%s.instance: %w", instance, err)
			}
			configured = strings.TrimSpace(configured)
			if configured != instance {
				return pluginRef{}, false, fmt.Errorf("plugins.%s.instance must match map key %q", instance, instance)
			}
		case "enabled":
			if err := valueNode.Decode(&enabled); err != nil {
				return pluginRef{}, false, fmt.Errorf("plugins.%s.enabled: %w", instance, err)
			}
		default:
			var value any
			if err := valueNode.Decode(&value); err != nil {
				return pluginRef{}, false, fmt.Errorf("plugins.%s.%s: %w", instance, key, err)
			}
			normalized, err := normalizeJSONValue(value)
			if err != nil {
				return pluginRef{}, false, fmt.Errorf("plugins.%s.%s: %w", instance, key, err)
			}
			config[key] = normalized
		}
	}
	ref.Kind = strings.TrimSpace(ref.Kind)
	ref.Instance = instance
	ref.Config = cloneMap(config)
	return pluginRef(ref), enabled, nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stepIDs(values []string) []workflow.StepID {
	out := make([]workflow.StepID, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, workflow.StepID(value))
		}
	}
	return out
}

func parseDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse workflow step timeout %q: %w", value, err)
	}
	return duration, nil
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
	_ json.Unmarshaler = (*DurationString)(nil)
	_ json.Unmarshaler = (*agentRef)(nil)
	_ json.Unmarshaler = (*sourceSpec)(nil)
	_ json.Unmarshaler = (*pluginRefs)(nil)
)
