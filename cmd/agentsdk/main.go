package main

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
	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	distremote "github.com/fluxplane/agentruntime/adapters/distribution/remote"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/apps/launch"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/openaiplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "agentsdk",
		Short:         "Run agentsdk tools",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(coder.NewCommand())
	cmd.AddCommand(newRunCommand())
	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(newRemoteCommand())
	cmd.AddCommand(newConnectCommand())
	cmd.AddCommand(newDiscoverCommand())
	return cmd
}

type runOptions struct {
	session      string
	conversation string
	provider     string
	model        string
	input        string
	debug        bool
	usage        bool
}

type serveOptions struct {
	debug    bool
	authPath string
}

type runLoader func(context.Context, string) (distribution.Loaded, error)

func newRunCommand() *cobra.Command {
	return newRunCommandWithLoader(distlocal.Load)
}

func newRunCommandWithLoader(loader runLoader) *cobra.Command {
	var opts runOptions
	cmd := &cobra.Command{
		Use:   "run [path]",
		Short: "Run a local app distribution",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLocalDistribution(cmd.Context(), loader, opts, args[0], cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "configured session name to open")
	cmd.Flags().StringVar(&opts.conversation, "conversation", "", "conversation id")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "model provider")
	cmd.Flags().StringVar(&opts.model, "model", "", "model name or provider/model")
	cmd.Flags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	return cmd
}

func runLocalDistribution(ctx context.Context, loader runLoader, opts runOptions, path string, in io.Reader, out, errOut io.Writer) error {
	if loader == nil {
		loader = distlocal.Load
	}
	loaded, err := loader(ctx, path)
	if err != nil {
		return err
	}
	if loaded.Distribution.Runtime == nil {
		return fmt.Errorf("run: distribution %q has no runtime", loaded.Distribution.Spec.Name)
	}
	if strings.TrimSpace(opts.session) == "" && loaded.Distribution.Spec.DefaultSession.Name == "" {
		return fmt.Errorf("run: distribution %q has no default session", loaded.Distribution.Spec.Name)
	}
	loaded = launch.AttachLocalRuntime(loaded)
	return distcli.Run(ctx, loaded.Distribution, distcli.RunOptions{
		Session:      opts.session,
		Conversation: opts.conversation,
		Provider:     opts.provider,
		Model:        opts.model,
		Input:        opts.input,
		Debug:        opts.debug,
		Usage:        opts.usage,
		Prompt:       loaded.Distribution.Spec.Name,
		In:           in,
		Out:          out,
		Err:          errOut,
	})
}

func newServeCommand() *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve [app-dir]",
		Short: "Run an app daemon",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return launch.Serve(cmd.Context(), launch.Options{
				AppDir:   args[0],
				Debug:    opts.debug,
				AuthPath: opts.authPath,
			})
		},
	}
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print daemon startup details")
	cmd.Flags().StringVar(&opts.authPath, "connectors-path", "~/.connectors", "connector credential store path")
	return cmd
}

type connectOptions struct {
	connectorsPath string
	auth           string
	groups         string
	instance       string
	fields         []string
	info           bool
}

const (
	defaultRemoteSession      = "slack-main"
	defaultRemoteConversation = "agentsdk-remote"
	defaultRemoteSocket       = "agentsdk-local.sock"
)

type remoteOptions struct {
	appDir          string
	url             string
	socket          string
	local           bool
	session         string
	sessionExplicit bool
	conversation    string
	input           string
	debug           bool
	usage           bool
}

func newRemoteCommand() *cobra.Command {
	var opts remoteOptions
	opts.session = defaultRemoteSession
	opts.conversation = defaultRemoteConversation
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Connect to a running agentsdk daemon session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.sessionExplicit = cmd.Flags().Changed("session")
			return runRemote(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.appDir, "app", "", "app directory to read daemon listener config from")
	cmd.Flags().StringVar(&opts.url, "url", "", "HTTP/SSE daemon listener URL")
	cmd.Flags().StringVar(&opts.socket, "socket", "", "Unix socket path or socket name")
	cmd.Flags().BoolVar(&opts.local, "local", false, "connect to the default local Unix socket")
	cmd.Flags().StringVar(&opts.session, "session", defaultRemoteSession, "configured session name to open")
	cmd.Flags().StringVar(&opts.conversation, "conversation", defaultRemoteConversation, "remote conversation id")
	cmd.Flags().StringVar(&opts.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	return cmd
}

func runRemote(ctx context.Context, opts remoteOptions) error {
	events, err := terminalEventRegistry()
	if err != nil {
		return err
	}
	return distremote.Run(ctx, distremote.Options{
		AppDir:              opts.appDir,
		URL:                 opts.url,
		Socket:              opts.socket,
		Local:               opts.local,
		Session:             opts.session,
		SessionExplicit:     opts.sessionExplicit,
		Conversation:        opts.conversation,
		Input:               opts.input,
		Debug:               opts.debug,
		Usage:               opts.usage,
		DefaultSession:      defaultRemoteSession,
		DefaultConversation: defaultRemoteConversation,
		DefaultSocket:       defaultRemoteSocket,
		Events:              events,
		In:                  os.Stdin,
		Out:                 os.Stdout,
		Err:                 os.Stderr,
	})
}

func newConnectCommand() *cobra.Command {
	var opts connectOptions
	cmd := &cobra.Command{
		Use:   "connect [provider]",
		Short: "Manage connector auth",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runConnectStatus(cmd.Context(), opts, cmd.OutOrStdout())
			}
			return runConnectProvider(cmd.Context(), opts, args[0], cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "~/.connectors", "connector credential store path")
	cmd.Flags().StringVar(&opts.auth, "auth", "", "authentication method kind to use")
	cmd.Flags().StringArrayVarP(&opts.fields, "field", "f", nil, "setup/auth field value (key=value, repeatable)")
	cmd.Flags().StringVar(&opts.groups, "groups", "", "operation groups to enable (comma-separated or all)")
	cmd.Flags().StringVar(&opts.instance, "instance", "", "instance ID to create/update")
	cmd.Flags().BoolVar(&opts.info, "info", false, "print available auth methods and fields, then exit")
	return cmd
}

func runConnectStatus(ctx context.Context, opts connectOptions, out io.Writer) error {
	basePath, err := resolveConnectorsPath(opts.connectorsPath)
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
	if len(instances) == 0 {
		_, _ = fmt.Fprintln(out, "No connection instances.")
		_, _ = fmt.Fprintln(out, "Run agentsdk connect <provider> to connect one.")
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

func runConnectProvider(ctx context.Context, opts connectOptions, provider string, in io.Reader, out io.Writer) error {
	engine, providers, err := newConnectEngine(ctx, opts.connectorsPath)
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close() }()
	def, ok := engine.Definition(provider)
	if !ok {
		return fmt.Errorf("unknown connector provider %q (available: %s)", provider, strings.Join(providers, ", "))
	}
	if opts.info {
		printConnectInfo(out, def)
		return nil
	}
	handler, err := newAgentsDKConnectHandler(ctx, engine, connectHandlerConfig{
		in:        in,
		out:       out,
		connector: provider,
		auth:      opts.auth,
		groups:    opts.groups,
		instance:  opts.instance,
		fields:    opts.fields,
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

func newConnectEngine(ctx context.Context, basePath string) (*connectorsruntime.Engine, []string, error) {
	providers, err := registeredConnectorProviderNames(ctx)
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

func registeredConnectorProviderNames(ctx context.Context) ([]string, error) {
	plugins := []pluginhost.Plugin{
		openaiplugin.New(),
		slackplugin.New(nil),
		gitlabplugin.New(nil, nil),
		jiraplugin.New(nil, nil),
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

type agentsdkConnectHandler struct {
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

func newAgentsDKConnectHandler(ctx context.Context, engine *connectorsruntime.Engine, cfg connectHandlerConfig) (*agentsdkConnectHandler, error) {
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
	return &agentsdkConnectHandler{
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

func (h *agentsdkConnectHandler) ResolveFields(ctx context.Context, _ connectorsruntime.InteractionContext, fields []connectorsdefinition.SetupFieldDef) (map[string]string, error) {
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

func (h *agentsdkConnectHandler) SelectOne(_ context.Context, _ connectorsruntime.InteractionContext, prompt string, options []connectorsruntime.SelectOption) (int, error) {
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

func (h *agentsdkConnectHandler) SelectMany(_ context.Context, _ connectorsruntime.InteractionContext, prompt string, options []connectorsruntime.SelectOption) ([]int, error) {
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

func (h *agentsdkConnectHandler) OpenURL(context.Context, connectorsruntime.InteractionContext, string, string) bool {
	return false
}

func (h *agentsdkConnectHandler) Status(_ context.Context, _ connectorsruntime.InteractionContext, message string) {
	if message != "" {
		_, _ = fmt.Fprintf(h.out, "  %s\n", message)
	}
}

func (h *agentsdkConnectHandler) resolvePrompt(field connectorsdefinition.SetupFieldDef) string {
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

func terminalEventRegistry() (*coreevent.Registry, error) {
	registry, err := app.NewEventRegistry(app.EventRegistryConfig{EventTypes: eventcatalog.All()})
	if err != nil {
		return nil, err
	}
	return registry, nil
}
