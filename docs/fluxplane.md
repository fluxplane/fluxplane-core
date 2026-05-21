# Fluxplane CLI

`fluxplane` is the generic CLI for authored Fluxplane app manifests. It owns the
app lifecycle that previously lived under `coder app ...`; `coder` remains the
bundled coding-agent product.

Install the generic app CLI from the engine module:

```bash
go install github.com/fluxplane/engine/cmd/fluxplane@latest
```

## Commands

```bash
fluxplane init ./my-app
fluxplane run . --input "Hello"
fluxplane serve .
fluxplane build . --target dockerfile,docker-compose,docker-image --image my-app:local
fluxplane deploy . --target docker-compose --image my-app:local
fluxplane undeploy . --target docker-compose
fluxplane config show .
fluxplane config edit .
fluxplane describe .
fluxplane discover .
fluxplane auth status
fluxplane datasource index build .
```

Fluxplane reads `fluxplane.yaml` as the app manifest name. The old
`agentsdk.app.*` names are not loaded as compatibility aliases.

Auth and datasource commands are scoped to the selected app manifest bundle.
They do not manage coder's product bundle or `.coder.yaml` configuration.
