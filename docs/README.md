# Fluxplane Engine Documentation

This directory holds the reference documentation for `github.com/fluxplane/engine`.
The repository root [`README.md`](../README.md) is the entry point. This index
groups the rest of the docs by audience.

## For users building or running an app

Start here if you want to author a Fluxplane app, run the `fluxplane` CLI, or
configure a daemon deployment.

- [Fluxplane CLI](fluxplane.md) — commands, flags, and what each one does.
- [Configuration](configuration.md) — `fluxplane.yaml` manifests and `.agents`
  resource trees: agents, sessions, commands, workflows, datasources, plugins,
  identity, runtime workspace, models, distribution, and daemon channels.
- [Plugin auth: Atlassian](plugins/atlassian.md) — Jira and Confluence auth
  shapes (scoped token, basic API token, OAuth2).
- [Plugin: GitLab](plugins/gitlab.md) — GitLab datasource entities and write
  operations.
- [Datasource embeddings](embeddings.md) — semantic search providers, chunking,
  store layout, and indexing CLI.

## For users of bundled tools

Reference material for agents and developers using the standard operation set.

- [File edit tool](tools/file-edit.md) — multi-edit shape, original-file
  coordinates, and good agent practice for `file_edit`.
- [Process and shell tools](tools/process.md) — direct process vs. shell
  operations and the managed process boundary.

## For runtime contributors

If you are working inside this module, start with the operative rules in
[`AGENTS.md`](../AGENTS.md), then use:

- [Architecture](architecture.md) — layer model, package responsibilities, and
  common flows.
- [Concepts](concepts.md) — vocabulary for workspace, project, request, task,
  command, workflow, operation, and execution.
- [Agent loop](agent-loop.md) — session execution loop, continuation, and
  transcript flow.
- [Security model](security.md) — operation safety envelope, system boundary,
  and what is and is not sandboxed.
- [Conversation continuity](conversation-continuity.md) — provider transcript
  invariants for replay, native continuation, and compaction.
- [Verification](verification.md) — `task verify`, Git hooks, codegate review,
  and the security scan.
- [Repository split](repository-split.md) — Engine/Coder module boundaries and
  local development.

## Design notes and history

These documents are working design notes rather than user guides. Expect rough
edges and open questions.

- [Migration from `agentsdk`](migration-from-agent-sdk.md) — historical
  rationale, concept mapping, package disposition, and parity backlog.
- [Constant self-evolvement](constant-self-evolvement.md) — design sketch for
  evaluation, improvement, and promotion loops.
- [Observations and reactions](observations-and-reactions.md) — design sketch
  for ambient observation and reaction rules.
- [Evaluation](evaluation.md) — local smoke-test flow for the public HTTP/SSE
  channel.

## See also

- Root [`README.md`](../README.md) — project overview and quickstart.
- Root [`AGENTS.md`](../AGENTS.md) — operative rules for agents and developers.
- Root [`CHANGELOG.md`](../CHANGELOG.md) — user-visible changes per release.
- The coding-agent product `coder` lives in
  [`github.com/fluxplane/coder`](https://github.com/fluxplane/coder).
