# Design: language toolchain activation

## Context

The Go and Markdown plugins prove the first language-support shape, but they
also show the scalability problem before adding Rust, JavaScript, TypeScript,
Python, and more. `core/language` currently mixes shared source concepts with
Go-specific toolchain contracts and Markdown-specific document contracts. The
coder app also enumerates every Go and Markdown operation directly in
`apps/coder.Bundle()`, which will not scale with more language plugins.

This design follows the completed Go roadmap and the configurable coder bundle
design in
[`2026-05-16-coder-bundle-config.md`](2026-05-16-coder-bundle-config.md). The
current Go/Markdown implementation remains usable, but the next language work
should split contracts, describe toolchains inertly, and project active
operation sets from project and toolchain signals.

## Goals

- Keep `core/language` language-neutral.
- Move Go and Markdown contracts into language-specific subpackages before
  adding more languages.
- Model toolchains as inert specs/statuses, not as core execution logic.
- Split parser/workspace language support from external toolchain support.
- Let project detection produce signals without activating plugins directly.
- Allow activation to happen through direct inventory/status queries or through
  typed runtime events.
- Connect language/toolchain activation to coder feature/tool-group expansion
  instead of adding more hardcoded operation lists.

## Non-goals

- Do not redesign Go operation behavior in this refactor.
- Do not add a generic language-server protocol abstraction yet.
- Do not move process probing into core.
- Do not make `projectplugin` import or know concrete language plugins.
- Do not implement code-edit/refactor operations here; those remain in
  [`2026-05-16-1425-code-edit.md`](2026-05-16-1425-code-edit.md).

## Package Split

Root `core/language` keeps only shared inert language concepts:

- language identity: `LanguageID`;
- source coordinates: `Position`, `Range`, `Location`;
- shared analysis output: `Diagnostic`, `Symbol`, `Outline`;
- generic package/document shapes when they are not language-specific;
- capability identifiers;
- toolchain metadata and status.

Language-specific contracts move out:

- `core/language/golang`
  - Go operation names such as `go_outline`, `go_test`, and `go_install`;
  - Go parser/navigation query and result structs;
  - Go toolchain query and result structs;
  - Go-specific helper DTOs such as parsed `GOPROXY` data.
- `core/language/markdown`
  - Markdown operation names;
  - markdown query/result structs;
  - markdown heading/link DTOs.

`plugins/golangplugin` should import `core/language/golang` for Go contracts
and root `core/language` only for shared source concepts. `plugins/markdownplugin`
should import `core/language/markdown` for markdown contracts.

The broad `core/language.Provider` interface should be removed. It assumes one
provider supports projects, packages, outlines, imports, implementations, calls,
formatting, and diagnostics. Operation sets plus capability metadata are the
source of truth instead. If a future runtime needs a narrow interface, add it
for a concrete use case rather than recreating a universal provider.

## Toolchain Model

Add inert toolchain metadata to root `core/language`.

`ToolchainSpec` describes an available capability surface without probing or
executing:

- `id`: stable id such as `go`, `node`, `npm`, `yarn`, or `cargo`;
- `display_name`;
- `languages`: related `LanguageID` values;
- `required_binaries`: binary names and optional minimum version hints;
- `capabilities`: test, build, format, lint, doc, list, package-info, install;
- `operation_sets`: operation set names contributed by the toolchain;
- `operations`: operation refs exposed by those operation sets;
- `activation_signals`: project signals that make the toolchain relevant.

`ToolchainStatus` records runtime availability:

- `id`;
- `available`;
- binary statuses with path/version/error fields;
- optional version text or structured version fields;
- diagnostics.

Core only defines these inert shapes. PATH lookup, version probing, and process
execution belong in runtime/orchestration/plugin code using
`runtime/system.System`.

Use the spelling `Toolchain`, not `ToolChain`.

## Operation Sets And Availability

Each language plugin should contribute separate operation sets for parser and
toolchain capabilities.

For Go:

- `golang.parser` or `golang.language`
  - Workspace/parser operations such as project/package/outline/symbol,
    definition, references, imports, implementations, callers, and callees.
  - These remain available even if the `go` binary is missing.
- `golang.toolchain`
  - External Go command operations such as `go_info`, `go_env`, `go_test`,
    `go_fmt`, `go_vet`, `go_build`, and `go_install`.
  - These are projected to agents only when `ToolchainStatus{id: "go"}` is
    available.

Operation specs may remain globally registered so catalogs can describe the
full capability surface. Availability controls projection to agents/sessions,
not whether a spec exists in the catalog.

Toolchain operations should still fail clearly if invoked after availability
changes, but normal coder projection should avoid advertising unavailable
toolchains.

## Project Signals

Project detection should expose inert signals rather than activating plugins.

Signals should cover manifests and ecosystem hints:

- `go.mod`, `go.work`;
- `package.json`, `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`;
- `Cargo.toml`, `Cargo.lock`;
- markdown documentation;
- task files and CI files when useful for capability selection.

Signals can be represented directly on project inventory facets and as a
dedicated signal list, for example:

- `ProjectSignal{kind, path, language, toolchain, confidence, metadata}`;
- `Inventory.Signals []ProjectSignal`;
- optional facet-level signal references for provenance.

`projectplugin` remains the owner of discovery, but it does not import language
plugins and does not decide which plugins or operation sets become active.

## Events

Activation can also happen through typed events. This is useful for session
replay, projections, and live updates when a workspace changes or when a
toolchain probe completes.

Add event samples through the normal bundle `EventTypes` path:

- `project.signals_observed`
  - emitted when project inventory discovers or refreshes signals;
  - payload includes workspace root/scope, project id when known, signals, and
    whether the scan was truncated.
- `language.toolchain_status_observed`
  - emitted when a toolchain resolver probes availability;
  - payload includes `ToolchainStatus` and diagnostics.

Events are not the only source of truth. Runtime code should also be able to
query current project inventory and toolchain status directly, so startup does
not depend on event replay.

## Coder Bundle Config Integration

The configurable coder bundle design already introduces features and tool
groups. Language/toolchain activation should plug into that design rather than
adding another configuration mechanism.

Add coder feature concepts:

- `FeatureProjectSignals`: include project inventory and signal context.
- `FeatureLanguageSupport`: include parser/workspace language operation sets
  relevant to project signals.
- `FeatureAvailableToolchains`: include toolchain operation sets whose status
  is available.

Bundle config remains inert. It declares desired features, explicit operation
adds/removes, datasource choices, agents, and delegation settings. Runtime or
distribution wiring resolves project signals and toolchain status, then expands
feature/tool-group selections into concrete operation refs for the root agent
and delegation profiles.

Explicit config still wins:

- explicit operation additions remain available even if not implied by a
  feature;
- explicit removals suppress feature-derived operations;
- tests can pin exact operation sets for deterministic variants.

## Implementation Sequence

1. [Done] Add this design and link it from the Go roadmap.
2. [Done] Split DTO packages without behavior changes:
   - move Go contracts into `core/language/golang`;
   - move Markdown contracts into `core/language/markdown`;
   - update plugin imports and typed operation contracts;
   - remove `core/language.Provider`.
3. [Done] Add `ToolchainSpec`, `ToolchainStatus`, and event payload types.
4. [Done] Extend resource/catalog composition to carry toolchain specs.
5. [Done] Add project signals to project inventory and signal events.
6. [Done] Add a toolchain status resolver that probes through
   `runtime/system.System`.
7. [Done] Split Go operation sets into parser and toolchain sets.
8. [Done] Teach coder bundle config feature expansion to consume project signals,
   toolchain statuses, and operation sets.
9. [Done] Replace hardcoded Go/Markdown operation lists in coder with feature/tool
   group expansion while preserving a full-capability preset.

## Progress Notes

Implemented in this cycle:

- Root `core/language` now contains only shared source concepts plus inert
  `ToolchainSpec`/`ToolchainStatus` models and the
  `language.toolchain_status_observed` event payload.
- Go contracts and operation names live in `core/language/golang`; Markdown
  contracts and operation names live in `core/language/markdown`.
- `plugins/golangplugin` contributes separate `golang.parser` and
  `golang.toolchain` operation sets plus a Go `ToolchainSpec`.
- `core/project` inventory exposes `Signal` records and the
  `project.signals_observed` event; `runtime/project` derives signals from Go,
  Node, Cargo, Markdown, task, and CI facets without importing language
  plugins. Signals are derived after project result bounding so
  `Inventory.Signals` only references projects included in `Inventory.Projects`.
- `runtime/language.ResolveToolchainStatus` probes required binaries through
  `runtime/system.System`, keeping core specs inert. Toolchain specs with no
  required binaries are treated as available without requiring process support.
- `apps/coder` expands language and toolchain operations from project signals,
  toolchain statuses, and operation sets, with explicit add/remove overrides.
  `Bundle()` uses the full-capability preset to preserve existing coder
  behavior.

Remaining follow-up after this implementation:

- Live reprojection when project signals or toolchain availability change
  during a long-running session remains a later runtime enhancement.
- Additional language/toolchain specs should follow the same split and signal
  pattern before adding Rust, JavaScript, TypeScript, or Python operations.

## Testing

- Root `core/language` contains no Go or Markdown query/result structs after
  the split.
- `plugins/golangplugin` imports `core/language/golang`; `plugins/markdownplugin`
  imports `core/language/markdown`.
- Go parser operations are projected without a Go binary.
- Go toolchain operations are omitted when `go` is unavailable and included
  when it is available.
- Toolchain specs are inert and process probing is implemented outside core.
- Project inventory reports expected signals without importing concrete
  language plugins.
- Signal/status events are registered, encoded, persisted, and replayable.
- Coder full-capability behavior remains equivalent when all configured
  features and toolchains are available.
- Explicit coder operation additions/removals override feature-derived
  defaults.

## Assumptions

- Availability affects operation projection, not catalog registration.
- Events are optional activation inputs; direct inventory/status queries remain
  supported.
- `go_callers` and `go_callees` type-aware precision is a later upgrade, not
  part of this scalability refactor.
- The first activation implementation can be synchronous at session/app startup;
  live reprojection after workspace changes can follow after the static path is
  stable.
