# Scheduled Trigger Example

This app runs a daemon-local schedule every minute using agent-level trigger
shorthand in `fluxplane.yaml`.

The manifest defines one agent, `health_summarizer`. Its `triggers` block says:

```yaml
triggers:
  - startup:
      prompt: |
        Send a desktop notification that says exactly: System monitoring active.
  - every: 1m
    prompt: |
      Collect a system-health snapshot with shell_exec, then notify the operator
      only if there is a clear actionable anomaly.
```

At load time, Fluxplane expands those entries into daemon triggers and generated
one-step workflows for the agent. The manifest does not need to define an
explicit session, command, workflow, or `daemon.triggers` block for this simple
case.

Run it from the repository root:

```bash
fluxplane serve --verbose examples/scheduled-trigger
```

The `--verbose` flag shows generated trigger and workflow events. The example
uses `notify-send` for desktop notifications and `shell_exec` for local system
measurements.

To run an equivalent check immediately without waiting for the schedule:

```bash
fluxplane run --session default --input "Collect a system-health snapshot with shell_exec, then notify me only if there is a clear actionable anomaly." --model=claude/haiku --yolo examples/scheduled-trigger
```

The app uses the `smart_model` alias from `fluxplane.yaml`. Override it with the
usual serve flags if needed, for example:

```bash
fluxplane serve --verbose --provider openrouter --model openai/gpt-5.5 examples/scheduled-trigger
```
