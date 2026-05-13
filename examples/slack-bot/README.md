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
projects, Jira issues and projects, and local markdown/text files through
`datasource_search`, `datasource_get`, `datasource_relation`, and
`datasource_batch_get`. Use `datasource_relation` for exact Slack channel
membership; message search only supports observed or inferred participants.

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
