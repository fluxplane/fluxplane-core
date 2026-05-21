# Fluxplane CLI

`fluxplane` is the generic CLI for authored Fluxplane app manifests. It owns the
app lifecycle that previously lived under `coder app ...`; `coder` remains the
bundled coding-agent product.

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

During the current split, Fluxplane still reads the existing
`agentsdk.app.yaml` manifest name. The manifest rename to `fluxplane.yaml` is a
separate follow-up slice.

Auth and datasource commands are scoped to the selected app manifest bundle.
They do not manage coder's product bundle or `.coder.yaml` configuration.
