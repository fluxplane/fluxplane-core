# Fluxplane Agent Runtime

Fluxplane Agent Runtime is a Go runtime and SDK for building agent systems
around durable session state, resource-declared capabilities, plugin
contributions, and transport-neutral channel clients.

This repository is the rewrite target for the current `agentsdk` ideas. It is
pre-1.0 and intentionally not source-compatible with the old SDK.

## Current Status

The current executable slice supports:

- in-process library use through `agentruntime.Service`;
- session handles with command and input submissions;
- IO-free spec builders in `sdk` for app, agent, operation, command, and
  session contributions;
- configured session profiles from resource manifests;
- run handles with results and semantic events;
- event-backed thread state;
- direct in-process channels;
- HTTP/SSE channel transport;
- local `agentruntime.json` resource manifests;
- `cmd/agentsdk coder` as the first product-style CLI over an embedded coder
  app declaration;
- first-party `echo` and `text` plugins;
- generic usage events and LLM provider/model catalog specs;
- provider-neutral LLM-agent runtime skeleton with a fake/test model path;
- safe-by-default tool projection for LLM-visible commands and operations;
- configured-session instantiation of manifest-declared LLM agents when a host
  provides a model implementation;
- bounded LLM-agent turns: `max_steps` limits the inner tool loop, while
  `max_continuations` caps stop-condition-driven follow-up turns;
- provider-neutral LLM adapter helpers for messages, tools, streaming,
  redaction, and fake provider tests;
- OpenAI Responses API adapter using `openai-go/v3`, including streaming
  content/reasoning/tool-call events, token usage events, max prompt-caching
  defaults, and automatic best-effort stored-response continuation;
- Codex provider wiring over the Codex Responses backend using local Codex
  OAuth credentials;
- markdown terminal rendering for streamed coder output and debug events;
- `delegate` and `plan` operations for policy-bounded sub-agent work, with
  terminal and Slack progress rendering;
- an architecture import report for maintainers.

Claude Code provider integration, trigger execution, and deeper context provider
parity are still under active design.

## Library Use

```go
svc, err := agentruntime.New(agentruntime.Config{Agent: agent})
if err != nil {
    return err
}

session, err := svc.Open(ctx, agentruntime.OpenRequest{Conversation: conversation})
if err != nil {
    return err
}

run, err := session.SendInput(ctx, agentruntime.Input{Text: "hello"})
if err != nil {
    return err
}

result, err := run.Wait(ctx)
if err != nil {
    return err
}
```

Configured profiles can be opened through the same API:

```go
session, err := svc.Open(ctx, agentruntime.OpenRequest{
    Session: agentruntime.SessionRef{Name: "coder"},
})
```

Apps can be declared with the IO-free builder package and then composed with
runtime implementations supplied by the host:

```go
lookupSpec := sdk.BuildOperation("lookup").
    WithDescription("Look up one value.").
    WithRisk(operation.RiskLow).
    Build()

bundle := sdk.NewApp("demo").
    WithModel("openai", "gpt-5.5", "coding").
    WithDefaultAgent(
        sdk.BuildAgent("main").
            AsLLMAgent("gpt-5.5").
            WithSystem("Help with coding tasks.").
            WithOperation("lookup").
            Build(),
    ).
    WithOperation(lookupSpec).
    WithCommandForOperation("lookup", lookupSpec).
    Build()
```

The same logical client contract is used for direct in-process execution and
remote HTTP/SSE execution:

```text
ChannelClient.Open/Resume/ListSessions
  -> SessionHandle
  -> Submit/SendInput/SendCommand
  -> RunHandle
  -> Events/Done/Wait/Result
```

## Dev Client

`apps/devclient` is a small diagnostic client for exercising the current
runtime:

```bash
go run ./apps/devclient input hello
go run ./apps/devclient -openai input "Reply with one sentence."
go run ./apps/devclient -debug -openai -synthetic-tool input "Use synthetic_lookup with key alpha, then answer with the value."
go run ./apps/devclient -app ./path/to/app -session coder echo hello
go run ./apps/devclient -openai -app ./path/to/app -session coder input "Inspect the app."
go run ./apps/devclient -app ./path/to/app text/upper hello
go run ./apps/devclient serve -addr 127.0.0.1:8080
go run ./apps/devclient -url http://127.0.0.1:8080 input hello
```

Use `-debug` to print submissions, model stream events, runtime events, and
results. `-openai` uses `OPENAI_API_KEY` through the OpenAI SDK and defaults
the model to `OPENAI_MODEL`, falling back to `gpt-4.1-mini` when unset.

## Coder CLI

`cmd/agentsdk` is the first product-style command. It runs the embedded
`apps/coder` declaration and wires the host-provided shell and HTTP operations:

```bash
go run ./cmd/agentsdk coder "Inspect this repository and suggest the next step."
go run ./cmd/agentsdk coder --debug "Run go test for the focused package."
go run ./cmd/agentsdk coder --usage "Reply with one sentence."
go run ./cmd/agentsdk coder --model codex/gpt-5.5 repl
go run ./cmd/agentsdk coder --provider codex --model gpt-5.5 "Summarize the current diff."
go run ./cmd/agentsdk coder --model openrouter/anthropic/claude-sonnet-4.6 "Reply with one sentence."
go run ./cmd/agentsdk coder --model anthropic/claude-haiku-4-5-20251001 "Reply with one sentence."
go run ./cmd/agentsdk coder --model minimax/MiniMax-M2.7 "Reply with one sentence."
go run ./cmd/agentsdk coder repl
```

The command defaults to `openai/gpt-5.5`. OpenAI uses `OPENAI_API_KEY` through
the OpenAI SDK. Codex uses the local Codex OAuth file at `~/.codex/auth.json`
or `CODEX_AUTH_PATH`. OpenRouter uses `OPENROUTER_API_KEY` and exact modeldb
model IDs, for example `openrouter/anthropic/claude-sonnet-4.6`. Anthropic
uses `ANTHROPIC_API_KEY` against the Messages API. MiniMax uses
`MINIMAX_API_KEY` against its Anthropic-compatible Messages endpoint. Normal
output streams assistant markdown and reasoning summaries in the terminal.
`--usage` prints grouped session totals with human-readable token, network, and
estimated cost lines after each prompt. `--debug` renders runtime events as
highlighted JSON markdown fences.

OpenAI-compatible providers use automatic best-effort Responses settings:
WebSocket-preferred transport intent, max prompt caching, reasoning encrypted
content for replay, and provider continuation when available. These are runtime
provider settings, not CLI-specific flags. OpenRouter runs through the same
Responses adapter with stateless replay continuation and provider-neutral cache
request fields by default.

## Resource Apps

Local resource apps currently use an `agentruntime.json` manifest. The current
shape declares commands over operation implementations supplied by the host
application or plugins, and named session profiles that clients can open.

```json
{
  "sessions": [
    {
      "name": "coder",
      "channel": {"name": "local"},
      "conversation": {"id": "devclient"},
      "delegation": {
        "allowed_profiles": [{"name": "worker"}],
        "max_parallel": 2
      }
    }
  ],
  "commands": [
    {
      "path": ["echo"],
      "operation": "echo",
      "policy": {
        "allowed_callers": ["user"],
        "required_trust": "verified"
      }
    }
  ]
}
```

Plugin-backed manifests can be small:

```json
{
  "plugins": [
    {"name": "echo"},
    {
      "name": "text",
      "config": {
        "commands": ["upper", "trim"]
      }
    }
  ]
}
```

Resource contributions receive canonical resource identities, so duplicate
short names can coexist across origins and namespaces. Commands execute only
when their target resolves to a bound operation implementation.

## Verification

The rewrite uses a local Taskfile:

```bash
task verify
```

This runs formatting checks, module consistency checks, `go vet`,
`golangci-lint`, tests, and the architecture import-direction check.

The architecture report can be inspected directly:

```bash
go run ./apps/archreport
go run ./apps/archreport -format dot
go run ./apps/archreport -format mermaid
```

## More

- [Migration plan](docs/migration-from-agent-sdk.md)
- [Changelog](CHANGELOG.md)
