# Slack Bot Example

Native rewrite Slack daemon example.

```bash
agentsdk connect slack --instance slack-bot
agentsdk connect gitlab --instance gitlab
agentsdk connect jira --instance jira
agentsdk serve examples/slack-bot
```

In another terminal, connect to the local direct channel declared by the app:

```bash
agentsdk remote --app examples/slack-bot
agentsdk remote --app examples/slack-bot --input "hello from local"
```

The `slack-bot` connector instance supplies the bot token and Socket Mode app token.
The Slack channel itself uses native Slack APIs rather than connector operation
execution.

The configured datasources expose Slack users, channels, and messages, GitLab
projects, Jira issues and projects, local markdown/text files, and public web
search results through `datasource_search`; record retrieval uses
`datasource_get` or `datasource_batch_get` where the entity supports it. Use
`datasource_relation` for exact Slack channel membership; message search only
supports observed or inferred participants.
Web search is exposed only through the canonical `web_search` datasource and
its `web.search_result` entity; the agent does not get the direct `web_request`
tool in this example.

Slack callers are resolved through Slack `users.info` when connector
credentials are available. The Slack profile email becomes the canonical
`core/user` ID. Add `identity` entries in `agentsdk.app.yaml` only for
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
  `groups:read`, `mpim:history`, `mpim:read`, `search:read`, and `users:read`.
- Event Subscriptions enabled with bot events: `app_mention`, `message.im`,
  `message.channels`, `message.groups`, and `message.mpim`.

Connector datasource scopes:

- Slack message search requires `search:read`.
- Slack channel discovery requires `channels:read`, `groups:read`, `im:read`,
  and `mpim:read`.
- Slack channel membership requires `channels:read` and `groups:read`.
- Jira project discovery requires `read:jira-work`.

If serve logs `slack channel connected` but never logs
`slack channel event received` after a DM or mention, Slack is not delivering
events to this app. Check the Event Subscriptions list above and reinstall the
app after changing scopes or events.

If serve only logs `event=app_home_opened`, Slack is delivering App Home events
but not message events. Enable the Messages Tab and add the bot event
subscriptions above, then reinstall the app.
