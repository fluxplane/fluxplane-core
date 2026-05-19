package coder

import (
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/identityplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/taskplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestBundleComposes(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []resource.ContributionBundle{Bundle()},
		Plugins: []pluginhost.Plugin{identityplugin.New(), codingplugin.New(sys), taskplugin.New(), skillplugin.New(), imageplugin.New(sys)},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.AgentSpecs) != 6 {
		t.Fatalf("agent specs len = %d, want 6", len(composition.AgentSpecs))
	}
	if got := composition.AgentSpecs[0].Turns.MaxSteps; got != 50 {
		t.Fatalf("max steps = %d, want 50", got)
	}
	if len(composition.OperationSpecs) != 88 {
		t.Fatalf("operation specs len = %d, want 88", len(composition.OperationSpecs))
	}
	if !agentHasOperation(composition.AgentSpecs[0], webplugin.SearchOp) {
		t.Fatalf("coder agent operations missing %s", webplugin.SearchOp)
	}
	for _, name := range []string{datasourceplugin.SearchOperation, datasourceplugin.GetOperation, datasourceplugin.BatchGetOperation} {
		if !agentHasOperation(composition.AgentSpecs[0], name) {
			t.Fatalf("coder agent operations missing %s", name)
		}
	}
	if !agentHasOperation(composition.AgentSpecs[0], "project_task_run") {
		t.Fatalf("coder agent operations missing project_task_run")
	}
	if !agentHasDatasource(composition.AgentSpecs[0], "web_search") {
		t.Fatalf("coder agent datasources = %#v, want web_search", composition.AgentSpecs[0].Datasources)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, "web_search", "web_search") {
		t.Fatalf("datasource specs = %#v, want web_search", composition.DatasourceSpecs)
	}
	session := composition.SessionSpecs[0]
	if len(session.Delegation.Commands) != 0 {
		t.Fatalf("delegation commands len = %d, want 0", len(session.Delegation.Commands))
	}
	if len(session.Delegation.Operations) == 0 {
		t.Fatal("delegation operations len = 0, want child operation caps")
	}
	for _, name := range []string{taskplugin.WorkerSession, taskplugin.ExplorerSession, taskplugin.ReviewerSession, taskplugin.TaskSession, taskplugin.PlanSession} {
		if !sessionAllowsProfile(session, name) {
			t.Fatalf("delegation allowed profiles = %#v, missing %s", session.Delegation.AllowedProfiles, name)
		}
	}
	for _, name := range []string{"project_task_run", "task_create", "task_modify", "task_get", "task_list", "task_list_artifacts", "task_get_artifact", "task_read_artifact", "task_validate", "review_request", "task_run", "task_scheduler_status", "task_scheduler_set_enabled", "go_info", "go_env", "go_version", "go_doc", "go_list", "go_test", "go_fmt", "go_vet", "go_build", "go_install", "go_callers", "go_callees"} {
		if !operationRefsContain(session.Delegation.Operations, name) {
			t.Fatalf("delegation operations missing %s", name)
		}
	}
	worker, ok := findAgentSpec(composition.AgentSpecs, taskplugin.WorkerAgent)
	if !ok {
		t.Fatalf("agent specs = %#v, missing %s", composition.AgentSpecs, taskplugin.WorkerAgent)
	}
	if len(worker.Commands) != 0 {
		t.Fatalf("worker commands len = %d, want 0", len(worker.Commands))
	}
	if len(worker.Operations) == 0 {
		t.Fatal("worker operations len = 0, want operation-projected tools")
	}
	commandSpecs := composition.Commands.All()
	shellCmd, ok := findCommandSpec(commandSpecs, "shell/exec")
	if !ok {
		t.Fatalf("command specs = %#v, missing /shell/exec", commandSpecs)
	}
	if shellCmd.Target.Kind != "operation" || shellCmd.Target.Operation.Name != "shell_exec" {
		t.Fatalf("shell exec command target = %#v, want shell_exec operation", shellCmd.Target)
	}
	reflectCmd, ok := findCommandSpec(commandSpecs, ReflectCommand)
	if !ok {
		t.Fatalf("command specs = %#v, missing /%s", commandSpecs, ReflectCommand)
	}
	if reflectCmd.Target.Kind != "prompt" {
		t.Fatalf("reflect command target = %#v, want prompt target", reflectCmd.Target)
	}
	if !strings.Contains(reflectCmd.Target.Prompt, "current coder session") {
		t.Fatalf("reflect prompt = %q, want current-session instructions", reflectCmd.Target.Prompt)
	}
	if len(reflectCmd.Policy.AllowedCallers) != 1 || reflectCmd.Policy.AllowedCallers[0] != policy.CallerUser || reflectCmd.Policy.RequiredTrust != policy.TrustVerified {
		t.Fatalf("reflect command policy = %#v, want verified user-only policy", reflectCmd.Policy)
	}
}

func TestExpandOperationsUsesActivationSignals(t *testing.T) {
	ops := expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{LanguageSupportFeature(), AvailableToolchainsFeature()},
		Activation: ActivationInput{
			ProjectSignals: []coreproject.Signal{{Language: "go", Toolchain: "go"}, {Language: "markdown"}},
			ToolchainStatuses: []language.ToolchainStatus{{
				ID:        "go",
				Available: false,
			}},
			LanguageSupports: builtinLanguageSupports(),
		},
	})
	if !containsName(ops, "go_outline") || !containsName(ops, "markdown_outline") {
		t.Fatalf("ops = %#v, want parser and markdown operations from signals", ops)
	}
	if containsName(ops, "go_test") {
		t.Fatalf("ops = %#v, want unavailable go toolchain operations omitted", ops)
	}

	ops = expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{LanguageSupportFeature(), AvailableToolchainsFeature()},
		Activation: ActivationInput{
			ProjectSignals:    []coreproject.Signal{{Language: "go", Toolchain: "go"}},
			ToolchainStatuses: []language.ToolchainStatus{{ID: "go", Available: true}},
			LanguageSupports:  builtinLanguageSupports(),
		},
		Add:    []string{"custom_op"},
		Remove: []string{"go_fmt"},
	})
	if !containsName(ops, "go_test") || !containsName(ops, "custom_op") {
		t.Fatalf("ops = %#v, want available toolchain and explicit add", ops)
	}
	if containsName(ops, "go_fmt") {
		t.Fatalf("ops = %#v, want explicit removal to win", ops)
	}
}
func agentHasOperation(spec agent.Spec, name string) bool {
	for _, ref := range spec.Operations {
		if ref.Name == operation.Name(name) {
			return true
		}
	}
	return false
}

func findCommandSpec(specs []command.Spec, name string) (command.Spec, bool) {
	for _, spec := range specs {
		if spec.Path.String() == "/"+name {
			return spec, true
		}
	}
	return command.Spec{}, false
}

func findAgentSpec(specs []agent.Spec, name string) (agent.Spec, bool) {
	for _, spec := range specs {
		if string(spec.Name) == name {
			return spec, true
		}
	}
	return agent.Spec{}, false
}

func agentHasDatasource(spec agent.Spec, name string) bool {
	for _, ref := range spec.Datasources {
		if ref.Name == coredatasource.Name(name) {
			return true
		}
	}
	return false
}

func hasDatasourceSpec(specs []coredatasource.Spec, name, kind string) bool {
	for _, spec := range specs {
		if spec.Name == coredatasource.Name(name) && spec.Kind == kind {
			return true
		}
	}
	return false
}

func operationRefsContain(refs []operation.Ref, name string) bool {
	for _, ref := range refs {
		if ref.Name == operation.Name(name) {
			return true
		}
	}
	return false
}

func sessionAllowsProfile(spec coresession.Spec, name string) bool {
	for _, ref := range spec.Delegation.AllowedProfiles {
		if string(ref.Name) == name {
			return true
		}
	}
	return false
}

func containsName(names []string, name string) bool {
	for _, current := range names {
		if current == name {
			return true
		}
	}
	return false
}
