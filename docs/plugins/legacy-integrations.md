# Legacy integrations

The packages under `plugins/integrations/*` are legacy first-party integration
implementations. Active product integration surfaces have moved to dex via
`github.com/fluxplane/fluxplane-dex/fluxplaneplugin`.

## Migration policy

- Do not treat `plugins/integrations/*` as blockers for the core
  `fpsystem.System` boundary-retirement migration.
- Prefer dex-backed plugin references for Jira, Confluence, GitLab, Kubernetes,
  Loki, Slack rich operations, OpenAPI, MySQL, OpenAI, AWS, Docker, Git, and web
  integration capabilities.
- Keep core work focused on active native packages, launch/runtime composition,
  and compatibility points that are still intentionally registered.
- New integration capabilities should land in dex, not in this legacy tree.

## Remaining core compatibility points

A small number of core paths still import packages from `plugins/integrations`:

- `apps/launch` uses Slack for the channel-path adapter/auth declarations and
  OpenAPI as a compatibility plugin.
- `apps/launch` datasource indexing uses the Slack channel-path/auth helper.
- `plugins/bundles/coding` still uses legacy `git` and `web` helpers for the
  standard coding bundle until those capabilities are fully supplied by dex or
  native replacements.

These compatibility points should be retired or isolated independently. They are
not evidence that the whole legacy integration tree must be migrated to new
system boundaries.
