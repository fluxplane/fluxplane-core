package authconnect

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/resource"
	coresecret "github.com/fluxplane/engine/core/secret"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	runtimesecret "github.com/fluxplane/engine/runtime/secret"
)

func TestCollectFieldsRejectsSensitivePromptOnNonTerminal(t *testing.T) {
	_, err := collectFields(options{
		in:  strings.NewReader("secret\n"),
		out: bytes.NewBuffer(nil),
	}, []coresecret.SetupFieldSpec{{
		Name:      "client_secret",
		Required:  true,
		Sensitive: true,
	}})
	if err == nil {
		t.Fatal("collectFields succeeded, want non-terminal sensitive prompt error")
	}
}

func TestCollectFieldsUsesEnvironmentForSensitiveField(t *testing.T) {
	t.Setenv("TEST_CLIENT_SECRET", "secret")
	fields, err := collectFields(options{
		in:  strings.NewReader(""),
		out: bytes.NewBuffer(nil),
	}, []coresecret.SetupFieldSpec{{
		Name:      "client_secret",
		Required:  true,
		Sensitive: true,
		Env:       coresecret.EnvSpec{Aliases: []string{"TEST_CLIENT_SECRET"}},
	}})
	if err != nil {
		t.Fatalf("collectFields: %v", err)
	}
	if fields["client_secret"] != "secret" {
		t.Fatalf("client_secret = %q", fields["client_secret"])
	}
}

func TestRunStoredWritesSetupFieldsToNativeSecretStore(t *testing.T) {
	dir := t.TempDir()
	out := bytes.Buffer{}
	ref := resource.PluginRef{Name: "chat", Instance: "main"}
	err := runStored(context.Background(), options{
		authPath: dir,
		fields:   []string{"bot_token=chat-bot-token", "app_token=chat-app-token"},
		out:      &out,
	}, ref, coresecret.AuthMethodSpec{
		Name:   "token",
		Method: coresecret.AuthMethodStored,
		Kind:   coresecret.KindBearerToken,
		Secret: coresecret.Plugin("chat", "main", "bot_token"),
		SetupFields: []coresecret.SetupFieldSpec{
			{Name: "bot_token", RequiredGroup: "api_token", Sensitive: true},
			{Name: "user_token", RequiredGroup: "api_token", Sensitive: true},
			{Name: "app_token", Sensitive: true},
		},
	})
	if err != nil {
		t.Fatalf("runStored: %v", err)
	}
	store := runtimesecret.NewFileStore(dir)
	bot, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("chat", "main", "bot_token"))
	if err != nil || !ok || bot.Value != "chat-bot-token" {
		t.Fatalf("bot secret = %#v ok=%v err=%v", bot, ok, err)
	}
	app, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("chat", "main", "app_token"))
	if err != nil || !ok || app.Value != "chat-app-token" {
		t.Fatalf("app secret = %#v ok=%v err=%v", app, ok, err)
	}
	if !strings.Contains(out.String(), "Connected chat instance main") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCollectFieldsAcceptsRequiredGroupAlternative(t *testing.T) {
	fields, err := collectFields(options{
		fields: []string{"user_token=chat-user-token"},
		in:     strings.NewReader(""),
		out:    bytes.NewBuffer(nil),
	}, []coresecret.SetupFieldSpec{
		{Name: "bot_token", RequiredGroup: "api_token", Sensitive: true},
		{Name: "user_token", RequiredGroup: "api_token", Sensitive: true},
	})
	if err != nil {
		t.Fatalf("collectFields: %v", err)
	}
	if fields["user_token"] != "chat-user-token" {
		t.Fatalf("user_token = %q", fields["user_token"])
	}
}

func TestTargetsForMappedMethodAndInstance(t *testing.T) {
	authTargets, err := pluginhost.ResolveAuthTargets(context.Background(), []resource.PluginRef{
		{Name: "issues", Instance: "company-a"},
		{Name: "chat", Instance: "team-chat"},
	}, []pluginhost.Plugin{fakePlugin{name: "issues"}, fakePlugin{name: "chat"}})
	if err != nil {
		t.Fatalf("ResolveAuthTargets: %v", err)
	}
	targets, err := targetsFor(options{
		plugins:   []string{"chat,issues"},
		methods:   []string{"chat=token", "issues=oauth2"},
		instances: []string{"chat=team-chat", "issues=company-a"},
	}, authTargets, false)
	if err != nil {
		t.Fatalf("targetsFor: %v", err)
	}
	if len(targets) != 2 || targets[0].ref().Name != "chat" || targets[0].ref().InstanceName() != "team-chat" || targets[0].method != "token" || targets[1].ref().Name != "issues" || targets[1].ref().InstanceName() != "company-a" || targets[1].method != "oauth2" {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestTargetsForRejectsBareMethodWithMultiplePlugins(t *testing.T) {
	authTargets, err := pluginhost.ResolveAuthTargets(context.Background(), []resource.PluginRef{
		{Name: "issues"},
		{Name: "chat"},
	}, []pluginhost.Plugin{fakePlugin{name: "issues"}, fakePlugin{name: "chat"}})
	if err != nil {
		t.Fatalf("ResolveAuthTargets: %v", err)
	}
	_, err = targetsFor(options{plugins: []string{"chat,issues"}, methods: []string{"oauth2"}}, authTargets, false)
	if err == nil || !strings.Contains(err.Error(), "bare --method") {
		t.Fatalf("targetsFor error = %v, want bare method error", err)
	}
}

func TestTargetsForRejectsUndeclaredInstance(t *testing.T) {
	authTargets, err := pluginhost.ResolveAuthTargets(context.Background(), []resource.PluginRef{{Name: "chat", Instance: "team-chat"}}, []pluginhost.Plugin{fakePlugin{name: "chat"}})
	if err != nil {
		t.Fatalf("ResolveAuthTargets: %v", err)
	}
	_, err = targetsFor(options{plugins: []string{"chat"}, instances: []string{"chat=other"}}, authTargets, false)
	if err == nil || !strings.Contains(err.Error(), `instance "other" is not declared`) {
		t.Fatalf("targetsFor error = %v, want undeclared instance", err)
	}
}

func TestRunStatusSummarizesPluginReadiness(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("chat", "team-chat", "user_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "chat-user-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath:  dir,
		plugins:   []string{"chat"},
		instances: []string{"chat=team-chat"},
		methods:   []string{"chat=token"},
		out:       &out,
	}, testCommandOptions([]resource.PluginRef{{Name: "chat", Instance: "team-chat"}}, fakePlugin{name: "chat"}))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "chat/team-chat [token] ✓") || strings.Contains(got, "set:") {
		t.Fatalf("status output = %q", got)
	}
}

func TestRunStatusPrintsResolvedFieldsAndRedactsSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("chat", "team-chat", "user_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "sensitive-test-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath:  dir,
		plugins:   []string{"chat"},
		instances: []string{"chat=team-chat"},
		methods:   []string{"chat=token"},
		out:       &out,
	}, testCommandOptions([]resource.PluginRef{{Name: "chat", Instance: "team-chat"}}, fakePlugin{name: "chat"}))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "user_token") || !strings.Contains(got, "<redacted>") || strings.Contains(got, "sensitive-test-token") || strings.Contains(got, "=") {
		t.Fatalf("status output = %q", got)
	}
}

func TestPrintResolvedFieldsShowsEnvSourceAndResolvedValue(t *testing.T) {
	t.Setenv("SERVICE_EMAIL", "user@example.invalid")
	out := bytes.Buffer{}
	newStatusRenderer(&out).printResolvedFields(&out, context.Background(), runtimesecret.EnvResolver{Environment: osEnvironment{}}, resource.PluginRef{Name: "issues", Instance: "issues"}, coresecret.AuthMethodSpec{
		Name:   "token",
		Method: coresecret.AuthMethodEnv,
		SetupFields: []coresecret.SetupFieldSpec{{
			Name: "email_env",
			Env:  coresecret.EnvSpec{Aliases: []string{"SERVICE_EMAIL"}},
		}},
	})
	got := out.String()
	if !strings.Contains(got, "email_env    SERVICE_EMAIL") || !strings.Contains(got, "email        user@example.invalid") {
		t.Fatalf("fields output = %q", got)
	}
}

func TestRunStatusRunsConnectivityByDefault(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("chat", "chat", "bot_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "chat-bot-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatusWithOptions(context.Background(), options{
		authPath: dir,
		plugins:  []string{"chat"},
		out:      &out,
	}, testCommandOptions([]resource.PluginRef{{Name: "chat"}}, fakePlugin{name: "chat"}))
	if err != nil {
		t.Fatalf("runStatusWithOptions: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "connection") || !strings.Contains(got, "ok") {
		t.Fatalf("status output = %q", got)
	}
}

func TestRunStatusNoTestSkipsConnectivity(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("chat", "chat", "bot_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "chat-bot-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatusWithOptions(context.Background(), options{
		authPath: dir,
		plugins:  []string{"chat"},
		noTest:   true,
		out:      &out,
	}, testCommandOptions([]resource.PluginRef{{Name: "chat"}}, fakePlugin{name: "chat"}))
	if err != nil {
		t.Fatalf("runStatusWithOptions: %v", err)
	}
	if got := out.String(); strings.Contains(got, "connection") {
		t.Fatalf("status output = %q, want no connectivity report", got)
	}
}

func TestRunStatusDoesNotListMissingOptionalMethods(t *testing.T) {
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath: t.TempDir(),
		plugins:  []string{"chat"},
		out:      &out,
	}, testCommandOptions([]resource.PluginRef{{Name: "chat"}}, fakePlugin{name: "chat"}))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "oauth2") || strings.Contains(got, "missing") || !strings.Contains(got, "chat [-] -") {
		t.Fatalf("status output = %q", got)
	}
}

func TestRunStatusShowsPartialRequiredFields(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	for _, secret := range []runtimesecret.StoredSecret{
		{Ref: coresecret.Plugin("issues", "issues", "email"), Kind: coresecret.KindBasic, Value: "user@example.invalid"},
		{Ref: coresecret.Plugin("issues", "issues", "token"), Kind: coresecret.KindBasic, Value: "api-token"},
	} {
		if err := store.SaveSecret(context.Background(), secret); err != nil {
			t.Fatalf("SaveSecret: %v", err)
		}
	}
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath: dir,
		plugins:  []string{"issues"},
		out:      &out,
	}, testCommandOptions([]resource.PluginRef{{Name: "issues"}}, partialAuthPlugin{name: "issues"}))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "issues [token] -") || !strings.Contains(got, "missing  site_url or base_url") || strings.Contains(got, "set:") {
		t.Fatalf("status output = %q", got)
	}
}

func TestSelectMethodUsesMethodNameNotKind(t *testing.T) {
	methods := []coresecret.AuthMethodSpec{{
		Name:        "token",
		Method:      coresecret.AuthMethodStored,
		DisplayName: "Chat token",
	}}
	if _, err := selectMethod(methods, "token", strings.NewReader(""), bytes.NewBuffer(nil)); err != nil {
		t.Fatalf("selectMethod token: %v", err)
	}
	if _, err := selectMethod(methods, "stored", strings.NewReader(""), bytes.NewBuffer(nil)); err == nil || !strings.Contains(err.Error(), `auth method "stored" is unavailable`) {
		t.Fatalf("selectMethod stored error = %v", err)
	}
}

func TestSelectMethodPrefersExactNameOverFriendlyAlias(t *testing.T) {
	methods := []coresecret.AuthMethodSpec{
		{Name: "api_token", Method: coresecret.AuthMethodStored, DisplayName: "Service API token"},
		{Name: "token", Method: coresecret.AuthMethodEnv, DisplayName: "Legacy token"},
	}
	method, err := selectMethod(methods, "token", strings.NewReader(""), bytes.NewBuffer(nil))
	if err != nil {
		t.Fatalf("selectMethod token: %v", err)
	}
	if method.Name != "token" {
		t.Fatalf("method = %#v, want exact token method", method)
	}
}

func TestPrintNativeInfoShowsMethodName(t *testing.T) {
	out := bytes.Buffer{}
	printNativeInfo(&out, resource.PluginRef{Name: "chat", Instance: "main"}, []coresecret.AuthMethodSpec{{
		Name:        "token",
		Method:      coresecret.AuthMethodStored,
		DisplayName: "Chat token",
		Description: "Chat token credentials.",
		Metadata:    map[string]string{"auth_scheme": "Bearer"},
	}})
	got := out.String()
	if !strings.Contains(got, "token - Chat token (stored)") || !strings.Contains(got, "auth_scheme=Bearer") || !strings.Contains(got, "Chat token credentials.") {
		t.Fatalf("info output = %q", got)
	}
}

func TestNewCommandExposesAuthSubcommands(t *testing.T) {
	cmd := NewCommand(CommandOptions{})
	if cmd.Name() != "auth" {
		t.Fatalf("command name = %q, want auth", cmd.Name())
	}
	for _, name := range []string{"connect", "info", "status"} {
		if child, _, err := cmd.Find([]string{name}); err != nil || child == nil || child.Name() != name {
			t.Fatalf("missing subcommand %q child=%v err=%v", name, child, err)
		}
	}
	if child, _, err := cmd.Find([]string{"test"}); err == nil && child != nil && child.Name() == "test" {
		t.Fatalf("unexpected auth test subcommand")
	}
}

func testCommandOptions(refs []resource.PluginRef, plugins ...pluginhost.Plugin) CommandOptions {
	return CommandOptions{TargetRegistry: func(ctx context.Context) ([]pluginhost.AuthTarget, error) {
		return pluginhost.ResolveAuthTargets(ctx, refs, plugins)
	}}
}

type partialAuthPlugin struct {
	name string
}

func (p partialAuthPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: p.name}
}

func (p partialAuthPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p partialAuthPlugin) AuthMethods(context.Context, pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	return []coresecret.AuthMethodSpec{{
		Name:   "api_token",
		Method: coresecret.AuthMethodStored,
		Kind:   coresecret.KindBasic,
		Secret: coresecret.Plugin(p.name, p.name, "token"),
		SetupFields: []coresecret.SetupFieldSpec{
			{Name: "email", Required: true},
			{Name: "token", Required: true},
			{Name: "cloud_id"},
			{Name: "site_url", RequiredGroup: "site_locator"},
			{Name: "base_url", RequiredGroup: "site_locator"},
		},
	}}, nil
}

type fakePlugin struct {
	name string
}

func (p fakePlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: p.name}
}

func (p fakePlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakePlugin) AuthMethods(context.Context, pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	return []coresecret.AuthMethodSpec{
		{
			Name:   "token",
			Method: coresecret.AuthMethodStored,
			Kind:   coresecret.KindBearerToken,
			Secret: coresecret.Plugin(p.name, p.name, "bot_token"),
			SetupFields: []coresecret.SetupFieldSpec{
				{Name: "bot_token", RequiredGroup: "api_token", Sensitive: true},
				{Name: "user_token", RequiredGroup: "api_token", Sensitive: true},
				{Name: "app_token", Sensitive: true},
			},
		},
		{
			Name:   "oauth2",
			Method: coresecret.AuthMethodOAuth2,
			Kind:   coresecret.KindOAuth2Token,
			Secret: coresecret.Plugin(p.name, p.name, "oauth2_token"),
			OAuth2: coresecret.OAuth2Spec{AuthorizeURL: "https://example.test/authorize", TokenURL: "https://example.test/token"},
		},
	}, nil
}

func (p fakePlugin) TestConnection(_ context.Context, ctx pluginhost.Context, req pluginhost.AuthTestRequest, reports chan<- pluginhost.AuthTestReport) error {
	reports <- pluginhost.AuthTestReport{
		Plugin:   p.name,
		Instance: ctx.Ref.InstanceName(),
		Method:   req.Method,
		Check:    "connection",
		Status:   "ok",
	}
	return nil
}
