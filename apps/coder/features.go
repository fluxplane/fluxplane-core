package coder

import (
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/plugins/integrations/loki"
	"github.com/fluxplane/agentruntime/plugins/integrations/mysql"
	"github.com/fluxplane/agentruntime/plugins/languages/golang"
	"github.com/fluxplane/agentruntime/plugins/languages/markdown"
	"github.com/fluxplane/agentruntime/plugins/native/discovery"
	"github.com/fluxplane/agentruntime/plugins/native/memory"
	"github.com/fluxplane/agentruntime/plugins/native/project"
	"github.com/fluxplane/agentruntime/plugins/native/task"
	runtimelanguage "github.com/fluxplane/agentruntime/runtime/language"
)

// FeatureName identifies a coder-local inert feature preset.
type FeatureName string

const (
	FeatureProjectSignals  FeatureName = "project_signals"
	FeatureFullLocalCoding FeatureName = "full_local_coding"
)

// FeatureSpec describes operations implied by a coder feature.
type FeatureSpec struct {
	Name          FeatureName
	OperationSets []string
	Operations    []string
}

// OperationExpansionConfig describes feature-derived and explicit operations.
type OperationExpansionConfig struct {
	Features []FeatureSpec
	Add      []string
	Remove   []string
}

func fullCapabilityOperationNames() []string {
	return expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{
			ProjectSignalsFeature(),
			FullLocalCodingFeature(),
		},
	})
}

func defaultDelegationOperationNames() []string {
	allowed := map[string]bool{}
	for _, name := range []string{
		project.InventoryOp, project.FilesOp, project.TasksOp, project.TaskRunOp, project.DocsOp,
		"dir_list", "dir_tree", "file_read", "file_edit",
		"grep", "glob", "git_status", "git_diff", "git_add", "git_commit",
		"shell_exec", "code_execute", "web_search", "web_request",
		task.TaskCreateOp, task.TaskModifyOp, task.TaskGetOp, task.TaskListOp,
		task.TaskListArtifactsOp, task.TaskGetArtifactOp, task.TaskReadArtifactOp,
		task.TaskValidateOp, task.ReviewRequestOp,
		task.TaskRunOp, task.TaskSchedulerStatusOp, task.TaskSchedulerSetEnabledOp,
		"datasource_search", "datasource_list", "datasource_get", "datasource_batch_get",
	} {
		allowed[name] = true
	}
	for _, name := range append(golangParserOperations(), golangToolchainOperations()...) {
		allowed[name] = true
	}
	for _, name := range markdownOperations() {
		allowed[name] = true
	}
	var out []string
	for _, name := range fullCapabilityOperationNames() {
		if allowed[name] {
			out = append(out, name)
		}
	}
	return out
}

// ProjectSignalsFeature includes project inventory and signal context.
func ProjectSignalsFeature() FeatureSpec {
	return FeatureSpec{
		Name:       FeatureProjectSignals,
		Operations: []string{project.InventoryOp, project.FilesOp, project.TasksOp, project.TaskRunOp, project.DocsOp},
	}
}

// FullLocalCodingFeature preserves the current broad coder product surface.
func FullLocalCodingFeature() FeatureSpec {
	return FeatureSpec{
		Name: FeatureFullLocalCoding,
		OperationSets: []string{
			golang.ParserSet,
			golang.ToolchainSet,
			markdown.Name,
		},
		Operations: []string{
			"dir_create", "dir_list", "dir_tree",
			"file_read", "file_create", "file_edit", "file_delete", "file_stat", "file_copy", "file_move",
			"glob", "grep",
			"web_search", "web_request",
			discovery.StatusOp, discovery.DiscoverOp, discovery.ProvidersOp, discovery.EndpointListOp, discovery.EndpointGetOp,
			loki.TestOp, loki.LabelsOp, loki.QueryOp, loki.RecentLogsOp,
			mysql.QueryOp,
			"datasource_search", "datasource_list", "datasource_get", "datasource_batch_get",
			"browser_open", "browser_navigate", "browser_click", "browser_type", "browser_select",
			"browser_read", "browser_screenshot", "browser_evaluate", "browser_wait", "browser_scroll",
			"browser_hover", "browser_back", "browser_forward", "browser_pdf", "browser_close",
			"git_status", "git_diff", "git_add", "git_commit", "git_tag", "git_push",
			"shell", "shell_info", "shell_exec", "process_run", "process_start", "process_ensure", "process_list", "process_status", "process_output", "process_wait", "process_stop", "process_kill",
			"code_execute",
			"clarify", task.TaskCreateOp, task.TaskModifyOp, task.TaskGetOp, task.TaskListOp,
			task.TaskListArtifactsOp, task.TaskGetArtifactOp, task.TaskReadArtifactOp,
			task.TaskValidateOp, task.ReviewRequestOp,
			task.TaskRunOp, task.TaskSchedulerStatusOp, task.TaskSchedulerSetEnabledOp,
			"skill",
			memory.MemorizeOp, memory.RetrieveOp, memory.ForgetOp, memory.OrganizeOp,
			"image_generate", "image_understand", "image_providers",
		},
	}
}

func expandOperations(cfg OperationExpansionConfig) []string {
	ordered := make([]string, 0)
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		ordered = append(ordered, name)
	}
	sets := operationSetsByName(runtimelanguage.OperationSets(builtinLanguageSupports()))
	for _, feature := range cfg.Features {
		for _, name := range feature.Operations {
			add(name)
		}
		for _, setName := range feature.OperationSets {
			addOperationSet(add, sets[setName])
		}
	}
	for _, name := range cfg.Add {
		add(name)
	}
	if len(cfg.Remove) > 0 {
		remove := map[string]bool{}
		for _, name := range cfg.Remove {
			remove[name] = true
		}
		filtered := ordered[:0]
		for _, name := range ordered {
			if !remove[name] {
				filtered = append(filtered, name)
			}
		}
		ordered = filtered
	}
	return ordered
}

func operationSetsByName(sets []operation.Set) map[string]operation.Set {
	out := map[string]operation.Set{}
	for _, set := range sets {
		out[set.Name] = set
	}
	return out
}

func addOperationSet(add func(string), set operation.Set) {
	for _, ref := range set.Operations {
		add(string(ref.Name))
	}
}

func builtinLanguageSupports() []runtimelanguage.Support {
	return []runtimelanguage.Support{
		golang.LanguageSupport(),
		markdown.LanguageSupport(),
	}
}

func golangParserOperations() []string {
	return []string{
		golang.ProjectOp, golang.PackagesOp, golang.OutlineOp,
		golang.SymbolOp, golang.DefinitionOp, golang.SymbolInfoOp,
		golang.ReferencesOp, golang.ImportsOp, golang.ImplementationsOp,
		golang.CallersOp, golang.CalleesOp,
	}
}

func golangToolchainOperations() []string {
	return []string{
		golang.InfoOp, golang.EnvOp, golang.VersionOp,
		golang.DocOp, golang.ListOp, golang.TestOp,
		golang.FmtOp, golang.VetOp, golang.BuildOp,
		golang.InstallOp,
	}
}

func markdownOperations() []string {
	return []string{markdown.OutlineOp, markdown.LinksOp, markdown.DiagnosticsOp}
}
