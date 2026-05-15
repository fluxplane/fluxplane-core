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
	coreagent "github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	corecommand "github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coreskill "github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/orchestration/toolprojection"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
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
	if !strings.Contains(help, "--yolo") {
		t.Fatalf("help = %q, want yolo flag", help)
	}
	if !strings.Contains(help, "discover") {
		t.Fatalf("help = %q, want discover command", help)
	}
	if !strings.Contains(help, "serve") {
		t.Fatalf("help = %q, want serve command", help)
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
		"file_edit",
		PlanExecPlugin,
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
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "claude-skill-state-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithText("load crm and dex skill"))
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
		Bundles: []agentruntime.ResourceBundle{Bundle()},
		Plugins: []pluginhost.Plugin{
			codingplugin.New(sys),
			planexecplugin.New(),
			skillplugin.New(),
			imageplugin.New(sys),
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
		Bundles: []agentruntime.ResourceBundle{
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
		Plugins: []pluginhost.Plugin{
			codingplugin.New(sys),
			planexecplugin.New(),
			skillplugin.New(),
			imageplugin.New(sys),
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel:       model,
		Channel:        channel.Ref{Name: "local"},
		Caller:         policy.Caller{Kind: policy.CallerUser},
		Trust:          policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
		ToolProjection: ToolProjectionConfig(),
	})
	if err != nil {
		t.Fatalf("NewFromComposition: %v", err)
	}
	sessionHandle, err := service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: "skill-trigger-test"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithText("please use smoke trigger and detail trigger now"))
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

func TestToolProjectionIncludesPlanExecOperations(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{Bundle()},
		Plugins: []pluginhost.Plugin{codingplugin.New(sys), planexecplugin.New(), skillplugin.New(), imageplugin.New(sys)},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
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
	for _, want := range []string{"plan", "delegate", "image"} {
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
