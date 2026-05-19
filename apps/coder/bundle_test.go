package coder

import (
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	corereaction "github.com/fluxplane/agentruntime/core/reaction"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codeplugin"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/dockerplugin"
	"github.com/fluxplane/agentruntime/plugins/golangplugin"
	"github.com/fluxplane/agentruntime/plugins/identityplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
	"github.com/fluxplane/agentruntime/plugins/kubernetesplugin"
	"github.com/fluxplane/agentruntime/plugins/markdownplugin"
	"github.com/fluxplane/agentruntime/plugins/projectplugin"
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
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, kubernetesplugin.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", kubernetesplugin.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, dockerplugin.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", dockerplugin.Name)
	}
	if !pluginListContains(localPlugins(sys), kubernetesplugin.Name) {
		t.Fatalf("coder local plugins missing %s", kubernetesplugin.Name)
	}
	if !pluginListContains(localPlugins(sys), dockerplugin.Name) {
		t.Fatalf("coder local plugins missing %s", dockerplugin.Name)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []resource.ContributionBundle{Bundle()},
		Plugins: []pluginhost.Plugin{identityplugin.New(), codingplugin.New(sys), taskplugin.New(), skillplugin.New(), imageplugin.New(sys), dockerplugin.New(sys), kubernetesplugin.New(sys)},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.AgentSpecs) != 7 {
		t.Fatalf("agent specs len = %d, want 7", len(composition.AgentSpecs))
	}
	if got := composition.AgentSpecs[0].Turns.MaxSteps; got != 50 {
		t.Fatalf("max steps = %d, want 50", got)
	}
	if len(composition.OperationSpecs) != 96 {
		t.Fatalf("operation specs len = %d, want 96", len(composition.OperationSpecs))
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
	if !agentHasDatasource(composition.AgentSpecs[0], kubernetesplugin.Name) {
		t.Fatalf("coder agent datasources = %#v, want %s", composition.AgentSpecs[0].Datasources, kubernetesplugin.Name)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, "web_search", "web_search") {
		t.Fatalf("datasource specs = %#v, want web_search", composition.DatasourceSpecs)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, kubernetesplugin.Name, kubernetesplugin.Name) {
		t.Fatalf("datasource specs = %#v, want %s", composition.DatasourceSpecs, kubernetesplugin.Name)
	}
	if !agentHasSkill(composition.AgentSpecs[0], "coder") {
		t.Fatalf("coder agent skills = %#v, want coder", composition.AgentSpecs[0].Skills)
	}
	if !hasSkillSpec(composition.SkillSpecs, "coder") {
		t.Fatalf("skill specs = %#v, want coder", composition.SkillSpecs)
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

func TestExpandOperationsUsesExplicitFeaturesAndOperationSets(t *testing.T) {
	ops := expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{ProjectSignalsFeature(), {OperationSets: []string{golangplugin.ParserSet, markdownplugin.Name}}},
	})
	if !containsName(ops, projectplugin.InventoryOp) || !containsName(ops, "go_outline") || !containsName(ops, "markdown_outline") {
		t.Fatalf("ops = %#v, want project, Go parser, and markdown operations", ops)
	}
	if containsName(ops, "go_test") {
		t.Fatalf("ops = %#v, want toolchain operations omitted until explicitly selected", ops)
	}

	ops = expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{{OperationSets: []string{golangplugin.ToolchainSet}}},
		Add:      []string{"custom_op"},
		Remove:   []string{"go_fmt"},
	})
	if !containsName(ops, "go_test") || !containsName(ops, "custom_op") {
		t.Fatalf("ops = %#v, want available toolchain and explicit add", ops)
	}
	if containsName(ops, "go_fmt") {
		t.Fatalf("ops = %#v, want explicit removal to win", ops)
	}
}

func TestBundleContributesLanguageActivationReactions(t *testing.T) {
	bundle := Bundle()
	if !hasReaction(bundle.Reactions, "coder.language.go.parser", "golang.parser") {
		t.Fatalf("reactions = %#v, want Go parser operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.language.markdown", "markdown") {
		t.Fatalf("reactions = %#v, want markdown operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.integration.docker.available", codeplugin.Name) {
		t.Fatalf("reactions = %#v, want Docker operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.toolchain.go.available", "golang.toolchain") {
		t.Fatalf("reactions = %#v, want Go toolchain operation-set reaction", bundle.Reactions)
	}
}

func hasReaction(rules []corereaction.Rule, name, operationSet string) bool {
	for _, rule := range rules {
		if rule.Name != name {
			continue
		}
		for _, action := range rule.Actions {
			if action.OperationSet == operationSet {
				return true
			}
		}
	}
	return false
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

func bundleHasPluginRef(bundles []resource.ContributionBundle, name string) bool {
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if ref.Name == name {
				return true
			}
		}
	}
	return false
}

func pluginListContains(plugins []pluginhost.Plugin, name string) bool {
	for _, plugin := range plugins {
		if plugin != nil && plugin.Manifest().Name == name {
			return true
		}
	}
	return false
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
func agentHasSkill(spec agent.Spec, name string) bool {
	for _, ref := range spec.Skills {
		if ref.Name == skill.Name(name) {
			return true
		}
	}
	return false
}

func hasSkillSpec(specs []skill.Spec, name string) bool {
	for _, spec := range specs {
		if spec.Name == skill.Name(name) {
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
