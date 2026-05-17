# Structured Operation Results and UI-Owned Rendering

Date: 2026-05-17

## Summary

Operation implementations should return typed semantic data and typed events. They should not encode terminal, web, or other human UI presentation in operation result text.

The operation layer answers: **what happened?**

The model-facing text answers: **what should the LLM remember?**

The UI layer answers: **how should this be presented to a human?**

This separation keeps plugins portable, enables rich terminal/web rendering, avoids brittle string parsing, and gives clients stable contracts.

## Problem

Some operations currently return `operation.Rendered.Text` that is effectively human UI formatting. For example, a tool may return a block like:

```text
[tool preset=python image=... exit=0]
=== STDOUT ===
...
=== STDERR ===
...
```

This creates several issues:

- Terminal UI falls back to plugin-authored raw text instead of rendering a first-class UI.
- Web UI, API clients, and tests cannot rely on stable typed fields.
- Model-facing transcript text gets mixed with human presentation chrome.
- Future UI changes require changing plugin output rather than renderer code.
- Ad hoc `map[string]any` result payloads make renderers brittle and hard to refactor.

## Desired architecture

### 1. Operations return typed result objects

Each operation that returns structured data should define a typed result struct with stable JSON tags.

Prefer:

```go
type ExampleResult struct {
    Path  string `json:"path"`
    Bytes int64  `json:"bytes"`
}
```

Avoid using ad hoc maps as the operation contract:

```go
map[string]any{"path": path, "bytes": bytes}
```

Maps may still appear in compatibility layers or generic infrastructure, but first-party plugin result contracts should be typed.

### 2. Operations emit typed events

Long-running or progressive operations should emit typed events for runtime/UI consumption.

Examples:

- execution started
- process output chunk
- file written
- test package completed
- operation completed

Events should be typed structs with stable JSON tags, not unstructured maps or text fragments.

### 3. Text is for the LLM side only

`operation.Rendered.Model` or equivalent model-facing text should be concise and stable. It may summarize important fields for the model, but it should not contain terminal-specific presentation such as:

- ANSI escapes
- decorative box drawing as UI chrome
- emojis as status markers
- terminal-oriented section banners
- web/UI labels

Model text is transcript context, not the human UI.

### 4. UI renders from typed data and events

Terminal, web, and other human interfaces should derive presentation from typed result objects and typed events.

For example:

- terminal UI may render emojis, colors, sections, and compact metadata;
- web UI may render cards, tabs, tables, or collapsible streams;
- debug UI may render raw JSON;
- accessibility-oriented UI may render plain text.

All of these should consume the same typed semantic data.

## Failure results should still carry typed output when useful

A failed operation may still produce meaningful typed output. For example, a code execution can exit with code `1` and still produce stdout/stderr, exit code, duration, and truncation metadata.

That execution result is not merely error details. It is the operation output, even though the status is failed.

Prefer:

```go
operation.Result{
    Status: operation.StatusFailed,
    Error: &operation.Error{Code: "code_execute_failed", Message: err.Error()},
    Output: operation.Rendered{
        Model: modelText,
        Data:  typedResult,
    },
}
```

Avoid hiding typed output inside ad hoc error details maps.

## First application: `code_execute`

`code_execute` should return a typed execution result, such as:

```go
type ExecuteResult struct {
    Preset          string   `json:"preset"`
    Image           string   `json:"image"`
    Files           []string `json:"files,omitempty"`
    Command         []string `json:"command,omitempty"`
    Stdout          string   `json:"stdout,omitempty"`
    Stderr          string   `json:"stderr,omitempty"`
    ExitCode        int      `json:"exit_code"`
    TimedOut        bool     `json:"timed_out,omitempty"`
    DurationMS      int64    `json:"duration_ms"`
    TimeoutMS       int64    `json:"timeout_ms,omitempty"`
    StdoutTruncated bool     `json:"stdout_truncated,omitempty"`
    StderrTruncated bool     `json:"stderr_truncated,omitempty"`
}
```

Its model text should be compact and LLM-oriented. Terminal UI should render the typed result with preset-specific emoji, metadata, and stdout/stderr sections.

## Acceptance criteria

- First-party operation result payloads added or refactored in this area use typed structs, not `map[string]any` contracts.
- Operation text is treated as LLM-facing transcript text only.
- Terminal/web UI render from typed result data and typed events.
- Failed operations can still return typed output when the output is semantically meaningful.
- `code_execute` becomes the initial concrete example of this pattern.
