# Design: coder shell

## Status

Implementation checkpoint, 2026-05-19. The initial standalone shell exists
behind `cmd/codershell` and the app-level `coder shell` command. It has a Bubble
Tea REPL surface, client/session abstraction, tab-per-session state object,
resource mention picker, cwd handling, structured transcript events, endpoint
selection, and a default direct-channel client backed by `agentruntime` session
handles. The implementation is still early: it is usable for exercising the UI
and channel path, but not yet a complete managed process shell.

## Summary

`coder shell` is an AI-native terminal shell/TUI for local development. It should
feel practical to users of fish, bash, and zsh while making coder concepts native:
workspace/project inventory, managed processes, operation safety, approvals,
agent assistance, app facets, tasks, workflows, and transcripts.

The shell should be useful as an independent product before AI features are
complete. The first functional surface is a full REPL view that can execute
commands through the runtime process boundary, with structured output events,
risk assessment, approval flow, and shell context retention. Coder/AI integration
is then added by attaching a local or remote coder client to the same shell
surface rather than by replacing the shell loop.

The implementation should use a Bubble Tea-style TUI rather than extending the
current line-oriented REPL. The first milestone is not to replace a POSIX shell;
it is to provide a safe, inspectable, coder-aware interactive surface where shell
commands are one kind of managed action alongside coder app actions, project
tasks, workflows, and agent turns.

`coder shell` remains separate from the first unified coder app surface parity
milestone. It can share runtime, process, terminal, command, project, and
configuration packages, but it should not force the default `coder` REPL to become
a full shell.

## Implementation Checkpoint: 2026-05-19

### Done

- Added the standalone `cmd/codershell` entrypoint and app command wiring for
  `coder shell`.
- Added an experimental Bubble Tea REPL view with header/status/timeline/prompt
  rendering, tab creation/switching, shell/ask input modes, slash handling, cwd
  changes, and mention picker state.
- Split shell state from the UI model with a `ShellObject` that owns tabs,
  session IDs, cwd, input mode, transcript, selected mentions, and context
  projection policy.
- Introduced the shell client boundary: `ShellClient`, `CreateSession`,
  `SubmitCommand`, `SubmitAsk`, `SubmitSlash`, `ChangeCWD`, and
  `ResourceSearch`.
- Added client implementations for:
  - deterministic `FakeClient` tests/demos;
  - local process-backed prototype client;
  - remote endpoint placeholder for `unix://`, `http(s)://`, and future target
    URLs;
  - default `DirectChannelClient` backed by `agentruntime.ChannelClient` and real
    `agentruntime.Session` handles.
- Made default `--connect` resolve to direct-channel mode. Plain text/ask input
  now submits through `Session.Submit(NewSubmission().WithText(...))`; slash input
  submits `command.Invocation` values through the direct channel path.
- Added connection metadata to shell state and transcripts so the UI can show the
  selected endpoint/client.
- Added structured transcript events for client connection, submitted input,
  command start/output/completion, ask output, slash submission, resource
  mentions, cwd changes, and errors.
- Added static resource search/mention plumbing as a stand-in for future
  datasource-backed completion.
- Added unit coverage for command wiring, endpoint parsing/selection, direct
  channel session/submit behavior, local client session behavior, shell object
  state transitions, ask context projection, slash dispatch, mention selection,
  and protocol round trips.

### Still open

- The TUI is still compact/prototype quality. It does not yet implement the final
  header/footer split, dashboard view, scrollback controls, viewport selection,
  long-output rendering, or a polished theme.
- Direct-channel plain input currently behaves as agent conversational input. Raw
  OS process execution is available only through the local prototype client path,
  not through the default direct-channel mode.
- The intended command-execution path for normal shell lines still needs a stable
  coder command/operation contract instead of the temporary direct text
  submission behavior.
- There is no streaming UI path yet: client submissions wait for run completion
  and then append compact transcript events.
- Remote transport is still a placeholder. `unix://` and `http(s)://` endpoints
  parse and preserve endpoint metadata, but do not connect to a daemon/control
  API yet.
- `cmdrisk` assessment, approval prompts, cancellation, background process
  lifecycle, and managed process dashboard rendering remain unimplemented in the
  TUI.
- Resource search is static. It should be replaced by datasource-style resource
  search owned by the connected client/session.
- Session close/resume policy is minimal; tab close, reconnect, and persisted
  shell transcripts are not finished.
- `.coder.yaml` shell preferences, profile loading, environment file handling,
  default panes, and model-visible history policy are not wired.
- Full-screen app/editor handoff is not implemented.
- The prototype has targeted package tests, but has not yet gone through the full
  repository `task verify` gate in this checkpoint.

### Next three bigger batches

1. **Real execution and streaming transport.** Define the stable shell command
   contract over the coder client/session boundary, connect normal shell input to
   managed runtime process execution, stream stdout/stderr into transcript events,
   support cancellation/backgrounding, and remove the ambiguity between
   direct-channel conversational input and process command input.
2. **Safety and lifecycle controls.** Insert `cmdrisk` before dispatch, render
   approval prompts, wire deny/allow/cancel flows, attach every process/task/run
   to its tab session, implement tab close/resume policy, and expose active runs
   in the dashboard/status surfaces.
3. **Product polish and remote attachment.** Finish the intended header/footer,
   scrollback, dashboard, theming, help, path/resource completion, persisted
   history policy, `.coder.yaml` preferences, and replace the remote placeholder
   with local daemon `unix://` plus HTTP/channel attachment.


## Goals

- Provide a fast local TUI for common development loops: inspect, edit, run,
  test, watch, background a process, ask the agent, approve or deny risky work,
  and resume prior context.
- Make the REPL view fully useful from the start: execute commands, stream
  output, maintain tabs, support shell/ask input modes, and show professional
  status information.
- Provide a dashboard/task-manager view for active agents, running tasks,
  managed processes, approvals, and recent events.
- Treat shell commands, project tasks, app actions, workflows, operations, and
  agent assistance as first-class interactive objects with consistent rendering,
  cancellation, history, and safety.
- Route all process execution through the runtime managed process boundary and
  operation safety envelope.
- Assess typed commands with `cmdrisk` before execution; require explicit user
  approval for risky commands after Enter and before dispatch.
- Keep UI communication behind a shell/coder client abstraction so the same TUI
  can later attach to a remotely running coder app.
- Make project/app facets visible and runnable by explicit action without
  automatically importing app resources into the coder agent session.
- Keep terminal state useful to both humans and models: structured events for
  command intent, command output, approvals, background process lifecycle, and
  selected transcript context.
- Support `.coder.yaml` as the place for workspace roots, env file preferences,
  shell profile preferences, default panes, and model-visible history policy.

## Non-goals

- Do not implement a complete POSIX shell parser in the first milestone.
- Do not bypass runtime process management by calling `os/exec`, `syscall`, or a
  raw shell path from the TUI package.
- Do not make the TUI directly call model/provider APIs or app internals. The UI
  talks to local or remote coder functionality through a client/session
  abstraction.
- Do not make app facet resources part of coder by default. App resources remain
  inventory until explicitly run/imported.
- Do not make `coder shell` the default `coder` command until interaction,
  accessibility, scripting, and non-TTY fallbacks are proven.
- Do not depend on Bubble Tea types from core, runtime, or orchestration layers.
  Bubble Tea belongs at an adapter/app boundary.

## Clarified Direction

### Product shape

`coder shell` is launched explicitly:

```text
coder shell [path]
```

The optional path selects the workspace root using the same discovery rules as
other coder commands. If omitted, the current working directory is used.

The shell has two runtime forms:

1. **Interactive TTY mode**: Bubble Tea TUI with REPL and dashboard views,
   command input, tabbed REPL sessions, status footer, process list, approvals,
   and agent suggestions.
2. **Non-TTY fallback**: line-oriented command loop or clear error, depending on
   whether stdin/stdout are usable. The fallback must not silently execute shell
   commands with different safety semantics.

The shell also has input modes inside the REPL:

- **Shell mode**: prompt marker `>`; Enter submits a command to the connected
  client for parsing, risk assessment, and runtime process execution.
- **Ask mode**: prompt marker uses an agent emoji, for example `🤖`; Enter sends
  the line as an agent ask with shell context. The agent response is displayed in
  the REPL timeline, but later should be dispatched through the same coder client
  path used by the regular coder app.

`Alt+Tab` toggles between shell and ask mode when an explicit toggle is needed.
The input component should also auto-switch when possible: typing `!` from ask
mode returns to shell/action mode, `/` enters command mode, and `@` opens the
mention picker without changing the current submit mode. `?` is accepted as an
ask shorthand for now. A natural-language line that is not executable may be
classified as an agent request instead of a rejected shell command, but only after
clear UI indication.

### Interaction model

The TUI is event-driven. User input is parsed according to the active input mode
and active view. The REPL remains the primary view for command entry, while the
dashboard is a task-manager view for observing and controlling background state.

User input is parsed into one of these action kinds:

| Input | Meaning | Example |
|---|---|---|
| Plain command | Managed process action | `go test ./...` |
| Coder command | Existing coder area/action command | `coder app run .` |
| Slash command | Coder/client command, except local exits | `/help`, `/task verify`, `/history` |
| Local exit | Shell-local termination | `/exit`, `exit` |
| Ask | Agent turn with shell context | ask mode: `why did this test fail?` |
| Operation run | Explicit operation invocation | `/op filesystem.read path=go.mod` |
| Workflow/task | Explicit workflow or project task | `/task verify`, `/workflow release` |

A plain command may be executed directly as a managed process without requiring a
full shell parser when it can be tokenized safely. Shell features such as pipes,
redirects, glob expansion, variable assignment, command substitution, and control
operators require an explicit policy decision:

- either reject with a clear message and suggestion,
- or execute through a configured shell interpreter as a managed process with the
  whole command string visible in the approval/safety prompt.

The initial milestone should reject ambiguous shell syntax unless an explicit
`--via-shell` option or shell preference enables it.

Slash command handling should match the eventual coder app behavior: `/exit` and
`exit` are handled locally by the shell application; every other slash command is
sent through the coder client/session abstraction and rendered from the returned
structured events or text. `/help` is therefore not a local hard-coded help menu
long-term; it is a generic builtin implemented on the harness/session side and
rendered by the shell.

### Views, tabs, and panes

The shell has top-level views with hotkey navigation:

- **REPL view**: command/ask timeline plus input box. This is the default view and
  the current implemented shape.
- **Dashboard view**: task-manager style overview of active agents, running tasks,
  managed processes started by the shell, pending approvals, and recent events.

The REPL view has tabs. Each tab is a separate coder session, not a lightweight
view over a global session. Tabs are labeled `1`, `2`, `3`, ... with the active
tab using a colored background. Each tab owns its session ID, transcript,
timeline, current input mode, input buffer, history cursor, cwd, resource
mentions, pending approvals, and running foreground/background processes. There
is no global process list: every process/task/agent event is linked to the tab's
client session. Switching tabs changes the active session; it must not merge
transcripts or background state across tabs.

Initial hotkeys:

| Key | Action |
|---|---|
| `ctrl+r` | Switch to REPL view |
| `ctrl+d` | Switch to dashboard view when input is empty; otherwise local EOF behavior |
| `ctrl+t` | New REPL tab |
| `ctrl+tab` / `shift+ctrl+tab` | Cycle tabs, where terminal support allows |
| `alt+1`..`alt+9` | Select tab by label |
| `alt+tab` | Toggle shell/ask mode when auto-switching is not enough |
| `F10` | Toggle help/keybinding menu |
| `ctrl+c` | Cancel active foreground process; if none, request exit |

The REPL layout should keep panes simple and predictable:

- **Tab strip**: compact numbered tabs, active tab highlighted.
- **Timeline pane**: chronological transcript of commands, agent turns, operation
  cards, approvals, and compact process output.
- **Input pane**: editable prompt with mode marker, command risk indicator, path
  completion, and optional approval state.
- **Footer/status pane**: workspace, project inventory, Go version, git branch,
  active model/session, running processes, current approvals, last exit status,
  current cwd, and remote/local connection state.

The UI should be compact by default: no outer padding, no decorative empty
margins, and no oversized borders. Header and footer are always present when
space allows; content panes adapt between single-column and denser compact
layouts for narrow terminals.

The project/status information currently in the header belongs in the footer.
The header should be minimal and professional: app title, active view, tab strip,
and high-priority state only. The footer carries detailed environment context.
### Tab/session ownership

Every tab is a separate coder session. The shell has no global execution state and
no global background process pool. All executable actions, task runs, workflows,
agent asks, process lifecycle events, approvals, resource search requests, and
transcript appends go through the client and carry the tab's session ID.

Implications:

- creating a tab creates or attaches to a coder session through the client;
- closing a tab requires a session-close policy for that session's running work;
- background processes belong to the session that started them;
- dashboard rows are grouped by tab/session, not by global process ownership;
- reconnect/resume is session-based, using the client target plus session ID;
- cwd and transcript are per-session/per-tab.


Panes are layout, not ownership boundaries. Execution state remains in runtime or
orchestration packages; the TUI only renders and dispatches.

### Input prefixes and reusable mode components

Input handling should be implemented as reusable prompt modes rather than one-off
branches in the REPL view. Each mode owns:

- icon/marker rendered left of the input,
- completion source filters,
- risk/validation display,
- submission target,
- placeholder/help text,
- local keyboard behavior.

Initial modes and prefixes:

| Prefix / mode | Marker | Meaning | Completion source |
|---|---|---|---|
| Shell mode | `>` | Runtime managed command | paths, executables, history |
| Ask mode | `🤖` | Ask active/default agent with shell context | agents, history, pinned context |
| `@` mention picker | `@` | Tag or target any resource | all searchable resources |
| `!` | workflow/action icon | Run workflow, operation, action, or task | workflows, operations, actions, tasks |
| `/` | command icon | Coder slash command | commands |

Typing a prefix switches the input component into that mode and updates the icon
left of the input immediately. For example, typing `!` should open fuzzy
completion over workflows, operations, actions, and project tasks. Prefix modes
can still submit through the same client abstraction; the mode only controls
editing, completion, validation, and rendering.

The `@` prefix is a general resource mention/tag picker, not only an agent
selector. Typing `@` opens the completion box immediately and searches across all
available resources. Continuing to type narrows the same search, for example:

```text
@        -> open all-resource completion
@ap      -> fuzzy-filter all resources by "ap"
@ap<Tab> -> accept highlighted result and insert a tagged mention
```

Completion rows should show a compact kind marker and label, for example:

```text
[🤖 agent] coder
[skill] code-review
[file] apps/coder/shell/shell.go
[url] https://example.test/spec
[workflow] release
[operation] filesystem.read
```

Selecting a result inserts a structured mention into the input buffer. The
rendered text can stay compact, such as `@coder`, but the input model should
retain the selected resource kind/id/URI so later dispatch can resolve it
reliably.

Mention semantics depend on the active mode and selected kind:

- `@agent` in ask mode targets or includes that agent in the conversation.
- `@skill` in ask mode means load/use that skill before executing the query.
- `@file` and `@url` mean ensure those resources are pulled into context later.
- `@workflow`, `@operation`, `@task`, and `@command` can be tagged for context or
  inserted as runnable targets depending on mode.

For the current implementation phase, focus on the UI interaction and resource
selection model. It is enough to mock resource results and preserve structured
mentions; no full AI/context-loading integration is required yet.

The first implementation can mock these interactions locally, but the UI shape
should assume the data ultimately comes from the client.

### Help menu

`F10` should open a compact help/keybinding menu. It should include:

- global view hotkeys,
- REPL tab hotkeys,
- input mode prefixes,
- local commands such as `exit` and `/exit`,
- status legend for risk/process indicators.

The help menu is a UI overlay, not a slash command. `/help` remains a coder
client/session command rendered as normal output.

### Theme

Use a Monokai-inspired default color scheme:

| Role | Suggested color |
|---|---|
| Background | `#272822` |
| Panel background | `#1f201b` |
| Foreground | `#F8F8F2` |
| Muted text | `#75715E` |
| Green/success | `#A6E22E` |
| Yellow/warning | `#E6DB74` |
| Orange/risk | `#FD971F` |
| Red/error | `#F92672` |
| Blue/info | `#66D9EF` |
| Purple/accent | `#AE81FF` |

Theme values should live in the app/adapter shell UI package, not in runtime or
core packages.

### History and transcript policy

`coder shell` maintains two related histories:

1. **Human shell history**: command lines and shell-local actions used for recall
   and search.
  2. **Model transcript context**: curated structured events made available to the
     agent.

Every interaction should be recorded in a shell-local transcript. A command entry
stores both request and response as structured events, for example a shell command
request with cwd/argv/text followed by output chunks and a final result with exit
status and bounded stdout/stderr. Agent asks, slash commands, approvals, resource
mentions, tab changes, and client/connection events are also transcript events.

The shell does not trigger an agent on every command. Instead, later agent or
command requests can choose a projection of the local transcript as context. For
an LLM-backed agent, that projection may become tool-call/tool-result-like
messages; for agentruntime domain flows, it should use the native request/result
or event vocabulary rather than pretending everything is an LLM tool. Example:

```text
> whoami
# transcript records shell request + process result

? what does this output mean?
# agent request receives prior transcript projection as context
```

By default, model context should include command text, exit status, working
directory, bounded stdout/stderr tail, operation result summaries, approvals,
resource mentions, and user-pinned output. It should not include full unbounded
terminal scrollback or secret-looking environment output.

`.coder.yaml` should allow policy such as:

```yaml
shell:
  history:
    persist: true
    model_context: summaries
    max_output_bytes_per_command: 12000
  execution:
    via_shell: false
    default_timeout: 10m
  env_files:
    - .env.local
  layout:
    default: timeline-detail
```

Exact config shape should be refined with the broader `.coder.yaml` design before
implementation.

### Execution transport and streaming

The shell app should not embed a shell interpreter. Plain and complex commands are
submitted to the connected coder client for execution. By default the client is a
local unix-socket connection to the embedded coder daemon for the selected coder
instance; later targets can use HTTP/SSE or other channel clients. The same
client interface should expose the existing coder command execution path used by
apps/coder for `!<cmd>` so process execution remains inside the runtime/system
boundary.

Streaming requirements:

- local default mode connects to the selected coder instance over its unix socket;
- each coder instance has an instance ID and an associated socket path that can be
  passed to `coder shell`;
- remote mode uses the coder client abstraction to subscribe to equivalent
  process output events over the selected transport;
- both modes normalize events into the same TUI event model;
- foreground process output streams into the active REPL timeline;
- dashboard process rows update from the same event stream;
- bounded captured stdout/stderr is retained for agent context and detail views.

The UI must not scrape rendered terminal text to infer process state.

### Command parsing, cwd, and path completion

The shell object tracks current working directory. `cd` changes shell state and is
recorded in the transcript; subsequent command, completion, and resource queries
use that cwd. By default, completion roots include the main workspace and a
secondary user root such as `/home/$USER`, subject to system/workspace policy.

Path completion should be workspace-aware and side-effect free:

- complete relative and absolute paths through the system/client boundary;
- support fuzzy matching over path segments;
- show directories before files;
- preserve typed quoting/escaping where possible;
- account for cwd changes from `cd`;
- later merge project tasks, slash commands, history, operations, and agent
### Shell object vs UI layer

Keep shell state and UI rendering cleanly separated. The UI should render and send
intents; it should not own command execution, transcript policy, cwd semantics, or
client connection behavior.

Sketch:

```text
ShellObject
  - instance/client target
  - cwd and allowed roots
  - tabs and active tab state
  - local transcript
  - history
  - resource mentions
  - running process/task/agent summaries
  - approval state
  - risk assessment cache
  - ShellClient

Shell UI
  - Bubble Tea model/update/view
  - viewport and markdown components
  - keybindings and overlays
  - completion popovers
  - layout/theme
  - renders ShellObject snapshots and dispatches intents
```

The shell object can live in an app/orchestration-adjacent package if it remains
surface-neutral enough; Bubble Tea components stay in the app/adapter UI layer.
The shell object tracks tabs and client/session handles, but session-owned state
remains authoritative on the client side. Local state is a mirror/cache for
rendering and offline editing of the input buffer.


  suggestions into the same completion UI with source labels.

Command parsing should reuse the same shell parser/cmdrisk parsing machinery where
possible so the UI can understand pipelines and replace/annotate parts of complex
commands without executing them locally. Ambiguous or risky syntax is still
submitted to the client execution path only after risk assessment and approval.


## Architecture

### Placement

- `cmd/coder`: executable glue only; routes `coder shell` to app assembly.
- `apps/coder`: product assembly for shell dependencies and command registration.
- `adapters/terminal` or `adapters/tui`: Bubble Tea rendering, key handling, pane
  layout, terminal capabilities, and TTY detection.
- `orchestration/session` / `orchestration/harness`: session submission,
  command dispatch, agent turns, and event stream bridging.
- `runtime/process` and `runtime/operation`: managed process execution,
  cancellation, streaming, approvals, and safety envelope.
- `runtime/project`: workspace/project inventory used by completions and visible
  facets.
- `core/*`: only inert specs/events/contracts if a stable shell-neutral contract
  is needed. No Bubble Tea, no IO, no rendering.

### Event flow

```text
keyboard input
  -> TUI update loop
  -> shell input classifier
  -> command/process/task/workflow/agent submission
  -> orchestration dispatch
  -> runtime operation/process execution through safety envelope
  -> structured events/output chunks
  -> TUI model update
  -> transcript/history persistence policy
```

The TUI should consume structured events instead of scraping terminal output. This
keeps process lifecycle, approvals, and model-visible summaries reliable.

### Process execution

Plain commands become client command-execution requests with:

- session ID,
- executable and argv, or parsed command structure accepted by the client command
  execution path,
- working directory,
- environment overlays from configured env files and session state,
- timeout and cancellation policy,
- safety metadata for authorization and approval,
- stdout/stderr streaming handles,
- terminal mode requirements, if any.

The shell app does not execute commands directly and does not embed a shell
interpreter. Long-running commands can be foreground or background managed
processes, but they belong to the session/tab that started them. Closing a tab
must not orphan that session's processes accidentally; the user must choose
whether to terminate, detach/resume later, or keep daemon-like work when policy
allows it.

### Client resource discovery and search

Autocomplete should be designed around a reusable client-facing resource search
API rather than hard-coded local lists. The shell frontend should be able to ask
the connected local or remote coder client for resources relevant to completion
and selection.
Autocomplete can reuse the agentruntime datasource concept rather than inventing
a separate ranking/indexing subsystem. Resource providers can expose searchable
records through datasource-style entities, and the shell client can federate or
proxy those records for completion. The UI should not own ranking policy beyond
showing source/kind labels and preserving the order returned by the client.


Sketch:

```go
type ResourceKind string

const (
    ResourceCommand   ResourceKind = "command"
    ResourceOperation ResourceKind = "operation"
    ResourceWorkflow  ResourceKind = "workflow"
    ResourceTask      ResourceKind = "task"
    ResourceSkill     ResourceKind = "skill"
    ResourceAgent     ResourceKind = "agent"
    ResourceAction    ResourceKind = "action"
    ResourcePath      ResourceKind = "path"
    ResourceFile      ResourceKind = "file"
    ResourceURL       ResourceKind = "url"
    ResourceHistory   ResourceKind = "history"
)

type ResourceSearchQuery struct {
    Text       string
    Kinds      []ResourceKind
    Limit      int
    Workspace  string
    CWD        string
    PrefixMode string
    Mention    bool
}

type ResourceSearchResult struct {
    Kind        ResourceKind
    ID          string
    Label       string
    Detail      string
    InsertText  string
    Description string
    URI         string
    Icon        string
    Score       float64
    Metadata    map[string]string
}

type ResourceMention struct {
    Kind       ResourceKind
    ID         string
    Label      string
    URI        string
    InsertText string
    Metadata   map[string]string
}

type ShellClient interface {
    ResourceSearch(ctx context.Context, query ResourceSearchQuery) ([]ResourceSearchResult, error)
}
```

The exact package and type names can change, but the interaction contract should
remain: the UI sends a query with kind filters such as `kind=[workflow,operation]`
or `kind=[skill]`, and the client returns ranked entries with insert text and
metadata. An `@` mention query sends no kind filter, or sends all relevant kinds,
so it can search agents, skills, files, URLs, workflows, operations, commands,
tasks, actions, paths, and history together. Local mode may implement this by
composing project inventory, filesystem completion, registered commands,
operation registries, workflow registries, skills, agents, URL/history indexes,
and history. Remote mode forwards the same query to the running coder app.
For now, do not spend effort on cross-kind ranking. The client/datasource layer
returns an ordered list; the UI renders it as-is with compact `[kind] label` rows.


Resource search should be:

- side-effect free,
- cancellable/debounced,
- ordered by the client/datasource layer,
- source-labeled in the UI,
- permission-aware where the client can cheaply know visibility,
- consistent across local and remote mode,
- able to return structured mention metadata, not only display text.


## Backlog / Deferred Ideas

These ideas remain valid but move behind the functional shell, dashboard, risk,
completion, and client-abstraction milestones:
### Full-screen subprocesses, editors, and rich output

Arbitrary full-screen subprocesses such as `htop`, `top`, pagers, and TUIs should
not run inside `coder shell` initially. They should fail to start with a clear
message explaining that interactive PTY passthrough is unsupported. The exception
is editing a resource, which uses a controlled file edit flow instead of running a
remote full-screen app directly.

Initial editor strategy:

1. Read the target file through the system/client boundary.
2. Write a temporary editable copy under an allowed local temp location.
3. Suspend or yield the Bubble Tea program while invoking local `$EDITOR` on the
   temp file.
4. On editor exit, detect file changes.
5. Write the changed content back through the system/client boundary.
6. Record the edit request/result in the tab/session transcript.

For local terminal yielding, research confirmed Bubble Tea provides external
command helpers such as `tea.ExecProcess`/`tea.Exec`, and Bubbles v2 includes a
viewport component at `github.com/charmbracelet/bubbles/v2/viewport`. Use the
latest Bubble Tea/Bubbles/Lip Gloss v2 line where possible. Do not generalize this
into arbitrary remote PTY support until there is an explicit client/session
transport for it.

Agent output should render live markdown where possible. Prefer an existing
component from the codewandler/markdown stack if available in the workspace; if
not, evaluate Charmbracelet Glamour/Glow-style rendering behind a reusable
markdown viewport component. The REPL timeline/detail/dashboard should use a
scrollable viewport component rather than hand-rolled line slicing for long
content.



- Detail pane for expanded selected item, full process output tail, structured
  operation result, diff preview, or approval rationale.
- Project task and workflow shortcuts beyond generic client-rendered slash
  commands.
- App facet import/run flows and resource visibility controls.
- AI-generated shell completions or suggestions; these must be labeled as
  suggestions, not normal completions.
- Configurable `via_shell` execution for pipes, redirects, glob expansion,
  variable assignment, command substitution, and control operators.
- Full-screen subprocess handling for editors. Arbitrary PTY apps such as `htop`,
  `top`, pagers, and TUIs are explicitly unsupported at first and should fail
  cleanly.
- Persistent shell-private history format and transcript-store unification.
- Non-TTY fallback behavior beyond clear errors and documented semantics.
- Mouse support, detailed accessibility audit, and theme customization beyond the
  default Monokai palette.
### Completions

Completions should be layered:

- filesystem paths and executables from the workspace/system boundary,
- project tasks from project inventory,
- coder area/action commands,
- app facets and resources visible in the workspace,
- workflows, agents, operations, and datasource names,
- history entries,
- optional AI suggestions clearly labeled as suggestions, not shell completions.

Completion providers must not perform hidden side effects.

### Approvals and safety

Risky actions render as approval cards with:

- command/action name,
- working directory and target resources,
- declared access descriptors,
- reason/rationale,
- exact argv or shell string,
- environment source summary without secret values,
- allow/deny controls and keyboard shortcuts.

The approval card is also a transcript event. The agent should be able to explain
why approval is needed, but it must not grant approval on behalf of the user.

## User Experience Sketch

```text
┌ coder shell ─ REPL ─ tabs: [1]  2  3 ───────────────────────────────┐
│                                                                      │
│  > go test ./apps/coder/...                                          │
│  ✔ process #12 exited 0 · 2.1s                                       │
│                                                                      │
│  🤖 explain the last run                                              │
│  agent: tests passed; no failures detected in the selected output.    │
│                                                                      │
│  > task verify                                                       │
│  ⚠ risk: medium · approval required                                  │
│  approve?  y allow   n deny   v details                              │
│                                                                      │
├──────────────────────────────────────────────────────────────────────┤
│ > _                         risk low        completions: apps/ cmd/  │
├──────────────────────────────────────────────────────────────────────┤
│ pwd agentruntime · go 1.26.1 · project github.com/fluxplane/...      │
│ facets go git task docs agents · processes 1 · tasks 0 · local       │
└──────────────────────────────────────────────────────────────────────┘
```

Dashboard view sketch:

```text
┌ coder shell ─ Dashboard ─────────────────────────────────────────────┐
│ Agents                                                               │
│  ● coder        idle       model gpt-5.5       session local         │
│                                                                      │
│ Processes                                                            │
│  #12 go test ./apps/coder/...     exited 0      2.1s                 │
│  #13 task verify                  running       8.4s                 │
│                                                                      │
│ Tasks / approvals                                                    │
│  none pending                                                        │
├──────────────────────────────────────────────────────────────────────┤
│ ctrl+r REPL · ctrl+d dashboard · ctrl+t new tab · ctrl+a mode        │
└──────────────────────────────────────────────────────────────────────┘
```

## Milestones

### Milestone 0: Spike and current scaffold

- Add a minimal `cmd/codershell` command while the main `coder shell` command is
  still experimental.
- Render a Bubble Tea input and timeline using fake events.
- Render project inventory/toolchain context in a footer/status area.
- Confirm terminal behavior in common terminals and CI/non-TTY environments.
- Mock the interaction model before implementing all backends: REPL/dashboard
  switching, tabs, mode prefixes, F10 help, resource search results, approval
  cards, and streaming process cards should all be easy to fake with local data.

### Milestone 1: Shell UI foundation

- Apply Monokai theme and professional layout.
- Move project/toolchain details from header to footer.
- Add top-level REPL and dashboard views with hotkeys.
- Add numbered REPL tabs, active tab styling, tab creation, and tab cycling.
- Add shell/ask input modes with prompt marker switching.
- Keep `/exit` and `exit` local; route other slash commands through a client
  placeholder even before full coder integration.
- Add reusable input mode components for shell, ask, `@agent`, `!` workflow/action,
  and `/` command prefixes.
- Add F10 help overlay.
- Add mocked `ResourceSearch` completion providers for commands, workflows,
  operations, skills, agents, tasks, paths, files, URLs, and history.
- Add `@` all-resource mention picker that opens immediately, fuzzy-filters while
  typing, shows `[kind] label` rows, and preserves structured mention metadata.

### Milestone 2: Runtime-backed managed commands

- Classify plain commands and run safe argv commands through
  `runtime/system.ProcessManager.Start` in local mode.
- Normalize process events into the TUI event model and stream stdout/stderr into
  the REPL timeline and dashboard process table.
- Support cancellation, foreground/background process list, exit status, and
  bounded output retention.
- Reject unsupported shell syntax with clear diagnostics.

### Milestone 3: Risk, approval, and completion

- Integrate `cmdrisk` realtime/debounced command assessment.
- Re-run risk assessment on Enter and render approval cards for risky commands.
- Execute approved commands only after explicit user approval.
- Add workspace path completion with fuzzy matching.

### Milestone 4: Coder/client integration

- Add local coder client adapter that connects to the selected embedded coder
  daemon over its unix socket by instance ID.
- Add remote coder client adapter for attaching to a running coder app over HTTP
  or another channel transport.
- Route non-exit slash commands and ask-mode input through the client abstraction.
- Display results as structured REPL/dashboard events.

### Milestone 5: Coder-native actions

- Add slash commands for project tasks, app actions, workflows, operations, and
  agent asks via the client/session path.
- Add project/app facet discovery to status and completions.
- Keep app resources visible but not auto-loaded.

### Milestone 6: Safety and transcript

- Persist shell history and structured transcript summaries according to policy.
- Add model context selection and pin/unpin controls.
- Ensure approval, command, and process events are available to agent context in a
  bounded and redacted form.

### Milestone 7: Usability hardening

- Add robust completion, history search, keyboard help, mouse-free navigation,
  accessibility review, terminal resize handling, and theme support.
- Add non-TTY behavior and documented fallback semantics.

## Testing

Unit tests:

- input classification for plain command, slash command, coder command, ask, and
  rejected ambiguous shell syntax.
- argv tokenization and explicit via-shell policy behavior.
- history/model transcript filtering and output byte limits.
- approval card metadata redaction.
- completion provider ordering and no-side-effect guarantees.

Integration tests:

- `coder shell` starts in a TTY harness and renders prompt/status panes.
- managed process stdout/stderr stream into timeline/detail panes.
- cancellation terminates the managed process and records a terminal event.
- background process lifecycle survives pane navigation and exits cleanly.
- approval prompts block execution until allowed or denied.
- app facets are visible in inventory/completion but not auto-imported into the
  coder session.
- viewport behavior, scrolling, and markdown rendering for long agent output.
- shell object state transitions independent from the Bubble Tea model.
- non-TTY invocation uses the documented fallback or exits with a clear error.

Manual/live tests:

- Run `task coder:live-test -- "start coder shell, run go test, ask for a summary"`
  once the command is wired into the live-test harness.
- Verify terminal rendering with narrow/wide windows, resize, copy/paste, Ctrl-C,
  Ctrl-D, and long output.

## Resolved Questions

1. **Shell interpreter:** Do not embed a shell interpreter in the shell app. Submit
   commands to the connected coder client; the client exposes the existing
   apps/coder `!<cmd>` execution path through the runtime/system boundary and, for
   remote targets, through channel clients.
2. **Transcript/history:** Store every interaction in a shell-local transcript as
   structured request/result/events. Command transcript entries include request,
   streamed output, and final result. Agent asks receive a selected projection of
   prior interactions as context, but commands do not trigger agents by default.
3. **Session attachment:** `coder shell` connects to the specified target. The
   current default is the in-process `direct` channel client backed by
   `agentruntime.ChannelClient`; local daemon `unix://` attachment remains the
   next remote-attachment milestone. Each coder instance still needs an instance
   ID and socket for the daemon form.
4. **Full-screen subprocesses:** Editors use system read to temp, yield terminal
   to `$EDITOR`, then write changed content back through the system/client
   boundary. Other full-screen tools use the same managed/yielded terminal model.
5. **Accessibility:** Do not block the feature on accessibility, but keep it on the
   hardening checklist and avoid designs that make it impossible.
6. **Ask shorthand:** `?` is acceptable for now. Natural language that is not
   executable may become an agent request after clear UI indication.
7. **Mode toggle:** Use `Alt+Tab` for explicit shell/ask toggle and prefer
   automatic switching from typed prefixes where possible.
8. **Initial remote transport:** Start with unix socket to the local embedded coder
   daemon. HTTP/channel transports come later.
9. **Path roots:** Yes, complete outside the workspace when allowed. Default roots
   include the main workspace and a secondary user root such as `/home/$USER`; cwd
   is tracked and changed by `cd`.
10. **Tab/session scope:** Each tab is a separate coder session. There is no global
    execution state and no global background process list; every process, task,
    approval, transcript event, and resource query is linked to a client session.
11. **Resource search/ranking:** Reuse agentruntime datasource concepts where
    possible. The UI does not own cross-kind ranking; it renders client/datasource
    results in returned order.
12. **Full-screen apps:** Arbitrary full-screen apps such as `htop`, `top`, pagers,
    and TUIs are unsupported initially and should fail clearly. Editing is allowed
    through controlled download-to-temp, local `$EDITOR`, write-back flow.


## Current Decision Log

- Use Bubble Tea-style TUI at the adapter/app boundary.
- Keep `coder shell` explicit and separate from the default coder REPL for now.
- Execute all commands through managed runtime operations; no raw process bypass.
- Reject ambiguous shell syntax initially unless an explicit via-shell policy is
  configured.
- Treat app facets as visible inventory, not automatic session imports.
- Current implementation uses `cmd/codershell` as a temporary standalone entry
  point while the app-level command shape is refined; `coder shell` also wires to
  the same package.
- The primary shell UI is the REPL view; dashboard is the second top-level view.
- Use a Monokai-inspired default theme.
- Move detailed project/toolchain/status data to the footer; keep the header
  focused on title, active view, tabs, and high-priority state.
- Handle `/exit` and `exit` locally. Send other slash commands through the coder
  client/session abstraction.
- The current default client is `direct`, backed by `agentruntime.ChannelClient`
  and per-tab `agentruntime.Session` handles. Plain input is currently
  conversational input; the managed process command contract is still the next
  implementation step.
- Support future remote coder attachment by keeping TUI dispatch behind a client
  abstraction.
- Integrate `cmdrisk` before command dispatch; risky commands require explicit
  user approval after Enter.
- Tabs are sessions: every tab creates or attaches to a separate coder session,
  and all stateful work is session-scoped through the client.
- Reuse datasource-style resource search for completions/mentions and preserve
  client ordering in the UI.
- Arbitrary full-screen PTY apps fail initially; only controlled editor flows are
  supported.
