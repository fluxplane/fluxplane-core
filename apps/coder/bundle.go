// Package coder declares the first-party coding agent app resources.
package coder

import (
	"context"
	"embed"

	"github.com/fluxplane/agentruntime/adapters/resources/agentdir"
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/command"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	corereaction "github.com/fluxplane/agentruntime/core/reaction"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
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
	"github.com/fluxplane/agentruntime/plugins/native/discovery"
	"github.com/fluxplane/agentruntime/plugins/native/image"
	"github.com/fluxplane/agentruntime/plugins/native/memory"
	"github.com/fluxplane/agentruntime/plugins/native/project"
	"github.com/fluxplane/agentruntime/plugins/native/task"
	"github.com/fluxplane/agentruntime/sdk"
)

const (
	AppName          = "coder"
	AgentName        = "coder"
	SessionName      = "coder"
	CodingPlugin     = "coding"
	DiscoveryPlugin  = discovery.Name
	IdentityPlugin   = "identity"
	TaskPlugin       = "task"
	SkillsPlugin     = "skills"
	ImagePlugin      = "image"
	KubernetesPlugin = "kubernetes"
	LokiPlugin       = loki.Name
	MySQLPlugin      = mysql.Name
	DockerPlugin     = "docker"
	MemoryPlugin     = memory.Name
	GitLabPlugin     = "gitlab"
	DefaultModel     = "gpt-5.5"
	DefaultNamespace = "apps/coder"
	ReflectCommand   = "reflect"
)

//go:embed resources/.agents/**
var embeddedResources embed.FS

// Bundle returns pure app resource declarations. Runtime implementations are
// supplied by the host command.
func Bundle() resource.ContributionBundle {
	embedded := embeddedResourceBundle()
	operations := fullCapabilityOperationNames()
	delegationOperations := defaultDelegationOperationNames()

	baseAgentSpec := coderAgentSpec(operations)
	codeReviewerAgentSpec := codeReviewerAgentSpec()

	bundle := sdk.NewApp(AppName).
		WithSource(resource.SourceRef{
			ID:       DefaultNamespace,
			Scope:    resource.ScopeEmbedded,
			Location: DefaultNamespace,
		}).
		WithDescription("Small local coding agent app.").
		WithModel("openai", DefaultModel, "coding").
		WithPlugin(resource.PluginRef{Name: DiscoveryPlugin}).
		WithPlugin(resource.PluginRef{Name: IdentityPlugin}).
		WithPlugin(resource.PluginRef{Name: CodingPlugin}).
		WithPlugin(resource.PluginRef{Name: TaskPlugin}).
		WithPlugin(resource.PluginRef{Name: SkillsPlugin}).
		WithPlugin(resource.PluginRef{Name: ImagePlugin}).
		WithPlugin(resource.PluginRef{Name: DockerPlugin}).
		WithPlugin(resource.PluginRef{Name: GitLabPlugin}).
		WithPlugin(resource.PluginRef{Name: KubernetesPlugin}).
		WithPlugin(resource.PluginRef{Name: LokiPlugin}).
		WithPlugin(resource.PluginRef{Name: MySQLPlugin}).
		WithPlugin(resource.PluginRef{Name: MemoryPlugin}).
		WithDefaultAgent(baseAgentSpec).
		WithAgent(codeReviewerAgentSpec).
		WithDefaultSession(coresession.Spec{
			Name:        SessionName,
			Description: "Default local coding session.",
			Agent:       agent.Ref{Name: AgentName},
			Metadata:    map[string]string{"app": AppName},
			Delegation: coresession.DelegationPolicy{
				AllowedProfiles: defaultDelegationProfiles(),
				AllowedAgents:   defaultDelegationAgents(),
				MaxParallel:     4,
				DefaultTimeout:  "10m",
				Operations:      operationRefs(delegationOperations),
			},
		}).
		WithSession(coresession.Spec{
			Name:        "code-reviewer",
			Description: "Delegated code review session.",
			Agent:       agent.Ref{Name: "code-reviewer"},
			Metadata:    map[string]string{"app": AppName},
		}).
		Build()
	bundle.Datasources = append(bundle.Datasources, coredatasource.Spec{
		Name:        "web_search",
		Description: "Default public web search datasource.",
		Kind:        "web_search",
		Entities:    []coredatasource.EntityType{web.SearchResultEntity},
	})
	bundle.Datasources = append(bundle.Datasources, coredatasource.Spec{
		Name:        kubernetes.Name,
		Description: "Default live Kubernetes cluster datasource.",
		Kind:        kubernetes.Name,
		Entities: []coredatasource.EntityType{
			kubernetes.ClusterEntity,
			kubernetes.NamespaceEntity,
			kubernetes.PodEntity,
			kubernetes.ServiceEntity,
			kubernetes.DeploymentEntity,
			kubernetes.ContainerEntity,
		},
	})
	bundle.Datasources = append(bundle.Datasources, coredatasource.Spec{
		Name:        gitlab.Name,
		Description: "Default live GitLab datasource.",
		Kind:        gitlab.Name,
		Entities: []coredatasource.EntityType{
			gitlab.ProjectEntity,
			gitlab.MergeRequestEntity,
			gitlab.MergeRequestDiffEntity,
			gitlab.MergeRequestNoteEntity,
			gitlab.PipelineEntity,
			gitlab.BranchEntity,
			gitlab.TagEntity,
			gitlab.CommitEntity,
			gitlab.RepositoryTreeEntity,
			gitlab.RepositoryFileEntity,
			gitlab.JobEntity,
			gitlab.JobTraceEntity,
			gitlab.UserEntity,
			gitlab.GroupEntity,
			gitlab.MembershipEntity,
		},
	})
	bundle.Datasources = append(bundle.Datasources, coredatasource.Spec{
		Name:        loki.Name,
		Description: "Default live Loki log datasource.",
		Kind:        loki.Name,
		Entities: []coredatasource.EntityType{
			loki.LogEntryEntity,
			loki.StreamEntity,
			loki.LabelEntity,
			loki.DetectedEndpointEntity,
		},
	})
	bundle.Reactions = append(bundle.Reactions, coderLanguageActivationReactions()...)
	bundle.Reactions = append(bundle.Reactions, coderEndpointActivationReactions()...)
	bundle.Reactions = append(bundle.Reactions, coderIntentActivationReactions()...)
	if len(bundle.Apps) > 0 {
		bundle.Apps[0].Sources = append(bundle.Apps[0].Sources, coreapp.SourceSpec{
			Location:  ".agents",
			Scope:     string(resource.ScopeProject),
			Ecosystem: "agentdir",
		})
		bundle.Apps[0].Discovery.IncludeGlobalUserResources = true
		bundle.Apps[0].Security = localCoderSecurity()
	}
	bundle.Append(embedded)
	bundle.Commands = append(bundle.Commands, shellExecCommandSpec())
	return bundle
}

func coderAgentSpec(operations []string) agent.Spec {
	spec := sdk.BuildAgent(AgentName).
		WithDescription("A compact local coding assistant with filesystem, web, browser, git, shell, background process, code execution, and clarification tools.").
		WithSystem(coderSystemPrompt()).
		AsLLMAgent(DefaultModel).
		WithMaxOutputTokens(4096).
		WithMaxSteps(50).
		WithAgency(agent.AgencyProfile{
			Autonomy: agent.AutonomyGoalDriven,
			Reactive: true,
			Social:   true,
			Stateful: true,
		}).
		WithOperations(operations...).
		WithDatasource("web_search").
		WithDatasource(kubernetes.Name).
		WithDatasource(gitlab.Name).
		WithDatasource(loki.Name).
		Build()
	spec.Skills = append(spec.Skills, skill.Ref{Name: "coder"})
	return spec
}

func codeReviewerAgentSpec() agent.Spec {
	return sdk.BuildAgent("code-reviewer").
		WithDescription("A focused code review assistant for inspecting patches and repository changes.").
		WithSystem(codeReviewerSystemPrompt()).
		AsLLMAgent(DefaultModel).
		WithMaxOutputTokens(4096).
		WithMaxSteps(25).
		Build()
}

func coderSystemPrompt() string {
	return `You are coder. Help with coding tasks using concise, concrete steps.

Prefer native project, filesystem, git, browser, web_search, and web_request operations over shell_exec. Prefer Go language, markdown, and code_execute operations when they are available in the current turn.
Use web_search for general web discovery, datasource_search with entities=["web.search_result"] for configured web_search datasource queries, and web_request only for fetching known URLs.
Use project_inventory/project_docs/project_tasks/project_task_run for workspace structure and discovered project tasks. When Go tools are available, use go_info/go_env/go_version/go_doc/go_list/go_test/go_fmt/go_vet/go_build/go_install for Go toolchain work, and go_project/go_packages/go_outline/go_symbol/go_definition/go_symbol_info/go_references/go_imports/go_implementations/go_callers/go_callees for Go code navigation.
When observability or endpoint tools are available, use loki_test/loki_labels/loki_query/loki_recent_logs for Loki logs, mysql_query with endpoint_ref for discovered MySQL endpoints, and discovery_status/discovery_discover/discovery_providers/endpoint_list/endpoint_get for endpoint discovery introspection and manual refresh.
When markdown tools are available, use markdown_outline/markdown_links/markdown_diagnostics for markdown documentation structure and local link checks.
When the user asks you to create a task for immediate execution, create or update the task to status=ready, call task_run for that task, and report whether it started, is already running, is not ready, or is waiting for capacity.
Use file_create for new files, file_edit for edits to existing files, and file_delete for deletion.
Use shell_exec only when no native operation fits. Ask before destructive actions.`
}

func codeReviewerSystemPrompt() string {
	return `You are reviewer. Review code changes with concise, actionable findings.

Prioritize correctness, safety, tests, maintainability, and repository architecture rules.
Inspect diffs and relevant surrounding code before making claims.
Report findings by severity with file and line references when available.
Avoid broad style commentary unless it affects behavior, maintainability, or project conventions.`
}

func coderLanguageActivationReactions() []corereaction.Rule {
	return []corereaction.Rule{{
		Name: "coder.language.go.parser",
		When: corereaction.Matcher{
			Assertion: project.AssertionLanguageDetected,
			Target:    "go",
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectLanguage, Name: "go"},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: golang.ParserSet,
		}},
	}, {
		Name: "coder.language.markdown",
		When: corereaction.Matcher{
			Assertion: project.AssertionLanguageDetected,
			Target:    "markdown",
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectLanguage, Name: "markdown"},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: markdown.Name,
		}},
	}, {
		Name: "coder.integration.docker.available",
		When: corereaction.Matcher{
			Assertion: docker.AssertionAvailable,
			Target:    docker.Name,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: docker.Name},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: code.Name,
		}},
	}, {
		Name: "coder.toolchain.go.available",
		When: corereaction.Matcher{
			Assertion: golang.AssertionToolchainAvailable,
			Target:    "go",
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectToolchain, Name: "go"},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: golang.ToolchainSet,
		}},
	}}
}

func coderEndpointActivationReactions() []corereaction.Rule {
	return []corereaction.Rule{{
		Name: "coder.endpoint.loki.available",
		When: corereaction.Matcher{
			Assertion: discovery.AssertionEndpointAvailable,
			Target:    loki.Name,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectEndpoint, Name: loki.Name},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: loki.Name,
		}, {
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: discovery.Name,
		}, {
			Kind:       corereaction.ActionEnableDatasource,
			Datasource: coredatasource.Ref{Name: loki.Name},
		}},
	}, {
		Name: "coder.endpoint.mysql.available",
		When: corereaction.Matcher{
			Assertion: discovery.AssertionEndpointAvailable,
			Target:    mysql.Name,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectEndpoint, Name: mysql.Name},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: mysql.Name,
		}, {
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: discovery.Name,
		}},
	}}
}

func coderIntentActivationReactions() []corereaction.Rule {
	return []corereaction.Rule{{
		Name: "coder.capability.browser.available",
		When: corereaction.Matcher{
			Assertion: browser.AssertionBrowserAvailable,
			Target:    browser.Name,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: browser.Name},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: browser.Name,
		}},
	}, {
		Name: "coder.capability.image.generation.available",
		When: corereaction.Matcher{
			Assertion: image.AssertionImageProviderReady,
			Target:    image.GenerationSet,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: image.GenerationSet},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: image.GenerationSet,
		}},
	}, {
		Name: "coder.capability.image.understanding.available",
		When: corereaction.Matcher{
			Assertion: image.AssertionImageProviderReady,
			Target:    image.UnderstandingSet,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: image.UnderstandingSet},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: image.UnderstandingSet,
		}},
	}, {
		Name: "coder.capability.memory_mutation.available",
		When: corereaction.Matcher{
			Assertion: memory.AssertionMemoryMutationReady,
			Target:    memory.MutationSet,
			Subject:   coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: memory.MutationSet},
		},
		Actions: []corereaction.Action{{
			Kind:         corereaction.ActionEnableOperationSet,
			OperationSet: memory.MutationSet,
		}},
	}}
}

func shellExecCommandSpec() command.Spec {
	return command.Spec{
		Path:        command.Path{"shell", "exec"},
		Description: "Run shell script text through the shell_exec operation.",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "shell_exec"},
		},
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{policy.CallerUser},
			RequiredTrust:  policy.TrustVerified,
		},
		Annotations: map[string]string{"tool_projection": "hidden"},
	}
}

func localCoderSecurity() policy.AuthorizationPolicy {
	subjects := []policy.SubjectRef{
		{Kind: policy.SubjectUser, ID: "*"},
		{Kind: policy.SubjectGroup, ID: "local_operators"},
	}
	resources := []policy.ResourceRef{
		{Kind: policy.ResourceWorkspace, Name: "*"},
		{Kind: policy.ResourcePath, Path: "**"},
		{Kind: policy.ResourceProcess, Name: "*"},
		{Kind: policy.ResourceNetwork, Name: "*"},
		{Kind: policy.ResourceConnector, Name: "*"},
		{Kind: policy.ResourceTask, Name: "*"},
		{Kind: policy.ResourceSession, Name: "*"},
		{Kind: policy.ResourceDatasource, Name: "*"},
		{Kind: policy.ResourceModel, Name: "*"},
		{Kind: policy.ResourceOperation, Name: "*"},
		{Kind: policy.ResourceSecret, Name: "*"},
	}
	actions := []policy.Action{
		"workspace.*",
		"process.*",
		"network.*",
		"connector.*",
		"task.*",
		"session.*",
		"datasource.*",
		policy.ActionModelInvoke,
		policy.ActionOperationInvoke,
		policy.ActionApprovalGrant,
		"secret.*",
	}
	return policy.AuthorizationPolicy{Grants: []policy.Grant{{
		Subjects:      subjects,
		Resources:     resources,
		Actions:       actions,
		RequiredTrust: policy.TrustPrivileged,
	}}}
}

func embeddedResourceBundle() resource.ContributionBundle {
	bundle, err := agentdir.LoadFS(context.Background(), embeddedResources, "resources/.agents", resource.SourceRef{
		ID:        DefaultNamespace + ":embedded-agentdir",
		Ecosystem: "agentdir",
		Scope:     resource.ScopeEmbedded,
		Location:  "apps/coder/resources/.agents",
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	})
	if err != nil {
		return resource.ContributionBundle{Diagnostics: []resource.Diagnostic{{
			Severity: resource.SeverityError,
			Source: resource.SourceRef{
				ID:        DefaultNamespace + ":embedded-agentdir",
				Ecosystem: "agentdir",
				Scope:     resource.ScopeEmbedded,
				Location:  "apps/coder/resources/.agents",
			},
			Message: err.Error(),
		}}}
	}
	for i := range bundle.Commands {
		if bundle.Commands[i].Path.String() == "/"+ReflectCommand {
			bundle.Commands[i].Policy = policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			}
		}
	}
	return bundle
}

func defaultDelegationProfiles() []coresession.Ref {
	names := []coresession.Name{
		task.WorkerSession,
		task.ExplorerSession,
		task.ReviewerSession,
		task.TaskSession,
		task.PlanSession,
		"code-reviewer",
	}
	out := make([]coresession.Ref, 0, len(names))
	for _, name := range names {
		out = append(out, coresession.Ref{Name: name})
	}
	return out
}

func defaultDelegationAgents() []agent.Ref {
	names := []agent.Name{
		task.WorkerAgent,
		task.ExplorerAgent,
		task.ReviewerAgent,
		task.TaskAgent,
		task.PlanAgent,
		"code-reviewer",
	}
	out := make([]agent.Ref, 0, len(names))
	for _, name := range names {
		out = append(out, agent.Ref{Name: name})
	}
	return out
}

func operationRefs(names []string) []operation.Ref {
	out := make([]operation.Ref, 0, len(names))
	for _, name := range names {
		out = append(out, operation.Ref{Name: operation.Name(name)})
	}
	return out
}
