# Scheduled Trigger Example

This app runs a daemon-local schedule every minute. Each tick submits a trigger
to the `heartbeat` session and runs the `heartbeat` workflow.

When the daemon starts, a startup trigger speaks `System monitoring active.`
once through `notify_send`, then immediately runs the same health workflow once.
After that, the scheduled trigger runs it every minute.

The workflow first runs a fixed `shell_exec` step that collects a small system
health snapshot: timestamp, uptime/load, memory, disk, and busiest processes.
It then maps that command output into a classifier agent step. If the classifier
does not return exactly `ACTION_NEEDED`, the workflow stops without notifying.
If there is something worth interrupting the operator about, an agent writes a
short desktop notification body and the final workflow step calls `notify_send`.
The recurring health workflow does not use TTS or tones.

Run it from the repository root:

```bash
fluxplane serve --verbose examples/scheduled-trigger
```

The `--verbose` flag shows the scheduled trigger, workflow operation, agent
summary, and notification events as they happen. The OS notification uses
`notify-send`. The startup welcome message uses embedded Piper with the Jenny
voice; recurring health checks stay silent unless they need to show a desktop
notification.

To run the same workflow immediately without waiting for the schedule, invoke
the example command target:

```bash
fluxplane run --session heartbeat --input /heartbeat --model=claude/haiku --yolo examples/scheduled-trigger
```

The app uses the `smart_model` alias from `fluxplane.yaml`. Override it with the
usual serve flags if needed, for example:

```bash
fluxplane serve --verbose --provider openrouter --model openai/gpt-5.5 examples/scheduled-trigger
```
