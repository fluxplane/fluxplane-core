package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codewandler/connectors/credential"
	connectorsdefinition "github.com/codewandler/connectors/definition"
	"github.com/codewandler/connectors/integrate"
	connectorsruntime "github.com/codewandler/connectors/runtime"
	agentruntime "github.com/fluxplane/agentruntime"
	anthropicadapter "github.com/fluxplane/agentruntime/adapters/anthropic"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/adapters/browsercdp"
	cmdriskadapter "github.com/fluxplane/agentruntime/adapters/cmdrisk"
	codexadapter "github.com/fluxplane/agentruntime/adapters/codex"
	"github.com/fluxplane/agentruntime/adapters/connectauth"
	"github.com/fluxplane/agentruntime/adapters/httpcontrol"
	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	minimaxadapter "github.com/fluxplane/agentruntime/adapters/minimax"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	openaiadapter "github.com/fluxplane/agentruntime/adapters/openai"
	openrouteradapter "github.com/fluxplane/agentruntime/adapters/openrouter"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coreusage "github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/channelruntime"
	"github.com/fluxplane/agentruntime/orchestration/daemon"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/openaiplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
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
	cmd.AddCommand(newCoderCommand())
	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(newRemoteCommand())
	cmd.AddCommand(newConnectCommand())
	return cmd
}

type serveOptions struct {
	debug    bool
	authPath string
}

func newServeCommand() *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve [app-dir]",
		Short: "Run an app daemon",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts, args[0])
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

type remoteTarget struct {
	baseURL     string
	socket      string
	bearerToken string
	session     string
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

type coderOptions struct {
	provider string
	model    string
	debug    bool
	usage    bool
}

func newCoderCommand() *cobra.Command {
	var opts coderOptions
	cmd := &cobra.Command{
		Use:   "coder [prompt]",
		Short: "Run the first-party coding agent",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.TrimSpace(strings.Join(args, " "))
			if prompt == "" {
				return errors.New("prompt is empty")
			}
			return runCoder(cmd.Context(), opts, prompt)
		},
	}
	cmd.PersistentFlags().StringVar(&opts.provider, "provider", "openai", "model provider")
	cmd.PersistentFlags().StringVar(&opts.model, "model", coder.DefaultModel, "model name or provider/model")
	cmd.PersistentFlags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.PersistentFlags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.AddCommand(newCoderReplCommand(&opts))
	return cmd
}

func newCoderReplCommand(opts *coderOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "repl",
		Short: "Run coder in an interactive session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoderREPL(cmd.Context(), *opts)
		},
	}
}

func runServe(ctx context.Context, opts serveOptions, appDir string) error {
	configureServeLogging(opts.debug)
	cfgFile, err := appconfig.LoadDirFile(ctx, appDir)
	if err != nil {
		return err
	}
	if err := cfgFile.Validate(); err != nil {
		return err
	}
	root, err := filepath.Abs(appDir)
	if err != nil {
		return err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		return err
	}
	dispatcher := slackplugin.NewDispatcher()
	connectorEngine, connectorInstances, err := serveConnectorEngine(ctx, opts, cfgFile.Connectors)
	if err != nil {
		return err
	}
	if connectorEngine != nil {
		defer func() { _ = connectorEngine.Close() }()
	}
	slackPlugin := slackplugin.NewWithConnectors(dispatcher, connectorEngine, connectorInstancesForKind(connectorInstances, slackplugin.Name))
	gitlabPlugin := gitlabplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, gitlabplugin.Name))
	jiraPlugin := jiraplugin.New(connectorEngine, connectorInstancesForKind(connectorInstances, jiraplugin.Name))
	basePlugins := []pluginhost.Plugin{
		slackPlugin,
		gitlabPlugin,
		jiraPlugin,
		planexecplugin.New(),
		textplugin.New(),
		webplugin.New(hostSystem),
	}
	bundle := cfgFile.Bundle
	plugins := basePlugins
	if len(bundle.Datasources) > 0 {
		registry, err := serveDatasourceRegistry(ctx, bundle, basePlugins, root)
		if err != nil {
			return err
		}
		plugins = append(plugins, datasourceplugin.New(registry))
		if !bundleHasPlugin(bundle, datasourceplugin.Name) {
			bundle.Plugins = append(bundle.Plugins, resource.PluginRef{Name: datasourceplugin.Name})
		}
	}
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{bundle},
		Plugins: plugins,
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:   localSandbox{Root: root},
			ACL:       localACL{},
			AllowPure: true,
		})),
	})
	if err != nil {
		return err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModelResolver: serveModelResolver{debug: opts.debug},
		LLMStreamPolicy:  debugStreamPolicy(opts.debug),
		ToolProjection: agentruntime.ToolProjectionConfig{
			AllowSideEffects:      true,
			MaxRisk:               operation.RiskMedium,
			IncludeBareOperations: true,
		},
		Channel: channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "agentsdk",
				Name: "agentsdk",
			},
		},
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		return err
	}
	channels, err := serveChannels(ctx, cfgFile.Daemon.Channels, opts, dispatcher)
	if err != nil {
		return err
	}
	host, err := daemon.New(daemon.Config{
		Client:         service,
		SessionCatalog: composition.SessionCatalog,
		Channels:       channels,
	})
	if err != nil {
		return err
	}
	if err := startServeListeners(ctx, cfgFile.Daemon.Listeners, cfgFile.Daemon.Channels, service, host); err != nil {
		return err
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if opts.debug {
		_, _ = fmt.Fprintf(os.Stderr, "agentsdk serve loaded %s\n", cfgFile.Path)
	}
	if len(channels) == 0 {
		<-runCtx.Done()
		return nil
	}
	for _, ch := range channels {
		if ch != nil && ch.Name() != "" {
			_, _ = fmt.Fprintf(os.Stderr, "channel %s starting\n", ch.Name())
		}
	}
	if err := host.RunChannels(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

type serveModelResolver struct {
	debug bool
}

func (r serveModelResolver) ResolveModel(_ context.Context, spec agent.Spec) (llmagent.Model, error) {
	selection := resolveModelSelection(coderOptions{provider: "openai", model: spec.Inference.Model})
	return newCoderModel(selection, coderOptions{debug: r.debug})
}

func serveConnectorEngine(ctx context.Context, opts serveOptions, docs map[string]appconfig.ConnectorDoc) (*connectorsruntime.Engine, []connectorplugin.Instance, error) {
	if len(docs) == 0 {
		return nil, nil, nil
	}
	engine, providers, err := newConnectEngine(ctx, opts.authPath)
	if err != nil {
		return nil, nil, err
	}
	knownProviders := map[string]bool{}
	for _, provider := range providers {
		knownProviders[provider] = true
	}
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	instances := make([]connectorplugin.Instance, 0, len(names))
	for _, instanceID := range names {
		doc := docs[instanceID]
		kind := strings.TrimSpace(doc.Kind)
		if kind == "" {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connector instance %q kind is empty", instanceID)
		}
		if !knownProviders[kind] {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connector instance %q uses unknown provider %q (available: %s)", instanceID, kind, strings.Join(providers, ", "))
		}
		stored, err := engine.Instances.Load(ctx, instanceID)
		if err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: load connector instance %q: %w", instanceID, err)
		}
		if stored.Connector != kind {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connector instance %q has kind %q, want %q", instanceID, stored.Connector, kind)
		}
		if err := engine.ConnectInstance(ctx, instanceID); err != nil {
			_ = engine.Close()
			return nil, nil, fmt.Errorf("serve: connect %s connector instance %q: %w", kind, instanceID, err)
		}
		instances = append(instances, connectorplugin.Instance{ID: instanceID, Kind: kind})
	}
	return engine, instances, nil
}

func serveDatasourceRegistry(ctx context.Context, bundle resource.ContributionBundle, plugins []pluginhost.Plugin, root string) (*coredatasource.Registry, error) {
	host, err := pluginhost.New(plugins...)
	if err != nil {
		return nil, err
	}
	refs := make([]resource.PluginRef, 0, len(bundle.Plugins))
	for _, ref := range bundle.Plugins {
		if ref.Name != datasourceplugin.Name {
			refs = append(refs, ref)
		}
	}
	resolved, err := host.Resolve(ctx, refs...)
	if err != nil {
		return nil, err
	}
	var providers []coredatasource.Provider
	for _, contribution := range resolved.DatasourceProviders {
		providers = append(providers, contribution.Provider)
	}
	providers = append(providers, datasourceplugin.NewFilesystemProvider(os.DirFS(root)))
	return datasourceplugin.BuildRegistry(ctx, bundle.Datasources, providers)
}

func bundleHasPlugin(bundle resource.ContributionBundle, name string) bool {
	for _, ref := range bundle.Plugins {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func connectorInstancesForKind(instances []connectorplugin.Instance, kind string) []connectorplugin.Instance {
	var out []connectorplugin.Instance
	for _, instance := range instances {
		if instance.Kind == kind {
			out = append(out, instance)
		}
	}
	return out
}

func serveChannels(ctx context.Context, docs []appconfig.ChannelDoc, opts serveOptions, dispatcher *slackplugin.Dispatcher) ([]channelruntime.Channel, error) {
	var out []channelruntime.Channel
	store := connectauth.NewStore(opts.authPath)
	for _, doc := range docs {
		switch doc.Type {
		case "direct":
			continue
		case "slack":
			creds, err := store.LoadSlack(ctx, doc.Connector)
			if err != nil {
				return nil, err
			}
			sessionName := doc.Session
			if sessionName == "" {
				sessionName = doc.Name
			}
			ch, err := slackplugin.NewChannel(slackplugin.ChannelConfig{
				Name:       doc.Name,
				Session:    agentruntime.SessionRef{Name: agentruntime.SessionName(sessionName)},
				BotToken:   creds.BotToken,
				UserToken:  creds.UserToken,
				AppToken:   creds.AppToken,
				BotUserID:  creds.BotUserID,
				TeamID:     creds.TeamID,
				Debug:      opts.debug,
				Access:     slackAccess(doc.Access),
				Dispatcher: dispatcher,
			})
			if err != nil {
				return nil, err
			}
			out = append(out, ch)
		default:
			return nil, fmt.Errorf("serve: unsupported channel type %q", doc.Type)
		}
	}
	return out, nil
}

func configureServeLogging(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func slackAccess(doc appconfig.AccessDoc) slackplugin.AccessPolicy {
	return slackplugin.AccessPolicy{
		Mode:             doc.Mode,
		AllowUsers:       append([]string(nil), doc.AllowUsers...),
		DenyUsers:        append([]string(nil), doc.DenyUsers...),
		AllowChannels:    append([]string(nil), doc.AllowChannels...),
		DenyChannels:     append([]string(nil), doc.DenyChannels...),
		AllowKinds:       append([]string(nil), doc.AllowKinds...),
		DefaultTrust:     userTrust(doc.DefaultTrust),
		Operators:        append([]string(nil), doc.Operators...),
		InternalUsers:    append([]string(nil), doc.InternalUsers...),
		InternalChannels: append([]string(nil), doc.InternalChannels...),
		Sharing:          firstNonEmptyString(doc.Sharing, "strict"),
	}
}

func userTrust(raw string) user.TrustLevel {
	switch strings.TrimSpace(raw) {
	case "operator":
		return user.TrustOperator
	case "internal":
		return user.TrustInternal
	default:
		return user.TrustPublic
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func startServeListeners(ctx context.Context, listeners []appconfig.ListenerDoc, channels []appconfig.ChannelDoc, client agentruntime.ChannelClient, host *daemon.Host) error {
	needsDirect := map[string]bool{}
	for _, ch := range channels {
		if ch.Type == "direct" && ch.Listener != "" {
			needsDirect[ch.Listener] = true
		}
	}
	for _, listenerDoc := range listeners {
		if listenerDoc.Type != "http" {
			return fmt.Errorf("serve: unsupported listener type %q", listenerDoc.Type)
		}
		mux := http.NewServeMux()
		controlServer, err := httpcontrol.NewServer(httpcontrol.ServerConfig{Host: host})
		if err != nil {
			return err
		}
		mux.Handle("/control/", http.StripPrefix("/control", controlServer))
		if needsDirect[listenerDoc.Name] {
			channelServer, err := httpssechannel.NewServer(httpssechannel.ServerConfig{Client: client})
			if err != nil {
				return err
			}
			mux.Handle("/", channelServer)
		}
		ln, display, cleanup, err := listenServe(listenerDoc.Addr)
		if err != nil {
			return err
		}
		handler, err := serveListenerHandler(listenerDoc, mux)
		if err != nil {
			cleanup()
			return err
		}
		server := &http.Server{Handler: handler}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
			cleanup()
		}()
		go func() {
			if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				_, _ = fmt.Fprintf(os.Stderr, "listener %s failed: %v\n", listenerDoc.Name, err)
				cleanup()
			}
		}()
		_, _ = fmt.Fprintf(os.Stderr, "listener %s on %s\n", listenerDoc.Name, display)
	}
	return nil
}

func serveListenerHandler(listener appconfig.ListenerDoc, next http.Handler) (http.Handler, error) {
	mode := strings.ToLower(strings.TrimSpace(authString(listener.Auth, "mode")))
	if mode == "" {
		if serveAddrIsTCP(listener.Addr) {
			return nil, fmt.Errorf("serve: listener %q uses TCP addr %q and requires auth", listener.Name, listener.Addr)
		}
		return next, nil
	}
	switch mode {
	case "local_socket":
		if serveAddrIsTCP(listener.Addr) {
			return nil, fmt.Errorf("serve: listener %q auth mode local_socket requires a unix socket addr", listener.Name)
		}
		return next, nil
	case "bearer", "token":
		token := authString(listener.Auth, "token")
		if token == "" {
			if env := authString(listener.Auth, "env"); env != "" {
				token = os.Getenv(env)
			}
		}
		if token == "" {
			return nil, fmt.Errorf("serve: listener %q bearer auth token is empty", listener.Name)
		}
		return bearerAuthHandler(token, next), nil
	default:
		return nil, fmt.Errorf("serve: listener %q unsupported auth mode %q", listener.Name, mode)
	}
}

func bearerAuthHandler(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agentsdk"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authString(auth map[string]any, key string) string {
	if len(auth) == 0 {
		return ""
	}
	value, ok := auth[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func serveAddrIsTCP(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return true
	}
	return !strings.HasSuffix(addr, ".sock")
}

func listenServe(addr string) (net.Listener, string, func(), error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if strings.HasSuffix(addr, ".sock") {
		path := resolveServeSocketPath(addr)
		if err := prepareServeSocketPath(path); err != nil {
			return nil, "", func() {}, err
		}
		ln, err := net.Listen("unix", path)
		if err != nil {
			return nil, "", func() {}, err
		}
		cleanup := func() { _ = os.Remove(path) }
		return ln, "unix:" + path, cleanup, nil
	}
	ln, err := net.Listen("tcp", addr)
	return ln, "http://" + addr, func() {}, err
}

func prepareServeSocketPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("serve: inspect unix socket %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("serve: unix socket path %s already exists and is not a socket", path)
	}
	conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("serve: unix socket %s is already in use", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("serve: remove stale unix socket %s: %w", path, err)
	}
	return nil
}

func runRemote(ctx context.Context, opts remoteOptions) error {
	session, err := openRemoteSession(ctx, opts)
	if err != nil {
		return err
	}
	tracker := coreusage.NewTracker()
	if strings.TrimSpace(opts.input) != "" {
		return sendRemotePrompt(ctx, session, opts, opts.input, tracker)
	}
	_, _ = fmt.Fprintln(os.Stderr, "agentsdk remote. Type /exit or /quit to stop.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		_, _ = fmt.Fprint(os.Stdout, "remote> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		if err := sendRemotePrompt(ctx, session, opts, prompt, tracker); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	return scanner.Err()
}

func openRemoteSession(ctx context.Context, opts remoteOptions) (agentruntime.Session, error) {
	target, err := resolveRemoteTarget(ctx, opts)
	if err != nil {
		return nil, err
	}
	client, err := httpssechannel.NewClient(httpssechannel.ClientConfig{
		BaseURL:     target.baseURL,
		UnixSocket:  target.socket,
		BearerToken: target.bearerToken,
	})
	if err != nil {
		return nil, err
	}
	sessionName := firstNonEmptyString(target.session, opts.session, defaultRemoteSession)
	conversation := strings.TrimSpace(opts.conversation)
	if conversation == "" {
		conversation = defaultRemoteConversation
	}
	return client.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: agentruntime.SessionName(sessionName)},
		Conversation: channel.ConversationRef{ID: conversation},
	})
}

func resolveRemoteTarget(ctx context.Context, opts remoteOptions) (remoteTarget, error) {
	var modes []string
	if strings.TrimSpace(opts.appDir) != "" {
		modes = append(modes, "--app")
	}
	if strings.TrimSpace(opts.url) != "" {
		modes = append(modes, "--url")
	}
	if strings.TrimSpace(opts.socket) != "" {
		modes = append(modes, "--socket")
	}
	if opts.local {
		modes = append(modes, "--local")
	}
	if len(modes) == 0 {
		return remoteTarget{}, fmt.Errorf("remote: specify one target with --app, --url, --socket, or --local")
	}
	if len(modes) > 1 {
		return remoteTarget{}, fmt.Errorf("remote: target flags are mutually exclusive: %s", strings.Join(modes, ", "))
	}
	switch modes[0] {
	case "--app":
		return resolveRemoteAppTarget(ctx, opts)
	case "--url":
		return remoteTarget{baseURL: strings.TrimRight(strings.TrimSpace(opts.url), "/"), session: opts.session}, nil
	case "--socket":
		return remoteTarget{baseURL: "http://unix", socket: resolveRemoteSocketPath(opts.socket), session: opts.session}, nil
	case "--local":
		return remoteTarget{baseURL: "http://unix", socket: resolveRemoteSocketPath(defaultRemoteSocket), session: opts.session}, nil
	default:
		return remoteTarget{}, fmt.Errorf("remote: unsupported target %s", modes[0])
	}
}

func resolveRemoteAppTarget(ctx context.Context, opts remoteOptions) (remoteTarget, error) {
	cfgFile, err := appconfig.LoadDirFile(ctx, opts.appDir)
	if err != nil {
		return remoteTarget{}, err
	}
	if err := cfgFile.Validate(); err != nil {
		return remoteTarget{}, err
	}
	ch, sessionName, err := selectRemoteDirectChannel(cfgFile.Daemon.Channels, opts.session, opts.sessionExplicit)
	if err != nil {
		return remoteTarget{}, err
	}
	listener, err := remoteListenerByName(cfgFile.Daemon.Listeners, ch.Listener)
	if err != nil {
		return remoteTarget{}, err
	}
	target, err := remoteTargetFromListener(listener)
	if err != nil {
		return remoteTarget{}, err
	}
	target.session = sessionName
	return target, nil
}

func selectRemoteDirectChannel(channels []appconfig.ChannelDoc, sessionName string, sessionExplicit bool) (appconfig.ChannelDoc, string, error) {
	var direct []appconfig.ChannelDoc
	for _, ch := range channels {
		if ch.Type == "direct" {
			direct = append(direct, ch)
		}
	}
	if len(direct) == 0 {
		return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: app has no direct daemon channel")
	}
	var matching []appconfig.ChannelDoc
	for _, ch := range direct {
		if remoteChannelSession(ch) == sessionName {
			matching = append(matching, ch)
		}
	}
	if len(matching) == 1 {
		ch := matching[0]
		return ch, remoteChannelSession(ch), nil
	}
	if len(matching) > 1 {
		return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: multiple direct channels match session %q: %s", sessionName, remoteChannelList(matching))
	}
	if sessionExplicit {
		return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: no direct channel matches session %q (available: %s)", sessionName, remoteChannelList(direct))
	}
	if len(direct) == 1 {
		ch := direct[0]
		return ch, remoteChannelSession(ch), nil
	}
	return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: multiple direct channels are available; pass --session (available: %s)", remoteChannelList(direct))
}

func remoteChannelSession(ch appconfig.ChannelDoc) string {
	if strings.TrimSpace(ch.Session) != "" {
		return strings.TrimSpace(ch.Session)
	}
	return strings.TrimSpace(ch.Name)
}

func remoteChannelList(channels []appconfig.ChannelDoc) string {
	var parts []string
	for _, ch := range channels {
		parts = append(parts, fmt.Sprintf("%s session=%s listener=%s", ch.Name, remoteChannelSession(ch), ch.Listener))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func remoteListenerByName(listeners []appconfig.ListenerDoc, name string) (appconfig.ListenerDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return appconfig.ListenerDoc{}, fmt.Errorf("remote: direct channel listener is empty")
	}
	for _, listener := range listeners {
		if listener.Name == name {
			return listener, nil
		}
	}
	return appconfig.ListenerDoc{}, fmt.Errorf("remote: listener %q not found", name)
}

func remoteTargetFromListener(listener appconfig.ListenerDoc) (remoteTarget, error) {
	if listener.Type != "http" {
		return remoteTarget{}, fmt.Errorf("remote: listener %q uses unsupported type %q", listener.Name, listener.Type)
	}
	addr := strings.TrimSpace(listener.Addr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	mode := strings.ToLower(strings.TrimSpace(authString(listener.Auth, "mode")))
	var token string
	switch mode {
	case "":
		if serveAddrIsTCP(addr) {
			return remoteTarget{}, fmt.Errorf("remote: listener %q uses TCP addr %q and requires auth", listener.Name, addr)
		}
	case "local_socket":
		if serveAddrIsTCP(addr) {
			return remoteTarget{}, fmt.Errorf("remote: listener %q auth mode local_socket requires a unix socket addr", listener.Name)
		}
	case "bearer", "token":
		token = authString(listener.Auth, "token")
		if token == "" {
			if env := authString(listener.Auth, "env"); env != "" {
				token = os.Getenv(env)
			}
		}
		if token == "" {
			return remoteTarget{}, fmt.Errorf("remote: listener %q bearer auth token is empty", listener.Name)
		}
	default:
		return remoteTarget{}, fmt.Errorf("remote: listener %q unsupported auth mode %q", listener.Name, mode)
	}
	if serveAddrIsTCP(addr) {
		if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
			return remoteTarget{baseURL: strings.TrimRight(addr, "/"), bearerToken: token}, nil
		}
		return remoteTarget{baseURL: "http://" + addr, bearerToken: token}, nil
	}
	return remoteTarget{baseURL: "http://unix", socket: resolveServeSocketPath(addr), bearerToken: token}, nil
}

func resolveServeSocketPath(addr string) string {
	addr = strings.TrimSpace(addr)
	if !strings.ContainsRune(addr, filepath.Separator) {
		base := os.Getenv("XDG_RUNTIME_DIR")
		if base == "" {
			base = os.TempDir()
		}
		return filepath.Join(base, addr)
	}
	return addr
}

func resolveRemoteSocketPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if filepath.IsAbs(raw) || strings.ContainsRune(raw, filepath.Separator) {
		return raw
	}
	return resolveServeSocketPath(raw)
}

func sendRemotePrompt(ctx context.Context, session agentruntime.Session, opts remoteOptions, prompt string, tracker *coreusage.Tracker) error {
	return runTerminalPrompt(ctx, session, prompt, terminalTurnOptions{
		Debug: opts.debug,
		Usage: opts.usage,
	}, tracker)
}

func runCoder(ctx context.Context, opts coderOptions, prompt string) error {
	session, err := openCoderSession(ctx, opts)
	if err != nil {
		return err
	}
	return sendCoderPrompt(ctx, session, opts, prompt, coreusage.NewTracker())
}

func runCoderREPL(ctx context.Context, opts coderOptions) error {
	session, err := openCoderSession(ctx, opts)
	if err != nil {
		return err
	}
	tracker := coreusage.NewTracker()
	_, _ = fmt.Fprintln(os.Stderr, "agentsdk coder repl. Type /exit or /quit to stop.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		_, _ = fmt.Fprint(os.Stdout, "coder> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		if err := sendCoderPrompt(ctx, session, opts, prompt, tracker); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func openCoderSession(ctx context.Context, opts coderOptions) (agentruntime.Session, error) {
	root, err := workspaceRoot()
	if err != nil {
		return nil, err
	}
	selection := resolveModelSelection(opts)
	model, err := newCoderModel(selection, opts)
	if err != nil {
		return nil, err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		return nil, err
	}
	hostSystem.SetClarifier(terminalui.Prompter{In: os.Stdin, Out: os.Stderr})
	browser, err := browsercdp.New(browsercdp.Config{Workspace: hostSystem.Workspace(), Headless: true})
	if err == nil {
		hostSystem.SetBrowser(browser)
	} else if opts.debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}
	bundle := coderBundle(selection.Provider, selection.Model)
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{bundle},
		Plugins: []pluginhost.Plugin{
			codingplugin.New(hostSystem),
			planexecplugin.New(),
		},
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:        localSandbox{Root: root},
			ACL:            localACL{},
			CommandRisk:    coderCommandRisk(root),
			MaxCommandRisk: operation.RiskMedium,
			AllowPure:      true,
		})),
	})
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel:        model,
		LLMStreamPolicy: debugStreamPolicy(opts.debug),
		ToolProjection: agentruntime.ToolProjectionConfig{
			AllowSideEffects: true,
			MaxRisk:          operation.RiskMedium,
		},
		Channel: channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "agentsdk",
				Name: "agentsdk",
			},
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
	})
	if err != nil {
		return nil, err
	}
	session, err := service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: coder.SessionName},
		Conversation: channel.ConversationRef{ID: "agentsdk-coder"},
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func coderBundle(provider, model string) agentruntime.ResourceBundle {
	bundle := coder.Bundle()
	if model == "" {
		return bundle
	}
	for i := range bundle.Agents {
		if bundle.Agents[i].Name == coder.AgentName {
			bundle.Agents[i].Inference.Model = model
		}
	}
	for i := range bundle.Apps {
		if bundle.Apps[i].Name == coder.AppName {
			bundle.Apps[i].Model.Provider = provider
			bundle.Apps[i].Model.Model = model
		}
	}
	return bundle
}

func sendCoderPrompt(ctx context.Context, session agentruntime.Session, opts coderOptions, prompt string, tracker *coreusage.Tracker) error {
	return runTerminalPrompt(ctx, session, prompt, terminalTurnOptions{
		Debug: opts.debug,
		Usage: opts.usage,
	}, tracker)
}

type terminalTurnOptions struct {
	Debug bool
	Usage bool
}

type terminalRenderResult struct {
	Streamed bool
}

func runTerminalPrompt(ctx context.Context, session agentruntime.Session, prompt string, opts terminalTurnOptions, tracker *coreusage.Tracker) error {
	run, err := session.SendInput(ctx, agentruntime.Input{Text: prompt})
	if err != nil {
		return err
	}
	eventsDone := renderTerminalEvents(run.Events(), tracker, opts.Debug)
	result, err := run.Wait(ctx)
	eventResult := terminalRenderResult{}
	if eventsDone != nil {
		eventResult = <-eventsDone
	}
	if !eventResult.Streamed {
		renderTerminalOutbound(os.Stdout, result)
	}
	if opts.Usage && tracker != nil {
		terminalui.RenderUsageSnapshot(os.Stderr, tracker.Snapshot())
	}
	if err != nil {
		return err
	}
	return resultError(result)
}

func resultError(result agentruntime.Result) error {
	if result.Input != nil && result.Input.Status != sessionruntime.InputStatusOK {
		if result.Input.Error != nil {
			return fmt.Errorf("%s: %s", result.Input.Error.Code, result.Input.Error.Message)
		}
		return fmt.Errorf("input failed: %s", result.Input.Status)
	}
	if result.Command != nil && result.Command.Status != sessionruntime.CommandStatusOK {
		if result.Command.Error != nil {
			return fmt.Errorf("%s: %s", result.Command.Error.Code, result.Command.Error.Message)
		}
		return fmt.Errorf("command failed: %s", result.Command.Status)
	}
	return nil
}

func renderTerminalOutbound(out io.Writer, result agentruntime.Result) {
	if out == nil {
		out = io.Discard
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		return
	}
	content := fmt.Sprint(result.Outbound.Message.Content)
	if content == "" {
		return
	}
	_ = terminalui.RenderMarkdown(out, content)
}

func renderTerminalEvents(events <-chan agentruntime.Event, tracker *coreusage.Tracker, debug bool) <-chan terminalRenderResult {
	done := make(chan terminalRenderResult, 1)
	go func() {
		renderer := terminalui.NewRenderer(os.Stdout, os.Stderr, false)
		for event := range events {
			trackUsageEvent(tracker, event)
			if debug {
				renderer.RenderDebug(event)
			}
			renderer.Render(event)
		}
		renderer.Finish()
		done <- terminalRenderResult{Streamed: renderer.HasStreamedContent()}
		close(done)
	}()
	return done
}

func trackUsageEvent(tracker *coreusage.Tracker, event agentruntime.Event) {
	if tracker == nil {
		return
	}
	if recorded, ok := usageFromEvent(event); ok {
		tracker.Add(recorded)
	}
}

func usageFromEvent(event agentruntime.Event) (coreusage.Recorded, bool) {
	if event.Runtime == nil || event.Runtime.Name != coreusage.EventRecordedName {
		return coreusage.Recorded{}, false
	}
	switch payload := event.Runtime.Payload.(type) {
	case coreusage.Recorded:
		if payload.Empty() {
			return coreusage.Recorded{}, false
		}
		return payload, true
	case map[string]any:
		data, err := json.Marshal(payload)
		if err != nil {
			return coreusage.Recorded{}, false
		}
		var recorded coreusage.Recorded
		if err := json.Unmarshal(data, &recorded); err != nil || recorded.Empty() {
			return coreusage.Recorded{}, false
		}
		return recorded, true
	default:
		return coreusage.Recorded{}, false
	}
}

func workspaceRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(wd)
}

func debugStreamPolicy(debug bool) llmagent.StreamPolicy {
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: debug}
}

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{ExposeThinkingSummary: true}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeThinkingSummary: true, ExposeToolArgs: true}
}

type modelSelection struct {
	Provider string
	Model    string
}

func resolveModelSelection(opts coderOptions) modelSelection {
	provider := strings.TrimSpace(opts.provider)
	if provider == "" {
		provider = "openai"
	}
	model := strings.TrimSpace(opts.model)
	if before, after, ok := strings.Cut(model, "/"); ok && before != "" && after != "" {
		if knownCLIProvider(before) && provider == "openai" {
			provider = before
			model = after
		}
	}
	if model == "" {
		model = coder.DefaultModel
	}
	return modelSelection{Provider: provider, Model: model}
}

func knownCLIProvider(provider string) bool {
	switch provider {
	case "openai", "codex", "openrouter", "anthropic", "minimax":
		return true
	default:
		return false
	}
}

func newCoderModel(selection modelSelection, opts coderOptions) (llmagent.Model, error) {
	_, modelSpec, found := modelcatalog.Find(selection.Provider, selection.Model)
	pricing := modelSpec.Pricing
	runtime := openaiadapter.DefaultResponsesRuntimeConfig()
	switch selection.Provider {
	case "openai":
		return openaiadapter.New(openaiadapter.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			Pricing:           pricing,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(opts.debug),
		})
	case "codex":
		return codexadapter.New(codexadapter.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			Pricing:           pricing,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(opts.debug),
		})
	case "openrouter":
		if !found {
			return nil, fmt.Errorf("openrouter model %q was not found in modeldb; use an exact OpenRouter model id, for example --model openrouter/anthropic/claude-sonnet-4.6", selection.Model)
		}
		if !modelcatalog.SupportsAPI(modelSpec, "openai-responses") {
			return nil, fmt.Errorf("openrouter model %q does not expose OpenAI Responses in modeldb", selection.Model)
		}
		reasoningEffort, reasoningSummary := openRouterReasoningDefaults(modelSpec)
		return openrouteradapter.New(openrouteradapter.Config{
			Model:             selection.Model,
			Pricing:           pricing,
			ReasoningEffort:   reasoningEffort,
			ReasoningSummary:  reasoningSummary,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(opts.debug),
		})
	case "anthropic":
		if err := requireMessagesModel(selection.Provider, selection.Model, modelSpec, found); err != nil {
			return nil, err
		}
		return anthropicadapter.New(anthropicadapter.Config{
			Model:           selection.Model,
			Pricing:         pricing,
			MaxOutputTokens: maxOutputTokens(modelSpec),
			PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
			Redactor:        debugRedactor(opts.debug),
		})
	case "minimax":
		if err := requireMessagesModel(selection.Provider, selection.Model, modelSpec, found); err != nil {
			return nil, err
		}
		return minimaxadapter.New(minimaxadapter.Config{
			Model:           selection.Model,
			Pricing:         pricing,
			MaxOutputTokens: maxOutputTokens(modelSpec),
			PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
			Redactor:        debugRedactor(opts.debug),
		})
	default:
		return nil, fmt.Errorf("unknown provider %q", selection.Provider)
	}
}

func requireMessagesModel(provider, model string, modelSpec corellm.ModelSpec, found bool) error {
	if !found {
		return fmt.Errorf("%s model %q was not found in modeldb", provider, model)
	}
	if !modelcatalog.SupportsAPI(modelSpec, "anthropic-messages") {
		return fmt.Errorf("%s model %q does not expose Anthropic Messages in modeldb", provider, model)
	}
	return nil
}

func maxOutputTokens(modelSpec corellm.ModelSpec) int {
	if modelSpec.MaxOutputTokens > 0 && modelSpec.MaxOutputTokens < int64(^uint(0)>>1) {
		return int(modelSpec.MaxOutputTokens)
	}
	return 0
}

func openRouterReasoningDefaults(modelSpec corellm.ModelSpec) (string, string) {
	effort := firstSupportedCSV(modelSpec.Annotations["modeldb.openai_responses.reasoning_efforts"], "minimal", "low", "medium", "high")
	summary := firstSupportedCSV(modelSpec.Annotations["modeldb.openai_responses.reasoning_summaries"], "auto", "concise", "detailed")
	return effort, summary
}

func firstSupportedCSV(csv string, preferred ...string) string {
	values := map[string]bool{}
	for _, value := range strings.Split(csv, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			values[value] = true
		}
	}
	for _, value := range preferred {
		if values[value] {
			return value
		}
	}
	return ""
}

type localSandbox struct {
	Root string
}

func (s localSandbox) Check(_ operation.Context, spec operation.Spec, input operation.Value) error {
	if spec.Semantics.Effects.Has(operation.EffectProcess) {
		_ = input
		_ = s.Root
	}
	return nil
}

type localACL struct{}

func (localACL) Authorize(operation.Context, operation.Spec, operation.Value) error {
	return nil
}

func coderCommandRisk(root string) operationruntime.CommandRiskClassifier {
	secretPrefixes := []string{
		filepath.Join(root, ".env"),
		filepath.Join(root, ".git", "config"),
		filepath.Join(root, ".git", "credentials"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		secretPrefixes = append(secretPrefixes,
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gh"),
		)
	}
	return cmdriskadapter.New(cmdriskadapter.Config{
		WorkingDirectory:        root,
		WorkspacePathPrefixes:   []string{root},
		SecretPathPrefixes:      secretPrefixes,
		SensitivePathPrefixes:   []string{filepath.Join(root, ".git")},
		Sandboxed:               false,
		Disposable:              false,
		Interactive:             false,
		NetworkApprovalAsMedium: true,
	})
}
