# Legacy integrations

The packages under `plugins/integrations/*` are legacy first-party integration
implementations. Active product integration surfaces should be registered by
product-owned Core integration code using `github.com/fluxplane/fluxplane-plugin`
and installable plugin modules under
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

A small number of core paths still import packages from `plugins/integrations`:

- `apps/launch` uses Slack for the channel-path adapter/auth declarations and
  OpenAPI as a compatibility plugin.
- `apps/launch` datasource indexing uses the Slack channel-path/auth helper.
- `plugins/bundles/coding` still uses legacy `git` and `web` helpers for the
  standard coding bundle until those capabilities are fully supplied by
  `fluxplane-plugins` or native replacements.

These compatibility points should be retired or isolated independently. They are
not evidence that the whole legacy integration tree must be migrated to new
system boundaries.
