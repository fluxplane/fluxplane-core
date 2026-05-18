# Native Slack Rewrite

## Summary

Migrate `plugins/slackplugin` away from connector-backed auth and execution.
Slack becomes a native plugin with read-only Slack data exposed through
datasources, while side-effecting Slack actions remain regular operations. This
first implementation round focuses on opaque `bot_token` auth, keeps Slack
channel behavior intact, and prepares OAuth2 declarations without live OAuth2
testing.

## Key Changes

- Split `plugins/slackplugin` into `auth.go`, `channel.go`, `datasource.go`,
  and `plugin.go`.
- Add native Slack auth:
  - stored `bot_token` at `plugin/slack/<instance>/bot_token`;
  - stored `app_token` at `plugin/slack/<instance>/app_token`, required when
    a Slack daemon channel is configured because Socket Mode needs it;
  - optional stored `user_token` for future user-scoped Slack access;
  - env fallback through configured token env names;
  - OAuth2 auth method declaration only, not live-tested in this round.
- Remove Slack connector ties:
  - drop Slack `ConnectorProviderContributor`;
  - remove `NewWithConnectors`;
  - remove connector-backed datasource/action plumbing;
  - stop reading old connector credentials for Slack.
- Keep channel semantics:
  - preserve Socket Mode event handling, thread routing, `slack_context`,
    caller/trust derivation, audience trust behavior, identity resolver
    behavior, streaming/status updates, and `channel_send`;
  - keep `channel_send` as a normal side-effecting operation with
    `EffectWriteExternal`.
- Expose Slack read access through datasource:
  - `slack.user` from `users.list` and `users.info`;
  - `slack.channel` from `conversations.list` and `conversations.info`;
  - `slack.message` from `search.messages`;
  - `slack.thread_message` or equivalent thread-read entity from
    `conversations.replies`;
  - `slack.channel members -> slack.user` relation from
    `conversations.members`.
- Keep context providers working:
  - do not replace `identity.current`, `datasource.catalog`, or
    `datasource.detected`;
  - ensure Slack inbound context still carries conversation metadata only, while
    identity resolution remains handled through the identity resolver.

## Config And Launch

- Add generic daemon channel `instance` parsing so Slack channels select a
  plugin instance without `connector`.
- Update launch wiring to resolve Slack credentials from native plugin auth
  store, not `adapters/connectauth`.
- Update `examples/slack-bot/agentsdk.app.yaml` and the local Slack bot
  deployment config:
  - remove `connectors.slack-bot`;
  - configure `plugins: [{ kind: slack, instance: slack-bot, config: { auth:
    { method: bot_token }}}]`;
  - change Slack datasource to `kind: slack` with
    `config.instance: slack-bot`;
  - change Slack daemon channel from `connector: slack-bot` to
    `instance: slack-bot`.
- Extend shared `adapters/distribution/authconnect` stored-auth support
  generically so `agentsdk connect slack --instance slack-bot --auth bot_token
  --field bot_token=... --field app_token=...` works without adding
  Slack-specific code to `apps/agentsdk`.
- Keep datasource-only setup valid with `bot_token` alone; require both
  `bot_token` and `app_token` for configured Slack daemon channels.

## Tests

- `plugins/slackplugin`:
  - plugin is not a connector provider;
  - auth methods include stored bot token and OAuth2 declaration;
  - stored/env token resolution works;
  - native datasource list/get/search/thread/relation calls Slack mock APIs;
  - datasource open/search works with `bot_token` only;
  - channel startup fails clearly without `app_token` and succeeds only when
    both `bot_token` and `app_token` are available;
  - channel startup uses `auth.test`;
  - `channel_send`, Slack caller/trust, and `slack_context` behavior stay
    unchanged.
- Launch/config:
  - daemon channel `instance` parses from YAML;
  - serve and datasource indexing no longer load Slack connector credentials;
  - Slack datasource opens without `connector`;
  - `apps/agentsdk` has no Slack-specific auth implementation.
- Verification:
  - `go test -count=1 ./plugins/slackplugin ./adapters/distribution/authconnect ./adapters/appconfig ./apps/launch`
  - `task verify`

## Assumptions

- The shared bot token from the chat is only for manual validation and must not
  be committed, stored in YAML, or used in tests.
- The Slack channel implementation remains Socket Mode only in this migration
  batch, so channel operation needs both `bot_token` and `app_token`;
  datasource-only Slack read access can work with `bot_token` when scopes
  permit.
- OAuth2 refresh-on-fetch is a follow-up batch, not part of this first Slack
  rewrite.
- Existing uncommitted GitLab/datasource work remains separate from the Slack
  migration.
