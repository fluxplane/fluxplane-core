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
- configured session profiles from resource manifests;
- run handles with results and semantic events;
- event-backed thread state;
- direct in-process channels;
- HTTP/SSE channel transport;
- local `agentruntime.json` resource manifests;
- first-party `echo` and `text` plugins;
- provider-neutral LLM-agent runtime skeleton with a fake/test model path;
- safe-by-default tool projection for LLM-visible commands and operations;
- an architecture import report for maintainers.

Terminal UX, Slack, model provider adapters, trigger execution, and
side-effecting operation plugins are still under active design.

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
go run ./apps/devclient -app ./path/to/app -session coder echo hello
go run ./apps/devclient -app ./path/to/app text/upper hello
go run ./apps/devclient serve -addr 127.0.0.1:8080
go run ./apps/devclient -url http://127.0.0.1:8080 input hello
```

Use `-debug` to print submissions, events, and results.

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
