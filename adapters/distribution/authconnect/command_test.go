package authconnect

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
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
	ref := resource.PluginRef{Name: "slack", Instance: "main"}
	err := runStored(context.Background(), options{
		authPath: dir,
		fields:   []string{"bot_token=slack-bot-token", "app_token=slack-app-token"},
		out:      &out,
	}, ref, coresecret.AuthMethodSpec{
		Name:   "token",
		Method: coresecret.AuthMethodStored,
		Kind:   coresecret.KindBearerToken,
		Secret: coresecret.Plugin("slack", "main", "bot_token"),
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
	bot, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("slack", "main", "bot_token"))
	if err != nil || !ok || bot.Value != "slack-bot-token" {
		t.Fatalf("bot secret = %#v ok=%v err=%v", bot, ok, err)
	}
	app, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("slack", "main", "app_token"))
	if err != nil || !ok || app.Value != "slack-app-token" {
		t.Fatalf("app secret = %#v ok=%v err=%v", app, ok, err)
	}
	if !strings.Contains(out.String(), "Connected slack instance main") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCollectFieldsAcceptsRequiredGroupAlternative(t *testing.T) {
	fields, err := collectFields(options{
		fields: []string{"user_token=slack-user-token"},
		in:     strings.NewReader(""),
		out:    bytes.NewBuffer(nil),
	}, []coresecret.SetupFieldSpec{
		{Name: "bot_token", RequiredGroup: "api_token", Sensitive: true},
		{Name: "user_token", RequiredGroup: "api_token", Sensitive: true},
	})
	if err != nil {
		t.Fatalf("collectFields: %v", err)
	}
	if fields["user_token"] != "slack-user-token" {
		t.Fatalf("user_token = %q", fields["user_token"])
	}
}

func TestTargetsForMappedMethodAndInstance(t *testing.T) {
	plugins := map[string]pluginhost.Plugin{"jira": fakePlugin{name: "jira"}, "slack": fakePlugin{name: "slack"}}
	targets, err := targetsFor(options{
		plugins:   []string{"slack,jira"},
		methods:   []string{"slack=token", "jira=oauth2"},
		instances: []string{"slack=team-chat", "jira=company-a"},
	}, plugins, false)
	if err != nil {
		t.Fatalf("targetsFor: %v", err)
	}
	if len(targets) != 2 || targets[0].plugin != "jira" || targets[0].instance != "company-a" || targets[0].method != "oauth2" || targets[1].plugin != "slack" || targets[1].instance != "team-chat" || targets[1].method != "token" {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestTargetsForRejectsBareMethodWithMultiplePlugins(t *testing.T) {
	plugins := map[string]pluginhost.Plugin{"jira": fakePlugin{name: "jira"}, "slack": fakePlugin{name: "slack"}}
	_, err := targetsFor(options{plugins: []string{"slack,jira"}, methods: []string{"oauth2"}}, plugins, false)
	if err == nil || !strings.Contains(err.Error(), "bare --method") {
		t.Fatalf("targetsFor error = %v, want bare method error", err)
	}
}

func TestRunStatusSummarizesPluginReadiness(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("slack", "team-chat", "user_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "slack-user-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath:  dir,
		plugins:   []string{"slack"},
		instances: []string{"slack=team-chat"},
		methods:   []string{"slack=token"},
		out:       &out,
	}, CommandOptions{NativeRegistry: func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{fakePlugin{name: "slack"}}, nil
	}})
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "slack/team-chat [token] ✓") || strings.Contains(got, "set:") {
		t.Fatalf("status output = %q", got)
	}
}

func TestRunStatusPrintsResolvedFieldsAndRedactsSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("slack", "team-chat", "user_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "sensitive-test-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath:  dir,
		plugins:   []string{"slack"},
		instances: []string{"slack=team-chat"},
		methods:   []string{"slack=token"},
		out:       &out,
	}, CommandOptions{NativeRegistry: func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{fakePlugin{name: "slack"}}, nil
	}})
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "user_token") || !strings.Contains(got, "<redacted>") || strings.Contains(got, "sensitive-test-token") || strings.Contains(got, "=") {
		t.Fatalf("status output = %q", got)
	}
}

func TestPrintResolvedFieldsShowsEnvSourceAndResolvedValue(t *testing.T) {
	t.Setenv("ATLASSIAN_EMAIL", "user@example.invalid")
	out := bytes.Buffer{}
	newStatusRenderer(&out).printResolvedFields(&out, context.Background(), runtimesecret.EnvResolver{Environment: osEnvironment{}}, resource.PluginRef{Name: "jira", Instance: "jira"}, coresecret.AuthMethodSpec{
		Name:   "token",
		Method: coresecret.AuthMethodEnv,
		SetupFields: []coresecret.SetupFieldSpec{{
			Name: "email_env",
			Env:  coresecret.EnvSpec{Aliases: []string{"ATLASSIAN_EMAIL"}},
		}},
	})
	got := out.String()
	if !strings.Contains(got, "email_env    ATLASSIAN_EMAIL") || !strings.Contains(got, "email        user@example.invalid") {
		t.Fatalf("fields output = %q", got)
	}
}

func TestRunStatusRunsConnectivityByDefault(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("slack", "slack", "bot_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "slack-bot-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatusWithOptions(context.Background(), options{
		authPath: dir,
		plugins:  []string{"slack"},
		out:      &out,
	}, CommandOptions{NativeRegistry: func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{fakePlugin{name: "slack"}}, nil
	}})
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
		Ref:   coresecret.Plugin("slack", "slack", "bot_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "slack-bot-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	out := bytes.Buffer{}
	err := runStatusWithOptions(context.Background(), options{
		authPath: dir,
		plugins:  []string{"slack"},
		noTest:   true,
		out:      &out,
	}, CommandOptions{NativeRegistry: func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{fakePlugin{name: "slack"}}, nil
	}})
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
		plugins:  []string{"slack"},
		out:      &out,
	}, CommandOptions{NativeRegistry: func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{fakePlugin{name: "slack"}}, nil
	}})
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "oauth2") || strings.Contains(got, "missing") || !strings.Contains(got, "slack [-] -") {
		t.Fatalf("status output = %q", got)
	}
}

func TestRunStatusShowsPartialRequiredFields(t *testing.T) {
	dir := t.TempDir()
	store := runtimesecret.NewFileStore(dir)
	for _, secret := range []runtimesecret.StoredSecret{
		{Ref: coresecret.Plugin("jira", "jira", "email"), Kind: coresecret.KindBasic, Value: "user@example.invalid"},
		{Ref: coresecret.Plugin("jira", "jira", "token"), Kind: coresecret.KindBasic, Value: "api-token"},
	} {
		if err := store.SaveSecret(context.Background(), secret); err != nil {
			t.Fatalf("SaveSecret: %v", err)
		}
	}
	out := bytes.Buffer{}
	err := runStatus(context.Background(), options{
		authPath: dir,
		plugins:  []string{"jira"},
		out:      &out,
	}, CommandOptions{NativeRegistry: func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{partialAuthPlugin{name: "jira"}}, nil
	}})
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "jira [token] -") || !strings.Contains(got, "missing  site_url or base_url") || strings.Contains(got, "set:") {
		t.Fatalf("status output = %q", got)
	}
}

func TestSelectMethodUsesMethodNameNotKind(t *testing.T) {
	methods := []coresecret.AuthMethodSpec{{
		Name:        "token",
		Method:      coresecret.AuthMethodStored,
		DisplayName: "Slack token",
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
		{Name: "api_token", Method: coresecret.AuthMethodStored, DisplayName: "Atlassian API token"},
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
	printNativeInfo(&out, resource.PluginRef{Name: "slack", Instance: "main"}, []coresecret.AuthMethodSpec{{
		Name:        "token",
		Method:      coresecret.AuthMethodStored,
		DisplayName: "Slack token",
		Description: "Slack token credentials.",
		Metadata:    map[string]string{"auth_scheme": "Bearer"},
	}})
	got := out.String()
	if !strings.Contains(got, "token - Slack token (stored)") || !strings.Contains(got, "auth_scheme=Bearer") || !strings.Contains(got, "Slack token credentials.") {
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
