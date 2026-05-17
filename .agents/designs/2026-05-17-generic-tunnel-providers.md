# DESIGN: Generic Tunnel Providers

## Status

Draft.

## Summary

Add a reusable tunnel model for exposing local daemon listeners through an
outbound cloud tunnel during development and controlled deployments. The first
providers are Cloudflare Tunnel and ngrok. The tunnel model should be generic so
it can support local HTTP/SSE channels, webhook-based channels such as Telegram,
and future inbound webhook integrations.

The intended resource shape is eventually app configuration such as:

```yaml
daemon:
  listeners:
    - name: local-http
      type: http
      addr: 127.0.0.1:8080
  channels:
    - name: local-web
      type: http_sse
      listener: local-http
      tunnel:
        provider: cloudflare
        mode: quick
        local_url: http://127.0.0.1:8080
        public_url_output: runtime
```

A channel should not need to know how a tunnel is created. It should receive a
public base URL from daemon assembly and use that URL for its own advertised
endpoints, webhook registration, or human-facing links.

## Goals

- Provide a reusable tunnel abstraction for any daemon listener or inbound
  channel.
- Support Cloudflare Quick Tunnels for zero-account local development.
- Support Cloudflare named tunnels for stable hostnames when a Cloudflare account
  and zone are available.
- Support ngrok for users who already have the CLI and account configured.
- Make tunnel lifecycle visible through operations and events.
- Keep provider process management behind runtime/system and the operation
  safety envelope.
- Avoid Telegram-specific or webhook-specific tunnel semantics.
- Preserve a path to core/app config without putting provider implementation in
  core.

## Non-goals

- Replacing production ingress, load balancers, or platform deployment.
- Guaranteeing uptime for provider free tiers.
- Implementing Cloudflare or ngrok account provisioning.
- Managing DNS zones beyond minimal named-tunnel routing helpers.
- Making public tunnels trusted by default. Tunnel reachability does not
  authenticate callers.

## Concepts

### Tunnel

A tunnel is a runtime process or provider-managed connection that forwards a
public HTTPS origin to a local listener.

Important fields:

- `provider`: `cloudflare`, `ngrok`, or future provider name.
- `mode`: provider-specific mode such as `quick`, `named`, or `agent`.
- `local_url`: local listener URL, for example `http://127.0.0.1:8080`.
- `public_url`: externally reachable URL discovered or configured at runtime.
- `status`: `starting`, `ready`, `failed`, `stopped`.
- `ephemeral`: whether the public URL is expected to change between runs.

### Tunnel Provider

A provider knows how to start, inspect, and stop one kind of tunnel.

Examples:

- Cloudflare Quick Tunnel:
  - command: `cloudflared tunnel --url http://127.0.0.1:8080`
  - public URL discovered from process output.
  - no login required.
  - random `trycloudflare.com` hostname.
- Cloudflare named tunnel:
  - command: `cloudflared tunnel run <name>`
  - stable hostname configured through Cloudflare DNS.
  - login/credentials required.
- ngrok:
  - command: `ngrok http 8080` or config-driven command.
  - public URL discovered through ngrok API or process output.
  - account/authtoken usually required.

### Tunnel Binding

A tunnel binding attaches a tunnel to a daemon listener or channel. The binding
is configuration; the tunnel process is runtime state.

Example future app config:

```yaml
daemon:
  listeners:
    - name: webhook-http
      type: http
      addr: 127.0.0.1:8080
      tunnel:
        provider: cloudflare
        mode: quick
        enabled: true
```

Channel-level shorthand should be allowed when the channel owns or implies the
listener:

```yaml
daemon:
  channels:
    - name: telegram-dev
      type: telegram
      listener: webhook-http
      tunnel:
        provider: cloudflare
        mode: quick
```

Daemon assembly resolves this into a listener tunnel and passes the resulting
public base URL to interested channel adapters.

## App Config Shape

Initial draft shape:

```yaml
tunnel_providers:
  - name: cloudflare
    type: cloudflare
    executable: cloudflared
    defaults:
      mode: quick
  - name: ngrok
    type: ngrok
    executable: ngrok
    defaults:
      region: eu

daemon:
  listeners:
    - name: local-http
      type: http
      addr: 127.0.0.1:8080
      tunnel:
        provider: cloudflare
        mode: quick
        local_url: http://127.0.0.1:8080
        enabled: true
        lifecycle: daemon
```

Provider-specific options should live under `options` to avoid changing the
common shape for every provider:

```yaml
tunnel:
  provider: ngrok
  mode: http
  local_url: http://127.0.0.1:8080
  options:
    region: eu
    hostname: dev.example.com
```

Suggested common fields:

```yaml
tunnel:
  provider: cloudflare
  mode: quick
  enabled: true
  local_url: http://127.0.0.1:8080
  public_url: ""           # configured stable URL, when known
  public_url_output: log    # log | event | file | runtime
  lifecycle: daemon         # daemon | manual | operation
  startup_timeout: 20s
  shutdown_timeout: 5s
  options: {}
```

## Operations

Expose a generic operation family named `cloud_tunnel` or, preferably, a more
provider-neutral name `tunnel` / `tunnel_manage`. The user-requested name
`cloud_tunnel` can be the first operation ID while keeping provider as input.

Draft operation:

```yaml
operation: cloud_tunnel
input:
  op: create | delete | status | list | register | unregister
  provider: cloudflare | ngrok
  name: telegram-dev
  local_url: http://127.0.0.1:8080
  mode: quick
  public_url: optional stable URL
  options: {}
```

Operation behavior:

- `create`: start or create a tunnel runtime handle. For named providers this
  may mean ensuring provider config exists, then running the tunnel.
- `delete`: stop a runtime handle or delete provider-side tunnel resources when
  explicitly allowed.
- `status`: report current public URL, process status, and provider metadata.
- `list`: list known tunnel handles from the daemon/runtime registry.
- `register`: register a public route/DNS/webhook consumer if provider supports
  it.
- `unregister`: remove a registered route or provider-side association.

Safety posture:

- `create` starts a managed process and opens a public ingress to a local
  service. It is side-effecting and must run through `runtime/operation` safety.
- `delete` may destroy provider-side resources; ask for approval unless deleting
  an ephemeral local runtime handle created by the same daemon run.
- Provider credentials and URLs containing secrets are redacted.
- Operations must not run shell strings. They invoke `cloudflared` or `ngrok`
  through the managed process boundary.

## Events

Tunnel lifecycle should emit events that adapters can wait on:

```text
tunnel.starting
tunnel.ready
tunnel.failed
tunnel.stopped
tunnel.public_url_changed
```

A webhook channel can wait for `tunnel.ready` before registering its webhook.
A local HTTP/SSE channel can render the public URL in terminal output.

## Architecture Placement

- `core`:
  - inert tunnel spec/config types only if the shape becomes stable app config.
  - no provider process logic.
- `runtime`:
  - tunnel runtime registry and lifecycle records if shared across adapters.
  - operation implementation may live here if provider-neutral and backed by
    system/process ports.
- `adapters/tunnel/cloudflare`:
  - `cloudflared` process integration and output parsing.
- `adapters/tunnel/ngrok`:
  - `ngrok` process/API integration and output parsing.
- `orchestration`:
  - daemon assembly attaches tunnels to listeners and channels.
- `plugins/tunnel` or provider plugins:
  - optional operation contributions and provider registration.
- `apps`:
  - select default providers for a distribution; no hidden tunnel defaults.

This respects the channel path:

```text
tunnel adapter -> daemon listener public URL -> channel adapter -> harness
```

The tunnel never calls session internals.

## Local HTTP/SSE Channel Use

A local HTTP/SSE channel can use the same tunnel binding to publish its browser
or remote client URL:

```yaml
daemon:
  listeners:
    - name: local-http
      type: http
      addr: 127.0.0.1:8080
      tunnel:
        provider: cloudflare
        mode: quick
  channels:
    - name: browser
      type: http_sse
      listener: local-http
```

Runtime output can show both URLs:

```text
Local:  http://127.0.0.1:8080
Public: https://example.trycloudflare.com
```

Security note: SSE/auth semantics remain the channel's responsibility. A tunnel
only exposes the listener.

## Provider Details

### Cloudflare Quick Tunnel

Use for local development.

Pros:

- Free.
- No account required.
- HTTPS by default.
- Outbound connection only.

Cons:

- Random URL per run.
- No SLA.
- Intended for development/testing, not production.

### Cloudflare Named Tunnel

Use for stable development, staging, or controlled deployments.

Pros:

- Stable hostname with a Cloudflare-managed domain.
- Can run as a service.
- Can be paired with Cloudflare Access for human-facing surfaces.

Cons:

- Requires Cloudflare account and DNS zone.
- More setup and credentials.

### ngrok

Use for users already familiar with ngrok or where ngrok features are desired.

Pros:

- Mature local webhook developer experience.
- CLI often already installed.
- Dashboard and request inspection.

Cons:

- Free plan limitations vary.
- Stable domains and advanced features may require paid plan.
- Account/authtoken usually needed.

## Security

- A tunnel is not authentication. Public tunnel traffic must still pass channel
  auth and policy.
- Do not raise trust because traffic arrived via Cloudflare or ngrok.
- Redact provider credentials and public URLs that embed secrets.
- Prefer route nonces for webhook paths.
- Enforce listener/channel auth for HTTP/SSE channels.
- Limit which local listeners can be exposed by policy.
- Emit clear terminal warnings when exposing a local listener publicly.

## Implementation Sequence

1. Define draft config structs in the app/daemon config area without moving to
   core until shape stabilizes.
2. Add a tunnel provider interface and runtime registry.
3. Implement Cloudflare Quick Tunnel provider.
4. Add operation `cloud_tunnel` for create/delete/status/list.
5. Wire daemon listener tunnel startup and public URL event emission.
6. Add ngrok provider.
7. Promote stable inert spec to core config if reused by multiple app loaders.
8. Reuse the public URL handoff in webhook channel providers such as Telegram.

## Open Questions

- Should the operation be named `cloud_tunnel` as requested, or should the model
  use `tunnel.manage` with `cloud_tunnel` as an alias?
- Should named Cloudflare DNS route creation be in scope for the first provider?
- Where should public URL discovery be persisted across daemon restarts?
- Should tunnel startup be automatic by default or require an explicit dev flag?
- How should multiple channels share one tunneled listener without duplicate
  process starts?
