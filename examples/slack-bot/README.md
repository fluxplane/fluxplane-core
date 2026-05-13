# Slack Bot Example

Native rewrite Slack daemon example.

```bash
agentsdk connect slack --instance slack-bot
agentsdk serve examples/slack-bot
```

The `slack-bot` connector instance supplies the bot token and Socket Mode app token.
The Slack channel itself uses native Slack APIs rather than connector operation
execution.

Slack app requirements:

- Socket Mode enabled.
- App Home > Messages Tab enabled, so users can DM the app from Slack.
- Bot Token Scopes include `app_mentions:read`, `chat:write`, `im:history`,
  `im:read`, `channels:history`, `channels:read`, `groups:history`,
  `groups:read`, `mpim:history`, and `mpim:read`.
- Event Subscriptions enabled with bot events: `app_mention`, `message.im`,
  `message.channels`, `message.groups`, and `message.mpim`.

If serve logs `slack channel connected` but never logs
`slack channel event received` after a DM or mention, Slack is not delivering
events to this app. Check the Event Subscriptions list above and reinstall the
app after changing scopes or events.

If serve only logs `event=app_home_opened`, Slack is delivering App Home events
but not message events. Enable the Messages Tab and add the bot event
subscriptions above, then reinstall the app.
