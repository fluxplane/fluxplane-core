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
	"github.com/fluxplane/agentruntime/plugins/bundles/coding"
	"github.com/fluxplane/agentruntime/plugins/integrations/docker"
	"github.com/fluxplane/agentruntime/plugins/integrations/gitlab"
	"github.com/fluxplane/agentruntime/plugins/integrations/kubernetes"
	"github.com/fluxplane/agentruntime/plugins/integrations/loki"
	"github.com/fluxplane/agentruntime/plugins/integrations/mysql"
	"github.com/fluxplane/agentruntime/plugins/integrations/web"
	"github.com/fluxplane/agentruntime/plugins/languages/golang"
	"github.com/fluxplane/agentruntime/plugins/languages/markdown"
	"github.com/fluxplane/agentruntime/plugins/native/browser"
	"github.com/fluxplane/agentruntime/plugins/native/code"
	"github.com/fluxplane/agentruntime/plugins/native/datasource"
	"github.com/fluxplane/agentruntime/plugins/native/discovery"
	"github.com/fluxplane/agentruntime/plugins/native/identity"
	"github.com/fluxplane/agentruntime/plugins/native/image"
	"github.com/fluxplane/agentruntime/plugins/native/memory"
	"github.com/fluxplane/agentruntime/plugins/native/project"
	"github.com/fluxplane/agentruntime/plugins/native/skills"
	"github.com/fluxplane/agentruntime/plugins/native/task"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestBundleComposes(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, kubernetes.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", kubernetes.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, discovery.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", discovery.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, docker.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", docker.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, gitlab.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", gitlab.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, loki.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", loki.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, mysql.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", mysql.Name)
	}
	if !bundleHasPluginRef([]resource.ContributionBundle{Bundle()}, memory.Name) {
		t.Fatalf("coder bundle plugin refs missing %s", memory.Name)
	}
	if !pluginListContains(localPlugins(sys), kubernetes.Name) {
		t.Fatalf("coder local plugins missing %s", kubernetes.Name)
	}
	if !pluginListContains(localPlugins(sys), discovery.Name) {
		t.Fatalf("coder local plugins missing %s", discovery.Name)
	}
	if !pluginListContains(localPlugins(sys), docker.Name) {
		t.Fatalf("coder local plugins missing %s", docker.Name)
	}
	if !pluginListContains(localPlugins(sys), gitlab.Name) {
		t.Fatalf("coder local plugins missing %s", gitlab.Name)
	}
	if !pluginListContains(localPlugins(sys), loki.Name) {
		t.Fatalf("coder local plugins missing %s", loki.Name)
	}
	if !pluginListContains(localPlugins(sys), mysql.Name) {
		t.Fatalf("coder local plugins missing %s", mysql.Name)
	}
	if !pluginListContains(localPlugins(sys), memory.Name) {
		t.Fatalf("coder local plugins missing %s", memory.Name)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []resource.ContributionBundle{Bundle()},
		Plugins: []pluginhost.Plugin{identity.New(), discovery.New(), coding.New(sys), task.New(), skills.New(), image.New(sys), docker.New(sys), gitlab.New(sys), kubernetes.New(sys), loki.New(sys), mysql.New(), memory.New()},
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
	if len(composition.OperationSpecs) != 112 {
		t.Fatalf("operation specs len = %d, want 112", len(composition.OperationSpecs))
	}
	if !agentHasOperation(composition.AgentSpecs[0], web.SearchOp) {
		t.Fatalf("coder agent operations missing %s", web.SearchOp)
	}
	for _, name := range []string{datasource.SearchOperation, datasource.GetOperation, datasource.BatchGetOperation} {
		if !agentHasOperation(composition.AgentSpecs[0], name) {
			t.Fatalf("coder agent operations missing %s", name)
		}
	}
	if !agentHasOperation(composition.AgentSpecs[0], "project_task_run") {
		t.Fatalf("coder agent operations missing project_task_run")
	}
	for _, name := range []string{
		"code_execute",
		golang.ProjectOp,
		golang.OutlineOp,
		golang.TestOp,
		markdown.OutlineOp,
		discovery.StatusOp,
		discovery.EndpointListOp,
		loki.QueryOp,
		mysql.QueryOp,
		browser.OpenOp,
		image.GenerateOp,
		memory.MemorizeOp,
		memory.ForgetOp,
		memory.OrganizeOp,
	} {
		if agentHasOperation(composition.AgentSpecs[0], name) {
			t.Fatalf("coder agent default operations include gated %s", name)
		}
		if _, err := composition.OperationCatalog.Resolve(name, resource.ResourceID{}); err != nil {
			t.Fatalf("operation catalog missing gated %s: %v", name, err)
		}
	}
	for _, tc := range []struct {
		set string
		op  string
	}{
		{set: code.Name, op: "code_execute"},
		{set: golang.ParserSet, op: golang.OutlineOp},
		{set: golang.ToolchainSet, op: golang.TestOp},
		{set: markdown.Name, op: markdown.OutlineOp},
		{set: discovery.Name, op: discovery.EndpointListOp},
		{set: loki.Name, op: loki.QueryOp},
		{set: mysql.Name, op: mysql.QueryOp},
		{set: browser.Name, op: browser.OpenOp},
		{set: image.Name, op: image.GenerateOp},
		{set: image.GenerationSet, op: image.GenerateOp},
		{set: image.UnderstandingSet, op: image.UnderstandOp},
		{set: memory.MutationSet, op: memory.MemorizeOp},
	} {
		if !operationSetsContain(composition.OperationSets, tc.set, tc.op) {
			t.Fatalf("operation sets missing %s -> %s: %#v", tc.set, tc.op, composition.OperationSets)
		}
	}
	if !agentHasOperation(composition.AgentSpecs[0], memory.RetrieveOp) {
		t.Fatalf("coder agent operations missing %s", memory.RetrieveOp)
	}
	if !agentHasDatasource(composition.AgentSpecs[0], "web_search") {
		t.Fatalf("coder agent datasources = %#v, want web_search", composition.AgentSpecs[0].Datasources)
	}
	if !agentHasDatasource(composition.AgentSpecs[0], kubernetes.Name) {
		t.Fatalf("coder agent datasources = %#v, want %s", composition.AgentSpecs[0].Datasources, kubernetes.Name)
	}
	if !agentHasDatasource(composition.AgentSpecs[0], gitlab.Name) {
		t.Fatalf("coder agent datasources = %#v, want %s", composition.AgentSpecs[0].Datasources, gitlab.Name)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, "web_search", "web_search") {
		t.Fatalf("datasource specs = %#v, want web_search", composition.DatasourceSpecs)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, kubernetes.Name, kubernetes.Name) {
		t.Fatalf("datasource specs = %#v, want %s", composition.DatasourceSpecs, kubernetes.Name)
	}
	if !datasourceSpecHasEntity(composition.DatasourceSpecs, kubernetes.Name, kubernetes.ClusterEntity) {
		t.Fatalf("kubernetes datasource spec missing %s: %#v", kubernetes.ClusterEntity, composition.DatasourceSpecs)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, gitlab.Name, gitlab.Name) {
		t.Fatalf("datasource specs = %#v, want %s", composition.DatasourceSpecs, gitlab.Name)
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
	for _, name := range []string{task.WorkerSession, task.ExplorerSession, task.ReviewerSession, task.TaskSession, task.PlanSession, "code-reviewer"} {
		if !sessionAllowsProfile(session, name) {
			t.Fatalf("delegation allowed profiles = %#v, missing %s", session.Delegation.AllowedProfiles, name)
		}
	}
	for _, name := range []string{task.WorkerAgent, task.ExplorerAgent, task.ReviewerAgent, task.TaskAgent, task.PlanAgent, "code-reviewer"} {
		if !sessionAllowsAgent(session, name) {
			t.Fatalf("delegation allowed agents = %#v, missing %s", session.Delegation.AllowedAgents, name)
		}
	}
	if !hasSessionSpec(composition.SessionSpecs, "code-reviewer", "code-reviewer") {
		t.Fatalf("session specs = %#v, missing code-reviewer session", composition.SessionSpecs)
	}
	for _, name := range []string{"project_task_run", "task_create", "task_modify", "task_get", "task_list", "task_list_artifacts", "task_get_artifact", "task_read_artifact", "task_validate", "review_request", "task_run", "task_scheduler_status", "task_scheduler_set_enabled", "go_info", "go_env", "go_version", "go_doc", "go_list", "go_test", "go_fmt", "go_vet", "go_build", "go_install", "go_callers", "go_callees"} {
		if !operationRefsContain(session.Delegation.Operations, name) {
			t.Fatalf("delegation operations missing %s", name)
		}
	}
	worker, ok := findAgentSpec(composition.AgentSpecs, task.WorkerAgent)
	if !ok {
		t.Fatalf("agent specs = %#v, missing %s", composition.AgentSpecs, task.WorkerAgent)
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
		Features: []FeatureSpec{ProjectEvidenceFeature(), {OperationSets: []string{golang.ParserSet, markdown.Name}}},
	})
	if !containsName(ops, project.InventoryOp) || !containsName(ops, "go_outline") || !containsName(ops, "markdown_outline") {
		t.Fatalf("ops = %#v, want project, Go parser, and markdown operations", ops)
	}
	if containsName(ops, "go_test") {
		t.Fatalf("ops = %#v, want toolchain operations omitted until explicitly selected", ops)
	}

	ops = expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{{OperationSets: []string{golang.ToolchainSet}}},
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

func TestDefaultCoderOperationsGateReactiveToolGroups(t *testing.T) {
	ops := fullCapabilityOperationNames()
	if got, want := len(ops), 56; got != want {
		t.Fatalf("default operation count = %d, want measured reduced surface %d: %#v", got, want, ops)
	}
	for _, name := range []string{
		"code_execute",
		golang.ProjectOp,
		golang.OutlineOp,
		golang.TestOp,
		markdown.OutlineOp,
		discovery.StatusOp,
		discovery.EndpointListOp,
		loki.QueryOp,
		mysql.QueryOp,
		browser.OpenOp,
		image.GenerateOp,
		memory.MemorizeOp,
		memory.ForgetOp,
		memory.OrganizeOp,
	} {
		if containsName(ops, name) {
			t.Fatalf("default operations include gated %s: %#v", name, ops)
		}
	}
	for _, name := range []string{
		project.InventoryOp,
		project.DocsOp,
		"file_read",
		"file_edit",
		"git_status",
		"shell_exec",
		web.SearchOp,
		task.TaskRunOp,
		memory.RetrieveOp,
	} {
		if !containsName(ops, name) {
			t.Fatalf("default operations missing baseline %s: %#v", name, ops)
		}
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
	if !hasReaction(bundle.Reactions, "coder.integration.docker.available", code.Name) {
		t.Fatalf("reactions = %#v, want Docker operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.toolchain.go.available", "golang.toolchain") {
		t.Fatalf("reactions = %#v, want Go toolchain operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.endpoint.loki.available", loki.Name) {
		t.Fatalf("reactions = %#v, want Loki endpoint operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.endpoint.loki.available", discovery.Name) {
		t.Fatalf("reactions = %#v, want Loki endpoint reaction to enable discovery operation set", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.endpoint.mysql.available", mysql.Name) {
		t.Fatalf("reactions = %#v, want MySQL endpoint operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.endpoint.mysql.available", discovery.Name) {
		t.Fatalf("reactions = %#v, want MySQL endpoint reaction to enable discovery operation set", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.capability.browser.available", browser.Name) {
		t.Fatalf("reactions = %#v, want browser availability operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.capability.image.generation.available", image.GenerationSet) {
		t.Fatalf("reactions = %#v, want image generation availability operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.capability.image.understanding.available", image.UnderstandingSet) {
		t.Fatalf("reactions = %#v, want image understanding availability operation-set reaction", bundle.Reactions)
	}
	if !hasReaction(bundle.Reactions, "coder.capability.memory_mutation.available", memory.MutationSet) {
		t.Fatalf("reactions = %#v, want memory mutation operation-set reaction", bundle.Reactions)
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

func operationSetsContain(sets []operation.Set, setName, operationName string) bool {
	for _, set := range sets {
		if set.Name != setName {
			continue
		}
		for _, ref := range set.Operations {
			if ref.Name == operation.Name(operationName) {
				return true
			}
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

func datasourceSpecHasEntity(specs []coredatasource.Spec, name string, entity coredatasource.EntityType) bool {
	for _, spec := range specs {
		if spec.Name != coredatasource.Name(name) {
			continue
		}
		for _, candidate := range spec.Entities {
			if candidate == entity {
				return true
			}
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

func sessionAllowsAgent(spec coresession.Spec, name string) bool {
	for _, ref := range spec.Delegation.AllowedAgents {
		if string(ref.Name) == name {
			return true
		}
	}
	return false
}

func hasSessionSpec(specs []coresession.Spec, name, agentName string) bool {
	for _, spec := range specs {
		if string(spec.Name) == name && string(spec.Agent.Name) == agentName {
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
