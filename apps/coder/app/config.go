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

	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
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
	Path      string                       `json:"path,omitempty" yaml:"path,omitempty"`
	Explicit  bool                         `json:"explicit" yaml:"explicit"`
	Workspace distribution.WorkspaceConfig `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	Imports   ImportConfig                 `json:"imports,omitempty" yaml:"imports,omitempty"`
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
	Version   int           `yaml:"version"`
	Workspace workspaceFile `yaml:"workspace"`
	Imports   ImportConfig  `yaml:"imports"`
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
	return ResolvedConfig{
		Path: path,
		Workspace: distribution.WorkspaceConfig{
			Roots:    roots,
			EnvFiles: resolveConfigPaths(configDir, doc.Workspace.EnvFiles),
		},
		Imports: doc.Imports,
	}, nil
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
