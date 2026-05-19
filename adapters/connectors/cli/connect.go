// Package cli adapts connector authentication flows into Cobra commands.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codewandler/connectors/credential"
	connectorsdefinition "github.com/codewandler/connectors/definition"
	"github.com/codewandler/connectors/integrate"
	connectorsruntime "github.com/codewandler/connectors/runtime"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/spf13/cobra"
)

// PluginRegistry supplies plugins whose connector providers should be exposed
// by the CLI command.
type PluginRegistry func(context.Context) ([]pluginhost.Plugin, error)

// Options configures connector CLI execution.
type Options struct {
	ConnectorsPath string
	Auth           string
	Groups         string
	Instance       string
	Fields         []string
	Info           bool
	In             io.Reader
	Out            io.Writer
}

// NewCommand builds the native connector auth command.
func NewCommand(registry PluginRegistry) *cobra.Command {
	var opts Options
	cmd := &cobra.Command{
		Use:   "connect [provider]",
		Short: "Manage connector auth",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.In = cmd.InOrStdin()
			opts.Out = cmd.OutOrStdout()
			if len(args) == 0 {
				return RunStatus(cmd.Context(), opts, registry)
			}
			return RunProvider(cmd.Context(), args[0], opts, registry)
		},
	}
	cmd.Flags().StringVar(&opts.ConnectorsPath, "connectors-path", "~/.connectors", "connector credential store path")
	cmd.Flags().StringVar(&opts.Auth, "auth", "", "authentication method kind to use")
	cmd.Flags().StringArrayVarP(&opts.Fields, "field", "f", nil, "setup/auth field value (key=value, repeatable)")
	cmd.Flags().StringVar(&opts.Groups, "groups", "", "operation groups to enable (comma-separated or all)")
	cmd.Flags().StringVar(&opts.Instance, "instance", "", "instance ID to create/update")
	cmd.Flags().BoolVar(&opts.Info, "info", false, "print available auth methods and fields, then exit")
	return cmd
}

// RunStatus prints stored connector instances.
func RunStatus(ctx context.Context, opts Options, _ PluginRegistry) error {
	basePath, err := resolveConnectorsPath(opts.ConnectorsPath)
	if err != nil {
		return err
	}
	instances, err := credential.NewInstanceStore(filepath.Join(basePath, "instances")).List(ctx)
	if err != nil {
		return err
	}
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Connector == instances[j].Connector {
			return instances[i].ID < instances[j].ID
		}
		return instances[i].Connector < instances[j].Connector
	})
	out := writerOr(opts.Out, os.Stdout)
	if len(instances) == 0 {
		_, _ = fmt.Fprintln(out, "No connection instances.")
		_, _ = fmt.Fprintln(out, "Run coder connect <provider> to connect one.")
		return nil
	}
	credStore := credential.NewFileStore(filepath.Join(basePath, "credentials"))
	_, _ = fmt.Fprintf(out, "%-16s %-24s %-12s %-10s %-20s %s\n", "PROVIDER", "INSTANCE", "AUTH", "HEALTH", "UPDATED", "SOURCE")
	_, _ = fmt.Fprintf(out, "%-16s %-24s %-12s %-10s %-20s %s\n", "────────", "────────", "────", "──────", "───────", "──────")
	for _, inst := range instances {
		source := inst.Source
		if source == "" {
			source = "manual"
		}
		updated := "-"
		if !inst.UpdatedAt.IsZero() {
			updated = inst.UpdatedAt.Local().Format("2006-01-02 15:04")
		}
		_, _ = fmt.Fprintf(out, "%-16s %-24s %-12s %-10s %-20s %s\n",
			inst.Connector, inst.ID, emptyDash(inst.AuthMethod), credentialHealth(ctx, credStore, inst.ID), updated, source)
	}
	return nil
}

// RunProvider starts setup for a connector provider or prints provider info.
func RunProvider(ctx context.Context, provider string, opts Options, registry PluginRegistry) error {
	engine, providers, err := newConnectEngine(ctx, opts.ConnectorsPath, registry)
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close() }()
	def, ok := engine.Definition(provider)
	if !ok {
		return fmt.Errorf("unknown connector provider %q (available: %s)", provider, strings.Join(providers, ", "))
	}
	out := writerOr(opts.Out, os.Stdout)
	if opts.Info {
		printConnectInfo(out, def)
		return nil
	}
	handler, err := newConnectHandler(ctx, engine, connectHandlerConfig{
		in:        readerOr(opts.In, os.Stdin),
		out:       out,
		connector: provider,
		auth:      opts.Auth,
		groups:    opts.Groups,
		instance:  opts.Instance,
		fields:    opts.Fields,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\n  %s Connector Setup\n", def.Info.DisplayName)
	_, _ = fmt.Fprintf(out, "  %s\n\n", strings.Repeat("─", len(def.Info.DisplayName)+17))
	result, err := engine.ConnectInteractive(ctx, provider, handler)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\n  ✓ Connected as %s\n", result.InstanceID)
	_, _ = fmt.Fprintf(out, "  Operations: %d  |  Entities: %d\n\n", result.Operations, result.Entities)
	return nil
}

func newConnectEngine(ctx context.Context, basePath string, registry PluginRegistry) (*connectorsruntime.Engine, []string, error) {
	providers, err := registeredConnectorProviderNames(ctx, registry)
	if err != nil {
		return nil, nil, err
	}
	if len(providers) == 0 {
		return nil, nil, fmt.Errorf("connect: no connector providers registered")
	}
	resolvedPath, err := resolveConnectorsPath(basePath)
	if err != nil {
		return nil, nil, err
	}
	engine, err := integrate.Engine(
		integrate.WithBasePath(resolvedPath),
		integrate.WithAllowList(providers...),
	)
	if err != nil {
		return nil, nil, err
	}
	return engine, providers, nil
}

func registeredConnectorProviderNames(ctx context.Context, registry PluginRegistry) ([]string, error) {
	if registry == nil {
		return nil, nil
	}
	plugins, err := registry(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, plugin := range plugins {
		contributor, ok := plugin.(pluginhost.ConnectorProviderContributor)
		if !ok {
			continue
		}
		manifest := plugin.Manifest()
		providers, err := contributor.ConnectorProviders(ctx, pluginhost.Context{Ref: resource.PluginRef{Name: manifest.Name}})
		if err != nil {
			return nil, err
		}
		for _, provider := range providers {
			name := strings.TrimSpace(provider.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func resolveConnectorsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "~/.connectors"
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path, nil
}

func credentialHealth(ctx context.Context, store credential.Store, instanceID string) string {
	creds, err := store.Load(ctx, instanceID)
	if err != nil {
		return "unknown"
	}
	if creds.Auth.ExpiresAt == "" {
		return "ok"
	}
	expiry, err := time.Parse(time.RFC3339, creds.Auth.ExpiresAt)
	if err != nil {
		return "unknown"
	}
	if time.Now().After(expiry) {
		return "expired"
	}
	return "ok"
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

type connectHandlerConfig struct {
	in        io.Reader
	out       io.Writer
	connector string
	auth      string
	groups    string
	instance  string
	fields    []string
}

type connectHandler struct {
	engine    *connectorsruntime.Engine
	reader    *bufio.Reader
	out       io.Writer
	connector string
	auth      string
	groups    string
	instance  string
	explicit  map[string]string
	previous  map[string]string
}

func newConnectHandler(ctx context.Context, engine *connectorsruntime.Engine, cfg connectHandlerConfig) (*connectHandler, error) {
	explicit := map[string]string{}
	for _, raw := range cfg.fields {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid field %q (expected key=value)", raw)
		}
		explicit[key] = value
	}
	previousInstanceID := cfg.connector
	if cfg.instance != "" {
		previousInstanceID = cfg.instance
	}
	return &connectHandler{
		engine:    engine,
		reader:    bufio.NewReader(cfg.in),
		out:       cfg.out,
		connector: cfg.connector,
		auth:      cfg.auth,
		groups:    cfg.groups,
		instance:  cfg.instance,
		explicit:  explicit,
		previous:  previousConnectFields(ctx, engine, previousInstanceID),
	}, nil
}

func (h *connectHandler) ResolveFields(ctx context.Context, _ connectorsruntime.InteractionContext, fields []connectorsdefinition.SetupFieldDef) (map[string]string, error) {
	resolved := map[string]string{}
	for _, field := range fields {
		if field.Name == "_instance_id" {
			value := h.instance
			if value == "" {
				value = h.resolvePrompt(field)
			}
			if value == "" {
				value = h.connector
			}
			if value != h.connector && h.instance == "" {
				h.previous = previousConnectFields(ctx, h.engine, value)
			}
			resolved[field.Name] = value
			continue
		}
		if value, ok := h.explicit[field.Name]; ok {
			_, _ = fmt.Fprintf(h.out, "  %s: (from --field)\n", field.Label)
			resolved[field.Name] = value
			continue
		}
		if value := h.previous[field.Name]; value != "" {
			_, _ = fmt.Fprintf(h.out, "  %s: (from previous credentials)\n", field.Label)
			resolved[field.Name] = value
			continue
		}
		fromEnv := ""
		for _, envKey := range field.EnvVar {
			if value := os.Getenv(envKey); value != "" {
				_, _ = fmt.Fprintf(h.out, "  %s: (from %s)\n", field.Label, envKey)
				fromEnv = value
				break
			}
		}
		if fromEnv != "" {
			resolved[field.Name] = fromEnv
			continue
		}
		value := h.resolvePrompt(field)
		if field.Required && value == "" {
			return nil, fmt.Errorf("field %q is required", field.Name)
		}
		resolved[field.Name] = value
	}
	return resolved, nil
}

func (h *connectHandler) SelectOne(_ context.Context, _ connectorsruntime.InteractionContext, prompt string, options []connectorsruntime.SelectOption) (int, error) {
	if h.auth != "" {
		for i, option := range options {
			if strings.EqualFold(option.Value, h.auth) || strings.EqualFold(option.Label, h.auth) {
				_, _ = fmt.Fprintf(h.out, "  Auth: %s\n", option.Label)
				return i, nil
			}
		}
		return 0, fmt.Errorf("auth method %q not found", h.auth)
	}
	if len(options) == 1 {
		_, _ = fmt.Fprintf(h.out, "  Auth: %s\n", options[0].Label)
		return 0, nil
	}
	_, _ = fmt.Fprintf(h.out, "\n  %s:\n", prompt)
	for i, option := range options {
		_, _ = fmt.Fprintf(h.out, "  [%d] %s\n", i+1, option.Label)
	}
	_, _ = fmt.Fprint(h.out, "  > ")
	input, _ := h.reader.ReadString('\n')
	input = strings.TrimSpace(input)
	idx := 0
	if len(input) >= 1 {
		idx = int(input[0]-'0') - 1
	}
	if idx < 0 || idx >= len(options) {
		idx = 0
	}
	return idx, nil
}

func (h *connectHandler) SelectMany(_ context.Context, _ connectorsruntime.InteractionContext, prompt string, options []connectorsruntime.SelectOption) ([]int, error) {
	if len(options) == 0 {
		return nil, nil
	}
	if h.groups != "" {
		if strings.EqualFold(h.groups, "all") {
			return allConnectOptionIndices(options), nil
		}
		byValue := map[string]int{}
		for i, option := range options {
			byValue[option.Value] = i
		}
		var selected []int
		for _, part := range strings.Split(h.groups, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			idx, ok := byValue[value]
			if !ok {
				return nil, fmt.Errorf("operation group %q not found", value)
			}
			selected = append(selected, idx)
		}
		if len(selected) == 0 {
			return allConnectOptionIndices(options), nil
		}
		return selected, nil
	}
	_, _ = fmt.Fprintf(h.out, "\n  %s\n", prompt)
	for i, option := range options {
		desc := option.Description
		if desc != "" {
			desc = " (" + desc + ")"
		}
		_, _ = fmt.Fprintf(h.out, "  [%d] %-25s%s\n", i+1, option.Label, desc)
	}
	_, _ = fmt.Fprint(h.out, "  Select (comma-separated, or 'all'): ")
	input, _ := h.reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" || strings.EqualFold(input, "all") {
		return allConnectOptionIndices(options), nil
	}
	var selected []int
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(part)
		if len(part) >= 1 {
			idx := int(part[0]-'0') - 1
			if idx >= 0 && idx < len(options) {
				selected = append(selected, idx)
			}
		}
	}
	if len(selected) == 0 {
		return allConnectOptionIndices(options), nil
	}
	return selected, nil
}

func (h *connectHandler) OpenURL(context.Context, connectorsruntime.InteractionContext, string, string) bool {
	return false
}

func (h *connectHandler) Status(_ context.Context, _ connectorsruntime.InteractionContext, message string) {
	if message != "" {
		_, _ = fmt.Fprintf(h.out, "  %s\n", message)
	}
}

func (h *connectHandler) resolvePrompt(field connectorsdefinition.SetupFieldDef) string {
	if field.Help != "" {
		_, _ = fmt.Fprintf(h.out, "  (%s)\n", field.Help)
	}
	prompt := fmt.Sprintf("  %s", field.Label)
	if field.Default != "" {
		prompt += fmt.Sprintf(" [%s]", field.Default)
	}
	prompt += ": "
	_, _ = fmt.Fprint(h.out, prompt)
	input, _ := h.reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		input = field.Default
	}
	return input
}

func previousConnectFields(ctx context.Context, engine *connectorsruntime.Engine, instanceID string) map[string]string {
	fields := map[string]string{}
	if inst, err := engine.Instances.Load(ctx, instanceID); err == nil {
		for k, v := range inst.Fields {
			fields[k] = v
		}
	}
	if creds, err := engine.Credentials.Load(ctx, instanceID); err == nil {
		for k, v := range creds.Fields {
			fields[k] = v
		}
	}
	return fields
}

func allConnectOptionIndices(options []connectorsruntime.SelectOption) []int {
	indices := make([]int, len(options))
	for i := range options {
		indices[i] = i
	}
	return indices
}

func printConnectInfo(out io.Writer, def *connectorsdefinition.Definition) {
	_, _ = fmt.Fprintf(out, "%s (%s)\n", def.Info.DisplayName, def.Name)
	_, _ = fmt.Fprintln(out, "Auth methods:")
	for _, method := range def.Auth.Methods {
		_, _ = fmt.Fprintf(out, "- %s (%s)\n", method.Kind, method.Label)
		for _, field := range append(def.Auth.Fields, method.Fields...) {
			required := "optional"
			if field.Required {
				required = "required"
			}
			env := ""
			if len(field.EnvVar) > 0 {
				env = fmt.Sprintf(" env=%s", strings.Join(field.EnvVar, ","))
			}
			_, _ = fmt.Fprintf(out, "  --field %s=<%s> (%s%s)\n", field.Name, field.Type, required, env)
		}
	}
	groups := def.OperationGroups()
	if len(groups) > 0 {
		_, _ = fmt.Fprintf(out, "Groups: %s\n", strings.Join(groups, ", "))
	}
}

func readerOr(value io.Reader, fallback io.Reader) io.Reader {
	if value != nil {
		return value
	}
	return fallback
}

func writerOr(value io.Writer, fallback io.Writer) io.Writer {
	if value != nil {
		return value
	}
	return fallback
}
