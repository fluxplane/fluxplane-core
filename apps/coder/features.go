package coder

import (
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/plugins/discoveryplugin"
	"github.com/fluxplane/agentruntime/plugins/golangplugin"
	"github.com/fluxplane/agentruntime/plugins/lokiplugin"
	"github.com/fluxplane/agentruntime/plugins/markdownplugin"
	"github.com/fluxplane/agentruntime/plugins/memoryplugin"
	"github.com/fluxplane/agentruntime/plugins/projectplugin"
	"github.com/fluxplane/agentruntime/plugins/taskplugin"
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
		projectplugin.InventoryOp, projectplugin.FilesOp, projectplugin.TasksOp, projectplugin.TaskRunOp, projectplugin.DocsOp,
		"dir_list", "dir_tree", "file_read", "file_edit",
		"grep", "glob", "git_status", "git_diff", "git_add", "git_commit",
		"shell_exec", "code_execute", "web_search", "web_request",
		taskplugin.TaskCreateOp, taskplugin.TaskModifyOp, taskplugin.TaskGetOp, taskplugin.TaskListOp,
		taskplugin.TaskListArtifactsOp, taskplugin.TaskGetArtifactOp, taskplugin.TaskReadArtifactOp,
		taskplugin.TaskValidateOp, taskplugin.ReviewRequestOp,
		taskplugin.TaskRunOp, taskplugin.TaskSchedulerStatusOp, taskplugin.TaskSchedulerSetEnabledOp,
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
		Operations: []string{projectplugin.InventoryOp, projectplugin.FilesOp, projectplugin.TasksOp, projectplugin.TaskRunOp, projectplugin.DocsOp},
	}
}

// FullLocalCodingFeature preserves the current broad coder product surface.
func FullLocalCodingFeature() FeatureSpec {
	return FeatureSpec{
		Name: FeatureFullLocalCoding,
		OperationSets: []string{
			golangplugin.ParserSet,
			golangplugin.ToolchainSet,
			markdownplugin.Name,
		},
		Operations: []string{
			"dir_create", "dir_list", "dir_tree",
			"file_read", "file_create", "file_edit", "file_delete", "file_stat", "file_copy", "file_move",
			"glob", "grep",
			"web_search", "web_request",
			discoveryplugin.StatusOp, discoveryplugin.DiscoverOp, discoveryplugin.ProvidersOp, discoveryplugin.EndpointListOp, discoveryplugin.EndpointGetOp,
			lokiplugin.TestOp, lokiplugin.LabelsOp, lokiplugin.QueryOp, lokiplugin.RecentLogsOp,
			"datasource_search", "datasource_list", "datasource_get", "datasource_batch_get",
			"browser_open", "browser_navigate", "browser_click", "browser_type", "browser_select",
			"browser_read", "browser_screenshot", "browser_evaluate", "browser_wait", "browser_scroll",
			"browser_hover", "browser_back", "browser_forward", "browser_pdf", "browser_close",
			"git_status", "git_diff", "git_add", "git_commit", "git_tag", "git_push",
			"shell", "shell_info", "shell_exec", "process_run", "process_start", "process_ensure", "process_list", "process_status", "process_output", "process_wait", "process_stop", "process_kill",
			"code_execute",
			"clarify", taskplugin.TaskCreateOp, taskplugin.TaskModifyOp, taskplugin.TaskGetOp, taskplugin.TaskListOp,
			taskplugin.TaskListArtifactsOp, taskplugin.TaskGetArtifactOp, taskplugin.TaskReadArtifactOp,
			taskplugin.TaskValidateOp, taskplugin.ReviewRequestOp,
			taskplugin.TaskRunOp, taskplugin.TaskSchedulerStatusOp, taskplugin.TaskSchedulerSetEnabledOp,
			"skill",
			memoryplugin.MemorizeOp, memoryplugin.RetrieveOp, memoryplugin.ForgetOp, memoryplugin.OrganizeOp,
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
		golangplugin.LanguageSupport(),
		markdownplugin.LanguageSupport(),
	}
}

func golangParserOperations() []string {
	return []string{
		golangplugin.ProjectOp, golangplugin.PackagesOp, golangplugin.OutlineOp,
		golangplugin.SymbolOp, golangplugin.DefinitionOp, golangplugin.SymbolInfoOp,
		golangplugin.ReferencesOp, golangplugin.ImportsOp, golangplugin.ImplementationsOp,
		golangplugin.CallersOp, golangplugin.CalleesOp,
	}
}

func golangToolchainOperations() []string {
	return []string{
		golangplugin.InfoOp, golangplugin.EnvOp, golangplugin.VersionOp,
		golangplugin.DocOp, golangplugin.ListOp, golangplugin.TestOp,
		golangplugin.FmtOp, golangplugin.VetOp, golangplugin.BuildOp,
		golangplugin.InstallOp,
	}
}

func markdownOperations() []string {
	return []string{markdownplugin.OutlineOp, markdownplugin.LinksOp, markdownplugin.DiagnosticsOp}
}
