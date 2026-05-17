package coder

import (
	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/operation"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/plugins/golangplugin"
	"github.com/fluxplane/agentruntime/plugins/markdownplugin"
	"github.com/fluxplane/agentruntime/plugins/projectplugin"
	"github.com/fluxplane/agentruntime/plugins/taskplugin"
)

// FeatureName identifies a coder-local inert feature preset.
type FeatureName string

const (
	FeatureProjectSignals      FeatureName = "project_signals"
	FeatureLanguageSupport     FeatureName = "language_support"
	FeatureAvailableToolchains FeatureName = "available_toolchains"
	FeatureFullLocalCoding     FeatureName = "full_local_coding"
)

// ActivationInput supplies observed project/toolchain state to feature
// expansion. Empty state is allowed for deterministic static bundles.
type ActivationInput struct {
	ProjectSignals    []coreproject.Signal
	ToolchainStatuses []language.ToolchainStatus
	OperationSets     []operation.Set
}

// FeatureSpec describes operations implied by a coder feature.
type FeatureSpec struct {
	Name          FeatureName
	OperationSets []string
	Operations    []string
}

// OperationExpansionConfig describes feature-derived and explicit operations.
type OperationExpansionConfig struct {
	Features   []FeatureSpec
	Activation ActivationInput
	Add        []string
	Remove     []string
}

func fullCapabilityActivation() ActivationInput {
	return ActivationInput{
		ProjectSignals: []coreproject.Signal{
			{Kind: "manifest", Path: "go.mod", Language: "go", Toolchain: "go", Confidence: 1},
			{Kind: "documentation", Path: "README.md", Language: "markdown", Confidence: 1},
		},
		ToolchainStatuses: []language.ToolchainStatus{{ID: "go", Available: true}},
		OperationSets:     builtinLanguageOperationSets(),
	}
}

func fullCapabilityOperationNames() []string {
	return expandOperations(OperationExpansionConfig{
		Features: []FeatureSpec{
			ProjectSignalsFeature(),
			LanguageSupportFeature(),
			AvailableToolchainsFeature(),
			FullLocalCodingFeature(),
		},
		Activation: fullCapabilityActivation(),
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
		taskplugin.TaskListArtifactsOp, taskplugin.TaskGetArtifactOp, taskplugin.TaskValidateOp,
		taskplugin.TaskRunOp, taskplugin.TaskSchedulerStatusOp, taskplugin.TaskSchedulerSetEnabledOp,
		"datasource_search", "datasource_get", "datasource_batch_get",
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

// LanguageSupportFeature includes parser/workspace language operations relevant
// to observed project signals.
func LanguageSupportFeature() FeatureSpec {
	return FeatureSpec{Name: FeatureLanguageSupport}
}

// AvailableToolchainsFeature includes operation sets for available toolchains.
func AvailableToolchainsFeature() FeatureSpec {
	return FeatureSpec{Name: FeatureAvailableToolchains}
}

// FullLocalCodingFeature preserves the current broad coder product surface.
func FullLocalCodingFeature() FeatureSpec {
	return FeatureSpec{
		Name: FeatureFullLocalCoding,
		Operations: []string{
			"dir_create", "dir_list", "dir_tree",
			"file_read", "file_create", "file_edit", "file_delete", "file_stat", "file_copy", "file_move",
			"glob", "grep",
			"web_search", "web_request",
			"datasource_search", "datasource_get", "datasource_batch_get",
			"browser_open", "browser_navigate", "browser_click", "browser_type", "browser_select",
			"browser_read", "browser_screenshot", "browser_evaluate", "browser_wait", "browser_scroll",
			"browser_hover", "browser_back", "browser_forward", "browser_pdf", "browser_close",
			"git_status", "git_diff", "git_add", "git_commit", "git_tag", "git_push",
			"shell_exec", "process_start", "process_list", "process_status", "process_output", "process_kill",
			"code_execute",
			"clarify", taskplugin.TaskCreateOp, taskplugin.TaskModifyOp, taskplugin.TaskGetOp, taskplugin.TaskListOp,
			taskplugin.TaskListArtifactsOp, taskplugin.TaskGetArtifactOp, taskplugin.TaskValidateOp,
			taskplugin.TaskRunOp, taskplugin.TaskSchedulerStatusOp, taskplugin.TaskSchedulerSetEnabledOp,
			"skill",
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
	sets := operationSetsByName(cfg.Activation.OperationSets)
	for _, feature := range cfg.Features {
		for _, name := range feature.Operations {
			add(name)
		}
		for _, setName := range feature.OperationSets {
			addOperationSet(add, sets[setName])
		}
		switch feature.Name {
		case FeatureLanguageSupport:
			if hasLanguageSignal(cfg.Activation.ProjectSignals, "go") {
				addOperationSet(add, sets[golangplugin.ParserSet])
			}
			if hasLanguageSignal(cfg.Activation.ProjectSignals, "markdown") {
				addOperationSet(add, sets[markdownplugin.Name])
			}
		case FeatureAvailableToolchains:
			for _, status := range cfg.Activation.ToolchainStatuses {
				if !status.Available {
					continue
				}
				if status.ID == "go" {
					addOperationSet(add, sets[golangplugin.ToolchainSet])
				}
			}
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

func hasLanguageSignal(signals []coreproject.Signal, lang language.LanguageID) bool {
	for _, signal := range signals {
		if signal.Language == lang {
			return true
		}
	}
	return false
}

func builtinLanguageOperationSets() []operation.Set {
	return []operation.Set{
		{Name: golangplugin.ParserSet, Operations: refs(golangParserOperations())},
		{Name: golangplugin.ToolchainSet, Operations: refs(golangToolchainOperations())},
		{Name: markdownplugin.Name, Operations: refs(markdownOperations())},
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

func refs(names []string) []operation.Ref {
	out := make([]operation.Ref, 0, len(names))
	for _, name := range names {
		out = append(out, operation.Ref{Name: operation.Name(name)})
	}
	return out
}
