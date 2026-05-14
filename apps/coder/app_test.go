package coder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/channel"
	corecommand "github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/toolprojection"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/system"
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
	if strings.Contains(help, "--openai-store") {
		t.Fatalf("help = %q, want openai-store removed", help)
	}
	hasDescribe := false
	for _, child := range cmd.Commands() {
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
		PlanExecPlugin,
		"agents",
		"explorer",
		"worker",
		SkillsPlugin,
		"datasources",
		"skills",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe tree output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "contributes:") {
		t.Fatalf("describe tree output contains nested contribution summary:\n%s", text)
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
		Bundles: []agentruntime.ResourceBundle{Bundle()},
		Plugins: []pluginhost.Plugin{
			codingplugin.New(sys),
			planexecplugin.New(),
			skillplugin.New(),
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel: llmagent.StaticModel{Response: llmagent.MessageResponse("ok")},
		Channel:  channel.Ref{Name: "local"},
		Caller:   policy.Caller{Kind: policy.CallerUser},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "context-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(corecommand.Invocation{
		Path:  corecommand.Path{"context"},
		Input: map[string]any{"fresh": true, "key": codingplugin.AgentsContextProvider},
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

func TestToolProjectionIncludesPlanExecOperations(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{Bundle()},
		Plugins: []pluginhost.Plugin{codingplugin.New(sys), planexecplugin.New(), skillplugin.New()},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	cfg := ToolProjectionConfig()
	cfg.Commands = composition.CommandCatalog
	cfg.Operations = composition.OperationCatalog
	cfg.Caller = policy.Caller{Kind: policy.CallerAgent}
	cfg.Trust = policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified}

	projected := toolprojection.Project(cfg)
	names := map[string]bool{}
	for _, spec := range projected.Tools {
		names[string(spec.Name)] = true
	}
	for _, want := range []string{"plan", "delegate"} {
		if !names[want] {
			t.Fatalf("projected tool names missing %q: %#v", want, names)
		}
	}
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
