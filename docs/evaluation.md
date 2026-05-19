# Evaluation

This document captures the local smoke-test flow for evaluating an AgentRuntime
app over the public HTTP/SSE channel protocol.

## Evaluate coder over a Unix socket

Start `coder` in one terminal. Use a model/provider with available quota; for
example, the Codex-backed model alias:

```bash
coder --model=codex serve --debug --yolo --socket /tmp/coder.sock
```

The server prints connection details similar to:

```text
coder serve listening on unix:/tmp/coder.sock
base_url: http://unix
session: coder
```

You can manually verify the channel with the generic remote client:

```bash
coder remote --socket /tmp/coder.sock --session=coder --usage
```

Then run the evaluator against the same socket using structured target flags:

```bash
coder evaluator target \
  --model=codex \
  --yolo \
  --socket /tmp/coder.sock \
  --session coder
```

The `target` subcommand builds the target description from flags and sends it to
the evaluator. The evaluator app is instructed to choose a concrete probe, call
`target_submit`, and report the observed thread ID, run ID, event count,
outbound text, and errors.

`--yolo` is needed for unattended runs because `target_submit` is a
side-effecting operation that writes to an external channel. Without `--yolo`,
the evaluator prompts for approval before contacting the target.

## Expected smoke-test signal

A successful evaluator run should report non-empty values for:

- thread ID
- run ID
- event count
- outbound text

and should report no target execution error. For a deterministic minimal probe,
ask the evaluator to submit an exact-response prompt such as:

```text
Reply with exactly: evaluator target probe ok
```

The observed outbound text should match that phrase.
