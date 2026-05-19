package golangplugin

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"sort"
	"strings"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/language/golang"
	"github.com/fluxplane/agentruntime/core/operation"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	runtimelanguage "github.com/fluxplane/agentruntime/runtime/language"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimeproject "github.com/fluxplane/agentruntime/runtime/project"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name               = "golang"
	ParserSet          = "golang.parser"
	ToolchainSet       = "golang.toolchain"
	ToolchainObserver  = "golang.toolchain.status"
	ToolchainSignals   = "golang.toolchain.signals"
	ProjectOp          = golang.ProjectOp
	InfoOp             = golang.InfoOp
	EnvOp              = golang.EnvOp
	VersionOp          = golang.VersionOp
	DocOp              = golang.DocOp
	ListOp             = golang.ListOp
	TestOp             = golang.TestOp
	FmtOp              = golang.FmtOp
	VetOp              = golang.VetOp
	BuildOp            = golang.BuildOp
	InstallOp          = golang.InstallOp
	GetOp              = golang.GetOp
	ModTidyOp          = golang.ModTidyOp
	PackagesOp         = golang.PackagesOp
	OutlineOp          = golang.OutlineOp
	SymbolOp           = golang.SymbolOp
	DefinitionOp       = golang.DefinitionOp
	SymbolInfoOp       = golang.SymbolInfoOp
	ReferencesOp       = golang.ReferencesOp
	ImportsOp          = golang.ImportsOp
	ImplementationsOp  = golang.ImplementationsOp
	CallersOp          = golang.CallersOp
	CalleesOp          = golang.CalleesOp
	SummaryProvider    = golang.SummaryProvider
	defaultMaxResults  = 200
	defaultSourceBytes = 512 * 1024
)

const (
	ObservationToolchainStatus = "toolchain.status"
	SignalToolchainAvailable   = "toolchain.available"
)

// Plugin contributes Go language support operations.
type Plugin struct {
	system  system.System
	manager *runtimeproject.Manager
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.SignalDeriverContributor = Plugin{}

// New returns a Go language plugin.
func New(sys system.System) Plugin {
	var manager *runtimeproject.Manager
	if sys != nil && sys.Workspace() != nil {
		manager = runtimeproject.NewManager(sys.Workspace())
	}
	return Plugin{system: sys, manager: manager}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Go language structure operations."}
}

// LanguageSupport returns the reusable Go language activation descriptor.
func LanguageSupport() runtimelanguage.Support {
	specs := specs()
	parserRefs := refsByName(specs, parserOperationNames())
	toolchainRefs := refsByName(specs, toolchainOperationNames())
	return runtimelanguage.StaticSupport{Spec: runtimelanguage.SupportSpec{
		Provider: language.ProviderSpec{
			Name:         language.ProviderName(Name),
			Language:     language.LanguageGo,
			Description:  "Go parser and toolchain support.",
			Capabilities: goCapabilities(),
		},
		OperationSets: []operation.Set{
			{
				Name:        ParserSet,
				Description: "Go parser and workspace language operations that do not require the go binary.",
				Operations:  parserRefs,
			},
		},
		ToolchainOperationSets: []operation.Set{
			{
				Name:        ToolchainSet,
				Description: "Go toolchain operations backed by external go commands.",
				Operations:  toolchainRefs,
			},
		},
		Toolchains: []language.ToolchainSpec{goToolchainSpec(toolchainRefs)},
	}}
}

// Contributions returns Go operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	support := LanguageSupport().SupportSpec()
	return resource.ContributionBundle{
		ContextProviders: []corecontext.ProviderSpec{summaryContextSpec()},
		Observers:        []coreenvironment.ObserverSpec{toolchainObserverSpec()},
		SignalDerivers:   []coreenvironment.SignalDeriverSpec{toolchainSignalDeriverSpec()},
		OperationSets: append(append([]operation.Set{}, support.OperationSets...),
			support.ToolchainOperationSets...),
		Toolchains: support.Toolchains,
		Operations: specs,
		PostEditChecks: []coresession.PostEditCheckSpec{{
			Name:        "golang.fmt",
			Description: "Run go fmt after Go source file edits.",
			MatchPaths:  []string{"*.go", "**/*.go"},
			Operation:   operation.Ref{Name: FmtOp},
			Input: map[string]any{
				"patterns": []any{"${path}"},
				"dry_run":  false,
			},
			Mode: coresession.PostEditCheckModeFix,
		}},
	}, nil
}

// EnvironmentObservers returns executable Go environment observers.
func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeenvironment.Observer, error) {
	if p.system == nil {
		return nil, nil
	}
	support := LanguageSupport().SupportSpec()
	if len(support.Toolchains) == 0 {
		return nil, nil
	}
	return []runtimeenvironment.Observer{goToolchainObserver{system: p.system, spec: support.Toolchains[0]}}, nil
}

// SignalDerivers returns executable Go signal derivation.
func (Plugin) SignalDerivers(context.Context, pluginhost.Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{goToolchainSignalDeriver{}}, nil
}

// ContextProviders returns executable Go context providers.
func (p Plugin) ContextProviders(context.Context, pluginhost.Context) ([]corecontext.Provider, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, nil
	}
	manager := p.manager
	if manager == nil {
		manager = runtimeproject.NewManager(p.system.Workspace())
	}
	return []corecontext.Provider{summaryProvider{plugin: p, manager: manager}}, nil
}

// Operations returns executable Go operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, fmt.Errorf("golangplugin: system workspace is nil")
	}
	manager := p.manager
	if manager == nil {
		manager = runtimeproject.NewManager(p.system.Workspace())
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[golang.ProjectQuery, operation.Rendered](specByName(ProjectOp), p.goProject(manager)),
		operationruntime.NewTypedResult[golang.GoInfoQuery, operation.Rendered](specByName(InfoOp), p.goInfo(), operationruntime.WithIntent(goInfoIntent)),
		operationruntime.NewTypedResult[golang.GoEnvQuery, operation.Rendered](specByName(EnvOp), p.goEnv(), operationruntime.WithIntent(goEnvIntent)),
		operationruntime.NewTypedResult[golang.GoVersionQuery, operation.Rendered](specByName(VersionOp), p.goVersion(), operationruntime.WithIntent(goVersionIntent)),
		operationruntime.NewTypedResult[golang.GoDocQuery, operation.Rendered](specByName(DocOp), p.goDoc(), operationruntime.WithIntent(goDocIntent)),
		operationruntime.NewTypedResult[golang.GoListQuery, operation.Rendered](specByName(ListOp), p.goList(), operationruntime.WithIntent(goListIntent)),
		operationruntime.NewTypedResult[golang.GoTestQuery, operation.Rendered](specByName(TestOp), p.goTest(), operationruntime.WithIntent(goTestIntent)),
		operationruntime.NewTypedResult[golang.GoFmtQuery, operation.Rendered](specByName(FmtOp), p.goFmt(), operationruntime.WithIntent(goFmtIntent)),
		operationruntime.NewTypedResult[golang.GoVetQuery, operation.Rendered](specByName(VetOp), p.goVet(), operationruntime.WithIntent(goVetIntent)),
		operationruntime.NewTypedResult[golang.GoBuildQuery, operation.Rendered](specByName(BuildOp), p.goBuild(), operationruntime.WithIntent(goBuildIntent)),
		operationruntime.NewTypedResult[golang.GoInstallQuery, operation.Rendered](specByName(InstallOp), p.goInstall(), operationruntime.WithIntent(goInstallIntent)),
		operationruntime.NewTypedResult[golang.GoGetQuery, operation.Rendered](specByName(GetOp), p.goGet(), operationruntime.WithIntent(goGetIntent)),
		operationruntime.NewTypedResult[golang.GoModTidyQuery, operation.Rendered](specByName(ModTidyOp), p.goModTidy(), operationruntime.WithIntent(goModTidyIntent)),
		operationruntime.NewTypedResult[golang.PackageQuery, operation.Rendered](specByName(PackagesOp), p.goPackages(manager)),
		operationruntime.NewTypedResult[golang.OutlineQuery, operation.Rendered](specByName(OutlineOp), p.goOutline()),
		operationruntime.NewTypedResult[golang.SymbolQuery, operation.Rendered](specByName(SymbolOp), p.goSymbol()),
		operationruntime.NewTypedResult[golang.NavigationQuery, operation.Rendered](specByName(DefinitionOp), p.goDefinition()),
		operationruntime.NewTypedResult[golang.NavigationQuery, operation.Rendered](specByName(SymbolInfoOp), p.goSymbolInfo()),
		operationruntime.NewTypedResult[golang.ReferenceQuery, operation.Rendered](specByName(ReferencesOp), p.goReferences()),
		operationruntime.NewTypedResult[golang.ImportQuery, operation.Rendered](specByName(ImportsOp), p.goImports()),
		operationruntime.NewTypedResult[golang.ImplementationQuery, operation.Rendered](specByName(ImplementationsOp), p.goImplementations()),
		operationruntime.NewTypedResult[golang.CallQuery, operation.Rendered](specByName(CallersOp), p.goCallers()),
		operationruntime.NewTypedResult[golang.CallQuery, operation.Rendered](specByName(CalleesOp), p.goCallees()),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec[golang.ProjectQuery](ProjectOp, "Summarize Go modules and go.work workspaces detected from Workspace project inventory. Uses memory-only refresh and reads only through the Workspace boundary."),
		specWithSemantics[golang.GoInfoQuery](InfoOp, "Return curated Go toolchain orientation including version, target, module/workspace paths, proxy/private settings, and cache/tool directories.", goToolReadSemantics()),
		specWithSemantics[golang.GoEnvQuery](EnvOp, "Return read-only structured go env values. Supports curated, explicit, all, and changed views; does not support go env -w or -u.", goToolReadSemantics()),
		specWithSemantics[golang.GoVersionQuery](VersionOp, "Return go version output for the toolchain or workspace-relative binaries with optional module build information.", goToolReadSemantics()),
		specWithSemantics[golang.GoDocQuery](DocOp, "Return package or symbol documentation through go doc. Supports explicit package/symbol selectors or position-derived source symbols.", goToolReadSemantics()),
		specWithSemantics[golang.GoListQuery](ListOp, "Return structured go list -json package or module metadata for explicit package/module patterns.", goToolReadSemantics()),
		specWithSemantics[golang.GoTestQuery](TestOp, "Run go test -json for selected package patterns and return structured package/test summaries.", goToolCheckSemantics()),
		specWithSemantics[golang.GoFmtQuery](FmtOp, "Run go fmt for selected package patterns. Defaults to dry-run preview and requires explicit dry_run=false for formatting writes.", goToolMutatingSemantics()),
		specWithSemantics[golang.GoVetQuery](VetOp, "Run go vet for selected package patterns and return diagnostics. Fix and vettool execution are unsupported.", goToolCheckSemantics()),
		specWithSemantics[golang.GoBuildQuery](BuildOp, "Run go build as a compile check for selected package patterns. Output artifact placement is unsupported.", goToolCheckSemantics()),
		specWithSemantics[golang.GoInstallQuery](InstallOp, "Run go install for explicit packages. Defaults to dry-run preview and restricts environment overrides.", goToolInstallSemantics()),
		specWithSemantics[golang.GoGetQuery](GetOp, "Run go get for explicit module or package queries. Defaults to dry-run preview and requires explicit dry_run=false to update go.mod/go.sum.", goToolDependencySemantics()),
		specWithSemantics[golang.GoModTidyQuery](ModTidyOp, "Run go mod tidy. Defaults to dry-run diff preview and requires explicit dry_run=false to update go.mod/go.sum.", goToolDependencySemantics()),
		spec[golang.PackageQuery](PackagesOp, "Group Go files into packages by directory and package name. This is parser-based and does not run go/packages or external commands."),
		spec[golang.OutlineQuery](OutlineOp, "Parse a Go file or package directory into a bounded language outline of declarations, signatures, docs, and positions."),
		spec[golang.SymbolQuery](SymbolOp, "Search parsed Go declaration symbols by name, kind, path, or package. This is declaration search, not full type-aware reference search."),
		spec[golang.NavigationQuery](DefinitionOp, "Resolve the AST/package-level Go declaration for an identifier, import, or package token at a source position. This is parser-based and reports incomplete semantic limitations."),
		spec[golang.NavigationQuery](SymbolInfoOp, "Return compact AST/package-level Go symbol information for a source position, falling back to the enclosing declaration when no identifier definition resolves."),
		spec[golang.ReferenceQuery](ReferencesOp, "Return bounded AST/package-level Go references for the selected symbol at a source position. Scope defaults to the same package directory, include_tests defaults to true, and results report parser-only limitations."),
		spec[golang.ImportQuery](ImportsOp, "Return direct and reverse Go import edges from parser-only source reads. Direction defaults to both, include_tests defaults to true, and reverse lookups stay bounded to the requested path scope."),
		spec[golang.ImplementationQuery](ImplementationsOp, "Return best-effort AST-only Go implementation relationships for a selected interface, concrete type, or method. Scope defaults to the selected package; module scope is supported."),
		spec[golang.CallQuery](CallersOp, "Return bounded AST-only direct callers for the selected Go function or method. Scope defaults to package, include_tests defaults to true, and module scope is best-effort for module-local function selectors."),
		spec[golang.CallQuery](CalleesOp, "Return bounded AST-only direct callees from the selected Go function or method body. Scope defaults to package, include_tests defaults to true, and unresolved external/function-value calls are reported as limitations."),
	}
}

func spec[I any](name, description string) operation.Spec {
	return specWithSemantics[I](name, description, operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectFilesystem, operation.EffectReadExternal},
		Idempotency: operation.IdempotencyIdempotent,
		Risk:        operation.RiskLow,
	})
}

func specWithSemantics[I any](name, description string, semantics operation.Semantics) operation.Spec {
	return operationruntime.WithTypedContract[I, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics:   semantics,
	})
}

func goToolReadSemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
		Idempotency: operation.IdempotencyIdempotent,
		Risk:        operation.RiskLow,
	}
}

func goToolCheckSemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
		Idempotency: operation.IdempotencyIdempotent,
		Risk:        operation.RiskMedium,
	}
}

func goToolMutatingSemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectUpdate, operation.EffectReadExternal},
		Idempotency: operation.IdempotencyIdempotent,
		Risk:        operation.RiskMedium,
	}
}

func goToolInstallSemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectWriteExternal, operation.EffectReadExternal},
		Idempotency: operation.IdempotencyNonIdempotent,
		Risk:        operation.RiskHigh,
	}
}

func goToolDependencySemantics() operation.Semantics {
	return operation.Semantics{
		Determinism: operation.DeterminismNonDeterministic,
		Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectUpdate, operation.EffectWriteExternal, operation.EffectReadExternal},
		Idempotency: operation.IdempotencyIdempotent,
		Risk:        operation.RiskHigh,
	}
}

func specByName(name string) operation.Spec {
	for _, spec := range specs() {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}
}

func refsByName(specs []operation.Spec, names []string) []operation.Ref {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	var out []operation.Ref
	for _, spec := range specs {
		if allowed[string(spec.Ref.Name)] {
			out = append(out, spec.Ref)
		}
	}
	return out
}

func parserOperationNames() []string {
	return []string{
		ProjectOp, PackagesOp, OutlineOp, SymbolOp, DefinitionOp, SymbolInfoOp,
		ReferencesOp, ImportsOp, ImplementationsOp, CallersOp, CalleesOp,
	}
}

func toolchainOperationNames() []string {
	return []string{InfoOp, EnvOp, VersionOp, DocOp, ListOp, TestOp, FmtOp, VetOp, BuildOp, InstallOp, GetOp, ModTidyOp}
}

func goCapabilities() []language.Capability {
	return []language.Capability{
		language.CapabilityProject,
		language.CapabilityPackage,
		language.CapabilityOutline,
		language.CapabilitySymbol,
		language.CapabilityDefinition,
		language.CapabilitySymbolInfo,
		language.CapabilityReferences,
		language.CapabilityImplementations,
		language.CapabilityCalls,
		language.CapabilityImports,
		language.CapabilityDiagnostics,
		language.CapabilityFormat,
	}
}

func goToolchainSpec(ops []operation.Ref) language.ToolchainSpec {
	return language.ToolchainSpec{
		ID:               "go",
		DisplayName:      "Go",
		Languages:        []language.LanguageID{language.LanguageGo},
		RequiredBinaries: []language.ToolchainBinarySpec{{Name: "go", VersionArgs: []string{"version"}}},
		Capabilities: []language.ToolchainCapability{
			language.ToolchainCapabilityPackageInfo,
			language.ToolchainCapabilityDoc,
			language.ToolchainCapabilityList,
			language.ToolchainCapabilityTest,
			language.ToolchainCapabilityFormat,
			language.ToolchainCapabilityLint,
			language.ToolchainCapabilityBuild,
			language.ToolchainCapabilityInstall,
		},
		OperationSets:     []string{ToolchainSet},
		Operations:        ops,
		ActivationSignals: []string{"go.mod", "go.work"},
	}
}

type goToolchainObserver struct {
	system system.System
	spec   language.ToolchainSpec
}

func (goToolchainObserver) Spec() coreenvironment.ObserverSpec {
	return toolchainObserverSpec()
}

func (o goToolchainObserver) Observe(ctx context.Context, _ runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	if o.system == nil {
		return nil, nil
	}
	status := runtimelanguage.ResolveToolchainStatus(ctx, o.system, o.spec)
	scope := "local"
	if workspace := o.system.Workspace(); workspace != nil && strings.TrimSpace(workspace.Root()) != "" {
		scope = "workspace:" + workspace.Root()
	}
	return []coreenvironment.Observation{{
		ID:      "toolchain:" + status.ID,
		Kind:    ObservationToolchainStatus,
		Scope:   scope,
		Content: status,
		Environment: coreenvironment.Ref{
			Name: "local",
		},
	}}, nil
}

func toolchainObserverSpec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:        ToolchainObserver,
		Description: "Observes local Go toolchain availability without exposing environment secrets.",
		Environment: coreenvironment.Ref{
			Name: "local",
		},
		Phase:           coreenvironment.PhaseSessionOpen,
		ObservableKinds: []string{ObservationToolchainStatus},
		Dynamic:         true,
	}
}

type goToolchainSignalDeriver struct{}

func (goToolchainSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return toolchainSignalDeriverSpec()
}

func (goToolchainSignalDeriver) Derive(_ context.Context, req runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	var out []coreenvironment.Signal
	for _, observation := range req.Observations {
		if observation.Kind != ObservationToolchainStatus {
			continue
		}
		status, ok := observation.Content.(language.ToolchainStatus)
		if !ok || !status.Available || strings.TrimSpace(status.ID) == "" {
			continue
		}
		signal := coreenvironment.Signal{
			Kind:           SignalToolchainAvailable,
			Target:         status.ID,
			Scope:          observation.Scope,
			Environment:    observation.Environment,
			Confidence:     1,
			ObservationIDs: observationIDs(observation.ID),
		}
		if strings.TrimSpace(status.Version) != "" {
			signal.Metadata = map[string]string{"version": strings.TrimSpace(status.Version)}
		}
		out = append(out, signal)
	}
	return out, nil
}

func toolchainSignalDeriverSpec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{
		Name:             ToolchainSignals,
		Description:      "Derives Go toolchain availability signals from toolchain status observations.",
		ObservationKinds: []string{ObservationToolchainStatus},
	}
}

func observationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

func summaryContextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             SummaryProvider,
		Description:      "Compact Go module and package orientation.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
		Annotations:      map[string]string{corecontext.AnnotationAutoContext: "true"},
	}
}

type summaryProvider struct {
	plugin  Plugin
	manager *runtimeproject.Manager
}

func (p summaryProvider) Spec() corecontext.ProviderSpec { return summaryContextSpec() }

func (p summaryProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	if p.plugin.system == nil || p.manager == nil {
		return nil, nil
	}
	inventory, _, err := p.manager.Inventory(ctx, coreproject.InventoryQuery{})
	if err != nil {
		return nil, nil
	}
	var projects []coreproject.Project
	for _, project := range inventory.Projects {
		if hasGoFacet(project) {
			projects = append(projects, project)
		}
	}
	pkgs, _ := p.plugin.collectPackages(ctx, ".", 40, maxBytes(0))
	if len(projects) == 0 && len(pkgs) == 0 {
		return nil, nil
	}
	content := renderGoSummary(projects, pkgs)
	return []corecontext.Block{{
		ID:        SummaryProvider,
		Provider:  SummaryProvider,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "Go Summary",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func renderGoSummary(projects []coreproject.Project, pkgs []language.Package) string {
	lines := []string{"Go workspace summary:"}
	if len(projects) > 0 {
		lines = append(lines, "- modules/workspaces:")
		for i, project := range projects {
			if i >= 5 {
				lines = append(lines, fmt.Sprintf("  - %d more", len(projects)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("  - %s [%s] %s", displayRoot(project.Root), project.ID, project.Name))
		}
	}
	if len(pkgs) > 0 {
		lines = append(lines, fmt.Sprintf("- packages discovered: %d", len(pkgs)))
		if groups := packageGroups(pkgs, 8); len(groups) > 0 {
			lines = append(lines, "- package dirs: "+strings.Join(groups, ", "))
		}
		if cmds := commandPackages(pkgs, 6); len(cmds) > 0 {
			lines = append(lines, "- command entrypoints: "+strings.Join(cmds, ", "))
		}
	}
	lines = append(lines, "Use go_info, go_env, go_version, go_doc, go_list, go_test, go_fmt, go_vet, go_build, go_install, go_project, go_packages, go_outline, go_symbol, go_definition, go_symbol_info, go_references, go_imports, go_implementations, go_callers, and go_callees for details.")
	return strings.Join(lines, "\n")
}

func packageGroups(pkgs []language.Package, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, pkg := range pkgs {
		dir := pkg.Dir
		if dir == "" {
			dir = "."
		}
		group := dir
		if strings.Contains(dir, "/") {
			group = strings.SplitN(dir, "/", 2)[0] + "/*"
		}
		if !seen[group] {
			out = append(out, group)
			seen[group] = true
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func commandPackages(pkgs []language.Package, limit int) []string {
	var out []string
	for _, pkg := range pkgs {
		if pkg.Name != "main" || !strings.HasPrefix(pkg.Dir, "cmd/") {
			continue
		}
		out = append(out, pkg.Dir)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func (p Plugin) goProject(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[golang.ProjectQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.ProjectQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_project_input", err.Error(), nil)
		}
		var projects []coreproject.Project
		rebuilt := false
		if strings.TrimSpace(req.Path) != "" {
			project, wasRebuilt, err := manager.Project(ctx, coreproject.ProjectQuery{Path: req.Path, Refresh: req.Refresh})
			if err != nil {
				return operation.Failed("go_project_failed", err.Error(), nil)
			}
			rebuilt = wasRebuilt
			if hasGoFacet(project) {
				projects = append(projects, project)
			}
		} else {
			inventory, wasRebuilt, err := manager.Inventory(ctx, coreproject.InventoryQuery{Refresh: req.Refresh})
			if err != nil {
				return operation.Failed("go_project_failed", err.Error(), nil)
			}
			rebuilt = wasRebuilt
			for _, project := range inventory.Projects {
				if hasGoFacet(project) {
					projects = append(projects, project)
				}
			}
		}
		if req.MaxResults > 0 && len(projects) > req.MaxResults {
			projects = projects[:req.MaxResults]
		}
		lines := []string{fmt.Sprintf("Go projects: %d", len(projects))}
		for _, project := range projects {
			lines = append(lines, fmt.Sprintf("- %s [%s]: %s", displayRoot(project.Root), project.ID, project.Name))
			for _, facet := range project.Facets {
				if facet.Kind == coreproject.FacetGoModule || facet.Kind == coreproject.FacetGoWorkspace {
					lines = append(lines, fmt.Sprintf("  - %s %s", facet.Kind, facet.Manifest.Path))
				}
			}
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"projects": compactProjects(projects), "rebuilt": rebuilt}})
	}
}

func (p Plugin) goPackages(manager *runtimeproject.Manager) operationruntime.TypedResultHandler[golang.PackageQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.PackageQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_packages_input", err.Error(), nil)
		}
		scope, err := p.packageScope(ctx, manager, req)
		if err != nil {
			return operation.Failed("go_packages_failed", err.Error(), nil)
		}
		pkgs, err := p.collectPackages(ctx, scope, req.MaxResults, maxBytes(0))
		if err != nil {
			return operation.Failed("go_packages_failed", err.Error(), nil)
		}
		lines := []string{fmt.Sprintf("Go packages: %d", len(pkgs))}
		for _, pkg := range pkgs {
			lines = append(lines, fmt.Sprintf("- %s %s (%d files)", pkg.Dir, pkg.Name, len(pkg.Files)))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"packages": compactPackages(pkgs)}})
	}
}

func (p Plugin) packageScope(ctx context.Context, manager *runtimeproject.Manager, req golang.PackageQuery) (string, error) {
	if strings.TrimSpace(req.Path) != "" {
		return req.Path, nil
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", nil
	}
	project, _, err := manager.Project(ctx, coreproject.ProjectQuery{ProjectID: coreproject.ID(req.ProjectID), Refresh: req.Refresh})
	if err != nil {
		return "", err
	}
	if project.Root == "" {
		return ".", nil
	}
	return project.Root, nil
}

func (p Plugin) goOutline() operationruntime.TypedResultHandler[golang.OutlineQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.OutlineQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_outline_input", err.Error(), nil)
		}
		if strings.TrimSpace(req.Path) == "" {
			req.Path = packagePath(req.PackageID)
			if strings.TrimSpace(req.Path) == "" {
				return operation.Failed("invalid_go_outline_input", "path or package_id is required", nil)
			}
		}
		files, err := p.goFilesForPath(ctx, req.Path)
		if err != nil {
			return operation.Failed("go_outline_failed", err.Error(), nil)
		}
		max := maxResults(req.MaxResults)
		var symbols []language.Symbol
		var diagnostics []language.Diagnostic
		truncated := false
		for _, file := range files {
			fileSymbols, err := p.parseFileSymbols(ctx, file, req.IncludeDocs, maxDocBytes(req.MaxBytes))
			if err != nil {
				if len(files) == 1 {
					return operation.Failed("go_outline_failed", err.Error(), map[string]any{"path": file})
				}
				diagnostics = append(diagnostics, diagnostic(file, err))
				continue
			}
			for _, symbol := range fileSymbols {
				if len(symbols) >= max {
					truncated = true
					break
				}
				symbols = append(symbols, symbol)
			}
			if truncated {
				break
			}
		}
		outline := language.Outline{Path: cleanRel(req.Path), Language: language.LanguageGo, Symbols: symbols, Truncated: truncated}
		lines := []string{fmt.Sprintf("Go outline: %s", outline.Path)}
		for _, symbol := range symbols {
			lines = append(lines, fmt.Sprintf("- %s %s", symbol.Kind, symbol.Name))
			if req.IncludeDocs && firstDocLine(symbol.Doc) != "" {
				lines = append(lines, "  doc: "+firstDocLine(symbol.Doc))
			}
			for _, child := range symbol.Children {
				lines = append(lines, fmt.Sprintf("  - %s %s", child.Kind, child.Name))
			}
		}
		if len(diagnostics) > 0 {
			lines = append(lines, fmt.Sprintf("Diagnostics: %d file(s) skipped", len(diagnostics)))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"outline": compactOutline(outline, req.IncludeDocs), "diagnostics": diagnostics}})
	}
}

func (p Plugin) goSymbol() operationruntime.TypedResultHandler[golang.SymbolQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.SymbolQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_symbol_input", err.Error(), nil)
		}
		max := maxResults(req.MaxResults)
		if strings.TrimSpace(req.Path) == "" {
			req.Path = packagePath(req.PackageID)
		}
		files, err := p.goFilesForPath(ctx, req.Path)
		if err != nil {
			return operation.Failed("go_symbol_failed", err.Error(), nil)
		}
		var symbols []language.Symbol
		var diagnostics []language.Diagnostic
		for _, file := range files {
			fileSymbols, err := p.parseFileSymbols(ctx, file, req.IncludeDocs, maxDocBytes(req.MaxBytes))
			if err != nil {
				if len(files) == 1 {
					return operation.Failed("go_symbol_failed", err.Error(), map[string]any{"path": file})
				}
				diagnostics = append(diagnostics, diagnostic(file, err))
				continue
			}
			for _, symbol := range flattenSymbols(fileSymbols) {
				if !symbolMatches(symbol, req) {
					continue
				}
				symbols = append(symbols, symbol)
				if len(symbols) >= max {
					break
				}
			}
			if len(symbols) >= max {
				break
			}
		}
		lines := []string{fmt.Sprintf("Go symbols: %d", len(symbols))}
		for _, symbol := range symbols {
			lines = append(lines, fmt.Sprintf("- %s %s %s:%d", symbol.Kind, symbol.Name, symbol.Location.Path, symbol.Location.Range.Start.Line))
			if req.IncludeDocs && firstDocLine(symbol.Doc) != "" {
				lines = append(lines, "  doc: "+firstDocLine(symbol.Doc))
			}
		}
		if len(diagnostics) > 0 {
			lines = append(lines, fmt.Sprintf("Diagnostics: %d file(s) skipped", len(diagnostics)))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"symbols": compactSymbols(symbols, req.IncludeDocs), "diagnostics": diagnostics}})
	}
}

func (p Plugin) collectPackages(ctx context.Context, rawPath string, limit int, readLimit int) ([]language.Package, error) {
	files, err := p.goFilesForPath(ctx, rawPath)
	if err != nil {
		return nil, err
	}
	max := maxResults(limit)
	type pkgBuilder struct {
		pkg language.Package
		imp map[string]language.Import
	}
	builders := map[string]*pkgBuilder{}
	for _, rel := range files {
		data, truncated, _, err := p.system.Workspace().ReadFile(ctx, rel, int64(readLimit))
		if err != nil {
			return nil, err
		}
		if truncated {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, rel, data, parser.ImportsOnly)
		if err != nil {
			continue
		}
		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		key := dir + "\x00" + file.Name.Name
		builder := builders[key]
		if builder == nil {
			builder = &pkgBuilder{pkg: language.Package{
				ID:       packageID(dir, file.Name.Name),
				Language: language.LanguageGo,
				Name:     file.Name.Name,
				Dir:      dir,
			}, imp: map[string]language.Import{}}
			builders[key] = builder
		}
		builder.pkg.Files = append(builder.pkg.Files, rel)
		if strings.HasSuffix(file.Name.Name, "_test") {
			builder.pkg.TestFor = strings.TrimSuffix(file.Name.Name, "_test")
		}
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, `"`)
			name := ""
			if spec.Name != nil {
				name = spec.Name.Name
			}
			builder.imp[importPath] = language.Import{Path: importPath, Name: name, SourcePath: rel, PackageID: builder.pkg.ID, Location: location(fset, rel, spec.Pos(), spec.End())}
		}
	}
	pkgs := make([]language.Package, 0, len(builders))
	for _, builder := range builders {
		sort.Strings(builder.pkg.Files)
		keys := make([]string, 0, len(builder.imp))
		for key := range builder.imp {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.pkg.Imports = append(builder.pkg.Imports, builder.imp[key])
		}
		pkgs = append(pkgs, builder.pkg)
	}
	sort.SliceStable(pkgs, func(i, j int) bool {
		if pkgs[i].Dir == pkgs[j].Dir {
			return pkgs[i].Name < pkgs[j].Name
		}
		return pkgs[i].Dir < pkgs[j].Dir
	})
	if len(pkgs) > max {
		pkgs = pkgs[:max]
	}
	return pkgs, nil
}

func (p Plugin) goFilesForPath(ctx context.Context, rawPath string) ([]string, error) {
	rel := cleanRel(rawPath)
	if rel != "" {
		if info, _, err := p.system.Workspace().Stat(ctx, rel); err == nil && !info.IsDir() {
			if strings.HasSuffix(rel, ".go") {
				if isVendoredPath(rel) {
					return nil, nil
				}
				return []string{rel}, nil
			}
			return nil, fmt.Errorf("path is not a Go file")
		}
	}
	root := rel
	if root == "" {
		root = "."
	}
	entries, _, _, err := p.system.Workspace().Walk(ctx, root, system.WalkOptions{Depth: 50, ShowHidden: false, MaxEntries: 10000, FilesOnly: true, SkipDirs: noisyDirs()})
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Path.Rel, ".go") && !isVendoredPath(entry.Path.Rel) {
			files = append(files, entry.Path.Rel)
		}
	}
	sort.Strings(files)
	return files, nil
}

func isVendoredPath(rel string) bool {
	for _, part := range strings.Split(cleanRel(rel), "/") {
		if part == "vendor" {
			return true
		}
	}
	return false
}

func (p Plugin) parseFileSymbols(ctx context.Context, rel string, includeDocs bool, docLimit int) ([]language.Symbol, error) {
	data, truncated, _, err := p.system.Workspace().ReadFile(ctx, rel, int64(defaultSourceBytes))
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("source file exceeds parser byte limit (%d bytes)", defaultSourceBytes)
	}
	mode := parser.ParseComments
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, mode)
	if err != nil {
		return nil, err
	}
	pkgID := packageID(pathDir(rel), file.Name.Name)
	var symbols []language.Symbol
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := language.SymbolFunction
			container := ""
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = language.SymbolMethod
				container = exprString(fset, d.Recv.List[0].Type)
				name = strings.TrimPrefix(container, "*") + "." + d.Name.Name
			}
			symbols = append(symbols, language.Symbol{
				ID:             symbolID(rel, kind, name, d.Pos()),
				Language:       language.LanguageGo,
				Kind:           kind,
				Name:           name,
				Container:      container,
				PackageID:      pkgID,
				Location:       location(fset, rel, d.Pos(), d.End()),
				Range:          location(fset, rel, d.Pos(), d.End()).Range,
				SelectionRange: location(fset, rel, d.Name.Pos(), d.Name.End()).Range,
				Signature:      funcSignature(fset, d),
				Doc:            docText(includeDocs, d.Doc, docLimit),
			})
		case *ast.GenDecl:
			symbols = append(symbols, genDeclSymbols(fset, rel, pkgID, includeDocs, docLimit, d)...)
		}
	}
	sort.SliceStable(symbols, func(i, j int) bool {
		left := symbols[i].Location.Range.Start.Line
		right := symbols[j].Location.Range.Start.Line
		if left == right {
			return symbols[i].Name < symbols[j].Name
		}
		return left < right
	})
	return symbols, nil
}

func genDeclSymbols(fset *token.FileSet, rel, pkgID string, includeDocs bool, docLimit int, decl *ast.GenDecl) []language.Symbol {
	var out []language.Symbol
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			kind := language.SymbolType
			var children []language.Symbol
			switch t := s.Type.(type) {
			case *ast.StructType:
				kind = language.SymbolStruct
				children = fieldSymbols(fset, rel, pkgID, s.Name.Name, t.Fields)
			case *ast.InterfaceType:
				kind = language.SymbolInterface
				children = fieldSymbols(fset, rel, pkgID, s.Name.Name, t.Methods)
			}
			out = append(out, language.Symbol{
				ID:             symbolID(rel, kind, s.Name.Name, s.Pos()),
				Language:       language.LanguageGo,
				Kind:           kind,
				Name:           s.Name.Name,
				PackageID:      pkgID,
				Location:       location(fset, rel, s.Pos(), s.End()),
				Range:          location(fset, rel, s.Pos(), s.End()).Range,
				SelectionRange: location(fset, rel, s.Name.Pos(), s.Name.End()).Range,
				Signature:      "type " + s.Name.Name + " " + exprString(fset, s.Type),
				Doc:            docText(includeDocs, firstDoc(s.Doc, decl.Doc), docLimit),
				Children:       children,
			})
		case *ast.ValueSpec:
			kind := language.SymbolVar
			if decl.Tok == token.CONST {
				kind = language.SymbolConst
			}
			for _, name := range s.Names {
				out = append(out, language.Symbol{
					ID:             symbolID(rel, kind, name.Name, name.Pos()),
					Language:       language.LanguageGo,
					Kind:           kind,
					Name:           name.Name,
					PackageID:      pkgID,
					Location:       location(fset, rel, name.Pos(), s.End()),
					Range:          location(fset, rel, name.Pos(), s.End()).Range,
					SelectionRange: location(fset, rel, name.Pos(), name.End()).Range,
					Signature:      valueSignature(fset, decl.Tok, s),
					Doc:            docText(includeDocs, firstDoc(s.Doc, decl.Doc), docLimit),
				})
			}
		}
	}
	return out
}

func fieldSymbols(fset *token.FileSet, rel, pkgID, container string, fields *ast.FieldList) []language.Symbol {
	if fields == nil {
		return nil
	}
	var out []language.Symbol
	for _, field := range fields.List {
		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{{Name: exprString(fset, field.Type), NamePos: field.Pos()}}
		}
		for _, name := range names {
			kind := language.SymbolField
			if _, ok := field.Type.(*ast.FuncType); ok {
				kind = language.SymbolMethod
			}
			out = append(out, language.Symbol{
				ID:             symbolID(rel, kind, container+"."+name.Name, name.Pos()),
				Language:       language.LanguageGo,
				Kind:           kind,
				Name:           name.Name,
				Container:      container,
				PackageID:      pkgID,
				Location:       location(fset, rel, name.Pos(), field.End()),
				Range:          location(fset, rel, name.Pos(), field.End()).Range,
				SelectionRange: location(fset, rel, name.Pos(), name.End()).Range,
				Signature:      name.Name + " " + exprString(fset, field.Type),
			})
		}
	}
	return out
}

func hasGoFacet(project coreproject.Project) bool {
	for _, facet := range project.Facets {
		if facet.Kind == coreproject.FacetGoModule || facet.Kind == coreproject.FacetGoWorkspace {
			return true
		}
	}
	return false
}

func flattenSymbols(symbols []language.Symbol) []language.Symbol {
	var out []language.Symbol
	for _, symbol := range symbols {
		out = append(out, symbol)
		out = append(out, flattenSymbols(symbol.Children)...)
	}
	return out
}

func symbolMatches(symbol language.Symbol, req golang.SymbolQuery) bool {
	if req.Kind != "" && !strings.EqualFold(string(symbol.Kind), string(req.Kind)) {
		return false
	}
	if req.PackageID != "" && symbol.PackageID != req.PackageID {
		return false
	}
	if req.Name != "" && symbol.Name != req.Name && bareSymbolName(symbol) != req.Name {
		return false
	}
	if req.Query != "" && !strings.Contains(strings.ToLower(symbol.Name), strings.ToLower(req.Query)) {
		return false
	}
	return true
}

func location(fset *token.FileSet, rel string, start, end token.Pos) language.Location {
	sp := fset.Position(start)
	ep := fset.Position(end)
	return language.Location{
		Path: rel,
		Range: language.Range{
			Start: language.Position{Line: sp.Line, Column: sp.Column},
			End:   language.Position{Line: ep.Line, Column: ep.Column},
		},
	}
}

func exprString(fset *token.FileSet, expr any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	return buf.String()
}

func funcSignature(fset *token.FileSet, decl *ast.FuncDecl) string {
	var buf bytes.Buffer
	buf.WriteString("func ")
	if decl.Recv != nil {
		_ = printer.Fprint(&buf, fset, decl.Recv)
		buf.WriteByte(' ')
	}
	buf.WriteString(decl.Name.Name)
	_ = printer.Fprint(&buf, fset, decl.Type)
	return buf.String()
}

func valueSignature(fset *token.FileSet, tok token.Token, spec *ast.ValueSpec) string {
	var buf bytes.Buffer
	buf.WriteString(tok.String())
	buf.WriteByte(' ')
	_ = printer.Fprint(&buf, fset, spec)
	return buf.String()
}

func firstDoc(left, right *ast.CommentGroup) *ast.CommentGroup {
	if left != nil {
		return left
	}
	return right
}

func docText(include bool, group *ast.CommentGroup, limit int) string {
	if !include || group == nil {
		return ""
	}
	text := strings.TrimSpace(group.Text())
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	if len(text) > limit {
		return text[:limit]
	}
	return text
}

func packageID(dir, name string) string {
	if dir == "" {
		return "go:package:" + name
	}
	return "go:package:" + dir + ":" + name
}

func symbolID(rel string, kind language.SymbolKind, name string, pos token.Pos) string {
	return fmt.Sprintf("go:symbol:%s:%s:%s:%d", rel, kind, name, pos)
}

func maxResults(value int) int {
	if value <= 0 || value > defaultMaxResults {
		return defaultMaxResults
	}
	return value
}

func maxBytes(value int) int {
	if value <= 0 || value > defaultSourceBytes {
		return defaultSourceBytes
	}
	return value
}

func maxDocBytes(value int) int {
	if value <= 0 || value > 2000 {
		return 2000
	}
	return value
}

func validateGoLanguage(id language.LanguageID) error {
	if id == "" || id == language.LanguageGo {
		return nil
	}
	return fmt.Errorf("unsupported language %q; this operation only supports %q", id, language.LanguageGo)
}

func diagnostic(rel string, err error) language.Diagnostic {
	return language.Diagnostic{Path: rel, Severity: "warning", Message: err.Error()}
}

func firstDocLine(doc string) string {
	for _, line := range strings.Split(strings.TrimSpace(doc), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func bareSymbolName(symbol language.Symbol) string {
	if symbol.Kind != language.SymbolMethod {
		return symbol.Name
	}
	if _, name, ok := strings.Cut(symbol.Name, "."); ok {
		return name
	}
	return symbol.Name
}

func packagePath(packageID string) string {
	const prefix = "go:package:"
	if !strings.HasPrefix(packageID, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(packageID, prefix)
	if rest == "" {
		return ""
	}
	parts := strings.Split(rest, ":")
	if len(parts) == 1 {
		return ""
	}
	return cleanRel(parts[0])
}

type projectSummary struct {
	ID   coreproject.ID `json:"id"`
	Root string         `json:"root,omitempty"`
	Name string         `json:"name,omitempty"`
	Kind string         `json:"kind,omitempty"`
}

func compactProjects(projects []coreproject.Project) []projectSummary {
	out := make([]projectSummary, 0, len(projects))
	for _, project := range projects {
		out = append(out, projectSummary{ID: project.ID, Root: project.Root, Name: project.Name, Kind: project.Kind})
	}
	return out
}

type packageSummary struct {
	ID        string              `json:"id"`
	Language  language.LanguageID `json:"language"`
	Name      string              `json:"name,omitempty"`
	Dir       string              `json:"dir,omitempty"`
	FileCount int                 `json:"file_count,omitempty"`
	TestFor   string              `json:"test_for,omitempty"`
}

func compactPackages(pkgs []language.Package) []packageSummary {
	out := make([]packageSummary, 0, len(pkgs))
	for _, pkg := range pkgs {
		out = append(out, packageSummary{
			ID:        pkg.ID,
			Language:  pkg.Language,
			Name:      pkg.Name,
			Dir:       pkg.Dir,
			FileCount: len(pkg.Files),
			TestFor:   pkg.TestFor,
		})
	}
	return out
}

type outlineSummary struct {
	Path      string              `json:"path,omitempty"`
	PackageID string              `json:"package_id,omitempty"`
	Language  language.LanguageID `json:"language,omitempty"`
	Symbols   []symbolSummary     `json:"symbols,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
}

type symbolSummary struct {
	ID        string              `json:"id,omitempty"`
	Language  language.LanguageID `json:"language,omitempty"`
	Kind      language.SymbolKind `json:"kind"`
	Name      string              `json:"name"`
	Container string              `json:"container,omitempty"`
	PackageID string              `json:"package_id,omitempty"`
	Path      string              `json:"path,omitempty"`
	Line      int                 `json:"line,omitempty"`
	Doc       string              `json:"doc,omitempty"`
}

func compactOutline(outline language.Outline, includeDocs bool) outlineSummary {
	return outlineSummary{
		Path:      outline.Path,
		PackageID: outline.PackageID,
		Language:  outline.Language,
		Symbols:   compactSymbols(outline.Symbols, includeDocs),
		Truncated: outline.Truncated,
	}
}

func compactSymbols(symbols []language.Symbol, includeDocs bool) []symbolSummary {
	out := make([]symbolSummary, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, compactSymbol(symbol, includeDocs))
	}
	return out
}

func compactSymbol(symbol language.Symbol, includeDocs bool) symbolSummary {
	doc := ""
	if includeDocs {
		doc = firstDocLine(symbol.Doc)
	}
	return symbolSummary{
		ID:        symbol.ID,
		Language:  symbol.Language,
		Kind:      symbol.Kind,
		Name:      symbol.Name,
		Container: symbol.Container,
		PackageID: symbol.PackageID,
		Doc:       doc,
		Path:      symbol.Location.Path,
		Line:      symbol.Location.Range.Start.Line,
	}
}

func cleanRel(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || raw == "." {
		return ""
	}
	clean := path.Clean(raw)
	if clean == "." {
		return ""
	}
	return strings.TrimPrefix(clean, "./")
}

func pathDir(rel string) string {
	dir := path.Dir(rel)
	if dir == "." {
		return ""
	}
	return dir
}

func displayRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

func noisyDirs() []string {
	return []string{".git", ".cache", "node_modules", "vendor", "dist", "build", "target", "tmp"}
}
