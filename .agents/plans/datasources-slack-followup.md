# Slack Datasource Follow-up Plan

## Context

Slack datasources now support exact channel membership through the generic
`datasource_relation` operation:

- `slack.channel` relation `members -> slack.user`
- backed by `slack.channel.members`
- hydrated through `slack.user.list` with `slack.user.info` fallback
- exposed in `examples/slack-bot` alongside `datasource_batch_get`

This fixes the main correctness gap from the review: channel membership no
longer needs to be inferred from message authors or mentions.

## Follow-ups

- Add a convenience operation or higher-level generic flow for
  search-then-relation, so prompts like "list all users from the lyse channels"
  can resolve channel names and fetch members in one tool call.
- Add a Slack user directory cache so membership hydration does not repeatedly
  scan `slack.user.list` for large workspaces.
- Add message-search facets for inferred analysis:
  - distinct authors
  - distinct mentioned users
  - channel-scoped counts
- Make datasource pagination limits configurable per datasource or connector
  action instead of relying only on the current bounded defaults.
- Add an integration-style Slack datasource test with mocked connector pages for
  multiple channels and multi-page channel membership.

## Acceptance Criteria

- Exact membership remains clearly labeled as exact and complete or partial.
- Inferred participant analysis uses a separate operation/result shape and is
  never mixed into exact channel membership.
- The Slack bot example continues to expose the exact membership path by
  default.
- `task verify` passes after each follow-up batch.
