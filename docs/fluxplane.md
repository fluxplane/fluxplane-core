# Fluxplane CLI

`fluxplane` is the generic CLI for Fluxplane apps. It owns the app lifecycle
that previously lived under `coder app ...`. The `coder` coding-agent product
ships separately from [`github.com/fluxplane/coder`](https://github.com/fluxplane/coder)
and is not installed by this CLI.

## Install

```bash
go install github.com/fluxplane/fluxplane-core/cmd/fluxplane@latest
```

`fluxplane` reads `fluxplane.yaml` as the app manifest. The legacy
`agentsdk.app.*` names are not loaded as compatibility aliases.

## Commands at a glance

| Command | Purpose |
|---|---|
| `fluxplane init <dir>` | Create a minimal app manifest in `<dir>`. |
| `fluxplane run <dir>` | Run the app once, locally, and exit. |
| `fluxplane serve <dir>` | Start the daemon with configured listeners and channels. |
| `fluxplane build <dir>` | Build named distribution artifacts such as binaries, docs, images, Compose files, manifests, or Helm charts. |
| `fluxplane deploy <dir>` | Deploy a named target, rebuilding referenced artifacts according to policy. |
| `fluxplane undeploy <dir>` | Remove a deployed distribution. |
| `fluxplane targets <dir>` | List available build and deploy targets. |
| `fluxplane config show <dir>` | Print the resolved app configuration. |
| `fluxplane config edit <dir>` | Open the app manifest in `$EDITOR`. |
| `fluxplane describe <dir>` | Describe distribution surfaces and resources. |
| `fluxplane discover <dir>` | Show resolved resource trees and plugin contributions. |
| `fluxplane healthcheck` | Check a served app health endpoint (defaults to `http://127.0.0.1:18080/control/status`). |
| `fluxplane op <dir> ...` | Run configured operations from an app manifest. |
| `fluxplane auth status` | Show plugin auth readiness for the selected app. |
| `fluxplane datasource index build\|embed\|status\|clear <dir>` | Manage configured datasource indexes: queue corpus, embed semantic chunks, show status, or remove entries. See [Datasource embeddings](embeddings.md). |

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
fluxplane targets ./my-app
fluxplane build ./my-app --target capabilities
fluxplane build ./my-app --target image,compose
fluxplane deploy ./my-app --target local
fluxplane undeploy ./my-app --target local
```

`fluxplane build` without `--target` builds all configured build targets.
`--out DIR` changes the root directory for generated artifact outputs, while
the build index remains under the app's `.deploy/` directory. Build targets are
named under `distribution.build.targets`; deploy targets are
named under `distribution.deploy.targets` and reference build targets with
`build: [...]`. Build kinds include `binary`, `dockerfile`, `docker-image`,
`docker-compose`, `kubernetes-manifest`, `helm-chart`, `runtime-stack`, and
`documentation`.
Deploy kinds include `docker-compose`, `kubectl`, and `helm`. Image names,
platforms, push behavior, container runtime settings, and Helm/Kubernetes
settings live on the named targets in `fluxplane.yaml`. Kubernetes and Helm
artifacts reference external Secrets instead of embedding env-file contents.
App Kubernetes and Helm artifacts reference runtime backend DSNs through an
external runtime Secret; temporary MySQL/NATS resources are generated only by
explicit `runtime-stack` targets.
Docker Compose artifacts use environment placeholders for local runtime
backend credentials, and `fluxplane deploy` creates `.deploy/docker-compose.runtime.env`
with random local credentials on first use.
Generated Helm charts omit `Namespace` resources so namespace ownership can
stay with platform bootstrap, Argo CD, or the Helm install command.
Use `fluxplane targets`
to inspect available build and deploy targets. `fluxplane deploy` without
`--target` uses the declared deploy target named `local` and fails if that
target does not exist. See
[Configuration → Distribution And Builds](configuration.md#distribution-and-builds)
for target examples and env-file handling.

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
fluxplane datasource index embed ./my-app
fluxplane datasource index status ./my-app
fluxplane datasource index clear ./my-app
```

`build` writes the structured field index and queues semantic corpus.
`embed` runs the embedding worker over queued chunks. `status` reports
index contents per datasource and entity. `clear` removes indexed records.
See [Datasource embeddings](embeddings.md) for providers, chunking, store
layout, and full flag reference.

## Scope

`fluxplane` is intentionally narrow: it owns the app manifest lifecycle.
Product-specific bundles (such as `coder` and its `.coder.yaml`) ship from
their own repositories and are not managed by `fluxplane`.
