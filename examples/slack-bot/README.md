# Slack Bot Example

Native rewrite Slack daemon example.

```bash
coder auth connect --plugin slack --instance slack-bot --method token --field bot_token=xoxb-... --field app_token=xapp-...
coder auth connect --plugin gitlab --instance gitlab
coder auth connect --plugin jira --instance jira
coder auth connect --plugin confluence --instance confluence
fluxplane serve examples/slack-bot
```

In another terminal, connect to the local direct channel declared by the app:

```bash
coder remote --app examples/slack-bot
coder remote --app examples/slack-bot --input "hello from local"
```

The native `slack/slack-bot` plugin instance supplies the bot token and Socket
Mode app token. Slack datasources can read with the bot token alone when scopes
permit; the daemon channel requires the `xapp-...` app token for Socket Mode.
The Jira and Confluence datasources use native Atlassian auth. For
service-account deployments, configure the Atlassian plugin with
`auth.method: token` and set `auth.token_env` to the runtime bearer token
environment variable.

The configured datasources expose Slack users, channels, messages, thread
messages, GitLab projects, Jira issues and projects, Confluence pages and
spaces, local markdown/text files, and public web search results through
`datasource_search`; record retrieval uses
`datasource_get` or `datasource_batch_get` where the entity supports it. Use
`datasource_relation` for exact Slack channel membership and message thread
reads; message search only supports observed or inferred participants.
Web search is exposed only through the canonical `web_search` datasource and
its `web.search_result` entity; the agent does not get the direct `web_request`
tool in this example.

Slack callers are resolved through Slack `users.info` when native Slack
credentials are available. The Slack profile email becomes the canonical
`core/user` ID. Add `identity` entries in `fluxplane.yaml` only for
overlays such as special groups, trust, or pinned provider-ID mappings:

```yaml
identity:
  users:
    - id: timo@company.org
      identities:
        - provider: slack
          provider_id: U0123456789
        - provider: gitlab/main
          provider_id: tfriedl
      groups: [admins]
  groups:
    - id: admins
      trust: operator
  rules:
    - match:
        provider: slack
        resolution: resolved
      groups: [slack-bot-users]
```

If Slack cannot return an email and no explicit identity mapping exists, the bot
sees an unresolved `slack_user:<id>` identity and does not receive raw Slack
claims in context. This example assigns resolved Slack users to
`slack-bot-users`, unresolved Slack identities to `anonymous`, and the configured
admin Slack ID to `slack-bot-admin` only after Slack resolution succeeds. Use
`/whoami` or `/context --fresh --key identity.current` to inspect the identity
visible to the runtime and model. When GitLab identity can be resolved from app
identity config or GitLab profile lookup, `identity.current` includes it
alongside the Slack entry identity.

Slack context distinguishes the sender from the audience. The sender's
canonical identity, groups, and trust come from `identity.current`; the Slack
message context only carries conversation metadata such as channel/thread IDs,
sharing mode, and audience trust for shared conversations. One-to-one DMs omit
audience trust. A privileged sender in a shared channel does not make the
channel audience privileged.

Slack app requirements:

- Socket Mode enabled.
- App Home > Messages Tab enabled, so users can DM the app from Slack.
- Bot Token Scopes include `app_mentions:read`, `chat:write`, `im:history`,
  `im:read`, `channels:history`, `channels:read`, `groups:history`,
  `groups:read`, `mpim:history`, `mpim:read`, `search:read`, `users:read`,
  and `users:read.email`.
- Event Subscriptions enabled with bot events: `app_mention`, `message.im`,
  `message.channels`, `message.groups`, and `message.mpim`.

Native datasource scopes:

- Slack message search requires `search:read`.
- Slack channel discovery requires `channels:read`, `groups:read`, `im:read`,
  and `mpim:read`.
- Slack channel membership requires `channels:read` and `groups:read`.
- Slack thread reads require the matching history scope for the conversation
  type, such as `channels:history`, `groups:history`, `im:history`, or
  `mpim:history`.
- Jira issue and project discovery requires `read:jira-work`.
- Confluence page and space discovery requires `read:page:confluence` and
  `read:space:confluence`.

If serve logs `slack channel connected` but never logs
`slack channel event received` after a DM or mention, Slack is not delivering
events to this app. Check the Event Subscriptions list above and reinstall the
app after changing scopes or events.

If serve only logs `event=app_home_opened`, Slack is delivering App Home events
but not message events. Enable the Messages Tab and add the bot event
subscriptions above, then reinstall the app.
