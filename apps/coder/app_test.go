package coder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fluxplane "github.com/fluxplane/engine"
	distcli "github.com/fluxplane/engine/adapters/distribution/cli"
	distdeploy "github.com/fluxplane/engine/adapters/distribution/deploy"
	"github.com/fluxplane/engine/adapters/resources/appconfig"
	"github.com/fluxplane/engine/apps/launch"
	coreagent "github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/channel"
	corecommand "github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	coreconversation "github.com/fluxplane/engine/core/conversation"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	coreendpoint "github.com/fluxplane/engine/core/endpoint"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	"github.com/fluxplane/engine/core/resource"
	coresecret "github.com/fluxplane/engine/core/secret"
	coreskill "github.com/fluxplane/engine/core/skill"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/orchestration/agentfactory"
	"github.com/fluxplane/engine/orchestration/app"
	clientapi "github.com/fluxplane/engine/orchestration/client"
	"github.com/fluxplane/engine/orchestration/distribution"
	"github.com/fluxplane/engine/orchestration/session"
	"github.com/fluxplane/engine/orchestration/toolprojection"
	"github.com/fluxplane/engine/plugins/bundles/coding"
	"github.com/fluxplane/engine/plugins/integrations/confluence"
	"github.com/fluxplane/engine/plugins/integrations/gitlab"
	"github.com/fluxplane/engine/plugins/integrations/jira"
	"github.com/fluxplane/engine/plugins/integrations/loki"
	"github.com/fluxplane/engine/plugins/integrations/slack"
	"github.com/fluxplane/engine/plugins/native/browser"
	"github.com/fluxplane/engine/plugins/native/discovery"
	"github.com/fluxplane/engine/plugins/native/image"
	"github.com/fluxplane/engine/plugins/native/memory"
	llmagent "github.com/fluxplane/engine/runtime/agent/llmagent"
	"github.com/fluxplane/engine/runtime/authstatus"
	runtimeendpoint "github.com/fluxplane/engine/runtime/endpoint"
	runtimeevidence "github.com/fluxplane/engine/runtime/evidence"
	runtimesecret "github.com/fluxplane/engine/runtime/secret"
	"github.com/fluxplane/engine/runtime/system"
)

func TestCommandDefaultsToREPLAndHasInputFlag(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "interactive session") {
		t.Fatalf("help = %q, want interactive help", help)
	}
	if !strings.Contains(help, "--input") {
		t.Fatalf("help = %q, want input flag", help)
	}
	if !strings.Contains(help, "--usage") {
		t.Fatalf("help = %q, want usage flag", help)
	}
	if !strings.Contains(help, "--provider") {
		t.Fatalf("help = %q, want provider flag", help)
	}
	if !strings.Contains(help, "--yolo") {
		t.Fatalf("help = %q, want yolo flag", help)
	}
	if !strings.Contains(help, "--workspace-root") {
		t.Fatalf("help = %q, want workspace-root flag", help)
	}
	if !strings.Contains(help, "--env-file") {
		t.Fatalf("help = %q, want env-file flag", help)
	}
	if !strings.Contains(help, "--allow-plugin-auth-env") {
		t.Fatalf("help = %q, want allow-plugin-auth-env flag", help)
	}
	if !strings.Contains(help, "--allow-private-network") {
		t.Fatalf("help = %q, want allow-private-network flag", help)
	}
	if !strings.Contains(help, "discover") {
		t.Fatalf("help = %q, want discover command", help)
	}
	if !strings.Contains(help, "auth") {
		t.Fatalf("help = %q, want auth command", help)
	}
	if !strings.Contains(help, "datasource") {
		t.Fatalf("help = %q, want datasource command", help)
	}
	if !strings.Contains(help, "evaluator") {
		t.Fatalf("help = %q, want evaluator command", help)
	}
	if !strings.Contains(help, "app") {
		t.Fatalf("help = %q, want app command", help)
	}
	if !strings.Contains(help, "build") {
		t.Fatalf("help = %q, want build command", help)
	}
	if !strings.Contains(help, "agent") {
		t.Fatalf("help = %q, want agent command", help)
	}
	if !strings.Contains(help, "op") {
		t.Fatalf("help = %q, want op command", help)
	}
	if !strings.Contains(help, "remote") {
		t.Fatalf("help = %q, want remote command", help)
	}
	if !strings.Contains(help, "serve") {
		t.Fatalf("help = %q, want serve command", help)
	}
	if !strings.Contains(help, "shell") {
		t.Fatalf("help = %q, want shell command", help)
	}
	if !strings.Contains(help, "workflow") {
		t.Fatalf("help = %q, want workflow command", help)
	}
	if strings.Contains(help, "--openai-store") {
		t.Fatalf("help = %q, want openai-store removed", help)
	}
	hasDescribe := false
	for _, child := range cmd.Commands() {
		if child.Name() == "connect" {
			t.Fatalf("coder command has connect subcommand, want auth connect")
		}
		if child.Name() == "repl" {
			t.Fatalf("coder command has repl subcommand, want coder to be the repl entrypoint")
		}
		if child.Name() == "describe" {
			hasDescribe = true
		}
	}
	if !hasDescribe {
		t.Fatalf("coder command missing describe subcommand")
	}
}

func TestAuthStatusUsesCoderScopedPlugins(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"auth", "--auth-path", t.TempDir(), "status", "--no-test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"confluence", "gitlab", "jira", "slack"} {
		if !strings.Contains(got, want) {
			t.Fatalf("auth status output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "openai") {
		t.Fatalf("auth status output includes launch-global openai plugin:\n%s", got)
	}
}

func TestCoderAuthTargetRegistryUsesDeclaredPluginRefs(t *testing.T) {
	targets, err := coderAuthTargetRegistry(startupResources{
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: GitLabPlugin}},
		}},
	})(context.Background())
	if err != nil {
		t.Fatalf("coderAuthTargetRegistry: %v", err)
	}
	if len(targets) != 1 || targets[0].Ref.Name != GitLabPlugin {
		t.Fatalf("targets = %#v, want only gitlab", targets)
	}
}

func TestRootRunFlagsDoNotLeakToSubcommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "build", args: []string{"build", "--yolo"}},
		{name: "discover", args: []string{"discover", "--input", "hello"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("Execute error = %v, want unknown flag", err)
			}
		})
	}
}

func TestBuildCommandHelpIncludesDockerBaseTarget(t *testing.T) {
	cmd := newBuildCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"--target", "docker-base", "--tag", "--platform", "--push", "--dry-run"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestBuildCommandRequiresTarget(t *testing.T) {
	cmd := newBuildCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "specify --target docker-base") {
		t.Fatalf("Execute error = %v, want target error", err)
	}
}

func TestResourceRunParsesUnknownFlagsAsInput(t *testing.T) {
	name, opts, err := parseResourceRunArgs([]string{"echo", "--arg", "name=Ada", "--count=3", "--enabled", "--", "tail"})
	if err != nil {
		t.Fatalf("parseResourceRunArgs: %v", err)
	}
	if name != "echo" {
		t.Fatalf("name = %q, want echo", name)
	}
	input, err := commandInput(opts)
	if err != nil {
		t.Fatalf("commandInput: %v", err)
	}
	got, ok := input.(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want map", input)
	}
	if got["name"] != "Ada" || got["count"] != "3" || got["enabled"] != true {
		t.Fatalf("input = %#v, want parsed args", got)
	}
	if args, ok := got["args"].([]string); !ok || len(args) != 1 || args[0] != "tail" {
		t.Fatalf("args = %#v, want tail", got["args"])
	}
}

func TestRunPromptHandlerIgnoresNonRunSlashCommands(t *testing.T) {
	handled, err := newRunPromptHandler(func(context.Context, string) (distribution.Loaded, error) {
		t.Fatalf("loader should not be called")
		return distribution.Loaded{}, nil
	})(context.Background(), "/context", nil, distcli.RunOptions{})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if handled {
		t.Fatalf("handled = true, want false")
	}
}

func TestRunPromptResourceOptionsFromSlashCommand(t *testing.T) {
	inv, ok, err := corecommand.ParseSlash(`/run op upper tail --app ./sample --arg text=hello --count=3 --debug`)
	if err != nil || !ok {
		t.Fatalf("ParseSlash ok=%v err=%v", ok, err)
	}
	opts := resourceRunOptionsFromInvocation(inv, distcli.RunOptions{Yolo: true, Dev: true}, 3)
	if opts.appPath != "./sample" || opts.args["text"] != "hello" || opts.args["count"] != "3" {
		t.Fatalf("opts = %#v, want app path and parsed args", opts)
	}
	if !opts.debug || !opts.yolo || !opts.dev {
		t.Fatalf("opts booleans = debug:%v yolo:%v dev:%v, want inherited/enabled", opts.debug, opts.yolo, opts.dev)
	}
	if len(opts.positional) != 1 || opts.positional[0] != "tail" {
		t.Fatalf("positional = %#v, want tail", opts.positional)
	}
}

func TestRunPromptAgentTextFromSlashPathRemainder(t *testing.T) {
	inv, ok, err := corecommand.ParseSlash(`/run agent writer implement tests`)
	if err != nil || !ok {
		t.Fatalf("ParseSlash ok=%v err=%v", ok, err)
	}
	opts := resourceRunOptionsFromInvocation(inv, distcli.RunOptions{}, 3)
	if got := textInput(opts); got != "implement tests" {
		t.Fatalf("textInput = %q, want path remainder as text", got)
	}
}

func TestRunPromptAppOptionsFromSlashCommand(t *testing.T) {
	inv, ok, err := corecommand.ParseSlash(`/run app ./demo --input hi --debug --max-continuations=7`)
	if err != nil || !ok {
		t.Fatalf("ParseSlash ok=%v err=%v", ok, err)
	}
	if len(inv.Args) == 0 || inv.Args[0] != "./demo" {
		t.Fatalf("invocation args = %#v, want app path argument", inv.Args)
	}
	inv.Args = inv.Args[1:]
	inheritedWorkspace := distribution.WorkspaceConfig{Roots: []distribution.WorkspaceRoot{{Name: "api", Path: "../api", Access: "read_only"}}}
	opts := appRunOptionsFromInvocation(inv, distcli.RunOptions{
		Yolo:           true,
		WorkspaceRoots: []string{"../web"},
		EnvFiles:       []string{".env"},
		Workspace:      inheritedWorkspace,
	})
	if opts.Input != "hi" || !opts.Debug || !opts.Yolo || opts.MaxContinuations != 7 || !opts.MaxContinuationsSet {
		t.Fatalf("opts = %#v, want parsed app run options", opts)
	}
	if len(opts.WorkspaceRoots) != 1 || opts.WorkspaceRoots[0] != "../web" {
		t.Fatalf("workspace roots = %#v, want inherited", opts.WorkspaceRoots)
	}
	if len(opts.EnvFiles) != 1 || opts.EnvFiles[0] != ".env" {
		t.Fatalf("env files = %#v, want inherited", opts.EnvFiles)
	}
	if len(opts.Workspace.Roots) != 1 || opts.Workspace.Roots[0].Access != "read_only" {
		t.Fatalf("workspace = %#v, want inherited structured workspace", opts.Workspace)
	}
}

func TestRunPromptHandlerRunsAppFacet(t *testing.T) {
	runtime := &fakeAppRunRuntime{}
	var loadedPath string
	var out bytes.Buffer
	handled, err := newRunPromptHandler(func(_ context.Context, path string) (distribution.Loaded, error) {
		loadedPath = path
		return distribution.Loaded{
			Distribution: distribution.Distribution{
				Spec: coredistribution.Spec{
					Name:           "sample",
					DefaultSession: fluxplane.SessionRef{Name: "main"},
				},
				Runtime: runtime,
			},
		}, nil
	})(context.Background(), `/run app ./demo --input hi --debug`, nil, distcli.RunOptions{Out: &out, Yolo: true})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if !handled {
		t.Fatalf("handled = false, want true")
	}
	if loadedPath != "./demo" {
		t.Fatalf("loaded path = %q, want ./demo", loadedPath)
	}
	if !runtime.request.Debug || !runtime.request.Yolo {
		t.Fatalf("runtime request = %#v, want debug and inherited yolo", runtime.request)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("output = %q, want rendered app output", out.String())
	}
}

func TestRemoteCommandUsesCoderDefaults(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"remote", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"--local", "--session", defaultRemoteSession, defaultRemoteConversation} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestCommandDoesNotExposeAppGroup(t *testing.T) {
	cmd := NewCommand()
	for _, child := range cmd.Commands() {
		if child.Name() == "app" {
			t.Fatalf("coder command unexpectedly exposes app command")
		}
	}
}

func TestAppCommandHasAppLifecycleActions(t *testing.T) {
	cmd := launch.NewAppCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"init", "run", "serve", "build", "deploy", "undeploy", "config", "healthcheck"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestDatasourceCommandHasIndexActions(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"datasource", "index", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"build", "embed", "status", "clear"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestDatasourceIndexBuildHasPluginAuthEnvFlag(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"datasource", "index", "build", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if help := out.String(); !strings.Contains(help, "--allow-plugin-auth-env") {
		t.Fatalf("help = %q, want allow-plugin-auth-env flag", help)
	}
}

func TestAppInitCreatesMinimalManifest(t *testing.T) {
	dir := t.TempDir()
	cmd := launch.NewAppCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	manifestPath := filepath.Join(dir, "agentsdk.app.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	file, err := appconfig.DecodeFile(manifestPath, data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Apps) != 1 || string(file.Bundle.Apps[0].Name) != filepath.Base(dir) {
		t.Fatalf("apps = %#v, want app named after directory", file.Bundle.Apps)
	}
	if !strings.Contains(out.String(), "created ") {
		t.Fatalf("output = %q, want created message", out.String())
	}
}

func TestAppRunHelpIncludesLaunchFlags(t *testing.T) {
	cmd := launch.NewAppCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"run [path]", "--session", "--conversation", "--provider", "--model", "--input", "--debug", "--usage", "--yolo", "--connectors-path", "--allow-plugin-auth-env", "--workspace-root"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestAppRunForwardsWorkspaceRootFlags(t *testing.T) {
	runtime := &fakeAppRunRuntime{}
	cmd := launch.NewAppCommandWithOptions(launch.AppCommandOptions{
		RunLoader: func(context.Context, string) (distribution.Loaded, error) {
			return distribution.Loaded{
				Distribution: distribution.Distribution{
					Spec: coredistribution.Spec{
						Name:           "sample",
						DefaultSession: fluxplane.SessionRef{Name: "main"},
					},
					Runtime: runtime,
				},
			}, nil
		},
	})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"run", "--input", "hello", "--workspace-root", "api=../api"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	roots := runtime.request.Launch.Workspace.Roots
	if len(roots) != 1 || roots[0].Name != "api" || roots[0].Path != "../api" {
		t.Fatalf("workspace roots = %#v, want api=../api", roots)
	}
}

func TestAppServeForwardsModelSelection(t *testing.T) {
	var got launch.Options
	cmd := launch.NewAppCommandWithOptions(launch.AppCommandOptions{
		ServeRunner: func(_ context.Context, opts launch.Options) error {
			got = opts
			return nil
		},
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"serve", "--provider", "codex", "--model", "gpt-5.5", "--health-addr", "127.0.0.1:18080", "examples/slack-bot"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.AppDir != "examples/slack-bot" || got.Provider != "codex" || got.Model != "gpt-5.5" || got.HealthAddr != "127.0.0.1:18080" {
		t.Fatalf("serve options = %#v, want app dir/provider/model", got)
	}
}

func TestAppHealthcheckCommandUsesStatusEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/control/status" {
			t.Fatalf("path = %q, want /control/status", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	cmd := launch.NewAppCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"healthcheck", "--url", server.URL + "/control/status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out.String()) != "ok" {
		t.Fatalf("output = %q, want ok", out.String())
	}
}

type fakeAppRunRuntime struct {
	request distribution.OpenRequest
}

func (r *fakeAppRunRuntime) OpenSession(_ context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	r.request = req
	info := clientapi.SessionInfo{
		Session:      req.Session,
		Thread:       corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch},
		Conversation: req.Conversation,
	}
	return fakeAppRunSession{info: info}, nil
}

type fakeAppRunSession struct {
	info clientapi.SessionInfo
}

func (s fakeAppRunSession) Info() clientapi.SessionInfo { return s.info }

func (s fakeAppRunSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	return fakeAppRunHandle{info: s.info, submission: submission}, nil
}

func (s fakeAppRunSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s fakeAppRunSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s fakeAppRunSession) Close(context.Context) error { return nil }

type fakeAppRunHandle struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (r fakeAppRunHandle) ID() clientapi.RunID { return "run-1" }

func (r fakeAppRunHandle) Session() clientapi.SessionInfo { return r.info }

func (r fakeAppRunHandle) Submission() clientapi.Submission { return r.submission }

func (r fakeAppRunHandle) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}

func (r fakeAppRunHandle) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r fakeAppRunHandle) Err() error { return nil }

func (r fakeAppRunHandle) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{
		RunID:      r.ID(),
		Session:    r.info,
		Submission: r.submission,
		Input:      &session.InputResult{Status: session.InputStatusOK},
		Outbound: &channel.Outbound{
			Kind:    channel.OutboundMessage,
			Message: &channel.Message{Content: "ok"},
		},
	}, nil
}

func TestAppBuildHelpIncludesDockerFlags(t *testing.T) {
	cmd := launch.NewAppCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"build [path]", "--target", "all|binary|dockerfile|docker-image|docker-compose|kubernetes", "--image", "--out", "--docker", "--tag", "--platform", "--push", "--dry-run", "--force", "--base-image", "--connectors-path", "--allow-plugin-auth-env", "--provider", "--model", "--effort"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestAppBuildRejectsUnsupportedTarget(t *testing.T) {
	cmd := launch.NewAppCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"build", "--target", "nope", "."})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unsupported app target "nope"`) {
		t.Fatalf("Execute error = %v, want unsupported target error", err)
	}
}

func TestAppDeployHelpIncludesDockerComposeTarget(t *testing.T) {
	cmd := launch.NewAppCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"deploy", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"deploy [path]", "--target", "docker-compose|kubernetes", "--image", "--base-image", "--connectors-path", "--allow-plugin-auth-env", "--provider", "--model", "--effort", "--dry-run", "--force", "--detach", "--namespace", "--registry-mode", "--registry"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestAppDeployDefaultsToDockerCompose(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "agentsdk.app.yaml", `
kind: app
name: sample
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	var calls []string
	runner := distdeploy.CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	})
	cmd := launch.NewAppDeployCommandWithRunner(runner)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--dry-run", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run calls = %#v, want none", calls)
	}
}

func TestAppUndeployHelpIncludesTargets(t *testing.T) {
	cmd := launch.NewAppCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"undeploy", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"undeploy [path]", "--target", "docker-compose|kubernetes", "--namespace", "--dry-run", "--volumes"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestAppUndeployDefaultsToDockerCompose(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "agentsdk.app.yaml", `
kind: app
name: sample
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var calls []string
	runner := distdeploy.CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	})
	cmd := launch.NewAppUndeployCommandWithRunner(runner)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--dry-run", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run calls = %#v, want none", calls)
	}
	want := "command=docker compose -f " + filepath.Join(dir, "docker-compose.yaml") + " down"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestAppUndeployRejectsUnsupportedTarget(t *testing.T) {
	cmd := launch.NewAppUndeployCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--target", "nope", "."})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `app undeploy: unsupported target "nope"`) {
		t.Fatalf("Execute error = %v, want unsupported target error", err)
	}
}

func TestAppConfigShowRendersLoadedDistribution(t *testing.T) {
	root := t.TempDir()
	cmd := launch.NewAppCommandWithOptions(launch.AppCommandOptions{
		ConfigLoader: func(_ context.Context, path string) (distribution.Loaded, error) {
			if path != "." {
				t.Fatalf("path = %q, want .", path)
			}
			return distribution.Loaded{
				Root:     root,
				Manifest: filepath.Join(root, "agentsdk.app.yaml"),
				Distribution: distribution.Distribution{
					Spec: coredistribution.Spec{
						Name:        "sample",
						Description: "Sample app.",
					},
				},
			}, nil
		},
	})
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"config", "show", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{`"distribution"`, `"name": "sample"`, `"description": "Sample app."`} {
		if !strings.Contains(text, want) {
			t.Fatalf("config show output missing %q:\n%s", want, text)
		}
	}
}

func TestAppConfigShowRequiresManifest(t *testing.T) {
	root := t.TempDir()
	cmd := launch.NewAppCommandWithOptions(launch.AppCommandOptions{
		ConfigLoader: func(context.Context, string) (distribution.Loaded, error) {
			return distribution.Loaded{Root: root}, nil
		},
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"config", "show"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no app manifest found") {
		t.Fatalf("Execute error = %v, want missing manifest", err)
	}
}

func TestAppConfigEditOpensLoadedManifest(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "agentsdk.app.yaml")
	var edited string
	cmd := launch.NewAppCommandWithOptions(launch.AppCommandOptions{
		ConfigLoader: func(context.Context, string) (distribution.Loaded, error) {
			return distribution.Loaded{Root: root, Manifest: manifest}, nil
		},
		EditorRunner: func(_ context.Context, path string, _ io.Reader, _, _ io.Writer) error {
			edited = path
			return nil
		},
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"config", "edit"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if edited != manifest {
		t.Fatalf("edited = %q, want %q", edited, manifest)
	}
}

func TestServeCommandHasWorkspaceRootFlag(t *testing.T) {
	cmd := newServeCommand(loadStartupResources(context.Background()))
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "--workspace-root") {
		t.Fatalf("help = %q, want workspace-root flag", out.String())
	}
	if !strings.Contains(out.String(), "--env-file") {
		t.Fatalf("help = %q, want env-file flag", out.String())
	}
}

func TestDescribeCommandRendersStaticCoderDistribution(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{`"distribution"`, `"name": "coder"`, `"apps"`, `"sessions"`, `"agents"`, `"plugins"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe output missing %q:\n%s", want, text)
		}
	}
}

func TestDescribeCommandRendersPluginContributionsInTree(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"plugins",
		CodingPlugin,
		"Plugin contributions:",
		"context_providers",
		"agents.md",
		"operations",
		"operation_sets",
		"browser",
		"code",
		"filesystem",
		"file_create",
		"file_edit",
		TaskPlugin,
		"agents",
		"explorer",
		"worker",
		SkillsPlugin,
		"datasources",
		"skills",
		ImagePlugin,
		"image_generate",
		"image_understand",
		"image_providers",
		"tool sets",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe tree output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "contributes:") {
		t.Fatalf("describe tree output contains nested contribution summary:\n%s", text)
	}
}

func TestStartupResourcesAppearInDescribeAndDiscover(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, root)
	writeFile(t, root, ".agents/skills/project-skill/SKILL.md", `---
name: project-skill
description: Project skill.
triggers: [project smoke]
---
Project skill body.
`)
	writeFile(t, home, ".agents/skills/home-skill/SKILL.md", `---
name: home-skill
description: Home skill.
triggers: [home smoke]
---
Home skill body.
`)
	writeFile(t, home, ".claude/skills/claude-skill/SKILL.md", `---
name: claude-skill
description: Claude skill.
triggers: [claude smoke]
---
Claude skill body.
`)
	writeFile(t, home, ".claude/agents/ticket-implementer.md", `---
name: ticket-implementer
description: Ticket implementation agent.
tools: Bash, Glob, Grep, Read, Edit, Write, Skill
model: sonnet
memory: project
---
Implement a ticket.
`)
	writeFile(t, home, ".claude/skills/dex/SKILL.md", `---
name: dex
description: Run dex CLI commands.
user-invocable: true
---
Dex skill body.
`)

	for _, args := range [][]string{
		{"describe", "-o", "json"},
		{"discover", "-o", "json"},
	} {
		cmd := NewCommand()
		out := bytes.Buffer{}
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute %v: %v", args, err)
		}
		text := out.String()
		for _, want := range []string{"project-skill", "home-skill", "claude-skill", "ticket-implementer", "dex", ".claude"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%v output missing %q:\n%s", args, want, text)
			}
		}
	}
}

func TestCoderStartupClaudeSkillsHaveActivationState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, root)
	writeFile(t, home, ".claude/agents/ticket-implementer.md", `---
name: ticket-implementer
description: Ticket implementation agent.
tools: Bash, Glob, Grep, Read, Edit, Write, Skill
model: sonnet
memory: project
---
Implement a ticket.
`)
	writeFile(t, home, ".claude/skills/crm/SKILL.md", `---
name: crm
description: Use CRM tools.
user-invocable: true
---
CRM skill body.
`)
	writeFile(t, home, ".claude/skills/dex/SKILL.md", `---
name: dex
description: Run dex CLI commands.
user-invocable: true
---
Dex skill body.
`)

	startup := loadStartupResources(ctx)
	if len(startup.Diagnostics) > 0 {
		t.Fatalf("startup diagnostics = %#v", startup.Diagnostics)
	}
	if !bundlesContainSkill(startup.Bundles, "crm") || !bundlesContainSkill(startup.Bundles, "dex") {
		t.Fatalf("startup bundles missing claude skills: %#v", startup.Bundles)
	}

	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	calls := 0
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		calls++
		if calls == 1 {
			return llmagent.OperationResponse(coreagent.OperationRequest{
				Operation: operation.Ref{Name: "skill"},
				Input: map[string]any{"actions": []map[string]any{
					{"action": "activate", "skill": "crm"},
					{"action": "activate", "skill": "dex"},
				}},
			}), nil
		}
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: startup.Bundles,
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "claude-skill-state-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("load crm and dex skill"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Input == nil || result.Input.Status != session.InputStatusOK {
		t.Fatalf("result input = %#v", result.Input)
	}
	effects := result.Input.Effects
	if result.Input.Effect != nil {
		effects = append(effects, *result.Input.Effect)
	}
	if len(effects) == 0 {
		t.Fatalf("result has no skill operation effects: %#v", result)
	}
	text := ""
	for _, effect := range effects {
		if effect.Result.IsError() {
			t.Fatalf("skill effect failed: %#v", effect.Result)
		}
		text += "\n" + fmt.Sprintf("%#v", effect.Result.Output)
	}
	for _, want := range []string{"active skills", "crm", "dex"} {
		if !strings.Contains(text, want) {
			t.Fatalf("skill effect output missing %q:\n%s", want, text)
		}
	}
}

func TestDescribeAgentCommandRendersStaticCoderAgent(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe", "agent", AgentName, "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{`"agent"`, `"name": "coder"`, `"operations"`, `"sessions"`, `"apps"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe agent output missing %q:\n%s", want, text)
		}
	}
}

func TestCompositionContextCommandRendersAgentsMD(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agent Rules\n\nUse system context.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel: llmagent.StaticModel{Response: llmagent.MessageResponse("ok")},
		Channel:  channel.Ref{Name: "local"},
		Caller:   policy.Caller{Kind: policy.CallerUser},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "context-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithCommand(corecommand.Invocation{
		Path:  corecommand.Path{"context"},
		Input: map[string]any{"fresh": true, "key": coding.AgentsContextProvider},
	}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	result, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Command == nil || result.Command.Status != session.CommandStatusOK {
		t.Fatalf("command result = %#v", result.Command)
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		t.Fatalf("outbound = %#v, want context output", result.Outbound)
	}
	output := result.Outbound.Message.Content
	if !strings.Contains(fmt.Sprint(output), "Use system context.") || !strings.Contains(fmt.Sprint(output), "## system") {
		t.Fatalf("output = %q, want AGENTS.md system context", output)
	}
}

func TestCoderAutoActivatesTriggeredSkillAndReference(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var requests []llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		requests = append(requests, req)
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{
			Bundle(),
			{
				Source: resource.SourceRef{ID: "test:skills", Scope: resource.ScopeProject, Location: "test/skills"},
				Skills: []coreskill.Spec{{
					Name:        "smoke-skill",
					Description: "Smoke skill.",
					Body:        "SKILL_BODY_VISIBLE",
					Triggers:    []string{"smoke trigger"},
					References: []coreskill.ReferenceSpec{{
						Path:     "references/detail.md",
						Body:     "REFERENCE_BODY_VISIBLE",
						Triggers: []string{"detail trigger"},
					}},
				}},
			},
		},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "skill-trigger-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("please use smoke trigger and detail trigger now"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(requests))
	}
	text := requestText(requests[0])
	for _, want := range []string{"SKILL_BODY_VISIBLE", "REFERENCE_BODY_VISIBLE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("model request missing %q:\n%s", want, text)
		}
	}

}

func bundlesContainSkill(bundles []resource.ContributionBundle, name string) bool {
	for _, bundle := range bundles {
		for _, spec := range bundle.Skills {
			if string(spec.Name) == name {
				return true
			}
		}
	}
	return false
}

func TestToolProjectionIncludesTaskOperations(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for _, want := range []string{
		"project_inventory",
		"file_read",
		"file_create",
		"file_edit",
		"file_delete",
		"git_status",
		"shell_exec",
		"go_outline",
		"markdown_outline",
	} {
		if !operationCatalogContains(composition.OperationCatalog, want) {
			t.Fatalf("operation catalog missing %q", want)
		}
	}
	cfg := ToolProjectionConfig()
	cfg.Commands = composition.CommandCatalog
	cfg.Operations = composition.OperationCatalog
	cfg.ToolSets = composition.ToolSetCatalog
	cfg.Caller = policy.Caller{Kind: policy.CallerAgent}
	cfg.Trust = policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified}

	projected := toolprojection.Project(cfg)
	names := map[string]bool{}
	for _, spec := range projected.Tools {
		names[string(spec.Name)] = true
	}
	for _, want := range []string{
		"project_inventory",
		"file_read",
		"file_create",
		"file_edit",
		"file_delete",
		"git_status",
		"shell_exec",
		"go_outline",
		"markdown_outline",
		"task_create",
		"task_modify",
		"task_run",
		"image",
	} {
		if !names[want] {
			t.Fatalf("projected tool names missing %q: %#v", want, names)
		}
	}
	for _, unwanted := range []string{"image_generate", "image_understand", "image_providers"} {
		if names[unwanted] {
			t.Fatalf("projected tool names include %q, want single image action tool: %#v", unwanted, names)
		}
	}
}

func TestCoderSessionProjectsCoreToolsToModel(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", "file_read", "shell_exec")
	assertRequestTools(t, request, memory.RetrieveOp, memory.MemorizeOp, memory.ForgetOp, memory.OrganizeOp, image.GenerateOp, image.ProvidersOp)
	assertRequestToolsAbsent(t, request, "go_outline", "markdown_outline", "loki_query", "mysql_query", "endpoint_list", "gitlab_mr", "jira_issue_search", "channel_send", browser.OpenOp, image.Name, image.UnderstandOp)
}

func TestCoderSessionActivatesIntegrationToolsFromAuthEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	authObserver := authstatus.NewObserver([]authstatus.Target{
		{
			Ref: resource.PluginRef{Name: gitlab.Name},
			Methods: []coresecret.AuthMethodSpec{{
				Name:   "personal_access_token",
				Method: coresecret.AuthMethodEnv,
				Kind:   coresecret.KindAPIKey,
				Env:    coresecret.EnvSpec{Name: "GITLAB_TOKEN"},
			}},
		},
		{
			Ref: resource.PluginRef{Name: jira.Name},
			Methods: []coresecret.AuthMethodSpec{{
				Name:   "api_token",
				Method: coresecret.AuthMethodEnv,
				Kind:   coresecret.KindAPIKey,
				Env:    coresecret.EnvSpec{Name: "JIRA_TOKEN"},
			}},
		},
	}, runtimesecret.EnvResolver{Environment: testEnvironment{values: map[string]string{
		"GITLAB_TOKEN": "gitlab-token",
		"JIRA_TOKEN":   "jira-token",
	}}})
	composition, err := app.Compose(app.Config{
		Bundles:              []fluxplane.ResourceBundle{Bundle()},
		Plugins:              localPlugins(sys),
		EnvironmentObservers: []runtimeevidence.Observer{authObserver},
		AssertionDerivers:    []runtimeevidence.AssertionDeriver{authstatus.NewAssertionDeriver()},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "integration-auth-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "gitlab_mr", "gitlab_commit", "jira_issue_search")
	assertRequestToolsAbsent(t, request, "channel_send", "slack_report_progress")
}

func TestCoderSessionActivatesGoToolsFromProjectEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "go-evidence-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", "go_outline", "go_project")
}

func TestCoderSessionActivatesLokiToolsFromEndpointEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, err := composition.Endpoints.Put(runtimeendpoint.Record{Spec: coreendpoint.Spec{Name: "loki-dev", URL: "http://loki:3100", Product: loki.Name}}); err != nil {
		t.Fatalf("put endpoint: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "endpoint-evidence-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", "loki_query", "loki_recent_logs", discovery.EndpointListOp, discovery.DiscoverOp)
	assertRequestToolsAbsent(t, request, "mysql_query")
}

func TestCoderSessionActivatesBrowserToolsFromAvailability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true, Browser: fakeBrowserManager{}})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "browser-availability-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", browser.OpenOp, browser.ReadOp, browser.ScreenshotOp)
}

func TestCoderSessionDoesNotActivateBrowserToolsWithoutAvailability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "browser-unavailable-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestToolsAbsent(t, request, browser.OpenOp, browser.ReadOp, browser.ScreenshotOp)
}

func TestCoderSessionActivatesImageToolsFromProviderAvailability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "image-availability-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", image.GenerateOp, image.ProvidersOp)
	assertRequestToolsAbsent(t, request, image.UnderstandOp)
}

func TestCoderSessionDoesNotActivateImageUnderstandingWithoutProvider(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "image-understand-unconfigured-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, image.GenerateOp, image.ProvidersOp)
	assertRequestToolsAbsent(t, request, image.UnderstandOp)
}

func TestCoderSessionActivatesMemoryMutationToolsFromStorageAvailability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	composition, err := app.Compose(app.Config{
		Bundles: []fluxplane.ResourceBundle{Bundle()},
		Plugins: localPlugins(sys),
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := fluxplane.NewFromComposition(composition, fluxplane.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser, Principal: policy.Principal{Kind: "user", ID: "local@test"}},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "memory-availability-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, memory.RetrieveOp, memory.MemorizeOp, memory.ForgetOp, memory.OrganizeOp)
}

func TestCoderLaunchProjectsCoreToolsToModel(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	runtime, err := launch.Launch(ctx, launch.RuntimeOptions{
		Root:                root,
		Spec:                Distribution().Spec,
		Bundles:             []resource.ContributionBundle{Bundle()},
		Plugins:             localPlugins,
		ToolProjection:      ToolProjectionConfig(),
		ModelResolver:       agentfactory.ModelResolverFunc(func(context.Context, coreagent.Spec) (llmagent.Model, error) { return model, nil }),
		AllowPrivateNetwork: true,
		Yolo:                true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()
	sessionHandle, err := runtime.Service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "launch-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", "file_read", "shell_exec")
}

func TestCoderLaunchActivatesIntegrationToolsAndDatasourcesFromAuthPath(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	authPath := t.TempDir()
	store := runtimesecret.NewFileStore(authPath)
	for _, secret := range []runtimesecret.StoredSecret{
		{Ref: coresecret.Plugin(gitlab.Name, gitlab.Name, "token"), Kind: coresecret.KindAPIKey, Value: "gitlab-token"},
		{Ref: coresecret.Plugin(gitlab.Name, gitlab.Name, "url"), Kind: coresecret.KindAPIKey, Value: "https://gitlab.example.invalid"},
		{Ref: coresecret.Plugin(jira.Name, jira.Name, "email"), Kind: coresecret.KindBasic, Value: "bot@example.invalid"},
		{Ref: coresecret.Plugin(jira.Name, jira.Name, "token"), Kind: coresecret.KindBasic, Value: "jira-token"},
		{Ref: coresecret.Plugin(jira.Name, jira.Name, "site_url"), Kind: coresecret.KindBasic, Value: "https://example.atlassian.invalid"},
		{Ref: coresecret.Plugin(confluence.Name, confluence.Name, "email"), Kind: coresecret.KindBasic, Value: "bot@example.invalid"},
		{Ref: coresecret.Plugin(confluence.Name, confluence.Name, "token"), Kind: coresecret.KindBasic, Value: "confluence-token"},
		{Ref: coresecret.Plugin(confluence.Name, confluence.Name, "site_url"), Kind: coresecret.KindBasic, Value: "https://example.atlassian.invalid"},
		{Ref: slack.BotTokenSecretRef(resource.PluginRef{Name: slack.Name}), Kind: coresecret.KindBearerToken, Value: "slack-bot-token"},
	} {
		if err := store.SaveSecret(ctx, secret); err != nil {
			t.Fatalf("SaveSecret(%s): %v", secret.Ref.ResourceName(), err)
		}
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	runtime, err := launch.Launch(ctx, launch.RuntimeOptions{
		Root:                root,
		Spec:                Distribution().Spec,
		Bundles:             []resource.ContributionBundle{Bundle()},
		Plugins:             localPlugins,
		ToolProjection:      ToolProjectionConfig(),
		ModelResolver:       agentfactory.ModelResolverFunc(func(context.Context, coreagent.Spec) (llmagent.Model, error) { return model, nil }),
		AllowPrivateNetwork: true,
		Yolo:                true,
		AuthPath:            authPath,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()
	sessionHandle, err := runtime.Service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "launch-integration-auth-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "gitlab_mr", "gitlab_commit", "jira_issue_search")
	assertRequestToolsAbsent(t, request, slack.ChannelSendOp, slack.ReportProgressOp)
	text := requestText(request)
	for _, want := range []string{"gitlab", "jira", "confluence", "slack"} {
		if !strings.Contains(text, want) {
			t.Fatalf("request text missing datasource %q:\n%s", want, text)
		}
	}
}

func TestCoderLaunchActivatesIntegrationToolsAndDatasourcesFromProcessAuthEnv(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GITLAB_TOKEN", "gitlab-token")
	t.Setenv("GITLAB_URL", "https://gitlab.example.invalid")
	t.Setenv("JIRA_EMAIL", "bot@example.invalid")
	t.Setenv("JIRA_API_TOKEN", "jira-token")
	t.Setenv("CONFLUENCE_EMAIL", "bot@example.invalid")
	t.Setenv("CONFLUENCE_API_TOKEN", "confluence-token")
	t.Setenv("ATLASSIAN_CLOUD_ID", "cloud-1")
	t.Setenv("SLACK_BOT_TOKEN", "slack-bot-token")
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	runtime, err := launch.Launch(ctx, launch.RuntimeOptions{
		Root:                root,
		Spec:                Distribution().Spec,
		Bundles:             []resource.ContributionBundle{Bundle()},
		Plugins:             localPlugins,
		ToolProjection:      ToolProjectionConfig(),
		ModelResolver:       agentfactory.ModelResolverFunc(func(context.Context, coreagent.Spec) (llmagent.Model, error) { return model, nil }),
		AllowPrivateNetwork: true,
		Yolo:                true,
		AuthPath:            filepath.Join(t.TempDir(), "missing-auth-store"),
		AllowPluginAuthEnv:  true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()
	sessionHandle, err := runtime.Service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "launch-integration-auth-env-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "gitlab_mr", "gitlab_commit", "jira_issue_search")
	assertRequestToolsAbsent(t, request, slack.ChannelSendOp, slack.ReportProgressOp)
	text := requestText(request)
	for _, want := range []string{"gitlab", "jira", "confluence", "slack"} {
		if !strings.Contains(text, want) {
			t.Fatalf("request text missing datasource %q:\n%s", want, text)
		}
	}
}

func TestCoderLaunchDoesNotActivateIntegrationToolsFromProcessAuthEnvByDefault(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GITLAB_TOKEN", "gitlab-token")
	t.Setenv("GITLAB_URL", "https://gitlab.example.invalid")
	t.Setenv("JIRA_EMAIL", "bot@example.invalid")
	t.Setenv("JIRA_API_TOKEN", "jira-token")
	t.Setenv("CONFLUENCE_EMAIL", "bot@example.invalid")
	t.Setenv("CONFLUENCE_API_TOKEN", "confluence-token")
	t.Setenv("ATLASSIAN_CLOUD_ID", "cloud-1")
	t.Setenv("SLACK_BOT_TOKEN", "slack-bot-token")
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	runtime, err := launch.Launch(ctx, launch.RuntimeOptions{
		Root:                root,
		Spec:                Distribution().Spec,
		Bundles:             []resource.ContributionBundle{Bundle()},
		Plugins:             localPlugins,
		ToolProjection:      ToolProjectionConfig(),
		ModelResolver:       agentfactory.ModelResolverFunc(func(context.Context, coreagent.Spec) (llmagent.Model, error) { return model, nil }),
		AllowPrivateNetwork: true,
		Yolo:                true,
		AuthPath:            filepath.Join(t.TempDir(), "missing-auth-store"),
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()
	sessionHandle, err := runtime.Service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "launch-integration-auth-env-default-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestToolsAbsent(t, request, "gitlab_mr", "gitlab_commit", "jira_issue_search", slack.ChannelSendOp, slack.ReportProgressOp)
}

func TestCoderStartupLaunchProjectsCoreToolsToModel(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	chdir(t, root)
	writeFile(t, root, ".agents/skills/project-skill/SKILL.md", `---
name: project-skill
description: Project skill.
---
Project skill body.
`)
	writeFile(t, home, ".claude/skills/user-skill/SKILL.md", `---
name: user-skill
description: User skill.
---
User skill body.
`)
	startup := loadStartupResources(ctx)
	if len(startup.Diagnostics) > 0 {
		t.Fatalf("startup diagnostics = %#v", startup.Diagnostics)
	}
	var request llmagent.Request
	model := llmagent.ModelFunc(func(_ context.Context, req llmagent.Request) (llmagent.Response, error) {
		request = req
		return llmagent.MessageResponse("ok"), nil
	})
	runtime, err := launch.Launch(ctx, launch.RuntimeOptions{
		Root:                startup.Root,
		Spec:                Distribution().Spec,
		Bundles:             startup.Bundles,
		Plugins:             localPlugins,
		ToolProjection:      ToolProjectionConfig(),
		ModelResolver:       agentfactory.ModelResolverFunc(func(context.Context, coreagent.Spec) (llmagent.Model, error) { return model, nil }),
		AllowPrivateNetwork: true,
		Yolo:                true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()
	sessionHandle, err := runtime.Service.Open(ctx, fluxplane.OpenRequest{
		Session:      fluxplane.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "startup-launch-tool-projection-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, fluxplane.NewSubmission().WithText("list tools"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := run.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	assertRequestTools(t, request, "project_inventory", "file_read", "shell_exec")
}

func operationCatalogContains(catalog session.OperationCatalog, name string) bool {
	for _, binding := range catalog {
		if binding.ID.Name == name {
			return true
		}
	}
	return false
}

func assertRequestTools(t *testing.T, request llmagent.Request, want ...string) {
	t.Helper()
	names := map[string]bool{}
	for _, spec := range request.Tools {
		names[string(spec.Name)] = true
	}
	for _, name := range want {
		if !names[name] {
			t.Fatalf("model request tools missing %q: tools=%#v agent=%q agent_ops=%d", name, names, request.Agent.Name, len(request.Agent.Operations))
		}
	}
}

func assertRequestToolsAbsent(t *testing.T, request llmagent.Request, unwanted ...string) {
	t.Helper()
	names := map[string]bool{}
	for _, spec := range request.Tools {
		names[string(spec.Name)] = true
	}
	for _, name := range unwanted {
		if names[name] {
			t.Fatalf("model request tools include %q: tools=%#v agent=%q agent_ops=%d", name, names, request.Agent.Name, len(request.Agent.Operations))
		}
	}
}

type fakeBrowserManager struct{}

func (fakeBrowserManager) Open(context.Context, system.BrowserOpenRequest) (system.BrowserOpenResult, error) {
	return system.BrowserOpenResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Navigate(context.Context, system.BrowserSessionRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Click(context.Context, system.BrowserSelectorRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Type(context.Context, system.BrowserTypeRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Select(context.Context, system.BrowserSelectRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Read(context.Context, system.BrowserReadRequest) (system.BrowserReadResult, error) {
	return system.BrowserReadResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example", Text: "Example"}, nil
}

func (fakeBrowserManager) Screenshot(context.Context, system.BrowserSessionRequest) (system.BrowserArtifact, error) {
	return system.BrowserArtifact{SessionID: "browser-test", Path: "screenshot.png", MediaType: "image/png"}, nil
}

func (fakeBrowserManager) Evaluate(context.Context, system.BrowserEvaluateRequest) (system.BrowserEvaluateResult, error) {
	return system.BrowserEvaluateResult{SessionID: "browser-test", Value: true}, nil
}

func (fakeBrowserManager) Wait(context.Context, system.BrowserWaitRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Scroll(context.Context, system.BrowserScrollRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Hover(context.Context, system.BrowserSelectorRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Back(context.Context, system.BrowserSessionRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) Forward(context.Context, system.BrowserSessionRequest) (system.BrowserPageResult, error) {
	return system.BrowserPageResult{SessionID: "browser-test", URL: "https://example.com", Title: "Example"}, nil
}

func (fakeBrowserManager) PDF(context.Context, system.BrowserSessionRequest) (system.BrowserArtifact, error) {
	return system.BrowserArtifact{SessionID: "browser-test", Path: "page.pdf", MediaType: "application/pdf"}, nil
}

func (fakeBrowserManager) Close(context.Context, system.BrowserSessionRequest) error {
	return nil
}

func TestBundleAppliesModelOverride(t *testing.T) {
	bundle := BundleWithModel("codex", "gpt-test")
	if bundle.Apps[0].Model.Model != "gpt-test" {
		t.Fatalf("app model = %q, want gpt-test", bundle.Apps[0].Model.Model)
	}
	if bundle.Apps[0].Model.Provider != "codex" {
		t.Fatalf("app provider = %q, want codex", bundle.Apps[0].Model.Provider)
	}
	if bundle.Agents[0].Inference.Model != "gpt-test" {
		t.Fatalf("agent model = %q, want gpt-test", bundle.Agents[0].Inference.Model)
	}
	if bundle.Agents[0].Name != AgentName {
		t.Fatalf("agent name = %q", bundle.Agents[0].Name)
	}
}

func TestDefaultModel(t *testing.T) {
	if DefaultModel != "gpt-5.5" {
		t.Fatalf("DefaultModel = %q, want gpt-5.5", DefaultModel)
	}
}

type testEnvironment struct {
	values map[string]string
}

func (e testEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[key]
	return value, ok, nil
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func requestText(req llmagent.Request) string {
	var parts []string
	appendBlocks := func(blocks []corecontext.Block) {
		for _, block := range blocks {
			if block.Content != "" {
				parts = append(parts, block.Content)
			}
		}
	}
	appendItems := func(items []coreconversation.Item) {
		for _, item := range items {
			if item.Content != nil {
				parts = append(parts, fmt.Sprint(item.Content))
			}
		}
	}
	appendBlocks(req.Context)
	if req.Transcript != nil {
		appendItems(req.Transcript.Items)
		appendItems(req.Transcript.NewItems)
	}
	return strings.Join(parts, "\n")
}
