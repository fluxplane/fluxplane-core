# Design: configurable coder app bundle

## Context

`apps/coder.Bundle()` currently returns the full embedded resource bundle for the coder application. It is intentionally pure resource declaration, with runtime implementations supplied separately by `apps/coder` distribution wiring and `apps/launch`.

The current construction is highly opinionated and static:

- app, agent, session, source, model, plugin, datasource, and discovery settings are all hard-coded in one function;
- the main agent operation list is a long inline string list;
- delegation policy repeats a smaller inline operation allowlist;
- model provider/use-case defaults are split between `bundle.go`, `app.go`, and `serve.go`;
- project/global resource discovery values are mutated after `sdk.NewApp(...).Build()`;
- `BundleWithModel(provider, model)` post-processes the built bundle rather than using the same construction path;
- test expectations depend on exact composed operation counts, making intentional variants awkward.

This is acceptable for one distribution, but it makes it hard to spin up multiple coder variants for tests, dogfood experiments, or future swarm-style scenarios.

## Goals

- Preserve `Bundle()` as the easy default entrypoint.
- Add one configuration shape that describes coder's app-level bundle choices.
- Keep defaults centralized and explicit.
- Make common variants cheap: model override, operation set override/extension, datasource enablement, project discovery enablement, delegation profile/limit/timeout settings.
- Avoid adding runtime behavior to `bundle.go`; it should continue to return inert resource declarations.
- Keep package layering intact: `apps/coder` may assemble app resources, but plugin implementations and runtime wiring stay outside the bundle config.

## Non-goals

- No compatibility shims beyond keeping `Bundle()` and possibly `BundleWithModel()` as convenience wrappers.
- No environment-variable loading inside bundle construction.
- No CLI flag plumbing in this step, unless later changes choose to map flags to `Config`.
- No generic framework for every app yet; this is a coder-specific design first.
- No plugin implementation construction in `Config`; `localPlugins` remains runtime/distribution wiring.

## Current boilerplate and magic values

### Naming and metadata

Current constants:

- `AppName = "coder"`
- `AgentName = "coder"`
- `SessionName = "coder"`
- plugin names: `coding`, `planexec`, `skills`, `image`
- `DefaultModel = "gpt-5.5"`
- `DefaultNamespace = "apps/coder"`

Hard-coded but not centralized as a user-facing config:

- app description: `Small local coding agent app.`
- agent description and long system prompt;
- session description: `Default local coding session.`
- app source: embedded `apps/coder`;
- project source: `.agents`, scope `project`, ecosystem `agentdir`;
- discovery: `IncludeGlobalUserResources = true`;
- default datasource: `web_search`, kind `web_search`.

### Model values

`bundle.go` declares:

- `WithModel("openai", DefaultModel, "coding")`
- agent inference via `AsLLMAgent(DefaultModel)`

`app.go` distribution declares:

- default provider `openai`, model `DefaultModel`, use-case `coding`

`serve.go` resolves served model selection with:

- CLI default `DefaultModel`
- default resolver provider `codex`
- default resolver model `DefaultModel`

This means the app bundle's model defaults and serving resolver defaults are related but not represented by one app configuration concept.

### Operations

The main agent gets a broad static operation list including project, Go, markdown, filesystem, search, browser, git, shell/process, code execution, clarification, delegation/planning/skills, and image operations.

The session delegation policy has another static operation list for child agents. It overlaps but is not derived from the primary list. It also includes `go_callers` and `go_callees`, which are not present in the visible main agent list in `Bundle()` but are expected by tests after composition.

### Delegation

Current defaults:

- allowed profiles: `worker`, `explorer`
- max parallel: `4`
- default timeout: `10m`
- child operation allowlist: inline list

These values are policy choices and should be overrideable for tests and swarm-style scenarios.

## Refined public API

Prefer an options-pattern API over a large partially-defaulted struct constructor. The package should expose a small set of opinionated domain helpers (`Agent`, `FeatureSpec`, `ToolGroup`) and then apply options to a config.

Important refinement: defaults should be **minimal**, not "everything coder can do". The default bundle should be able to do basic useful work, but richer capabilities should be enabled explicitly with feature presets. A convenience `WithAllFeatures()` option can recreate today's full-fat coder app.

Suggested files:

- `apps/coder/config.go`: `Config`, `AgentConfig`, feature/tool-group types, default helpers.
- `apps/coder/options.go`: `Option`, `DefaultOptions()`, `NewConfig(...)`, and option helpers.
- `apps/coder/features.go`: built-in `FeatureSpec` declarations such as `CodeIntelligenceFeature()` and `ResearchFeature()`.
- `apps/coder/toolgroups.go`: operation mappings for lower-level tool groups.
- `apps/coder/agents.go`: `Agent(...)`, `DefaultAgent()`, and agent options.
- `apps/coder/bundle.go`: resource construction from `Config`.

```go
// Config describes the inert coder app resource bundle.
type Config struct {
    App AppConfig

    // Agent is the default/root agent used by the default session.
    Agent AgentConfig

    // Agents are additional coder-defined agents. They are emitted as agent
    // specs and may be referenced by delegation profiles/sub-agent setup.
    Agents []AgentConfig

    // Features are coder-local inert presets. Applying them materializes
    // plugin refs, tool groups, operations, datasources, agents, and
    // delegation fragments into the config.
    Features []FeatureSpec

    Plugins []resource.PluginRef

    Datasources     []coredatasource.Ref
    DatasourceSpecs []coredatasource.Spec

    Delegation DelegationConfig
}

type AppConfig struct {
    Name        string
    Description string
    Source      resource.SourceRef
    Model       ModelConfig
    ProjectSource *coreapp.SourceSpec
    IncludeGlobalUserResources bool
    Metadata map[string]string
}

type AgentConfig struct {
    Name        string
    Description string
    System      string
    Model       ModelConfig
    MaxOutputTokens int
    MaxSteps        int
    Agency          agent.AgencyProfile

    ToolGroups []ToolGroup
    Operations []operation.Name
    Datasources []coredatasource.Ref
    Metadata map[string]string
}

func Agent(name, description string, opts ...AgentOption) AgentConfig
```

The key change is that `AgentName`, `AgentDescription`, `AgentSystemPrompt`, etc. no longer appear as parallel top-level fields. The main agent is `Config.Agent`; extra agents are `Config.Agents`. This makes multi-agent/swarm-style scenarios more natural and avoids config field explosion.

Construction API:

```go
type Option func(*Config)

func DefaultOptions() []Option
func NewConfig(opts ...Option) Config
func Bundle() resource.ContributionBundle
func BundleWithOptions(opts ...Option) resource.ContributionBundle
func BundleWithConfig(cfg Config) resource.ContributionBundle
```

`Bundle()` becomes minimal by default:

```go
func Bundle() resource.ContributionBundle {
    return BundleWithOptions(DefaultOptions()...)
}
```

A full current-coder equivalent is explicit:

```go
func FullBundle() resource.ContributionBundle {
    return BundleWithOptions(WithAllFeatures())
}
```

or callers can use:

```go
bundle := coder.BundleWithOptions(coder.WithAllFeatures())
```

`NewConfig` applies minimal default options first, then caller options:

```go
func NewConfig(opts ...Option) Config {
    var cfg Config
    for _, opt := range DefaultOptions() {
        opt(&cfg)
    }
    for _, opt := range opts {
        opt(&cfg)
    }
    return cfg
}
```

This gives callers a small working base and an explicit way to opt into richer capabilities.

Example:

```go
bundle := coder.BundleWithOptions(
    coder.WithModel("openai", "gpt-5.5-codex"),
    coder.WithFeature(coder.CodeIntelligenceFeature()),
    coder.WithFeature(coder.ResearchFeature()),
    coder.WithoutFeature(coder.FeatureBrowser),
    coder.WithDelegation(coder.Delegation{MaxParallel: 16, DefaultTimeout: "2m"}),
)
```

Swarm-style example:

```go
var bundles []resource.ContributionBundle
for i := 0; i < n; i++ {
    name := fmt.Sprintf("coder-%d", i)
    bundles = append(bundles, coder.BundleWithOptions(
        coder.WithAppName(name),
        coder.WithMainAgent(coder.Agent(name, "swarm coder", coder.WithAgentModel("openai", models[i%len(models)]))),
        coder.WithSessionName(name),
        coder.WithFeature(coder.CodeIntelligenceFeature()),
    ))
}
```

## Defaulting and options strategy

Use default options as the source of truth for the **minimal** coder bundle, not for the maximal current app.

Minimal defaults should include only enough to be useful and safe:

- app/source/session/model metadata;
- the root coder agent;
- project/resource discovery;
- local read-only inspection operations such as project inventory, file reads, glob/grep, and markdown outline/links/diagnostics;
- no browser, shell, process management, git mutation, file writes, image generation, or delegation unless explicitly enabled.

Example:

```go
func DefaultOptions() []Option {
    return []Option{
        WithApp(DefaultApp()),
        WithMainAgent(DefaultAgent()),
        WithFeature(ProjectInspectionFeature()),
        WithFeature(FilesystemReadFeature()),
        WithFeature(MarkdownFeature()),
    }
}
```

The current broad coder capability set should move to:

```go
func WithAllFeatures() Option {
    return WithFeatures(AllFeatures()...)
}

func AllFeatures() []FeatureSpec {
    return []FeatureSpec{
        ProjectInspectionFeature(),
        CodeIntelligenceFeature(),
        MarkdownFeature(),
        FilesystemReadFeature(),
        FilesystemWriteFeature(),
        ResearchFeature(),
        BrowserFeature(),
        GitFeature(),
        ShellFeature(),
        ProcessFeature(),
        CodeExecutionFeature(),
        ClarificationFeature(),
        DelegationFeature(),
        SkillsFeature(),
        ImageFeature(),
    }
}
```

Important semantic rule: `Config` is the normalized output. Options may be high-level and additive; `BundleWithConfig` should not guess missing defaults. Callers that want defaults use `NewConfig`/`BundleWithOptions`. Callers that want the historical full coder behavior use `BundleWithOptions(WithAllFeatures())`.

This avoids zero-value ambiguity while still giving ergonomic partial customization.

Recommended option conventions:

- `WithX(...)` replaces a singleton (`WithAppName`, `WithModel`, `WithMainAgent`).
- `AddX(...)` appends (`AddAgent`, `AddOperations`, `AddDatasourceSpecs`).
- `WithoutX(...)` removes (`WithoutFeature`, `WithoutOperations`, `WithoutPlugins`).
- `EnableX`/`DisableX` are only used for boolean-like toggles.

`DefaultOptions()` should be ordered and readable so it documents the minimal base. `AllFeatures()` should be ordered and readable so it documents the full opinionated coder product.

## Feature specs and tool groups

Features should be more than enum flags. Treat each feature as a coder-local inert preset, similar in spirit to a mini-plugin, but not a runtime plugin and not placed under `plugins/` yet.

A feature declares what it contributes; package-level resolver logic applies and dedupes those declarations into `Config`.

```go
type FeatureName string

type FeatureSpec struct {
    Name        FeatureName
    Description string

    Plugins []resource.PluginRef

    MainAgent AgentFeatureSpec
    Agents    []AgentConfig

    Datasources     []coredatasource.Ref
    DatasourceSpecs []coredatasource.Spec

    Delegation DelegationFeatureSpec
    AppSources []coreapp.SourceSpec
}

type AgentFeatureSpec struct {
    ToolGroups []ToolGroup
    Operations []operation.Name
    Datasources []coredatasource.Ref
}

type DelegationFeatureSpec struct {
    Enabled bool
    AllowedProfiles []coresession.Ref
    Operations []operation.Ref
    MaxParallel *int
    DefaultTimeout *string
}
```

Recommendation: use `FeatureSpec` structs rather than a behavior-heavy `Feature interface { Apply(*Config) }`. Specs are inert, inspectable, and align with this repository's `Spec` naming rule. If behavior is needed, keep it as package-local resolver functions:

```go
func ApplyFeature(cfg *Config, feature FeatureSpec)
func ApplyFeatures(cfg *Config, features ...FeatureSpec)
```

Built-in features are declared by functions:

```go
func ProjectInspectionFeature() FeatureSpec
func CodeIntelligenceFeature() FeatureSpec
func MarkdownFeature() FeatureSpec
func FilesystemReadFeature() FeatureSpec
func FilesystemWriteFeature() FeatureSpec
func ResearchFeature() FeatureSpec
func BrowserFeature() FeatureSpec
func GitFeature() FeatureSpec
func ShellFeature() FeatureSpec
func ProcessFeature() FeatureSpec
func CodeExecutionFeature() FeatureSpec
func ClarificationFeature() FeatureSpec
func DelegationFeature() FeatureSpec
func SkillsFeature() FeatureSpec
func ImageFeature() FeatureSpec
```

Feature names still exist for dedupe/removal:

```go
const (
    FeatureProjectInspection FeatureName = "project_inspection"
    FeatureCodeIntelligence  FeatureName = "code_intelligence"
    FeatureResearch          FeatureName = "research"
    FeatureBrowser           FeatureName = "browser"
)
```

`ToolGroup` remains a lower-level building block used by features and agents:

```go
type ToolGroup string

const (
    ToolGroupProject  ToolGroup = "project"
    ToolGroupGo       ToolGroup = "go"
    ToolGroupMarkdown ToolGroup = "markdown"
    ToolGroupFSRead   ToolGroup = "fs_read"
    ToolGroupFSWrite  ToolGroup = "fs_write"
    ToolGroupResearch ToolGroup = "research"
    ToolGroupBrowser  ToolGroup = "browser"
    ToolGroupGitRead  ToolGroup = "git_read"
    ToolGroupGitWrite ToolGroup = "git_write"
    ToolGroupShell    ToolGroup = "shell"
    ToolGroupProcess  ToolGroup = "process"
    ToolGroupImage    ToolGroup = "image"
)
```

Tool-group operation mappings stay explicit:

```go
func OperationsForToolGroups(groups ...ToolGroup) []operation.Name
func OperationRefsForToolGroups(groups ...ToolGroup) []operation.Ref
```

Example feature declarations:

```go
func CodeIntelligenceFeature() FeatureSpec {
    return FeatureSpec{
        Name: FeatureCodeIntelligence,
        Description: "Project and Go code navigation for local code understanding.",
        MainAgent: AgentFeatureSpec{
            ToolGroups: []ToolGroup{ToolGroupProject, ToolGroupGo, ToolGroupMarkdown},
        },
        Delegation: DelegationFeatureSpec{
            Operations: OperationRefsForToolGroups(ToolGroupProject, ToolGroupGo, ToolGroupMarkdown),
        },
    }
}

func ResearchFeature() FeatureSpec {
    return FeatureSpec{
        Name: FeatureResearch,
        Description: "Public web and configured datasource research.",
        MainAgent: AgentFeatureSpec{
            ToolGroups: []ToolGroup{ToolGroupResearch},
            Datasources: []coredatasource.Ref{{Name: "web_search"}},
        },
        DatasourceSpecs: []coredatasource.Spec{DefaultWebSearchDatasourceSpec()},
    }
}
```

Feature presets give product-level ergonomics while tool groups keep the final operation list inspectable and testable. `WithAllFeatures()` simply applies all built-in presets; it should not be confused with the minimal default.

## Agent helpers and sub-agents

Add `Agent(name, description string, opts ...AgentOption) AgentConfig` as package-level authoring sugar for coder-specific agent specs. It should be smaller than `sdk.BuildAgent` and operate at config level.

```go
type AgentOption func(*AgentConfig)

func Agent(name, description string, opts ...AgentOption) AgentConfig
func DefaultAgent() AgentConfig

func WithAgentSystem(prompt string) AgentOption
func WithAgentModel(provider, model string) AgentOption
func WithAgentMaxSteps(n int) AgentOption
func WithAgentToolGroups(groups ...ToolGroup) AgentOption
func WithAgentOperations(names ...operation.Name) AgentOption
func WithAgentDatasources(refs ...coredatasource.Ref) AgentOption
```

`Config.Agent` is the root/default agent. `Config.Agents` contains additional app-bundled agents. Bundle construction emits all of them as resource agent specs.

Sub-agent behavior has two parts:

1. emit additional `agent.Spec` entries from `Config.Agents`;
2. make them usable by delegation/session policy.

Do not hide too much magic here. A simple first pass can add an option:

```go
func AddSubAgent(agent AgentConfig, profile coresession.Ref) Option
```

or:

```go
type SubAgentConfig struct {
    Agent AgentConfig
    Profile coresession.Ref
}
```

Then default delegation can include configured sub-agent profiles explicitly. This avoids assuming every extra agent should automatically be callable in every session. If the product wants that behavior, provide an explicit option:

```go
func AutoDelegateToConfiguredAgents() Option
```

Recommendation: add `Config.Agent` and `Config.Agents` now, but keep the first implementation conservative: extra agents are emitted; delegation profile linkage is explicit via options.
## Sub-agent tool policy

Do not implicitly give sub-agents every tool available to the main agent. Delegated children should have an explicit capability story because they often run with less context and less supervision than the root agent.

There are two related layers:

1. **core/session policy**, which controls the effective child session profile at runtime;
2. **apps/coder config policy**, which decides what session specs and delegation policy fragments coder emits.

### What exists today

`core/session.DelegationPolicy` already contains policy fields that are directly relevant:

```go
type DelegationPolicy struct {
    AllowedProfiles []session.Ref
    AllowedAgents   []agent.Ref
    MaxParallel     int
    DefaultTimeout  string
    Context         []context.ProviderRef
    Commands        []command.Path
    Operations      []operation.Ref
    Policy          policy.InvocationPolicy
    Annotations     map[string]string
}
```

`orchestration/subagent.Supervisor` already enforces/narrows part of this policy:

- `AllowedProfiles` authorizes requested child profiles.
- `AllowedAgents` can restrict resolved child profile agents.
- `Context`, `Commands`, and `Operations` narrow the resolved child session profile.
- If the child profile has no resolved base operations, policy operations can become the child's operations.
- If policy operations are empty, the resolved child profile keeps its own operations.
- `MaxParallel` and `DefaultTimeout` are enforced by the supervisor.

This means the runtime already supports an **explicit/provided allowlist** at the session-profile level via `DelegationPolicy.Operations`, `Commands`, and `Context`. It does **not** have an explicit enum that says `inherit`/`explicit`/`provided`; represent that in coder config first and only move it into core if needed by multiple apps or serialized resource semantics.

### Coder-local policy model

Add a coder config concept for child capability projection:

```go
type SubAgentToolMode string

const (
    // SubAgentToolsExplicit means use only the child AgentConfig/session config.
    SubAgentToolsExplicit SubAgentToolMode = "explicit"

    // SubAgentToolsInherit means copy the parent/root agent's effective tool refs.
    // Useful for full-power workers, but should not be the default.
    SubAgentToolsInherit SubAgentToolMode = "inherit"

    // SubAgentToolsProvided means the parent session's delegation policy provides
    // the effective allowlist through DelegationPolicy.Operations/Commands/Context.
    SubAgentToolsProvided SubAgentToolMode = "provided"
)

type SubAgentConfig struct {
    Agent   AgentConfig
    Profile coresession.Ref

    ToolMode SubAgentToolMode

    // Used when ToolMode is provided. These map to core/session.DelegationPolicy.
    ProvidedContext    []corecontext.ProviderRef
    ProvidedCommands   []command.Path
    ProvidedOperations []operation.Ref
}
```

Recommended defaults:

- `explicit` for configured extra agents;
- `provided` for generic worker/explorer profiles where the parent app wants to centrally define the child allowlist;
- `inherit` only by explicit opt-in, mainly for tests or full-power swarms.

### How modes map to current core support

| Mode | Meaning | Current support | Implementation location |
|---|---|---|---|
| `explicit` | Child session/profile uses its own configured operations/context/commands. Parent only authorizes profile/agent. | Supported when `DelegationPolicy.Operations`, `Commands`, and `Context` are empty. | `apps/coder` emits child session specs with explicit tools. Core already preserves them. |
| `provided` | Parent delegation policy supplies/narrows the child's tool allowlist. | Supported via `DelegationPolicy.Operations`, `Commands`, and `Context`. | `apps/coder` fills delegation policy fields. Core already narrows in supervisor. |
| `inherit` | Child receives same effective tools as parent/root agent. | Partially supported as a construction-time copy, not as a runtime semantic. | Implement in `apps/coder` by copying root effective refs into child specs or delegation policy. Postpone core support unless runtime inheritance is needed. |

Recommendation: implement the enum/mode in `apps/coder` first. Use existing `core/session.DelegationPolicy` for `provided`. Do not add a core enum yet.

### What may belong in core later

Move policy mode into `core/session` only if we need serialized, app-independent semantics like:

```go
type ToolProjectionMode string

const (
    ToolProjectionExplicit ToolProjectionMode = "explicit"
    ToolProjectionProvided ToolProjectionMode = "provided"
    ToolProjectionInherit  ToolProjectionMode = "inherit"
)

type DelegationPolicy struct {
    // existing fields...
    ToolProjection ToolProjectionMode `json:"tool_projection,omitempty"`
}
```

Do this only if runtime needs to know the difference between an empty allowlist meaning "inherit" versus "use child profile as-is". Today `narrowProfile` already has meaningful empty-list behavior, so adding this too early could create ambiguity.

Potential future core additions:

- profile-specific delegation rules, e.g. `DelegationPolicy.Profiles []DelegatedProfilePolicy`, so worker and explorer can receive different allowlists;
- explicit capability projection mode if empty-list semantics become insufficient;
- a normalized `CapabilityPolicy` shared by commands, operations, context providers, datasources, and maybe MCP/tool providers.

For the current coder design, app-level policy is enough.


## Builder pattern, likely unnecessary initially

With an options pattern, a separate builder may not be needed. `NewConfig(opts...)` plus `BundleWithOptions(opts...)` covers most construction use cases.

If later tests need fluent mutation, a builder can wrap `Config` without changing the core API:

```go
type Builder struct { cfg Config }

func NewBuilder(opts ...Option) *Builder
func (b *Builder) With(opts ...Option) *Builder
func (b *Builder) Build() resource.ContributionBundle
func (b *Builder) Config() Config
```

Do not add this until options prove insufficient.

## Distribution integration

`Distribution()` and `loadStartupResources()` currently call `Bundle()` directly. A follow-up can add configurable distribution entrypoints:

```go
func DistributionWithConfig(cfg Config) distribution.Distribution
func NewCommandWithConfig(cfg Config) *cobra.Command
```

However, the first step can be limited to bundle construction. Runtime concerns such as `localPlugins`, `ToolProjectionConfig`, socket defaults, yolo/dev/debug flags, and model resolver behavior should not move into bundle `Config` unless they describe inert resources.

If served model defaults should share `Config.Model`, introduce a separate distribution-level config later:

```go
type DistributionConfig struct {
    Bundle Config
    DefaultConversation string
    Serve ServeConfig
    ToolProjection agentruntime.ToolProjectionConfig
}
```

## Migration plan

1. Add `config.go` with `Config`, `AppConfig`, `AgentConfig`, `ModelConfig`, `DelegationConfig`, `FeatureSpec`, `FeatureName`, and `ToolGroup`.
2. Add `features.go` with built-in feature preset declarations:
   - `ProjectInspectionFeature()`
   - `CodeIntelligenceFeature()`
   - `MarkdownFeature()`
   - `FilesystemReadFeature()` / `FilesystemWriteFeature()`
   - `ResearchFeature()`
   - `BrowserFeature()`
   - `GitFeature()`
   - `ShellFeature()` / `ProcessFeature()` / `CodeExecutionFeature()`
   - `DelegationFeature()` / `SkillsFeature()` / `ImageFeature()`
3. Add `toolgroups.go` with:
   - `OperationsForToolGroups(...)`
   - `OperationRefsForToolGroups(...)`
   - exact operation mappings extracted from today's inline lists.
4. Add `options.go` with `Option`, minimal `DefaultOptions()`, `AllFeatures()`, `WithAllFeatures()`, `NewConfig(opts...)`, and core option helpers.
5. Add `agents.go` with `Agent(...)`, `DefaultAgent()`, and `AgentOption` helpers.
6. Implement feature application/dedupe rules for plugins, operations, datasources, app sources, extra agents, and delegation fragments.
7. Implement `BundleWithConfig(cfg Config)` from config only.
8. Implement `BundleWithOptions(opts ...Option)` and rewrite `Bundle()` to use minimal `DefaultOptions()`.
9. Decide whether to keep today's `Bundle()` behavior or introduce a temporary `FullBundle()`/`BundleWithOptions(WithAllFeatures())` call site migration. If preserving current CLI behavior is required, `loadStartupResources` can call `BundleWithOptions(WithAllFeatures())` while `Bundle()` remains the minimal library default.
10. Rewrite `BundleWithModel()` to use `BundleWithOptions(WithModel(...))` or remove it if no longer needed by callers.
11. Add tests:
   - minimal `Bundle()` composes and includes basic inspection/read capabilities only;
   - `BundleWithOptions(WithAllFeatures())` matches today's broad composition expectations;
   - individual feature specs contribute expected operations/plugin refs/datasources;
   - feature removal removes feature-implied contributions but not explicit additions;
   - main-agent helper changes names/descriptions/model consistently;
   - extra agents are emitted;
   - explicit sub-agent/delegation config links extra agents as intended;
   - sub-agent tool policy modes are honored:
     - `explicit` keeps child agent/session tools;
     - `provided` maps to `DelegationPolicy.Operations`/`Commands`/`Context`;
     - `inherit` copies the root agent's effective tools only when explicitly requested.
12. Only after this is stable, consider `DistributionWithConfig` / `NewCommandWithConfig` for distribution-level configurability.
## Settled decisions

### Should `FeatureCodeIntelligence` include web search?

Options:

1. **Local-only code intelligence**: project inventory, Go AST/navigation, markdown docs, grep/glob, and file reads only.
2. **Local plus web search**: local code intelligence plus `web_search`, `web_request`, and datasource search for external API/library discovery.
3. **Split feature**: keep `FeatureCodeIntelligence` local-only and add `FeatureResearch` or `FeatureWebResearch` for web/datasource access.

Decision: option 3. `FeatureCodeIntelligence` means repo/workspace intelligence and stays deterministic/offline-friendly. `ResearchFeature` opts into network/datasource behavior. The full coder app can enable both features to preserve today's behavior.

### Should filesystem write operations be separate from read operations?

Options:

1. **Single filesystem feature**: `FeatureFilesystem` enables read and write operations together.
2. **Read/write split**: `FeatureFilesystemRead` enables `dir_*`, `file_read`, `file_stat`, `glob`, `grep`; `FeatureFilesystemWrite` enables `dir_create`, `file_create`, `file_edit`, `file_copy`, `file_move`, `file_delete`.
3. **Tool-group only split**: expose one `FeatureFilesystem`, but internally map to `ToolGroupFSRead` and `ToolGroupFSWrite` based on another safety option.

Decision: option 2. Read and write capabilities have different safety posture and are useful independently for review/audit agents. The full coder app enables both to preserve today's behavior.

### Should every configured extra agent become a delegation profile automatically?

Options:

1. **Automatic**: every `Config.Agents` entry is emitted and added to delegation profiles.
2. **Explicit only**: extra agents are emitted, but delegation linkage requires `AddSubAgent(...)` or a `Delegation` entry.
3. **Opt-in automatic**: explicit by default, with an option like `AutoDelegateToConfiguredAgents()`.

Decision: option 3, with option 2 as the default behavior. Extra agents are emitted, but delegation linkage is explicit by default. `AutoDelegateToConfiguredAgents()` may exist for swarm/test setup. If auto-delegation is enabled, it still chooses an explicit `SubAgentToolMode`; it must not imply full tool inheritance.

### Should features alter the system prompt?

Options:

1. **No prompt changes**: features only affect resources, operation refs, plugins, datasources, and delegation.
2. **Prompt fragments**: features contribute standardized prompt snippets appended to the agent system prompt.
3. **Prompt template**: generate the full system prompt from enabled features and app/agent metadata.

Decision: option 1 for the first implementation. Keep the current system prompt explicit and stable. Add option 2 later only if tests show that models need feature-aware instructions. Avoid option 3 for now because prompt generation can obscure product behavior and create brittle diffs.

### Should model provider default be `openai` or `codex` for serve mode?

Options:

1. **Keep current split**: bundle/distribution defaults use `openai`; serve resolver default provider remains `codex`.
2. **Unify on bundle config**: serve mode uses `Config.App.Model.Provider` and `Config.App.Model.Model` as resolver defaults.
3. **Separate distribution config**: bundle config owns inert app defaults; `DistributionConfig`/`ServeConfig` owns serve resolver defaults.

Decision: option 3. The bundle model is an inert app resource declaration; serve resolver defaults are runtime/distribution behavior. Keep them separate conceptually, but make the values visible in a later `DistributionConfig` so the current `openai`/`codex` split is intentional rather than scattered magic.

### Should disabling a feature remove manually added operations?

Options:

1. **Disable wins globally**: `WithoutFeature` removes all operations associated with that feature, even if manually added elsewhere.
2. **Explicit additions win**: `WithoutFeature` removes only feature-implied operations; `AddOperations` remains authoritative.
3. **Last option wins**: option application order determines whether add/remove wins.

Decision: option 2. Feature flags control feature-implied defaults, while explicit operation additions are respected. Use `WithoutOperations(...)` when a caller wants a hard removal. Avoid order-dependent semantics because they are harder to reason about and test.

### Should datasource refs and datasource specs be feature-derived or directly configured?

Options:

1. **Feature-derived only**: `FeatureResearch`/`FeatureSearch` automatically creates the `web_search` ref and datasource spec.
2. **Direct only**: callers configure `Datasources` and `DatasourceSpecs`; features only affect operations.
3. **Both, with dedupe**: features add default datasource refs/specs; callers can add/remove/replace them explicitly.

Decision: option 3. Defaults stay ergonomic, but tests and custom apps get direct control. Bundle construction dedupes by datasource name and makes explicit removals possible with `WithoutDatasource(name)`.

### Should plugin refs be feature-derived or directly configured?

Options:

1. **Always default plugins**: `DefaultOptions()` always includes coding, planexec, skills, and image plugin refs regardless of features.
2. **Feature-derived plugins**: features imply plugin refs, e.g. `FeatureImage` implies `image`, `FeatureDelegation` implies `planexec`.
3. **Both, with explicit overrides**: defaults/features add plugin refs; callers can add/remove refs explicitly.

Decision: option 3. This preserves the current full coder capability set while allowing smaller variants. Use dedupe by plugin name. Runtime plugin implementation selection stays outside bundle config.

### Should `Config.ToolGroups` exist globally as well as per-agent?

Options:

1. **Global only**: one set of tool groups applies to all agents.
2. **Per-agent only**: each `AgentConfig` declares its own tool groups; features provide defaults for the main agent.
3. **Both**: global tool groups are app-wide defaults; agent tool groups override/extend them.

Decision: option 2 initially. Per-agent tool groups are clearer for multi-agent setups. Features can populate the default/root agent's tool groups in `DefaultOptions()`. Add global defaults later only if repeated per-agent config becomes painful.

### Should sub-agent tool projection modes live in core now?

Options:

1. **Coder-local only**: `apps/coder` has `SubAgentToolMode`; it compiles that choice into child session specs and existing `DelegationPolicy` fields.
2. **Core enum now**: add `ToolProjectionMode` to `core/session.DelegationPolicy` immediately.
3. **Hybrid annotations**: keep existing core fields but put mode hints into `DelegationPolicy.Annotations`.

Decision: option 1. Current core already supports explicit child specs and parent-provided narrowing through `DelegationPolicy.Context`, `Commands`, and `Operations`. A core enum is only needed if multiple apps need serialized mode semantics or if runtime must distinguish empty allowlists from intentional inheritance. Avoid annotations for semantic policy.


### Additional settled decisions

- **Sub-agent default tool mode**: configured extra agents default to `explicit`; generic worker/explorer presets may use `provided`; never default to `inherit`.
- **Minimal vs full bundle**: library `Bundle()` becomes minimal; `BundleWithOptions(WithAllFeatures())` recreates the full coder app. CLI/startup may temporarily call `WithAllFeatures()` if product behavior must remain unchanged during migration.
- **Feature representation**: use inert `FeatureSpec` presets declared by functions such as `CodeIntelligenceFeature()`, not enum-only flags or behavior-heavy `Apply` interfaces.
- **Feature preset location**: keep presets in `apps/coder` now. Move to shared package only after another app needs the abstraction.
- **Builder pattern**: do not add initially. Use `NewConfig(opts...)` and `BundleWithOptions(opts...)`; add a builder later only if options prove insufficient.

## Recommendation

Refine the implementation around `Config` plus options, with features modeled as inert coder-local `FeatureSpec` presets rather than enum flags. `DefaultOptions()` should produce a minimal but useful coder bundle. `WithAllFeatures()` / `AllFeatures()` should recreate the current broad coder capability set explicitly.

Feature specs should declare plugin refs, tool groups, direct operations, datasources, app sources, extra agents, and delegation fragments. Package-level resolver logic applies and dedupes those specs into config. `Config.Agent` and `Config.Agents` should replace parallel `AgentName`/description/system fields and make multi-agent variants natural.

Implement conservatively: extra agents are emitted, but delegation/sub-agent routing is explicit. Keep feature presets in `apps/coder` rather than `plugins/` until a second app needs the abstraction. This keeps the design inspectable while creating a path toward richer swarms, small focused coder variants, and test harnesses.
