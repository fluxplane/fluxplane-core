# Fluxplane CLI

`fluxplane` is the generic CLI for Fluxplane apps. It owns the app lifecycle
that previously lived under `coder app ...`. The `coder` coding-agent product
ships separately from [`github.com/fluxplane/coder`](https://github.com/fluxplane/coder)
and is not installed by this CLI.

## Install

```bash
go install github.com/fluxplane/engine/cmd/fluxplane@latest
```

`fluxplane` reads `fluxplane.yaml` as the app manifest. The legacy
`agentsdk.app.*` names are not loaded as compatibility aliases.

## Commands at a glance

| Command | Purpose |
|---|---|
| `fluxplane init <dir>` | Create a minimal app manifest in `<dir>`. |
| `fluxplane run <dir>` | Run the app once, locally, and exit. |
| `fluxplane serve <dir>` | Start the daemon with configured listeners and channels. |
| `fluxplane build <dir>` | Build distribution artifacts (Dockerfile, image, compose). |
| `fluxplane deploy <dir>` | Deploy a built distribution to a target. |
| `fluxplane undeploy <dir>` | Remove a deployed distribution. |
| `fluxplane config show <dir>` | Print the resolved app configuration. |
| `fluxplane config edit <dir>` | Open the app manifest in `$EDITOR`. |
| `fluxplane describe <dir>` | Describe distribution surfaces and resources. |
| `fluxplane discover <dir>` | Show resolved resource trees and plugin contributions. |
| `fluxplane auth status` | Show plugin auth readiness for the selected app. |
| `fluxplane datasource index build <dir>` | Index configured datasources. |

Run `fluxplane <command> --help` for the full flag list.

## Common flows

### Create and run a new app

```bash
fluxplane init ./my-app
fluxplane run ./my-app --input "Hello"
```

`run` opens an ephemeral local distribution from the directory, attaches a
local runtime, and submits one input through the same session path used by
remote channels.

### Serve as a daemon

```bash
fluxplane serve ./my-app
```

Reads the `daemon` block from `fluxplane.yaml`, starts the configured listeners
(HTTP, Unix socket, or Slack), and exposes channels for direct, HTTP/SSE, or
plugin-backed clients.

### Build and deploy

```bash
fluxplane build ./my-app --target dockerfile,docker-compose,docker-image \
  --image my-app:local
fluxplane deploy ./my-app --target docker-compose --image my-app:local
fluxplane undeploy ./my-app --target docker-compose
```

Build targets include `dockerfile`, `docker-image`, `docker-compose`, and
`kubernetes`. Generated manifests are written under `.deploy/` and added to
`.gitignore`. See [Configuration → Distribution And Builds](configuration.md#distribution-and-builds)
for Kubernetes registry modes and env-file handling.

### Inspect configuration

```bash
fluxplane config show ./my-app
fluxplane describe ./my-app
fluxplane discover ./my-app
```

`config show` prints the merged app document. `describe` reports the
distribution metadata and supported surfaces. `discover` walks the resource
roots (project `.agents`, `$HOME/.agents`, plugin bundles) and shows what the
runtime would load.

### Manage plugin auth

```bash
fluxplane auth status
```

Lists each plugin instance, the configured auth method, and whether credentials
are present. Credentials themselves are managed by product-specific commands
such as `coder auth connect` for the coder bundle; `fluxplane` scopes auth
status to the selected app manifest.

### Index datasources

```bash
fluxplane datasource index build ./my-app
```

Builds the structured field index for configured datasources. Semantic
embeddings are queued by build and run separately; see
[Datasource embeddings](embeddings.md).

## Scope

`fluxplane` is intentionally narrow: it owns the app manifest lifecycle.
Product-specific bundles (such as `coder` and its `.coder.yaml`) ship from
their own repositories and are not managed by `fluxplane`.
