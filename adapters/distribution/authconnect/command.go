// Package authconnect adapts plugin auth declarations into distribution CLI
// setup commands.
package authconnect

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/adapters/auth/oauth2flow"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/oauth2client"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// PluginRegistry supplies plugins whose auth methods should be exposed by the
// auth command.
type PluginRegistry func(context.Context) ([]pluginhost.Plugin, error)

// CommandOptions configures the shared auth command.
type CommandOptions struct {
	NativeRegistry PluginRegistry
}

type options struct {
	authPath  string
	plugins   []string
	methods   []string
	instances []string
	fields    []string
	in        io.Reader
	out       io.Writer
}

type target struct {
	plugin   string
	instance string
	method   string
}

// NewCommand builds an auth command shared by first-party distributions.
func NewCommand(cfg CommandOptions) *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage plugin auth",
		Args:  cobra.NoArgs,
	}
	cmd.PersistentFlags().StringVar(&opts.authPath, "auth-path", runtimesecret.DefaultFileStorePath, "native plugin credential store path")
	cmd.PersistentFlags().StringArrayVar(&opts.plugins, "plugin", nil, "plugin name; may be repeated or comma-separated")
	cmd.PersistentFlags().StringArrayVar(&opts.instances, "instance", nil, "instance ID or plugin=instance; may be repeated")
	cmd.PersistentFlags().StringArrayVar(&opts.methods, "method", nil, "auth method or plugin=method; may be repeated")

	cmd.AddCommand(newInfoCommand(&opts, cfg))
	cmd.AddCommand(newStatusCommand(&opts, cfg))
	cmd.AddCommand(newConnectCommand(&opts, cfg))
	cmd.AddCommand(newTestCommand(&opts, cfg))
	return cmd
}

func newInfoCommand(opts *options, cfg CommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "List plugin auth methods",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := *opts
			o.in = cmd.InOrStdin()
			o.out = cmd.OutOrStdout()
			return runInfo(cmd.Context(), o, cfg)
		},
	}
}

func newStatusCommand(opts *options, cfg CommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show stored auth readiness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := *opts
			o.in = cmd.InOrStdin()
			o.out = cmd.OutOrStdout()
			return runStatus(cmd.Context(), o, cfg)
		},
	}
}

func newConnectCommand(opts *options, cfg CommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect plugin auth",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := *opts
			o.in = cmd.InOrStdin()
			o.out = cmd.OutOrStdout()
			return runConnect(cmd.Context(), o, cfg)
		},
	}
	cmd.Flags().StringArrayVarP(&opts.fields, "field", "f", nil, "setup/auth field value (key=value, repeatable)")
	return cmd
}

func newTestCommand(opts *options, cfg CommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Test plugin auth connectivity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := *opts
			o.in = cmd.InOrStdin()
			o.out = cmd.OutOrStdout()
			return runTest(cmd.Context(), o, cfg)
		},
	}
}

func runInfo(ctx context.Context, opts options, cfg CommandOptions) error {
	plugins, err := pluginMap(ctx, cfg.NativeRegistry)
	if err != nil {
		return err
	}
	targets, err := targetsFor(opts, plugins, true)
	if err != nil {
		return err
	}
	out := writerOr(opts.out, os.Stdout)
	for _, target := range targets {
		methods, err := methodsFor(ctx, plugins[target.plugin], target.ref())
		if err != nil {
			return err
		}
		printNativeInfo(out, target.ref(), methods)
	}
	return nil
}

func runStatus(ctx context.Context, opts options, cfg CommandOptions) error {
	plugins, err := pluginMap(ctx, cfg.NativeRegistry)
	if err != nil {
		return err
	}
	targets, err := targetsFor(opts, plugins, true)
	if err != nil {
		return err
	}
	out := writerOr(opts.out, os.Stdout)
	store := runtimesecret.NewFileStore(opts.authPath)
	now := time.Now().UTC()
	for _, target := range targets {
		methods, err := methodsFor(ctx, plugins[target.plugin], target.ref())
		if err != nil {
			return err
		}
		methods, err = filterMethods(methods, target.method)
		if err != nil {
			return err
		}
		for _, method := range methods {
			status := methodStatus(ctx, store, target.ref(), method, now)
			_, _ = fmt.Fprintf(out, "%s/%s %s: %s", target.plugin, target.instance, method.Name, status.Status)
			if status.Message != "" {
				_, _ = fmt.Fprintf(out, " (%s)", status.Message)
			}
			_, _ = fmt.Fprintln(out)
		}
	}
	return nil
}

func runConnect(ctx context.Context, opts options, cfg CommandOptions) error {
	plugins, err := pluginMap(ctx, cfg.NativeRegistry)
	if err != nil {
		return err
	}
	targets, err := targetsFor(opts, plugins, false)
	if err != nil {
		return err
	}
	out := writerOr(opts.out, os.Stdout)
	for _, target := range targets {
		methods, err := methodsFor(ctx, plugins[target.plugin], target.ref())
		if err != nil {
			return err
		}
		method, err := selectMethod(methods, target.method, readerOr(opts.in, os.Stdin), out)
		if err != nil {
			return err
		}
		switch method.Method {
		case coresecret.AuthMethodOAuth2:
			if err := runOAuth2(ctx, opts, target.ref(), method); err != nil {
				return err
			}
		case coresecret.AuthMethodEnv:
			printEnvInstructions(out, target.plugin, method)
		case coresecret.AuthMethodStored:
			if err := runStored(ctx, opts, target.ref(), method); err != nil {
				return err
			}
		default:
			return fmt.Errorf("auth connect %s: auth method %q is not supported by this command", target.plugin, method.Method)
		}
	}
	return nil
}

func runTest(ctx context.Context, opts options, cfg CommandOptions) error {
	plugins, err := pluginMap(ctx, cfg.NativeRegistry)
	if err != nil {
		return err
	}
	targets, err := targetsFor(opts, plugins, false)
	if err != nil {
		return err
	}
	out := writerOr(opts.out, os.Stdout)
	resolver := runtimesecret.ChainResolver{
		runtimesecret.NewFileStore(opts.authPath),
		runtimesecret.EnvResolver{Environment: osEnvironment{}},
	}
	for _, target := range targets {
		plugin := plugins[target.plugin]
		methods, err := methodsFor(ctx, plugin, target.ref())
		if err != nil {
			return err
		}
		method, err := selectMethod(methods, target.method, readerOr(opts.in, os.Stdin), out)
		if err != nil {
			return err
		}
		tester, ok := plugin.(pluginhost.AuthTestContributor)
		if !ok {
			return fmt.Errorf("auth test %s: plugin does not support auth testing", target.plugin)
		}
		pluginCtx := pluginhost.Context{Ref: target.ref()}
		pluginCtx, err = pluginhost.PrepareContext(ctx, plugin, pluginCtx)
		if err != nil {
			return err
		}
		if factory, ok := plugin.(pluginhost.InstanceFactory); ok {
			plugin, err = factory.Instantiate(ctx, pluginCtx)
			if err != nil {
				return err
			}
			tester, ok = plugin.(pluginhost.AuthTestContributor)
			if !ok {
				return fmt.Errorf("auth test %s: plugin does not support auth testing", target.plugin)
			}
		}
		result, err := tester.AuthTest(ctx, pluginCtx, pluginhost.AuthTestRequest{Ref: target.ref(), Method: method.Name, Secrets: resolver})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s/%s %s: %s", target.plugin, target.instance, method.Name, result.Status)
		if result.Message != "" {
			_, _ = fmt.Fprintf(out, " (%s)", result.Message)
		}
		_, _ = fmt.Fprintln(out)
	}
	return nil
}

func runStored(ctx context.Context, opts options, ref resource.PluginRef, method coresecret.AuthMethodSpec) error {
	out := writerOr(opts.out, os.Stdout)
	fields, err := collectFields(opts, method.SetupFields)
	if err != nil {
		return err
	}
	store := runtimesecret.NewFileStore(opts.authPath)
	kind := method.Kind
	if kind == "" {
		kind = coresecret.KindBearerToken
	}
	saved := 0
	for _, spec := range method.SetupFields {
		name := strings.TrimSpace(spec.Name)
		value := strings.TrimSpace(fields[name])
		if name == "" || value == "" {
			continue
		}
		if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{
			Ref:   coresecret.Plugin(ref.Name, ref.InstanceName(), name),
			Kind:  kind,
			Value: value,
			Metadata: map[string]string{
				"auth_method": strings.TrimSpace(method.Name),
			},
		}); err != nil {
			return err
		}
		saved++
	}
	if saved == 0 {
		return fmt.Errorf("auth connect %s: no stored auth fields were provided", ref.Name)
	}
	_, _ = fmt.Fprintf(out, "Connected %s instance %s\n", ref.Name, ref.InstanceName())
	return nil
}

func runOAuth2(ctx context.Context, opts options, ref resource.PluginRef, method coresecret.AuthMethodSpec) error {
	out := writerOr(opts.out, os.Stdout)
	fields, err := collectFields(opts, method.SetupFields)
	if err != nil {
		return err
	}
	clientID := strings.TrimSpace(fields["client_id"])
	clientSecret := strings.TrimSpace(fields["client_secret"])
	auth, err := oauth2flow.Authorize(ctx, oauth2flow.Config{
		AuthorizeURL: method.OAuth2.AuthorizeURL,
		ClientID:     clientID,
		Scopes:       method.OAuth2.Scopes,
		ExtraParams:  method.OAuth2.ExtraParams,
		Out:          out,
	})
	if err != nil {
		return err
	}
	token, err := oauth2client.ExchangeCode(ctx, http.DefaultClient, oauth2client.TokenRequest{
		TokenURL:     method.OAuth2.TokenURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  auth.RedirectURI,
		Code:         auth.Code,
	})
	if err != nil {
		return err
	}
	store := runtimesecret.NewFileStore(opts.authPath)
	expiresAt := time.Time{}
	if token.ExpiresIn > 0 {
		expiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	metadata := map[string]string{
		"client_id":  clientID,
		"token_type": strings.TrimSpace(token.TokenType),
		"scope":      strings.TrimSpace(token.Scope),
	}
	for _, spec := range method.SetupFields {
		name := strings.TrimSpace(spec.Name)
		value := strings.TrimSpace(fields[name])
		if name == "" || value == "" || spec.Sensitive || name == "client_secret" {
			continue
		}
		metadata[name] = value
	}
	for key, value := range method.OAuth2.ExtraParams {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			metadata["oauth2_"+strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{
		Ref:       method.Secret,
		Kind:      method.Kind,
		Value:     token.AccessToken,
		Metadata:  metadata,
		ExpiresAt: expiresAt,
	}); err != nil {
		return err
	}
	if token.RefreshToken != "" {
		if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{
			Ref:   relatedSecretRef(ref, method.Name, "refresh_token"),
			Kind:  coresecret.KindOAuth2Token,
			Value: token.RefreshToken,
		}); err != nil {
			return err
		}
	}
	if clientSecret != "" {
		if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{
			Ref:   relatedSecretRef(ref, method.Name, "client_secret"),
			Kind:  coresecret.KindOAuth2Token,
			Value: clientSecret,
		}); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(out, "Connected %s instance %s\n", ref.Name, ref.InstanceName())
	return nil
}

func methodsFor(ctx context.Context, plugin pluginhost.Plugin, ref resource.PluginRef) ([]coresecret.AuthMethodSpec, error) {
	pluginCtx := pluginhost.Context{Ref: ref}
	var err error
	pluginCtx, err = pluginhost.PrepareContext(ctx, plugin, pluginCtx)
	if err != nil {
		return nil, err
	}
	if factory, ok := plugin.(pluginhost.InstanceFactory); ok {
		plugin, err = factory.Instantiate(ctx, pluginCtx)
		if err != nil {
			return nil, err
		}
	}
	contributor, ok := plugin.(pluginhost.AuthMethodContributor)
	if !ok {
		return nil, fmt.Errorf("auth %s: plugin does not declare auth methods", ref.Name)
	}
	return contributor.AuthMethods(ctx, pluginCtx)
}

func selectMethod(methods []coresecret.AuthMethodSpec, requested string, in io.Reader, out io.Writer) (coresecret.AuthMethodSpec, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	for _, method := range methods {
		name := strings.ToLower(strings.TrimSpace(method.Name))
		if requested != "" && requested == name {
			return method, nil
		}
	}
	if requested != "" {
		return coresecret.AuthMethodSpec{}, fmt.Errorf("auth: auth method %q is unavailable", requested)
	}
	if len(methods) == 1 {
		return methods[0], nil
	}
	return promptMethod(methods, in, out)
}

func promptMethod(methods []coresecret.AuthMethodSpec, in io.Reader, out io.Writer) (coresecret.AuthMethodSpec, error) {
	if len(methods) == 0 {
		return coresecret.AuthMethodSpec{}, fmt.Errorf("auth: plugin does not declare auth methods")
	}
	_, _ = fmt.Fprintln(out, "Auth methods:")
	for i, method := range methods {
		_, _ = fmt.Fprintf(out, "  %d. %s (%s)\n", i+1, firstNonEmpty(method.DisplayName, method.Name), method.Name)
	}
	_, _ = fmt.Fprint(out, "Select auth method: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return coresecret.AuthMethodSpec{}, err
	}
	selected := strings.ToLower(strings.TrimSpace(line))
	for i, method := range methods {
		if selected == fmt.Sprint(i+1) || selected == strings.ToLower(strings.TrimSpace(method.Name)) {
			return method, nil
		}
	}
	return coresecret.AuthMethodSpec{}, fmt.Errorf("auth: auth method %q is unavailable", selected)
}

func filterMethods(methods []coresecret.AuthMethodSpec, requested string) ([]coresecret.AuthMethodSpec, error) {
	if strings.TrimSpace(requested) == "" {
		return methods, nil
	}
	method, err := selectMethod(methods, requested, strings.NewReader(""), io.Discard)
	if err != nil {
		return nil, err
	}
	return []coresecret.AuthMethodSpec{method}, nil
}

func collectFields(opts options, specs []coresecret.SetupFieldSpec) (map[string]string, error) {
	fields := parseFields(opts.fields)
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" || strings.TrimSpace(fields[name]) != "" {
			continue
		}
		if envValue := firstEnv(spec.Env); envValue != "" {
			fields[name] = envValue
		}
	}
	reader := bufio.NewReader(readerOr(opts.in, os.Stdin))
	out := writerOr(opts.out, os.Stdout)
	terminalInput := isTerminalInput(opts.in)
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" || strings.TrimSpace(fields[name]) != "" {
			continue
		}
		if group := strings.TrimSpace(spec.RequiredGroup); group != "" && !terminalInput && fieldGroupSatisfied(fields, specs, group) {
			continue
		}
		if !spec.Required && strings.TrimSpace(spec.RequiredGroup) == "" && !terminalInput {
			continue
		}
		value, err := readSetupField(reader, out, opts.in, spec)
		if err != nil {
			return nil, err
		}
		fields[name] = value
	}
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if spec.Required && strings.TrimSpace(fields[name]) == "" {
			return nil, fmt.Errorf("auth connect: setup field %q is required", name)
		}
	}
	for group, names := range requiredGroups(specs) {
		ok := false
		for _, name := range names {
			if strings.TrimSpace(fields[name]) != "" {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("auth connect: at least one setup field in group %q is required: %s", group, strings.Join(names, ", "))
		}
	}
	return fields, nil
}

func readSetupField(reader *bufio.Reader, out io.Writer, in io.Reader, spec coresecret.SetupFieldSpec) (string, error) {
	_, _ = fmt.Fprintf(out, "%s: ", displayFieldName(spec))
	if spec.Sensitive {
		file, ok := inputFile(in)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return "", fmt.Errorf("auth connect: sensitive setup field %q must be supplied by --field or environment when stdin is not a terminal", spec.Name)
		}
		data, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(out)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	value, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func printNativeInfo(out io.Writer, ref resource.PluginRef, methods []coresecret.AuthMethodSpec) {
	_, _ = fmt.Fprintf(out, "%s/%s\n\nAuth methods:\n", ref.Name, ref.InstanceName())
	for _, method := range methods {
		name := strings.TrimSpace(method.Name)
		label := firstNonEmpty(method.DisplayName, name)
		if label != "" && label != name {
			label = name + " - " + label
		}
		_, _ = fmt.Fprintf(out, "  %s (%s)\n", firstNonEmpty(label, name), method.Method)
		for _, field := range method.SetupFields {
			req := ""
			if field.Required {
				req = " required"
			} else if strings.TrimSpace(field.RequiredGroup) != "" {
				req = " required-group=" + strings.TrimSpace(field.RequiredGroup)
			}
			envs := append([]string{field.Env.Name}, field.Env.Aliases...)
			envs = nonEmpty(envs)
			env := ""
			if len(envs) > 0 {
				env = " env=" + strings.Join(envs, ",")
			}
			_, _ = fmt.Fprintf(out, "    field %s%s%s\n", field.Name, req, env)
		}
	}
}

func printEnvInstructions(out io.Writer, provider string, method coresecret.AuthMethodSpec) {
	envs := append([]string{method.Env.Name}, method.Env.Aliases...)
	envs = nonEmpty(envs)
	if len(envs) == 0 {
		_, _ = fmt.Fprintf(out, "%s token auth resolves an environment variable at runtime.\n", provider)
		return
	}
	_, _ = fmt.Fprintf(out, "%s token auth resolves an environment variable at runtime. Set one of: %s\n", provider, strings.Join(envs, ", "))
}

type authStatus struct {
	Status  string
	Message string
}

func methodStatus(ctx context.Context, store runtimesecret.FileStore, ref resource.PluginRef, method coresecret.AuthMethodSpec, now time.Time) authStatus {
	switch method.Method {
	case coresecret.AuthMethodEnv:
		if firstEnv(method.Env) != "" {
			return authStatus{Status: "configured"}
		}
		return authStatus{Status: "missing", Message: "environment variable is not set"}
	case coresecret.AuthMethodOAuth2:
		return secretStatus(ctx, store, method.Secret, now)
	case coresecret.AuthMethodStored:
		if len(method.SetupFields) == 0 {
			return secretStatus(ctx, store, method.Secret, now)
		}
		return setupFieldStatus(ctx, store, ref, method, now)
	default:
		return authStatus{Status: "unknown", Message: "unsupported auth method"}
	}
}

func setupFieldStatus(ctx context.Context, store runtimesecret.FileStore, ref resource.PluginRef, method coresecret.AuthMethodSpec, now time.Time) authStatus {
	fields := map[string]bool{}
	expired := false
	for _, spec := range method.SetupFields {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			continue
		}
		stored, ok, err := store.LoadSecret(ctx, coresecret.Plugin(ref.Name, ref.InstanceName(), name))
		if err != nil {
			return authStatus{Status: "error", Message: err.Error()}
		}
		if ok && strings.TrimSpace(stored.Value) != "" {
			fields[name] = true
			if !stored.ExpiresAt.IsZero() && !stored.ExpiresAt.After(now) {
				expired = true
			}
		}
	}
	if expired {
		return authStatus{Status: "expired"}
	}
	missingRequired := []string{}
	for _, spec := range method.SetupFields {
		if spec.Required && !fields[strings.TrimSpace(spec.Name)] {
			missingRequired = append(missingRequired, strings.TrimSpace(spec.Name))
		}
	}
	for group, names := range requiredGroups(method.SetupFields) {
		ok := false
		for _, name := range names {
			if fields[name] {
				ok = true
				break
			}
		}
		if !ok {
			missingRequired = append(missingRequired, group+"("+strings.Join(names, "|")+")")
		}
	}
	if len(missingRequired) > 0 {
		if len(fields) > 0 {
			return authStatus{Status: "partial", Message: "missing " + strings.Join(missingRequired, ", ")}
		}
		return authStatus{Status: "missing", Message: "missing " + strings.Join(missingRequired, ", ")}
	}
	if len(fields) > 0 {
		return authStatus{Status: "configured"}
	}
	return authStatus{Status: "missing"}
}

func secretStatus(ctx context.Context, store runtimesecret.FileStore, ref coresecret.Ref, now time.Time) authStatus {
	stored, ok, err := store.LoadSecret(ctx, ref)
	if err != nil {
		return authStatus{Status: "error", Message: err.Error()}
	}
	if !ok || strings.TrimSpace(stored.Value) == "" {
		return authStatus{Status: "missing"}
	}
	if !stored.ExpiresAt.IsZero() && !stored.ExpiresAt.After(now) {
		return authStatus{Status: "expired"}
	}
	return authStatus{Status: "configured"}
}

func pluginMap(ctx context.Context, registry PluginRegistry) (map[string]pluginhost.Plugin, error) {
	plugins, err := nativePlugins(ctx, registry)
	if err != nil {
		return nil, err
	}
	out := map[string]pluginhost.Plugin{}
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		name := strings.TrimSpace(plugin.Manifest().Name)
		if name == "" {
			continue
		}
		if _, ok := plugin.(pluginhost.AuthMethodContributor); ok {
			out[name] = plugin
		}
	}
	return out, nil
}

func targetsFor(opts options, plugins map[string]pluginhost.Plugin, defaultAll bool) ([]target, error) {
	names := splitValues(opts.plugins)
	if len(names) == 0 && defaultAll {
		for name := range plugins {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("auth: at least one --plugin is required")
	}
	methods, methodBare, err := mappedValues(opts.methods)
	if err != nil {
		return nil, err
	}
	instances, instanceBare, err := mappedValues(opts.instances)
	if err != nil {
		return nil, err
	}
	if len(methodBare) > 0 && len(names) != 1 {
		return nil, fmt.Errorf("auth: bare --method is only valid with one --plugin")
	}
	if len(instanceBare) > 0 && len(names) != 1 {
		return nil, fmt.Errorf("auth: bare --instance is only valid with one --plugin")
	}
	var out []target
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := plugins[name]; !ok {
			return nil, fmt.Errorf("auth: unknown plugin %q", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		instance := name
		if value := strings.TrimSpace(instances[name]); value != "" {
			instance = value
		} else if len(instanceBare) > 0 {
			instance = instanceBare[len(instanceBare)-1]
		}
		method := strings.TrimSpace(methods[name])
		if method == "" && len(methodBare) > 0 {
			method = methodBare[len(methodBare)-1]
		}
		out = append(out, target{plugin: name, instance: instance, method: method})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].plugin < out[j].plugin })
	return out, nil
}

func (t target) ref() resource.PluginRef {
	return resource.PluginRef{Name: t.plugin, Instance: t.instance}
}

func nativePlugins(ctx context.Context, registry PluginRegistry) ([]pluginhost.Plugin, error) {
	if registry == nil {
		return nil, nil
	}
	return registry(ctx)
}

func relatedSecretRef(ref resource.PluginRef, methodName, name string) coresecret.Ref {
	return coresecret.Plugin(ref.Name, ref.InstanceName(), strings.TrimSpace(methodName)+"_"+strings.TrimSpace(name))
}

func parseFields(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = strings.TrimSpace(val)
		}
	}
	return out
}

func firstEnv(spec coresecret.EnvSpec) string {
	for _, name := range append([]string{spec.Name}, spec.Aliases...) {
		if value := strings.TrimSpace(os.Getenv(strings.TrimSpace(name))); value != "" {
			return value
		}
	}
	return ""
}

func displayFieldName(spec coresecret.SetupFieldSpec) string {
	return firstNonEmpty(spec.DisplayName, spec.Name)
}

func requiredGroups(specs []coresecret.SetupFieldSpec) map[string][]string {
	groups := map[string][]string{}
	for _, spec := range specs {
		group := strings.TrimSpace(spec.RequiredGroup)
		name := strings.TrimSpace(spec.Name)
		if group != "" && name != "" {
			groups[group] = append(groups[group], name)
		}
	}
	return groups
}

func fieldGroupSatisfied(fields map[string]string, specs []coresecret.SetupFieldSpec, group string) bool {
	for _, spec := range specs {
		if strings.TrimSpace(spec.RequiredGroup) == group && strings.TrimSpace(fields[strings.TrimSpace(spec.Name)]) != "" {
			return true
		}
	}
	return false
}

func mappedValues(values []string) (map[string]string, []string, error) {
	mapped := map[string]string{}
	var bare []string
	for _, value := range splitValues(values) {
		key, val, ok := strings.Cut(value, "=")
		if !ok {
			bare = append(bare, strings.TrimSpace(value))
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" || val == "" {
			return nil, nil, fmt.Errorf("auth: mapped value %q is invalid", value)
		}
		mapped[key] = val
	}
	return mapped, bare, nil
}

func splitValues(values []string) []string {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
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

func inputFile(value io.Reader) (*os.File, bool) {
	if value == nil {
		return os.Stdin, true
	}
	file, ok := value.(*os.File)
	return file, ok
}

func isTerminalInput(value io.Reader) bool {
	file, ok := inputFile(value)
	return ok && term.IsTerminal(int(file.Fd()))
}

type osEnvironment struct{}

func (osEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := os.LookupEnv(strings.TrimSpace(key))
	return value, ok, nil
}
