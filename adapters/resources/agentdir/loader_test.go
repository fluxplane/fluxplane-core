package agentdir

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
	"github.com/fluxplane/fluxplane-skill"
)

func TestLoadDirParsesEngineerSubset(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".agents")
	writeFile(t, agentsDir, "agents/main.md", `---
name: main
description: Main coding agent.
model: claude-sonnet-4-20250514
max-tokens: 16000
turns:
  max-steps: 100
  continuation:
    max-continuations: 3
    stop-condition:
      type: max-continuations
      max: 3
temperature: 0.2
thinking: enabled
effort: high
tools: [bash, file_read]
skills: [architecture]
commands: [review, design]
---
You are the main agent.
`)
	writeFile(t, agentsDir, "agents/analyst.md", `---
description: Analyst agent.
tools: [file_read]
---
Analyze the request.
`)
	writeFile(t, agentsDir, "agents/implementer.md", `---
name: implementer
description: Implementation agent.
turns:
  max-steps: 250
  continuation:
    max-continuations: 25
    stop-condition:
      type: or
      conditions:
        - type: prompt
          prompt: |
            Check acceptance criteria.
        - type: tool-sentinel
        - type: max-continuations
          max: 25
tools:
  - bash
---
Implement the feature.
`)
	writeFile(t, agentsDir, "commands/review.md", `---
description: Review code changes.
argument-hint: "<file or diff>"
---
Review the following code:

{{.Query}}
`)
	writeFile(t, agentsDir, "commands/design.md", `---
description: Design a change.
---
Design the requested change.
`)
	writeFile(t, agentsDir, "commands/feat.yaml", `name: feat
description: Analyze and implement a feature
policy:
  agent_callable: true
input_schema:
  type: object
  properties:
    description:
      type: string
target:
  workflow: feature
  input: "{{ .description }}"
`)
	writeFile(t, agentsDir, "workflows/feature.yaml", `name: feature
description: End-to-end feature implementation
steps:
  - id: analyze
    agent: analyst
  - id: implement
    agent: implementer
    depends-on: [analyze]
`)
	writeFile(t, agentsDir, "skills/architecture/SKILL.md", `---
name: architecture
description: Evaluate architecture.
---
# Architecture
`)

	bundle, err := LoadDir(context.Background(), root)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if bundle.Source.Scope != "project" {
		t.Fatalf("source scope = %q", bundle.Source.Scope)
	}
	if bundle.Source.Trust.Kind != policy.TrustSource || bundle.Source.Trust.Level != policy.TrustVerified {
		t.Fatalf("source trust = %#v", bundle.Source.Trust)
	}
	if got, want := len(bundle.Agents), 3; got != want {
		t.Fatalf("agents len = %d, want %d", got, want)
	}
	if got, want := len(bundle.Commands), 3; got != want {
		t.Fatalf("commands len = %d, want %d", got, want)
	}
	if got, want := len(bundle.Workflows), 1; got != want {
		t.Fatalf("workflows len = %d, want %d", got, want)
	}
	if got, want := len(bundle.Skills), 1; got != want {
		t.Fatalf("skills len = %d, want %d", got, want)
	}

	main := findAgent(t, bundle.Agents, "main")
	if main.System != "You are the main agent." {
		t.Fatalf("main system = %q", main.System)
	}
	if main.Inference.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("main model = %q", main.Inference.Model)
	}
	if main.Inference.MaxOutputTokens != 16000 || main.Inference.Temperature != 0.2 {
		t.Fatalf("main inference = %#v", main.Inference)
	}
	if main.Inference.Thinking != "enabled" || main.Inference.ReasoningEffort != "high" {
		t.Fatalf("main reasoning = %#v, want thinking enabled effort high", main.Inference)
	}
	if main.Turns.MaxSteps != 100 {
		t.Fatalf("main max steps = %d", main.Turns.MaxSteps)
	}
	if got, want := main.Tools[0].Name, "bash"; got != want {
		t.Fatalf("main first tool = %q, want %q", got, want)
	}
	if got, want := string(main.Skills[0].Name), "architecture"; got != want {
		t.Fatalf("main first skill = %q, want %q", got, want)
	}
	if got, want := main.Commands[1].Name, "design"; got != want {
		t.Fatalf("main second command = %q, want %q", got, want)
	}

	analyst := findAgent(t, bundle.Agents, "analyst")
	if analyst.Description != "Analyst agent." {
		t.Fatalf("analyst description = %q", analyst.Description)
	}

	implementer := findAgent(t, bundle.Agents, "implementer")
	if implementer.Turns.Continuation.StopCondition.Type != "or" || len(implementer.Turns.Continuation.StopCondition.Conditions) != 3 {
		t.Fatalf("implementer stop = %#v", implementer.Turns.Continuation.StopCondition)
	}
	if implementer.Turns.Continuation.StopCondition.Conditions[0].Type != "prompt" || implementer.Turns.Continuation.StopCondition.Conditions[2].Max != 25 {
		t.Fatalf("implementer nested stop = %#v", implementer.Turns.Continuation.StopCondition.Conditions)
	}

	review := findCommand(t, bundle.Commands, "review")
	if review.Target.Kind != invocation.TargetPrompt {
		t.Fatalf("review target kind = %q", review.Target.Kind)
	}
	if review.Annotations["argument_hint"] != "<file or diff>" {
		t.Fatalf("review annotations = %#v", review.Annotations)
	}

	feat := findCommand(t, bundle.Commands, "feat")
	if feat.Target.Kind != invocation.TargetWorkflow || feat.Target.Workflow != "feature" {
		t.Fatalf("feat target = %#v", feat.Target)
	}
	if feat.Target.Input != "{{ .description }}" {
		t.Fatalf("feat target input = %#v", feat.Target.Input)
	}
	if len(feat.Input.Schema.Data) == 0 {
		t.Fatalf("feat input schema is empty")
	}
	if got, want := feat.Policy.AllowedCallers, []policy.CallerKind{policy.CallerUser, policy.CallerAgent}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("feat callers = %#v, want %#v", got, want)
	}

	flow := bundle.Workflows[0]
	if flow.Name != "feature" || flow.Raw["name"] != "feature" {
		t.Fatalf("workflow = %#v", flow)
	}
	if got, want := flow.Steps[0].Agent.Name, "analyst"; string(got) != want {
		t.Fatalf("analyze agent = %q, want %q", got, want)
	}
	if got, want := flow.Steps[1].DependsOn, []workflow.StepID{"analyze"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("implement deps = %#v, want %#v", got, want)
	}

	if got, want := string(bundle.Skills[0].Name), "architecture"; got != want {
		t.Fatalf("skill name = %q, want %q", got, want)
	}
	if bundle.Skills[0].Source.URI == "" {
		t.Fatalf("skill source URI is empty")
	}
}

func TestDecodeWorkflowSupportsOperationSteps(t *testing.T) {
	spec, err := DecodeWorkflow("ops.yaml", []byte(`name: ops
steps:
  - id: load
    operation: fetch
    input:
      query: A100
  - id: summarize
    kind: operation
    operation: summarize
    depends-on: [load]
    error-policy: continue
`))
	if err != nil {
		t.Fatalf("DecodeWorkflow: %v", err)
	}
	if got, want := spec.Steps[0].Kind, workflow.StepOperation; got != want {
		t.Fatalf("first kind = %q, want %q", got, want)
	}
	if got, want := spec.Steps[0].Operation, (operation.Ref{Name: "fetch"}); got != want {
		t.Fatalf("first operation = %#v, want %#v", got, want)
	}
	if input, ok := spec.Steps[0].Input.(map[string]any); !ok || input["query"] != "A100" {
		t.Fatalf("first input = %#v, want query", spec.Steps[0].Input)
	}
	if got, want := spec.Steps[1].ErrorPolicy, workflow.StepErrorContinue; got != want {
		t.Fatalf("second error policy = %q, want %q", got, want)
	}
}

func TestLoadDirAcceptsClaudeStyleAgentAndSkillFrontmatter(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".claude")
	writeFile(t, agentDir, "agents/ticket-implementer.md", `---
name: ticket-implementer
description: Ticket implementation agent.
tools: Bash, Glob, Grep, Read, Edit, Write, Skill
model: sonnet
memory: project
---
Implement a ticket.
`)
	writeFile(t, agentDir, "skills/dex/SKILL.md", `---
name: dex
description: Run dex CLI commands.
user-invocable: true
---
Use dex commands.
`)
	writeFile(t, agentDir, "skills/golang-pro/SKILL.md", `---
name: golang-pro
description: Go specialist.
metadata:
  domain: language
  triggers: Go, Golang, goroutines
  role: specialist
---
Use Go guidance.
`)

	bundle, err := LoadDir(context.Background(), agentDir)
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	agent := findAgent(t, bundle.Agents, "ticket-implementer")
	if len(agent.Tools) != 7 || agent.Tools[0].Name != "Bash" || agent.Tools[6].Name != "Skill" {
		t.Fatalf("agent tools = %#v", agent.Tools)
	}
	dex := findSkill(t, bundle.Skills, "dex")
	if dex.Metadata["user_invocable"] != "true" {
		t.Fatalf("dex metadata = %#v, want user_invocable", dex.Metadata)
	}
	golang := findSkill(t, bundle.Skills, "golang-pro")
	if got := golang.Triggers; len(got) != 3 || got[0] != "Go" || got[1] != "Golang" || got[2] != "goroutines" {
		t.Fatalf("golang triggers = %#v", got)
	}
	if golang.Metadata["domain"] != "language" || golang.Metadata["role"] != "specialist" {
		t.Fatalf("golang metadata = %#v", golang.Metadata)
	}
}

func TestDecodeSkillAcceptsOfficialAndUnknownFrontmatter(t *testing.T) {
	spec, err := DecodeSkill("fallback", "file:///tmp/SKILL.md", []byte(`---
name: doc-helper
description: Helps with docs.
allowed-tools: Bash, Read
allowed_tools: ignored
when_to_use: Use when editing documentation.
user_invocable: true
x-vendor-extra: kept by tolerant parser
---
# Docs
`))
	if err != nil {
		t.Fatalf("DecodeSkill() error = %v", err)
	}
	if spec.Name != "doc-helper" {
		t.Fatalf("name = %q", spec.Name)
	}
	if _, ok := spec.Metadata["when_to_use"]; ok {
		t.Fatalf("metadata includes non-official when_to_use: %#v", spec.Metadata)
	}
	if _, ok := spec.Metadata["user_invocable"]; ok {
		t.Fatalf("metadata includes non-official user_invocable alias: %#v", spec.Metadata)
	}
	if got := spec.Annotations["allowed_tools"]; got != "Bash,Read" {
		t.Fatalf("allowed tools annotation = %q", got)
	}
}

func TestLoadDirWithOptionsContinuesAfterBadSkill(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, ".claude")
	writeFile(t, agentDir, "skills/bad/SKILL.md", `---
name: bad
metadata:
  nested:
    value: unsupported
---
Bad.
`)
	writeFile(t, agentDir, "skills/golang-pro/SKILL.md", `---
name: golang-pro
description: Go specialist.
unknown_extension_field: Use when working on Go code.
---
Go guidance.
`)

	if _, err := LoadDir(context.Background(), agentDir); err == nil {
		t.Fatal("LoadDir() error = nil, want strict load error")
	}
	bundle, err := LoadDirWithOptions(context.Background(), agentDir, LoadOptions{ContinueOnError: true})
	if err != nil {
		t.Fatalf("LoadDirWithOptions() error = %v", err)
	}
	if len(bundle.Skills) != 1 || bundle.Skills[0].Name != "golang-pro" {
		t.Fatalf("skills = %#v, want only golang-pro", bundle.Skills)
	}
	if len(bundle.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1: %#v", len(bundle.Diagnostics), bundle.Diagnostics)
	}
	if len(bundle.EventTypes) != 0 {
		t.Fatalf("event types len = %d, want 0 registration samples", len(bundle.EventTypes))
	}
	var emitted []event.Event
	bundle, err = LoadDirWithOptions(context.Background(), agentDir, LoadOptions{
		ContinueOnError: true,
		Events: event.SinkFunc(func(ev event.Event) {
			emitted = append(emitted, ev)
		}),
	})
	if err != nil {
		t.Fatalf("LoadDirWithOptions with sink error = %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted len = %d, want 1", len(emitted))
	}
	loadErr, ok := emitted[0].(resource.LoadError)
	if !ok || loadErr.Kind != "skill" || !loadErr.Continued {
		t.Fatalf("load error event = %#v", emitted[0])
	}
}

func TestDecodeWorkflowRejectsUnknownDependency(t *testing.T) {
	_, err := DecodeWorkflow("feature.yaml", []byte(`name: feature
steps:
  - id: implement
    agent: implementer
    depends-on: [analyze]
`))
	if err == nil {
		t.Fatal("DecodeWorkflow() error = nil, want unknown dependency error")
	}
}

func TestDecodePromptCommandRejectsEmptyPrompt(t *testing.T) {
	_, err := DecodePromptCommand("review.md", []byte(`---
description: Review code.
---
`))
	if err == nil {
		t.Fatal("DecodePromptCommand() error = nil, want empty prompt error")
	}
}

func TestDecodeYAMLCommandTargetsPrompt(t *testing.T) {
	spec, err := DecodeCommand("review.yaml", []byte(`name: review
description: Review this session.
target:
  prompt: |
    Review the current assistant session.
`))
	if err != nil {
		t.Fatalf("DecodeCommand() error = %v", err)
	}
	if spec.Target.Kind != invocation.TargetPrompt {
		t.Fatalf("target kind = %q, want prompt", spec.Target.Kind)
	}
	if spec.Target.Prompt != "Review the current assistant session." {
		t.Fatalf("prompt = %q", spec.Target.Prompt)
	}
}

func TestDecodeYAMLCommandRejectsMultipleTargets(t *testing.T) {
	_, err := DecodeCommand("review.yaml", []byte(`name: review
target:
  workflow: reflect
  prompt: Review this session.
`))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("DecodeCommand() error = %v, want exactly one target error", err)
	}
}

func TestDecodeAgentRejectsUnknownFrontmatterField(t *testing.T) {
	_, err := DecodeAgent("main.md", []byte(`---
name: main
surprise: true
---
You are useful.
`))
	if err == nil || !strings.Contains(err.Error(), "schema validation failed") {
		t.Fatalf("DecodeAgent error = %v, want schema validation failure", err)
	}
}

func TestDecodeAgentRejectsMaxContinuationsWithoutStopCondition(t *testing.T) {
	_, err := DecodeAgent("main.md", []byte(`---
name: main
turns:
  max-steps: 50
  continuation:
    max-continuations: 3
---
You are useful.
`))
	if err == nil || !strings.Contains(err.Error(), "stop_condition") {
		t.Fatalf("DecodeAgent error = %v, want stop_condition failure", err)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func findAgent(t *testing.T, specs []agent.Spec, name string) agent.Spec {
	t.Helper()
	for _, spec := range specs {
		if string(spec.Name) == name {
			return spec
		}
	}
	t.Fatalf("agent %q not found", name)
	return agent.Spec{}
}

func findCommand(t *testing.T, specs []command.Spec, name string) command.Spec {
	t.Helper()
	for _, spec := range specs {
		if len(spec.Path) == 1 && spec.Path[0] == name {
			return spec
		}
	}
	t.Fatalf("command %q not found", name)
	return command.Spec{}
}

func findSkill(t *testing.T, specs []skill.Spec, name string) skill.Spec {
	t.Helper()
	for _, spec := range specs {
		if string(spec.Name) == name {
			return spec
		}
	}
	t.Fatalf("skill %q not found", name)
	return skill.Spec{}
}

// TestFrontmatterStringsSkipsNilListEntries regresses an fmt.Sprint(nil)
// foot-gun on the YAML frontmatter ingestion path. A list with an empty
// item (e.g. `tools:\n  - bash\n  -\n  - read`) decodes to []any{"bash",
// nil, "read"} and the old code wrote the literal "<nil>" into the
// resulting tools / skills / triggers / capabilities list, silently
// corrupting agent definitions instead of skipping the empty entry.
func TestFrontmatterStringsSkipsNilListEntries(t *testing.T) {
	got := frontmatterStrings([]any{"bash", nil, "read"})
	want := []string{"bash", "read"}
	if len(got) != len(want) {
		t.Fatalf("frontmatterStrings returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frontmatterStrings[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, value := range got {
		if value == "<nil>" {
			t.Fatalf("frontmatterStrings leaked <nil> string into result: %v", got)
		}
	}
}
