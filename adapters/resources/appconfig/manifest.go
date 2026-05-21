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

	"github.com/fluxplane/engine/core/agent"
	coreapp "github.com/fluxplane/engine/core/app"
	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/invocation"
	corellm "github.com/fluxplane/engine/core/llm"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	corereaction "github.com/fluxplane/engine/core/reaction"
	"github.com/fluxplane/engine/core/resource"
	coresession "github.com/fluxplane/engine/core/session"
	coreskill "github.com/fluxplane/engine/core/skill"
	"github.com/fluxplane/engine/core/user"
	"github.com/fluxplane/engine/core/workflow"
	invjsonschema "github.com/invopop/jsonschema"
	santhoshjsonschema "github.com/santhosh-tekuri/jsonschema/v6"
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
	return DecodeFile(clean, data)
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
	Runtime      RuntimeConfig
	Daemon       DaemonConfig
	Connectors   map[string]ConnectorDoc
}

// DecodeFile decodes one local app file. It supports both the legacy single
// app document and the rewrite-native multi-document kind-based shape.
func DecodeFile(path string, data []byte) (File, error) {
	source := manifestSource(path)
	bundle := resource.ContributionBundle{Source: source}
	distribution := coredistribution.Spec{}
	runtime := RuntimeConfig{}
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
			if err := validateYAMLNode[Manifest](doc); err != nil {
				return File{}, fmt.Errorf("appconfig: validate app document schema: %w", err)
			}
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
					Name:     plugin.Kind,
					Instance: plugin.Instance,
					Config:   cloneMap(plugin.Config),
				})
			}
			for _, ds := range manifest.Datasource.Datasources {
				bundle.Datasources = append(bundle.Datasources, ds.Spec())
			}
			for i, raw := range manifest.Commands {
				spec, err := raw.Spec()
				if err != nil {
					return File{}, fmt.Errorf("appconfig: validate commands[%d]: %w", i, err)
				}
				bundle.Commands = append(bundle.Commands, spec)
			}
			for i, raw := range manifest.Workflows {
				spec, err := raw.Spec()
				if err != nil {
					return File{}, fmt.Errorf("appconfig: validate workflows[%d]: %w", i, err)
				}
				bundle.Workflows = append(bundle.Workflows, spec)
			}
			for i, raw := range manifest.Operations {
				spec, err := raw.Spec()
				if err != nil {
					return File{}, fmt.Errorf("appconfig: validate operations[%d]: %w", i, err)
				}
				bundle.Operations = append(bundle.Operations, spec)
			}
			bundle.Observers = append(bundle.Observers, manifest.Observations.Observers...)
			bundle.AssertionDerivers = append(bundle.AssertionDerivers, manifest.Observations.AssertionDerivers...)
			for i, rule := range manifest.Reactions {
				if err := rule.Validate(); err != nil {
					return File{}, fmt.Errorf("appconfig: validate reactions[%d]: %w", i, err)
				}
				bundle.Reactions = append(bundle.Reactions, rule)
			}
			models, err := manifest.Models.Contributions()
			if err != nil {
				return File{}, err
			}
			if err := validateModelReference(strings.TrimSpace(manifest.Models.Default), models, "models.default"); err != nil {
				return File{}, err
			}
			if err := validateModelReference(strings.TrimSpace(manifest.Distribution.Deploy.Model), models, "distribution.deploy.model"); err != nil {
				return File{}, err
			}
			modelProviders := mergeLLMProviders(manifest.LLMProviders, models.Providers)
			for i, provider := range modelProviders {
				if err := provider.Validate(); err != nil {
					return File{}, fmt.Errorf("appconfig: validate llm_providers[%d]: %w", i, err)
				}
			}
			bundle.LLMProviders = append(bundle.LLMProviders, modelProviders...)
			bundle.LLMModelAliases = append(bundle.LLMModelAliases, models.Aliases...)
			distribution = manifest.Distribution.Spec()
			runtime = manifest.Runtime
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
		case "command":
			spec, err := decodeCommandDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Commands = append(bundle.Commands, spec)
		case "workflow":
			spec, err := decodeWorkflowDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Workflows = append(bundle.Workflows, spec)
		case "operation":
			spec, err := decodeOperationDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Operations = append(bundle.Operations, spec)
		case "datasource":
			spec, err := decodeDatasourceDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Datasources = append(bundle.Datasources, spec)
		case "observer":
			spec, err := decodeObserverDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Observers = append(bundle.Observers, spec)
		case "assertion_deriver":
			spec, err := decodeAssertionDeriverDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.AssertionDerivers = append(bundle.AssertionDerivers, spec)
		case "reaction":
			spec, err := decodeReactionDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.Reactions = append(bundle.Reactions, spec)
		case "llm_provider":
			spec, err := decodeLLMProviderDoc(doc)
			if err != nil {
				return File{}, err
			}
			bundle.LLMProviders = append(bundle.LLMProviders, spec)
		case "":
			return File{}, fmt.Errorf("appconfig: document %d kind is empty", i+1)
		default:
			return File{}, fmt.Errorf("appconfig: unsupported document kind %q", kind)
		}
	}
	return File{Path: filepath.Clean(path), Bundle: bundle, Distribution: distribution, Runtime: runtime, Daemon: daemon, Connectors: connectors}, nil
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

func validateYAMLNode[T any](node yaml.Node) error {
	value, err := jsonValueFromYAMLNode(node)
	if err != nil {
		return err
	}
	return validateJSONValue[T](value)
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
	var schemaValue any
	if err := json.Unmarshal(schemaData, &schemaValue); err != nil {
		return fmt.Errorf("decode schema resource: %w", err)
	}
	compiler := santhoshjsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
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

func jsonValueFromYAMLNode(node yaml.Node) (any, error) {
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil, nil
		}
		node = *node.Content[0]
	}
	var decoded any
	if err := node.Decode(&decoded); err != nil {
		return nil, err
	}
	data, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON value: %w", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal JSON value: %w", err)
	}
	return out, nil
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
		switch strings.TrimSpace(root.Access) {
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
	return nil
}

type kindDoc struct {
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
}

// Manifest is the app manifest file shape accepted by this adapter.
type Manifest struct {
	Kind           string                     `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name           coreapp.Name               `json:"name,omitempty" yaml:"name,omitempty"`
	Description    string                     `json:"description,omitempty" yaml:"description,omitempty"`
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
	Plugins        []pluginRef                `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	Commands       []commandDoc               `json:"commands,omitempty" yaml:"commands,omitempty"`
	Workflows      []workflowDoc              `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	Operations     []operationDoc             `json:"operations,omitempty" yaml:"operations,omitempty"`
	Observations   observationsDoc            `json:"observations,omitempty" yaml:"observations,omitempty"`
	Reactions      []corereaction.Rule        `json:"reactions,omitempty" yaml:"reactions,omitempty"`
	LLMProviders   []corellm.ProviderSpec     `json:"llm_providers,omitempty" yaml:"llm_providers,omitempty"`
	Runtime        RuntimeConfig              `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Daemon         DaemonConfig               `json:"daemon,omitempty" yaml:"daemon,omitempty"`
	Connectors     map[string]ConnectorDoc    `json:"connectors,omitempty" yaml:"connectors,omitempty"`
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

// RuntimeDataDoc contains runtime-owned durable data store settings.
type RuntimeDataDoc struct {
	Store RuntimeDataStoreDoc `json:"store,omitempty" yaml:"store,omitempty"`
}

type RuntimeDataStoreDoc struct {
	Kind   string `json:"kind,omitempty" yaml:"kind,omitempty"`
	DSN    string `json:"dsn,omitempty" yaml:"dsn,omitempty"`
	DSNEnv string `json:"dsn_env,omitempty" yaml:"dsn_env,omitempty"`
}

// RuntimeEventsDoc contains runtime-owned durable event store settings.
type RuntimeEventsDoc struct {
	Store RuntimeEventStoreDoc `json:"store,omitempty" yaml:"store,omitempty"`
}

type RuntimeEventStoreDoc struct {
	Kind         string `json:"kind,omitempty" yaml:"kind,omitempty"`
	DSN          string `json:"dsn,omitempty" yaml:"dsn,omitempty"`
	DSNEnv       string `json:"dsn_env,omitempty" yaml:"dsn_env,omitempty"`
	Stream       string `json:"stream,omitempty" yaml:"stream,omitempty"`
	Subject      string `json:"subject,omitempty" yaml:"subject,omitempty"`
	CreateStream bool   `json:"create_stream,omitempty" yaml:"create_stream,omitempty"`
}

// WorkspaceConfig contains additional local filesystem workspace roots.
type WorkspaceConfig struct {
	Roots       []WorkspaceRootDoc `json:"roots,omitempty" yaml:"roots,omitempty"`
	ScratchRoot string             `json:"scratch_root,omitempty" yaml:"scratch_root,omitempty"`
	EnvFiles    []string           `json:"env_files,omitempty" yaml:"env_files,omitempty"`
}

type WorkspaceRootDoc struct {
	Name     string   `json:"name" yaml:"name"`
	Path     string   `json:"path" yaml:"path"`
	Access   string   `json:"access,omitempty" yaml:"access,omitempty"`
	Create   bool     `json:"create,omitempty" yaml:"create,omitempty"`
	EnvFiles []string `json:"env_files,omitempty" yaml:"env_files,omitempty"`
}

type modelConfigDoc struct {
	Default   string              `json:"default,omitempty" yaml:"default,omitempty"`
	Available []modelAvailableDoc `json:"available,omitempty" yaml:"available,omitempty"`
}

type modelAvailableDoc struct {
	Provider string         `json:"provider" yaml:"provider"`
	Model    string         `json:"model" yaml:"model"`
	Aliases  []string       `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Params   modelParamsDoc `json:"params,omitempty" yaml:"params,omitempty"`
}

type modelParamsDoc struct {
	Thinking string `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	Effort   string `json:"effort,omitempty" yaml:"effort,omitempty"`
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
				Thinking:        strings.TrimSpace(raw.Params.Thinking),
				ReasoningEffort: strings.TrimSpace(raw.Params.Effort),
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
		Deploy: coredistribution.DeploySpec{
			Model: strings.TrimSpace(d.Deploy.Model),
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

type deployDoc struct {
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
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

type datasourceConfigDoc struct {
	Index       datasourceIndexDefaultsDoc `json:"index,omitempty" yaml:"index,omitempty"`
	Datasources []DatasourceDoc            `json:"datasources,omitempty" yaml:"datasources,omitempty"`
}

func (d datasourceConfigDoc) Spec() coreapp.DatasourceSpec {
	out := coreapp.DatasourceSpec{
		Index: coreapp.DatasourceIndexSpec{
			Concurrency: d.Index.Concurrency,
			Freshness:   strings.TrimSpace(d.Index.Freshness),
		},
	}
	for _, ds := range d.Datasources {
		out.Datasources = append(out.Datasources, ds.Spec())
	}
	return out
}

type datasourceIndexDefaultsDoc struct {
	Concurrency int    `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
	Freshness   string `json:"freshness,omitempty" yaml:"freshness,omitempty"`
}

// DatasourceDoc declares one configured datasource instance.
type DatasourceDoc struct {
	Name        string             `json:"name" yaml:"name"`
	Description string             `json:"description,omitempty" yaml:"description,omitempty"`
	Entities    []string           `json:"entities,omitempty" yaml:"entities,omitempty"`
	Connector   string             `json:"connector,omitempty" yaml:"connector,omitempty"`
	Kind        string             `json:"kind,omitempty" yaml:"kind,omitempty"`
	Type        string             `json:"type,omitempty" yaml:"type,omitempty"`
	Path        string             `json:"path,omitempty" yaml:"path,omitempty"`
	Include     []string           `json:"include,omitempty" yaml:"include,omitempty"`
	Config      map[string]string  `json:"config,omitempty" yaml:"config,omitempty"`
	Index       datasourceIndexDoc `json:"index,omitempty" yaml:"index,omitempty"`
	Semantic    semanticDoc        `json:"semantic,omitempty" yaml:"semantic,omitempty"`
}

type datasourceIndexDoc struct {
	Enabled   bool   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Freshness string `json:"freshness,omitempty" yaml:"freshness,omitempty"`
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
		Index: coredatasource.IndexSpec{
			Enabled:   d.Index.Enabled,
			Freshness: strings.TrimSpace(d.Index.Freshness),
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

// ConnectorDoc declares one connector instance required by app serving.
type ConnectorDoc struct {
	Kind string `json:"kind" yaml:"kind"`
}

// DaemonConfig contains process wiring consumed by app serving.
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
	Instance  string    `json:"instance,omitempty" yaml:"instance,omitempty"`
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
	Kind        string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Model       string   `json:"model,omitempty" yaml:"model,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	Turns       turnsDoc `json:"turns,omitempty" yaml:"turns,omitempty"`
	Thinking    string   `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	Effort      string   `json:"effort,omitempty" yaml:"effort,omitempty"`
	Operations  []string `json:"operations,omitempty" yaml:"operations,omitempty"`
	Tools       []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	Context     []string `json:"context,omitempty" yaml:"context,omitempty"`
	Datasources []string `json:"datasources,omitempty" yaml:"datasources,omitempty"`
	Skills      []string `json:"skills,omitempty" yaml:"skills,omitempty"`
	System      string   `json:"system,omitempty" yaml:"system,omitempty"`
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

func decodeAgentDoc(node yaml.Node) (agent.Spec, error) {
	if err := validateYAMLNode[agentDoc](node); err != nil {
		return agent.Spec{}, fmt.Errorf("appconfig: validate agent document schema: %w", err)
	}
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
		Turns: agent.TurnPolicy{
			MaxSteps: raw.Turns.MaxSteps,
			Continuation: agent.ContinuationPolicy{
				MaxContinuations: raw.Turns.Continuation.MaxContinuations,
				ContextPolicy:    strings.TrimSpace(raw.Turns.Continuation.ContextPolicy),
				StopCondition:    raw.Turns.Continuation.StopCondition.Spec(),
			},
		},
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
		return agent.Spec{}, fmt.Errorf("appconfig: validate agent document: %w", err)
	}
	return spec, nil
}

type commandDoc struct {
	Kind        string            `json:"kind,omitempty" yaml:"kind,omitempty"`
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

func decodeCommandDoc(node yaml.Node) (command.Spec, error) {
	if err := validateYAMLNode[commandDoc](node); err != nil {
		return command.Spec{}, fmt.Errorf("appconfig: validate command document schema: %w", err)
	}
	var raw commandDoc
	if err := node.Decode(&raw); err != nil {
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
	Kind        string            `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string            `json:"version,omitempty" yaml:"version,omitempty"`
	Inputs      operation.Type    `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Outputs     operation.Type    `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Steps       []workflowStepDoc `json:"steps,omitempty" yaml:"steps,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type workflowStepDoc struct {
	ID              string               `json:"id" yaml:"id"`
	Kind            string               `json:"kind,omitempty" yaml:"kind,omitempty"`
	Operation       string               `json:"operation,omitempty" yaml:"operation,omitempty"`
	Agent           string               `json:"agent,omitempty" yaml:"agent,omitempty"`
	Input           operation.Value      `json:"input,omitempty" yaml:"input,omitempty"`
	InputMap        map[string]string    `json:"input_map,omitempty" yaml:"input_map,omitempty"`
	DependsOn       []string             `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	DependsOnDash   []string             `json:"depends-on,omitempty" yaml:"depends-on,omitempty"`
	When            workflow.Condition   `json:"when,omitempty" yaml:"when,omitempty"`
	Retry           workflow.RetryPolicy `json:"retry,omitempty" yaml:"retry,omitempty"`
	Timeout         string               `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	ErrorPolicy     string               `json:"error_policy,omitempty" yaml:"error_policy,omitempty"`
	ErrorPolicyDash string               `json:"error-policy,omitempty" yaml:"error-policy,omitempty"`
	IdempotencyKey  string               `json:"idempotency_key,omitempty" yaml:"idempotency_key,omitempty"`
	IdempotencyDash string               `json:"idempotency-key,omitempty" yaml:"idempotency-key,omitempty"`
}

func decodeWorkflowDoc(node yaml.Node) (workflow.Spec, error) {
	if err := validateYAMLNode[workflowDoc](node); err != nil {
		return workflow.Spec{}, fmt.Errorf("appconfig: validate workflow document schema: %w", err)
	}
	var raw workflowDoc
	if err := node.Decode(&raw); err != nil {
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
		Kind:           workflow.StepKind(strings.TrimSpace(d.Kind)),
		Input:          d.Input,
		InputMap:       cloneStringMap(d.InputMap),
		DependsOn:      stepIDs(firstStringSlice(d.DependsOn, d.DependsOnDash)),
		When:           d.When,
		Retry:          d.Retry,
		ErrorPolicy:    workflow.StepErrorPolicy(firstNonEmpty(strings.TrimSpace(d.ErrorPolicy), strings.TrimSpace(d.ErrorPolicyDash))),
		IdempotencyKey: firstNonEmpty(strings.TrimSpace(d.IdempotencyKey), strings.TrimSpace(d.IdempotencyDash)),
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
	if timeout := strings.TrimSpace(d.Timeout); timeout != "" {
		parsed, err := parseDuration(timeout)
		if err != nil {
			return workflow.Step{}, err
		}
		step.Timeout = parsed
	}
	return step, nil
}

type operationDoc struct {
	Kind        string              `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name        string              `json:"name,omitempty" yaml:"name,omitempty"`
	Ref         operation.Ref       `json:"ref,omitempty" yaml:"ref,omitempty"`
	Description string              `json:"description,omitempty" yaml:"description,omitempty"`
	Input       operation.Type      `json:"input,omitempty" yaml:"input,omitempty"`
	Output      operation.Type      `json:"output,omitempty" yaml:"output,omitempty"`
	Semantics   operation.Semantics `json:"semantics,omitempty" yaml:"semantics,omitempty"`
	Examples    []operation.Example `json:"examples,omitempty" yaml:"examples,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

func decodeOperationDoc(node yaml.Node) (operation.Spec, error) {
	if err := validateYAMLNode[operationDoc](node); err != nil {
		return operation.Spec{}, fmt.Errorf("appconfig: validate operation document schema: %w", err)
	}
	var raw operationDoc
	if err := node.Decode(&raw); err != nil {
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

func decodeDatasourceDoc(node yaml.Node) (coredatasource.Spec, error) {
	if err := validateYAMLNode[DatasourceDoc](node); err != nil {
		return coredatasource.Spec{}, fmt.Errorf("appconfig: validate datasource document schema: %w", err)
	}
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

func decodeObserverDoc(node yaml.Node) (coreevidence.ObserverSpec, error) {
	if err := validateYAMLNode[observerDoc](node); err != nil {
		return coreevidence.ObserverSpec{}, fmt.Errorf("appconfig: validate observer document schema: %w", err)
	}
	var raw observerDoc
	if err := node.Decode(&raw); err != nil {
		return coreevidence.ObserverSpec{}, fmt.Errorf("appconfig: decode observer document: %w", err)
	}
	spec := raw.Spec()
	if strings.TrimSpace(spec.Name) == "" {
		return coreevidence.ObserverSpec{}, fmt.Errorf("appconfig: observer document name is empty")
	}
	return spec, nil
}

func decodeAssertionDeriverDoc(node yaml.Node) (coreevidence.AssertionDeriverSpec, error) {
	if err := validateYAMLNode[assertionDeriverDoc](node); err != nil {
		return coreevidence.AssertionDeriverSpec{}, fmt.Errorf("appconfig: validate assertion_deriver document schema: %w", err)
	}
	var raw assertionDeriverDoc
	if err := node.Decode(&raw); err != nil {
		return coreevidence.AssertionDeriverSpec{}, fmt.Errorf("appconfig: decode assertion_deriver document: %w", err)
	}
	spec := raw.Spec()
	if strings.TrimSpace(spec.Name) == "" {
		return coreevidence.AssertionDeriverSpec{}, fmt.Errorf("appconfig: assertion_deriver document name is empty")
	}
	return spec, nil
}

func decodeReactionDoc(node yaml.Node) (corereaction.Rule, error) {
	if err := validateYAMLNode[reactionDoc](node); err != nil {
		return corereaction.Rule{}, fmt.Errorf("appconfig: validate reaction document schema: %w", err)
	}
	var raw reactionDoc
	if err := node.Decode(&raw); err != nil {
		return corereaction.Rule{}, fmt.Errorf("appconfig: decode reaction document: %w", err)
	}
	spec := raw.Spec()
	if err := spec.Validate(); err != nil {
		return corereaction.Rule{}, fmt.Errorf("appconfig: validate reaction document: %w", err)
	}
	return spec, nil
}

type observerDoc struct {
	Kind            string                        `json:"kind,omitempty" yaml:"kind,omitempty"`
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
	Kind             string                           `json:"kind,omitempty" yaml:"kind,omitempty"`
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
	Kind        string                `json:"kind,omitempty" yaml:"kind,omitempty"`
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
	Kind        string               `json:"kind,omitempty" yaml:"kind,omitempty"`
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

func decodeLLMProviderDoc(node yaml.Node) (corellm.ProviderSpec, error) {
	if err := validateYAMLNode[llmProviderDoc](node); err != nil {
		return corellm.ProviderSpec{}, fmt.Errorf("appconfig: validate llm provider document schema: %w", err)
	}
	var raw llmProviderDoc
	if err := node.Decode(&raw); err != nil {
		return corellm.ProviderSpec{}, fmt.Errorf("appconfig: decode llm provider document: %w", err)
	}
	spec := raw.Spec()
	if err := spec.Validate(); err != nil {
		return corellm.ProviderSpec{}, fmt.Errorf("appconfig: validate llm provider document: %w", err)
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
	if err := validateYAMLNode[sessionDoc](node); err != nil {
		return coresession.Spec{}, fmt.Errorf("appconfig: validate session document schema: %w", err)
	}
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
	model := m.ModelPolicy.Spec()
	if defaultModel := strings.TrimSpace(m.Models.Default); defaultModel != "" {
		model.Model = defaultModel
		model.Provider = ""
	}
	spec := coreapp.Spec{
		Name:           m.Name,
		Description:    m.Description,
		DefaultAgent:   agent.Ref(m.DefaultAgent),
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
	properties.Set("scope", &invjsonschema.Schema{Type: "string"})
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

type pluginRef coreapp.PluginRef

func (pluginRef) JSONSchema() *invjsonschema.Schema {
	properties := invjsonschema.NewProperties()
	properties.Set("kind", &invjsonschema.Schema{Type: "string"})
	properties.Set("instance", &invjsonschema.Schema{Type: "string"})
	properties.Set("config", &invjsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: invjsonschema.TrueSchema,
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

func (p *pluginRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var kind string
		if err := node.Decode(&kind); err != nil {
			return err
		}
		*p = pluginRef{Kind: strings.TrimSpace(kind)}
		return nil
	}
	var ref coreapp.PluginRef
	if err := node.Decode(&ref); err != nil {
		return err
	}
	ref.Kind = strings.TrimSpace(ref.Kind)
	ref.Instance = strings.TrimSpace(ref.Instance)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
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
	_ json.Unmarshaler = (*agentRef)(nil)
	_ json.Unmarshaler = (*sourceSpec)(nil)
	_ json.Unmarshaler = (*pluginRef)(nil)
)
