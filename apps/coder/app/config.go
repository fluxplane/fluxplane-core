// Package coderapp coordinates the coder product CLI and local configuration.
package coderapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/engine/apps/coder"
	"github.com/fluxplane/engine/apps/launch"
	"github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	corereaction "github.com/fluxplane/engine/core/reaction"
	"github.com/fluxplane/engine/core/resource"
	coreskill "github.com/fluxplane/engine/core/skill"
	coreworkflow "github.com/fluxplane/engine/core/workflow"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	configNameYAML = ".coder.yaml"
	configNameYML  = ".coder.yml"
)

// Config describes programmatic coder app construction.
type Config struct {
	Root            string
	CoderConfigPath string
	Workspace       distribution.WorkspaceConfig
	Editor          launch.EditorRunner
}

// App is the resolved coder product.
type App struct {
	root   string
	config ResolvedConfig
	editor launch.EditorRunner
}

// ResolvedConfig is the effective local coder configuration.
type ResolvedConfig struct {
	Path         string                       `json:"path,omitempty" yaml:"path,omitempty"`
	Explicit     bool                         `json:"explicit" yaml:"explicit"`
	Workspace    distribution.WorkspaceConfig `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	Imports      ImportConfig                 `json:"imports,omitempty" yaml:"imports,omitempty"`
	Observations ObservationConfig            `json:"observations,omitempty" yaml:"observations,omitempty"`
	Reactions    []corereaction.Rule          `json:"reactions,omitempty" yaml:"reactions,omitempty"`
}

// ImportConfig reserves explicit coder resource imports for a later slice.
type ImportConfig struct {
	Agents     []string `json:"agents,omitempty" yaml:"agents,omitempty"`
	Skills     []string `json:"skills,omitempty" yaml:"skills,omitempty"`
	Workflows  []string `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	Operations []string `json:"operations,omitempty" yaml:"operations,omitempty"`
	Apps       []string `json:"apps,omitempty" yaml:"apps,omitempty"`
}

type fileConfig struct {
	Version      int             `yaml:"version"`
	Workspace    workspaceFile   `yaml:"workspace"`
	Imports      ImportConfig    `yaml:"imports"`
	Observations observationFile `yaml:"observations"`
	Reactions    []reactionFile  `yaml:"reactions"`
}

// ObservationConfig carries inert observer configuration from .coder.yaml.
type ObservationConfig struct {
	Observers         []coreevidence.ObserverSpec         `json:"observers,omitempty" yaml:"observers,omitempty"`
	AssertionDerivers []coreevidence.AssertionDeriverSpec `json:"assertion_derivers,omitempty" yaml:"assertion_derivers,omitempty"`
}

type workspaceFile struct {
	Roots    []workspaceRootFile `yaml:"roots"`
	EnvFiles []string            `yaml:"env_files"`
}

type workspaceRootFile struct {
	Name     string   `yaml:"name"`
	Path     string   `yaml:"path"`
	Access   string   `yaml:"access"`
	Create   bool     `yaml:"create"`
	EnvFiles []string `yaml:"env_files"`
}

type observationFile struct {
	Observers         []observerFile         `yaml:"observers"`
	AssertionDerivers []assertionDeriverFile `yaml:"assertion_derivers"`
}

type observerFile struct {
	Name            string                        `yaml:"name"`
	Description     string                        `yaml:"description"`
	Environment     environmentRefFile            `yaml:"environment"`
	Phase           coreevidence.ObservationPhase `yaml:"phase"`
	ObservableKinds []string                      `yaml:"observable_kinds"`
	Dynamic         bool                          `yaml:"dynamic"`
	Disabled        bool                          `yaml:"disabled"`
	Annotations     map[string]string             `yaml:"annotations"`
}

type environmentRefFile struct {
	Name coreevidence.Name `yaml:"name"`
}

type assertionDeriverFile struct {
	Name             string                  `yaml:"name"`
	Description      string                  `yaml:"description"`
	ObservationKinds []string                `yaml:"observation_kinds"`
	Assertions       []assertionTemplateFile `yaml:"assertions"`
	Annotations      map[string]string       `yaml:"annotations"`
}

type assertionTemplateFile struct {
	Kind    string      `yaml:"kind"`
	Target  string      `yaml:"target"`
	Subject subjectFile `yaml:"subject"`
	Scope   string      `yaml:"scope"`
	Source  string      `yaml:"source"`
}

type subjectFile struct {
	Kind coreevidence.SubjectKind `yaml:"kind"`
	Name string                   `yaml:"name"`
	ID   string                   `yaml:"id"`
}

type reactionFile struct {
	Name        string               `yaml:"name"`
	Mode        corereaction.Mode    `yaml:"mode"`
	When        matcherFile          `yaml:"when"`
	Actions     []reactionActionFile `yaml:"actions"`
	Description string               `yaml:"description"`
	Annotations map[string]string    `yaml:"annotations"`
}

type matcherFile struct {
	Assertion string            `yaml:"assertion"`
	Target    string            `yaml:"target"`
	Subject   subjectFile       `yaml:"subject"`
	Scope     string            `yaml:"scope"`
	Source    string            `yaml:"source"`
	Meta      map[string]string `yaml:"meta"`
}

type reactionActionFile struct {
	Kind                corereaction.ActionKind `yaml:"kind"`
	Skill               skillRefFile            `yaml:"skill"`
	Reference           referenceActionFile     `yaml:"reference"`
	OperationSet        string                  `yaml:"operation_set"`
	Datasource          datasourceRefFile       `yaml:"datasource"`
	ContextProvider     contextProviderRefFile  `yaml:"context_provider"`
	Workflow            workflowActionFile      `yaml:"workflow"`
	Operation           operationActionFile     `yaml:"operation"`
	Command             commandInvocationFile   `yaml:"command"`
	RequireApproval     bool                    `yaml:"require_approval"`
	IdempotencyFragment string                  `yaml:"idempotency_fragment"`
	Metadata            map[string]string       `yaml:"metadata"`
}

type skillRefFile struct {
	Name coreskill.Name `yaml:"name"`
}

type datasourceRefFile struct {
	Name coredatasource.Name `yaml:"name"`
}

type contextProviderRefFile struct {
	Name corecontext.ProviderName `yaml:"name"`
}

type referenceActionFile struct {
	Skill skillRefFile `yaml:"skill"`
	Path  string       `yaml:"path"`
}

type workflowActionFile struct {
	Name  coreworkflow.Name `yaml:"name"`
	Input operation.Value   `yaml:"input"`
}

type operationActionFile struct {
	Operation operationRefFile `yaml:"operation"`
	Input     operation.Value  `yaml:"input"`
}

type operationRefFile struct {
	Name    operation.Name    `yaml:"name"`
	Version operation.Version `yaml:"version"`
}

type commandInvocationFile struct {
	Path  command.Path    `yaml:"path"`
	Args  []string        `yaml:"args"`
	Input operation.Value `yaml:"input"`
}

// New resolves local coder configuration and returns the product wrapper.
func New(_ context.Context, cfg Config) (*App, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absRoot = filepath.Clean(absRoot)
	resolved, err := ResolveConfig(cfg)
	if err != nil {
		return nil, err
	}
	editor := cfg.Editor
	if editor == nil {
		editor = launch.OpenEditor
	}
	return &App{root: absRoot, config: resolved, editor: editor}, nil
}

// Command returns the configured coder CLI command.
func (a *App) Command() *cobra.Command {
	if a == nil {
		a = &App{}
	}
	cmd := coder.NewCommandWithOptions(coder.CommandOptions{
		Workspace:     cloneWorkspaceConfig(a.config.Workspace),
		Bundles:       coderConfigBundles(a.config),
		AppRunCommand: a.newAppRunCommand(),
	})
	cmd.PersistentFlags().String("config", a.config.Path, "coder config file path")
	cmd.AddCommand(a.newConfigCommand())
	return cmd
}

func (a *App) newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect coder configuration",
	}
	cmd.AddCommand(a.newConfigShowCommand())
	cmd.AddCommand(a.newConfigEditCommand())
	return cmd
}

func (a *App) newConfigShowCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show resolved coder configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return renderConfig(cmd.OutOrStdout(), a.config, output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "yaml", "Output format: yaml|json")
	return cmd
}

func (a *App) newConfigEditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edit local coder configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := strings.TrimSpace(a.config.Path)
			if path == "" {
				path = filepath.Join(a.root, configNameYAML)
			}
			if err := ensureCoderConfigFile(path); err != nil {
				return err
			}
			editor := a.editor
			if editor == nil {
				editor = launch.OpenEditor
			}
			return editor(cmd.Context(), path, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

// ResolveConfig loads and merges coder defaults, discovered config, explicit
// config, and programmatic overrides.
func ResolveConfig(cfg Config) (ResolvedConfig, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ResolvedConfig{}, err
	}
	absRoot = filepath.Clean(absRoot)
	path := strings.TrimSpace(cfg.CoderConfigPath)
	explicit := path != ""
	if explicit {
		if !filepath.IsAbs(path) {
			path = filepath.Join(absRoot, path)
		}
		path = filepath.Clean(path)
	} else {
		path, err = discoverConfig(absRoot)
		if err != nil {
			return ResolvedConfig{}, err
		}
	}
	resolved := ResolvedConfig{Path: path, Explicit: explicit}
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			if explicit && os.IsNotExist(err) {
				resolved.Workspace = mergeWorkspace(resolved.Workspace, cfg.Workspace)
				return resolved, nil
			}
			return ResolvedConfig{}, err
		}
		file, err := decodeConfigFile(path)
		if err != nil {
			return ResolvedConfig{}, err
		}
		resolved.Workspace = file.Workspace
		resolved.Imports = file.Imports
		resolved.Observations = file.Observations
		resolved.Reactions = file.Reactions
	}
	resolved.Workspace = mergeWorkspace(resolved.Workspace, cfg.Workspace)
	return resolved, nil
}

func ensureCoderConfigFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("coder config edit: config path is empty")
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("version: 1\n"), 0o600)
}

func discoverConfig(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	if stat, err := os.Stat(dir); err == nil && !stat.IsDir() {
		dir = filepath.Dir(dir)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for {
		for _, name := range []string{configNameYAML, configNameYML} {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			} else if err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func decodeConfigFile(path string) (ResolvedConfig, error) {
	path = filepath.Clean(path)
	configDir := filepath.Dir(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return ResolvedConfig{}, err
	}
	var doc fileConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return ResolvedConfig{}, fmt.Errorf("coder config %s: %w", path, err)
	}
	if doc.Version != 0 && doc.Version != 1 {
		return ResolvedConfig{}, fmt.Errorf("coder config %s: unsupported version %d", path, doc.Version)
	}
	roots := make([]distribution.WorkspaceRoot, 0, len(doc.Workspace.Roots))
	for _, raw := range doc.Workspace.Roots {
		root, err := workspaceRootFromFile(configDir, raw)
		if err != nil {
			return ResolvedConfig{}, fmt.Errorf("coder config %s: %w", path, err)
		}
		roots = append(roots, root)
	}
	observations, err := observationConfigFromFile(doc.Observations)
	if err != nil {
		return ResolvedConfig{}, fmt.Errorf("coder config %s: %w", path, err)
	}
	reactions := make([]corereaction.Rule, 0, len(doc.Reactions))
	for i, raw := range doc.Reactions {
		rule := raw.Rule()
		if err := rule.Validate(); err != nil {
			return ResolvedConfig{}, fmt.Errorf("coder config %s: validate reactions[%d]: %w", path, i, err)
		}
		reactions = append(reactions, rule)
	}
	return ResolvedConfig{
		Path: path,
		Workspace: distribution.WorkspaceConfig{
			Roots:    roots,
			EnvFiles: resolveConfigPaths(configDir, doc.Workspace.EnvFiles),
		},
		Imports:      doc.Imports,
		Observations: observations,
		Reactions:    reactions,
	}, nil
}

func observationConfigFromFile(raw observationFile) (ObservationConfig, error) {
	out := ObservationConfig{
		Observers:         make([]coreevidence.ObserverSpec, 0, len(raw.Observers)),
		AssertionDerivers: make([]coreevidence.AssertionDeriverSpec, 0, len(raw.AssertionDerivers)),
	}
	for i, observer := range raw.Observers {
		spec := observer.Spec()
		if strings.TrimSpace(spec.Name) == "" {
			return ObservationConfig{}, fmt.Errorf("observations.observers[%d].name is empty", i)
		}
		out.Observers = append(out.Observers, spec)
	}
	for i, deriver := range raw.AssertionDerivers {
		spec := deriver.Spec()
		if strings.TrimSpace(spec.Name) == "" {
			return ObservationConfig{}, fmt.Errorf("observations.assertion_derivers[%d].name is empty", i)
		}
		out.AssertionDerivers = append(out.AssertionDerivers, spec)
	}
	return out, nil
}

func (f observerFile) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            strings.TrimSpace(f.Name),
		Description:     strings.TrimSpace(f.Description),
		Environment:     f.Environment.Ref(),
		Phase:           f.Phase,
		ObservableKinds: trimStrings(f.ObservableKinds),
		Dynamic:         f.Dynamic,
		Disabled:        f.Disabled,
		Annotations:     cloneStringMap(f.Annotations),
	}
}

func (f environmentRefFile) Ref() coreevidence.Ref {
	return coreevidence.Ref{Name: f.Name}
}

func (f assertionDeriverFile) Spec() coreevidence.AssertionDeriverSpec {
	assertions := make([]coreevidence.AssertionTemplate, 0, len(f.Assertions))
	for _, assertion := range f.Assertions {
		assertions = append(assertions, assertion.Template())
	}
	return coreevidence.AssertionDeriverSpec{
		Name:             strings.TrimSpace(f.Name),
		Description:      strings.TrimSpace(f.Description),
		ObservationKinds: trimStrings(f.ObservationKinds),
		Assertions:       assertions,
		Annotations:      cloneStringMap(f.Annotations),
	}
}

func (f assertionTemplateFile) Template() coreevidence.AssertionTemplate {
	return coreevidence.AssertionTemplate{
		Kind:    strings.TrimSpace(f.Kind),
		Target:  strings.TrimSpace(f.Target),
		Subject: f.Subject.Subject(),
		Scope:   strings.TrimSpace(f.Scope),
		Source:  strings.TrimSpace(f.Source),
	}
}

func (f reactionFile) Rule() corereaction.Rule {
	actions := make([]corereaction.Action, 0, len(f.Actions))
	for _, action := range f.Actions {
		actions = append(actions, action.Action())
	}
	return corereaction.Rule{
		Name:        strings.TrimSpace(f.Name),
		Mode:        f.Mode,
		When:        f.When.Matcher(),
		Actions:     actions,
		Description: strings.TrimSpace(f.Description),
		Annotations: cloneStringMap(f.Annotations),
	}
}

func (f matcherFile) Matcher() corereaction.Matcher {
	return corereaction.Matcher{
		Assertion: strings.TrimSpace(f.Assertion),
		Target:    strings.TrimSpace(f.Target),
		Subject:   f.Subject.Subject(),
		Scope:     strings.TrimSpace(f.Scope),
		Source:    strings.TrimSpace(f.Source),
		Meta:      cloneStringMap(f.Meta),
	}
}

func (f subjectFile) Subject() coreevidence.Subject {
	return coreevidence.Subject{
		Kind: f.Kind,
		Name: strings.TrimSpace(f.Name),
		ID:   strings.TrimSpace(f.ID),
	}
}

func (f reactionActionFile) Action() corereaction.Action {
	return corereaction.Action{
		Kind:                f.Kind,
		Skill:               f.Skill.Ref(),
		Reference:           f.Reference.Action(),
		OperationSet:        strings.TrimSpace(f.OperationSet),
		Datasource:          f.Datasource.Ref(),
		ContextProvider:     f.ContextProvider.Ref(),
		Workflow:            f.Workflow.Action(),
		Operation:           f.Operation.Action(),
		Command:             f.Command.Invocation(),
		RequireApproval:     f.RequireApproval,
		IdempotencyFragment: strings.TrimSpace(f.IdempotencyFragment),
		Metadata:            cloneStringMap(f.Metadata),
	}
}

func (f skillRefFile) Ref() coreskill.Ref {
	return coreskill.Ref{Name: f.Name}
}

func (f datasourceRefFile) Ref() coredatasource.Ref {
	return coredatasource.Ref{Name: f.Name}
}

func (f contextProviderRefFile) Ref() corecontext.ProviderRef {
	return corecontext.ProviderRef{Name: f.Name}
}

func (f referenceActionFile) Action() corereaction.ReferenceAction {
	return corereaction.ReferenceAction{
		Skill: f.Skill.Ref(),
		Path:  strings.TrimSpace(f.Path),
	}
}

func (f workflowActionFile) Action() corereaction.WorkflowAction {
	return corereaction.WorkflowAction{
		Name:  f.Name,
		Input: f.Input,
	}
}

func (f operationActionFile) Action() corereaction.OperationAction {
	return corereaction.OperationAction{
		Operation: f.Operation.Ref(),
		Input:     f.Input,
	}
}

func (f operationRefFile) Ref() operation.Ref {
	return operation.Ref{
		Name:    f.Name,
		Version: f.Version,
	}
}

func (f commandInvocationFile) Invocation() command.Invocation {
	return command.Invocation{
		Path:  append(command.Path(nil), f.Path...),
		Args:  append([]string(nil), f.Args...),
		Input: f.Input,
	}
}

func workspaceRootFromFile(configDir string, raw workspaceRootFile) (distribution.WorkspaceRoot, error) {
	path := strings.TrimSpace(raw.Path)
	if path == "" {
		return distribution.WorkspaceRoot{}, errors.New("workspace root path is required")
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		roots, err := distribution.ParseWorkspaceRoots([]string{path})
		if err != nil {
			return distribution.WorkspaceRoot{}, err
		}
		name = roots[0].Name
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, "@") {
		return distribution.WorkspaceRoot{}, fmt.Errorf("workspace root name %q is invalid", name)
	}
	access := strings.TrimSpace(raw.Access)
	if access == "" {
		access = "read_write"
	}
	return distribution.WorkspaceRoot{
		Name:     name,
		Path:     resolveConfigPath(configDir, path),
		Access:   access,
		Create:   raw.Create,
		EnvFiles: trimStrings(raw.EnvFiles),
	}, nil
}

func mergeWorkspace(base, override distribution.WorkspaceConfig) distribution.WorkspaceConfig {
	out := cloneWorkspaceConfig(base)
	out.Roots = append(out.Roots, cloneWorkspaceRoots(override.Roots)...)
	if strings.TrimSpace(override.ScratchRoot) != "" {
		out.ScratchRoot = strings.TrimSpace(override.ScratchRoot)
	}
	out.EnvFiles = append(out.EnvFiles, override.EnvFiles...)
	return out
}

func cloneWorkspaceConfig(cfg distribution.WorkspaceConfig) distribution.WorkspaceConfig {
	out := distribution.WorkspaceConfig{
		Roots:       cloneWorkspaceRoots(cfg.Roots),
		ScratchRoot: strings.TrimSpace(cfg.ScratchRoot),
		EnvFiles:    append([]string(nil), cfg.EnvFiles...),
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneWorkspaceRoots(roots []distribution.WorkspaceRoot) []distribution.WorkspaceRoot {
	if len(roots) == 0 {
		return nil
	}
	out := make([]distribution.WorkspaceRoot, 0, len(roots))
	for _, root := range roots {
		root.EnvFiles = append([]string(nil), root.EnvFiles...)
		out = append(out, root)
	}
	return out
}

func resolveConfigPaths(configDir string, values []string) []string {
	values = trimStrings(values)
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, resolveConfigPath(configDir, value))
	}
	return out
}

func resolveConfigPath(configDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(configDir, value))
}

func renderConfig(out io.Writer, cfg ResolvedConfig, output string) error {
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "", "yaml":
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	case "json":
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(cfg)
	default:
		return fmt.Errorf("coder config show: unsupported output %q", output)
	}
}

func coderConfigBundles(cfg ResolvedConfig) []resource.ContributionBundle {
	if len(cfg.Observations.Observers) == 0 && len(cfg.Observations.AssertionDerivers) == 0 && len(cfg.Reactions) == 0 {
		return nil
	}
	path := strings.TrimSpace(cfg.Path)
	return []resource.ContributionBundle{{
		Source: resource.SourceRef{
			ID:       "coder:config",
			Scope:    resource.ScopeProject,
			Location: path,
		},
		Observers:         append([]coreevidence.ObserverSpec(nil), cfg.Observations.Observers...),
		AssertionDerivers: append([]coreevidence.AssertionDeriverSpec(nil), cfg.Observations.AssertionDerivers...),
		Reactions:         append([]corereaction.Rule(nil), cfg.Reactions...),
	}}
}

func trimStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
