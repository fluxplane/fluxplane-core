# Claude Middleman

`claude-middleman` is a local Anthropic-compatible capture proxy for comparing
Claude Code CLI request shapes with Fluxplane's `claudecode` provider.

It records redacted request headers and JSON bodies under
`.tmp/claude-middleman/<run-id>/` and can either return a stub response or
forward to `https://api.anthropic.com`.

## Safe Default Capture

This starts Claude in minimal `--print --bare` mode, points it at the local
proxy, uses a dummy API key, and stubs the response:

```sh
go run ./cmd/claude-middleman \
  --run-id latest-bare-stub \
  --prompt "Reply with exactly ok." \
  --model sonnet \
  --output-format stream-json
```

Use this for quick regression checks of the SDK-style request path. It does not
exercise subscription OAuth because `--bare` intentionally disables OAuth and
keychain reads.

## Interactive Subscription Capture

To compare against real interactive Claude Code, run the proxy without launching
Claude:

```sh
go run ./cmd/claude-middleman \
  --run-claude=false \
  --run-id latest-interactive-oauth-stub
```

Copy the printed local base URL, then start Claude in another terminal with API
key variables explicitly unset:

```sh
env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
  ANTHROPIC_BASE_URL=http://127.0.0.1:<port> \
  CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
  CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 \
  CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1 \
  CLAUDE_CODE_DISABLE_BACKGROUND_PLUGIN_REFRESH=1 \
  claude --model sonnet \
    --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
    --disable-slash-commands --no-chrome --tools= \
    "Reply with exactly ok."
```

This should capture an OAuth subscription request with `Authorization: Bearer`
and `User-Agent: claude-cli/... (external, cli)`. If Claude asks whether to use
`ANTHROPIC_API_KEY`, stop and rerun with the environment variable unset; the
API-key path is the wrong comparison for the subscription provider.

Stop the interactive Claude session after the first answer. Stop the proxy with
Ctrl-C so it writes `summary.json`.

## Forwarding

Stub mode is preferred for shape comparisons because it avoids token spend and
network variability. Use forwarding only when the provider response behavior
matters:

```sh
go run ./cmd/claude-middleman \
  --forward \
  --run-id latest-forward \
  --prompt "Reply with exactly ok." \
  --model sonnet
```

Captured files are redacted by default, but treat forwarded captures as
sensitive because payloads can contain workspace context.

## What To Compare

For the answer-turn request, inspect the latest `*_v1_messages.json` file and
compare:

- auth header: subscription path uses `Authorization`, not `X-Api-Key`;
- `User-Agent`: interactive CLI uses `(external, cli)`, while `--bare --print`
  uses `(external, sdk-cli)`;
- `Anthropic-Beta`: current interactive captures included
  `claude-code-20250219`, `oauth-2025-04-20`,
  `interleaved-thinking-2025-05-14`, `redact-thinking-2026-02-12`,
  `context-management-2025-06-27`, `prompt-caching-scope-2026-01-05`,
  `effort-2025-11-24`, and `extended-cache-ttl-2025-04-11`;
- `X-Stainless-Package-Version` and Claude Code version in the billing system
  block;
- short system identity: interactive says
  `You are Claude Code, Anthropic's official CLI for Claude.`;
- `thinking`, `output_config.effort`, and `context_management` fields.

Interactive Claude may send an extra title-generation request before the real
answer turn. Do not mirror that in the provider unless Fluxplane needs Claude
Code UI session metadata.

## Provider Notes

The provider under `adapters/llm/claudecode` should mimic the interactive
subscription answer request where that affects provider compatibility. It
should not import Claude Code's long default system prompt wholesale; Fluxplane
owns its own agent/system context. Prompt caching placement is handled by the
generic Anthropic Messages path and conversation/runtime layers, not this
capture tool.
