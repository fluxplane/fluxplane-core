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

	connectcli "github.com/fluxplane/agentruntime/adapters/connectors/cli"
	"github.com/fluxplane/agentruntime/adapters/oauth2flow"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/oauth2client"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// PluginRegistry supplies plugins whose auth methods should be exposed by the
// connect command.
type PluginRegistry func(context.Context) ([]pluginhost.Plugin, error)

// CommandOptions configures the shared connect command.
type CommandOptions struct {
	NativeRegistry    PluginRegistry
	ConnectorRegistry connectcli.PluginRegistry
}

type options struct {
	connectorsPath string
	authPath       string
	auth           string
	groups         string
	instance       string
	fields         []string
	info           bool
	in             io.Reader
	out            io.Writer
}

// NewCommand builds a connect command shared by first-party distributions.
func NewCommand(cfg CommandOptions) *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "connect [provider]",
		Short: "Manage provider auth",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.in = cmd.InOrStdin()
			opts.out = cmd.OutOrStdout()
			if len(args) == 0 {
				return runStatus(cmd.Context(), opts, cfg)
			}
			return runProvider(cmd.Context(), strings.TrimSpace(args[0]), opts, cfg)
		},
	}
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "~/.connectors", "connector credential store path for connector-backed providers")
	cmd.Flags().StringVar(&opts.authPath, "auth-path", runtimesecret.DefaultFileStorePath, "native plugin credential store path")
	cmd.Flags().StringVar(&opts.auth, "auth", "", "authentication method kind to use")
	cmd.Flags().StringArrayVarP(&opts.fields, "field", "f", nil, "setup/auth field value (key=value, repeatable)")
	cmd.Flags().StringVar(&opts.groups, "groups", "", "operation groups to enable for connector-backed providers")
	cmd.Flags().StringVar(&opts.instance, "instance", "", "instance ID to create/update")
	cmd.Flags().BoolVar(&opts.info, "info", false, "print available auth methods and fields, then exit")
	return cmd
}

func runStatus(ctx context.Context, opts options, cfg CommandOptions) error {
	if cfg.ConnectorRegistry != nil {
		if err := connectcli.RunStatus(ctx, connectorOptions(opts), cfg.ConnectorRegistry); err != nil {
			return err
		}
	}
	providers, err := nativeProviderNames(ctx, cfg.NativeRegistry)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return nil
	}
	out := writerOr(opts.out, os.Stdout)
	_, _ = fmt.Fprintf(out, "\nNative auth providers: %s\n", strings.Join(providers, ", "))
	return nil
}

func runProvider(ctx context.Context, provider string, opts options, cfg CommandOptions) error {
	if provider == "" {
		return fmt.Errorf("connect: provider is required")
	}
	if plugin, ok, err := findNativePlugin(ctx, cfg.NativeRegistry, provider); err != nil {
		return err
	} else if ok {
		return runNativeProvider(ctx, provider, plugin, opts)
	}
	if cfg.ConnectorRegistry == nil {
		return fmt.Errorf("connect: unknown native provider %q", provider)
	}
	return connectcli.RunProvider(ctx, provider, connectorOptions(opts), cfg.ConnectorRegistry)
}

func runNativeProvider(ctx context.Context, provider string, plugin pluginhost.Plugin, opts options) error {
	out := writerOr(opts.out, os.Stdout)
	instance := strings.TrimSpace(opts.instance)
	if instance == "" {
		instance = provider
	}
	ref := resource.PluginRef{Name: provider, Instance: instance}
	methods, err := authMethods(ctx, plugin, ref)
	if err != nil {
		return err
	}
	if opts.info {
		printNativeInfo(out, provider, methods)
		return nil
	}
	method, err := selectMethod(methods, opts.auth)
	if err != nil {
		return err
	}
	switch method.Method {
	case coresecret.AuthMethodOAuth2:
		return runOAuth2(ctx, opts, ref, method)
	case coresecret.AuthMethodEnv:
		printEnvInstructions(out, provider, method)
		return nil
	case coresecret.AuthMethodStored:
		return runStored(ctx, opts, ref, method)
	default:
		return fmt.Errorf("connect %s: auth method %q is not supported by this command", provider, method.Method)
	}
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
		ref := coresecret.Plugin(ref.Name, ref.InstanceName(), name)
		if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{
			Ref:   ref,
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
		return fmt.Errorf("connect %s: no stored auth fields were provided", ref.Name)
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

func authMethods(ctx context.Context, plugin pluginhost.Plugin, ref resource.PluginRef) ([]coresecret.AuthMethodSpec, error) {
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
		return nil, fmt.Errorf("connect %s: plugin does not declare auth methods", ref.Name)
	}
	return contributor.AuthMethods(ctx, pluginCtx)
}

func selectMethod(methods []coresecret.AuthMethodSpec, requested string) (coresecret.AuthMethodSpec, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	for _, method := range methods {
		name := strings.ToLower(strings.TrimSpace(method.Name))
		kind := strings.ToLower(strings.TrimSpace(string(method.Method)))
		if requested == "" && method.Method == coresecret.AuthMethodOAuth2 {
			return method, nil
		}
		if requested != "" && (requested == name || requested == kind) {
			return method, nil
		}
	}
	if requested == "" && len(methods) > 0 {
		return methods[0], nil
	}
	return coresecret.AuthMethodSpec{}, fmt.Errorf("connect: auth method %q is unavailable", requested)
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
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" || strings.TrimSpace(fields[name]) != "" || !spec.Required {
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
			return nil, fmt.Errorf("connect: setup field %q is required", name)
		}
	}
	return fields, nil
}

func readSetupField(reader *bufio.Reader, out io.Writer, in io.Reader, spec coresecret.SetupFieldSpec) (string, error) {
	_, _ = fmt.Fprintf(out, "%s: ", displayFieldName(spec))
	if spec.Sensitive {
		file, ok := inputFile(in)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return "", fmt.Errorf("connect: sensitive setup field %q must be supplied by --field or environment when stdin is not a terminal", spec.Name)
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

func printNativeInfo(out io.Writer, provider string, methods []coresecret.AuthMethodSpec) {
	_, _ = fmt.Fprintf(out, "%s\n\nAuth methods:\n", provider)
	for _, method := range methods {
		_, _ = fmt.Fprintf(out, "  %s (%s)\n", firstNonEmpty(method.DisplayName, method.Name), method.Method)
		for _, field := range method.SetupFields {
			req := ""
			if field.Required {
				req = " required"
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

func nativeProviderNames(ctx context.Context, registry PluginRegistry) ([]string, error) {
	plugins, err := nativePlugins(ctx, registry)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, plugin := range plugins {
		if _, ok := plugin.(pluginhost.AuthMethodContributor); ok {
			names = append(names, plugin.Manifest().Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func findNativePlugin(ctx context.Context, registry PluginRegistry, provider string) (pluginhost.Plugin, bool, error) {
	plugins, err := nativePlugins(ctx, registry)
	if err != nil {
		return nil, false, err
	}
	for _, plugin := range plugins {
		if strings.TrimSpace(plugin.Manifest().Name) == provider {
			if _, ok := plugin.(pluginhost.AuthMethodContributor); ok {
				return plugin, true, nil
			}
			return nil, false, nil
		}
	}
	return nil, false, nil
}

func nativePlugins(ctx context.Context, registry PluginRegistry) ([]pluginhost.Plugin, error) {
	if registry == nil {
		return nil, nil
	}
	return registry(ctx)
}

func connectorOptions(opts options) connectcli.Options {
	return connectcli.Options{
		ConnectorsPath: opts.connectorsPath,
		Auth:           opts.auth,
		Groups:         opts.groups,
		Instance:       opts.instance,
		Fields:         opts.fields,
		Info:           opts.info,
		In:             opts.in,
		Out:            opts.out,
	}
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
