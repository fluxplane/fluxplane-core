# Code Execute Terminal Rendering Improvement

Date: 2026-05-17

## Summary

Improve the terminal UX for `code_execute` operation results. The current terminal rendering exposes the container execution result in a raw shape. It should instead render a concise, human-friendly block with language-specific emoji, execution metadata, and clearly separated stdout/stderr sections.

Failure modes should be visually obvious and use a red cross marker (`❌`).

## Goals

- Make `code_execute` results quickly scannable in terminal UI.
- Use language/preset-specific emoji for common presets:
  - Python: `🐍`
  - Go: `🐹`
  - Node.js: `🟩` or another Node-specific marker if preferred
  - Unknown/default: `📦`
- Use `❌` for failures, timeouts, and container/start errors.
- Preserve useful execution metadata:
  - preset
  - image/container image
  - duration
  - exit code when available
  - timeout when relevant
  - truncation information when relevant
- Keep stdout and stderr visually distinct.
- Do not rely on the final model summary to communicate execution status; render from structured operation data.

## Non-goals

- Do not change `code_execute` execution behavior.
- Do not change sandbox/container policy.
- Do not change provider-facing operation schemas unless existing result data is insufficient.
- Do not parse language output beyond basic stdout/stderr display.

## Proposed terminal rendering

### Successful Python execution

```text
🐍 Python code executed successfully
   preset: python
   image: python:3.12-alpine
   duration: 0.2s
   exit: 0

stdout
────────────────────────────────────────
20260517-115537
2026-05-17
```

### Successful Go execution

```text
🐹 Go code executed successfully
   preset: go
   image: golang:1.24-alpine
   duration: 1.4s
   exit: 0

stdout
────────────────────────────────────────
ok
```

### Successful Node.js execution

```text
🟩 Node.js code executed successfully
   preset: node
   image: node:22-alpine
   duration: 0.5s
   exit: 0

stdout
────────────────────────────────────────
hello from node
```

### Success with no output

```text
🐍 Python code executed successfully
   preset: python
   image: python:3.12-alpine
   duration: 0.1s
   exit: 0

(no stdout or stderr)
```

## Failure rendering

All failure modes should start with `❌` so they are visually distinct from successful executions.

### Non-zero exit

```text
❌ 🐍 Python code failed
   preset: python
   image: python:3.12-alpine
   duration: 0.3s
   exit: 1

stderr
────────────────────────────────────────
Traceback (most recent call last):
  File "/workspace/main.py", line 1, in <module>
    raise RuntimeError("boom")
RuntimeError: boom
```

### Non-zero exit with stdout and stderr

```text
❌ 🟩 Node.js code failed
   preset: node
   image: node:22-alpine
   duration: 0.4s
   exit: 1

stdout
────────────────────────────────────────
starting job...

stderr
────────────────────────────────────────
Error: boom
    at main (/workspace/main.js:2:9)
```

### Timeout

```text
❌ 🐹 Go code timed out
   preset: go
   image: golang:1.24-alpine
   timeout: 30s
   duration: 30.0s

stdout
────────────────────────────────────────
starting long job...

stderr
────────────────────────────────────────
context deadline exceeded
```

### Container/start error

```text
❌ 🐍 Python code could not start
   preset: python
   image: python:3.12-alpine

error
────────────────────────────────────────
docker: image pull failed: ...
```

## Truncated output rendering

When stdout or stderr is truncated, show that prominently in metadata and inline near the truncated stream.

```text
🐍 Python code executed successfully
   preset: python
   image: python:3.12-alpine
   duration: 0.8s
   exit: 0
   output: truncated to 12 KiB

stdout
────────────────────────────────────────
line 1
line 2
...
[stdout truncated: 48 KiB omitted]
```

If only one stream is truncated, the metadata may be stream-specific:

```text
   stdout: truncated to 12 KiB
```

## Suggested status mapping

| Condition | Header |
|---|---|
| exit code `0` | `{emoji} {Language} code executed successfully` |
| non-zero exit | `❌ {emoji} {Language} code failed` |
| timeout | `❌ {emoji} {Language} code timed out` |
| container/start error | `❌ {emoji} {Language} code could not start` |
| unknown preset success | `📦 Code executed successfully` |
| unknown preset failure | `❌ 📦 Code failed` |

## Suggested preset labels

| Preset | Emoji | Label |
|---|---:|---|
| `python` | `🐍` | `Python` |
| `go` | `🐹` | `Go` |
| `node` | `🟩` | `Node.js` |
| other/unknown | `📦` | `Code` |

## Implementation notes

- The terminal renderer should prefer structured `code_execute` result fields over string parsing.
- Keep rendering logic in the terminal/UI layer; operation execution should not embed terminal-specific formatting.
- The renderer should handle absent fields gracefully, especially for start errors where exit code or duration may be unavailable.
- stdout/stderr blocks should be omitted when empty, unless both are empty, in which case render `(no stdout or stderr)`.
- The failure cross `❌` should be present for every error-like state, including timeout and start failures.

## Acceptance criteria

- Successful Python, Go, and Node.js executions render with preset-specific emoji and a clear success header.
- Non-zero exits, timeouts, and start errors render with `❌` in the header.
- stdout and stderr are separated by labeled sections.
- Empty output renders as `(no stdout or stderr)`.
- Truncation metadata is visible when output is bounded or omitted.
- Existing raw execution data remains available to the model/debug path; only terminal presentation changes.
