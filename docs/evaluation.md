# Channel Evaluation

This document is for developers smoke-testing a running Fluxplane app over the
public HTTP/SSE channel protocol. It is not a tutorial; see
[Configuration](configuration.md) first if you have not yet authored an app.

It captures the generic shape of an end-to-end channel smoke test. Product
repositories, including `github.com/fluxplane/coder`, may provide richer product
specific evaluator commands.

## Evaluate an app over a Unix socket

Start a Fluxplane app in one terminal with a local socket listener configured in
`fluxplane.yaml`:

```bash
fluxplane serve ./my-app --verbose
```

A socket-backed app prints connection details similar to:

```text
fluxplane serve listening on unix:/tmp/fluxplane.sock
base_url: http://unix
session: default
```

Use a direct channel client, an HTTP/SSE client, or a product-specific remote
client to submit a deterministic probe to the running app. The probe should ask
for an exact response so failures are easy to recognize:

```text
Reply with exactly: evaluator target probe ok
```

Record the thread ID, run ID, event count, outbound text, and any target errors.
If the submitting tool is side-effect aware, approvals must be enabled or granted
because a channel probe writes to an external runtime surface.

## Expected smoke-test signal

A successful run should report non-empty values for:

- thread ID
- run ID
- event count
- outbound text

and should report no target execution error. The observed outbound text should
match the exact-response phrase.
