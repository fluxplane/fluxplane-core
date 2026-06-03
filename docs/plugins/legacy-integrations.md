# Legacy integrations

The remaining packages under `plugins/integrations/*` are legacy first-party
integration implementations. Active product integration surfaces should be
registered by product-owned Core integration code using
`github.com/fluxplane/fluxplane-plugin` and installable plugin modules under
`github.com/fluxplane/fluxplane-plugins/...`. They should not depend on Dex.

## Migration policy

- Do not treat `plugins/integrations/*` as blockers for the core
  `fpsystem.System` boundary-retirement migration.
- Prefer `fluxplane-plugin` plus `fluxplane-plugins` references for Jira,
  Confluence, GitLab, Kubernetes, Loki, Slack rich operations, OpenAPI, MySQL,
  OpenAI, AWS, Docker, Git, and web integration capabilities.
- Keep core work focused on active native packages, launch/runtime composition,
  and compatibility points that are still intentionally registered.
- New integration capabilities should land in `fluxplane-plugins`, not in this
  legacy tree or Dex.

## Remaining core compatibility points

Only AWS environment observation remains in `plugins/integrations` at this
checkpoint. The Slack channel transport moved to `adapters/channels/slack`;
Slack operation/datasource surfaces belong in `fluxplane-plugins/slack`.

These compatibility points should be retired or isolated independently. They are
not evidence that the whole legacy integration tree must be migrated to new
system boundaries.
