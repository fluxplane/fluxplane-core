# Process and shell tools

The process and shell tools provide a structured way for agents to run commands, manage long-lived local processes, and inspect output through the runtime `System` boundary.

They are designed for two related but distinct workflows:

- **Process operations** run direct executables with explicit arguments.
- **Shell operations** run script text through a selected shell such as `bash` or `sh`.

Use process operations when the executable and arguments are known. Use shell operations when shell syntax is intentional: pipes, redirects, conditionals, multiple commands, expansion, or scripts.

## Tool model

### Direct process tools

Direct process tools execute a binary without adding a shell wrapper.

Available operations:

- `process_run` — run one executable and wait for completion.
- `process_start` — start one executable in the background.
- `process_ensure` — return an existing running labeled process or start it.
- `process_list` — list managed processes.
- `process_status` — inspect one process by id or label.
- `process_output` — read bounded stdout/stderr for one process by id or label.
- `process_wait` — wait for one background process to exit.
- `process_stop` — gracefully stop one process by id or label.
- `process_kill` — forcefully kill one process by id or label.

### Shell tools

Shell tools execute script text through an available shell.

Available operations:

- `shell` — run or start shell script text.
- `shell_info` — list available shells, paths, and version information.
- `shell_exec` — run `command` as shell script text and wait for completion.

`sh`, `bash`, `zsh`, `fish`, `pwsh`, `powershell`, and `cmd` are the supported shell names. Availability depends on the host environment exposed through the runtime system.

## Foreground execution

Use `process_run` for direct executable execution:

```json
{
  "command": "go",
  "args": ["test", "./plugins/shellplugin"],
  "workdir": ".",
  "timeout_ms": 120000
}
```

Use `shell` when shell syntax is desired:

```json
{
  "op": "exec",
  "shell": "bash",
  "commands": [
    "set -euo pipefail",
    "go test ./plugins/shellplugin | tee /tmp/shellplugin-test.log",
    "grep PASS /tmp/shellplugin-test.log"
  ],
  "workdir": ".",
  "timeout_ms": 120000
}
```

The `commands` array is joined into one script. For POSIX-like shells it is passed as:

```text
shell -c '<joined script>'
```

PowerShell-style shells use `-NoProfile -Command`, and `cmd` uses `/C`.

## Background processes

Use `process_start` or `shell` with `op=start` for long-running work.

Example direct process:

```json
{
  "command": "python3",
  "args": ["-m", "http.server", "8080"],
  "workdir": "public",
  "label": "dev:http-server:8080",
  "tags": ["dev", "http"],
  "metadata": {
    "kind": "dev.server",
    "port": "8080"
  }
}
```

Example shell process:

```json
{
  "op": "start",
  "shell": "bash",
  "commands": [
    "while true; do date; sleep 10; done"
  ],
  "label": "demo:heartbeat",
  "tags": ["demo"]
}
```

The result includes a managed process id. The id can be used with status, output, wait, stop, and kill operations.

For background starts, `timeout_ms` is not a process lifetime. Use
`process_wait.timeout_ms` to bound how long to wait for completion, and use
`process_stop` or `process_kill` when a background process should be ended.

## Labels, tags, and metadata

Background process management is easier when processes are labeled.

- `label` is a stable human-readable identifier for one managed process role.
- `tags` are optional grouping hints.
- `metadata` stores structured string properties about the process.

Labels are especially useful for long-lived support processes such as port-forwarding, local dev servers, watchers, and daemons.

For example:

```json
{
  "command": "kubectl",
  "args": ["port-forward", "-n", "default", "svc/my-service", "8080:80"],
  "label": "k8s:port-forward:default/svc/my-service:8080-80",
  "metadata": {
    "kind": "kubernetes.port_forward",
    "namespace": "default",
    "resource": "svc/my-service",
    "local_port": "8080",
    "remote_port": "80"
  }
}
```

A labeled process can later be managed by label:

```json
{
  "label": "k8s:port-forward:default/svc/my-service:8080-80"
}
```

## Ensuring a process is running

`process_ensure` is useful when the desired state is “this process should be running.” It checks for an existing running process with the requested label. If one exists, it returns that process. If not, it starts a new process.

Example Kubernetes port-forward:

```json
{
  "command": "kubectl",
  "args": ["port-forward", "-n", "default", "svc/my-service", "8080:80"],
  "label": "k8s:port-forward:default/svc/my-service:8080-80",
  "metadata": {
    "kind": "kubernetes.port_forward",
    "namespace": "default",
    "resource": "svc/my-service",
    "local_port": "8080",
    "remote_port": "80"
  }
}
```

If the port-forward is already running, the operation reports the existing process instead of creating a duplicate.

## Inspecting process state

List all managed processes:

```json
{}
```

Inspect by process id:

```json
{
  "process_id": "proc-3"
}
```

Inspect by label:

```json
{
  "label": "dev:http-server:8080"
}
```

The process status includes command, args, workdir, label, tags, metadata, start/end timestamps, running state, exit code, and any recorded error.

## Reading output

`process_output` returns the bounded stdout and stderr buffers for a managed process:

```json
{
  "label": "k8s:port-forward:default/svc/my-service:8080-80"
}
```

The result also reports whether stdout or stderr was truncated.

This is a snapshot of currently retained output, not a cursor-based tail stream.

## Waiting, stopping, and killing

Wait for a background process to exit:

```json
{
  "label": "dev:http-server:8080",
  "timeout_ms": 10000
}
```

Gracefully stop it:

```json
{
  "label": "dev:http-server:8080"
}
```

Force kill it:

```json
{
  "label": "dev:http-server:8080"
}
```

Prefer `process_stop` for normal shutdown. Use `process_kill` when the process does not stop or when immediate termination is intended.

On Unix-like systems, managed processes are started in their own process group. Stop sends a graceful termination signal to the group; kill sends a forceful kill signal to the group. On Windows, both map to process termination through the host process API.

## Shell discovery

Use `shell_info` before choosing a shell when portability matters:

```json
{}
```

The response lists supported shell names, whether each one is available, the resolved executable path, and best-effort version output.

Example use:

1. Call `shell_info`.
2. Prefer `bash` if available.
3. Fall back to `sh` for POSIX shell scripts.
4. Use `pwsh` or `powershell` for PowerShell scripts.

## When to use which tool

Use `process_run` when:

- the command is a direct executable;
- arguments are known as separate values;
- shell expansion, pipes, redirects, and conditionals are not needed.

Use `shell` when:

- the task is naturally a script;
- the command needs pipes, redirects, globbing, variables, conditionals, or command substitution;
- multiple shell commands should run as one unit.

Use `process_start` when:

- the process should continue after the tool call returns;
- the process does not need de-duplication by label.

Use `process_ensure` when:

- the process represents desired local state;
- duplicate starts would be harmful or confusing;
- the process has a stable label, such as a Kubernetes port-forward.

Use `process_stop` before `process_kill` unless forceful termination is intended.

## Kubernetes port-forward workflow

A common long-lived process is `kubectl port-forward`.

Start or reuse the port-forward:

```json
{
  "command": "kubectl",
  "args": ["port-forward", "-n", "default", "svc/my-service", "8080:80"],
  "label": "k8s:port-forward:default/svc/my-service:8080-80",
  "metadata": {
    "kind": "kubernetes.port_forward",
    "namespace": "default",
    "resource": "svc/my-service",
    "local_port": "8080",
    "remote_port": "80"
  }
}
```

Check whether it is running:

```json
{
  "label": "k8s:port-forward:default/svc/my-service:8080-80"
}
```

Read output to confirm forwarding:

```json
{
  "label": "k8s:port-forward:default/svc/my-service:8080-80"
}
```

Stop it when no longer needed:

```json
{
  "label": "k8s:port-forward:default/svc/my-service:8080-80"
}
```

## Current limitations

- Managed process state is in memory for the current runtime instance.
- `process_output` is a bounded snapshot, not a cursor-based tail API.
- Readiness checks are not yet first-class. For now, inspect output or wait briefly before depending on a newly-started background process.
- Labels are lookup keys, but not a durable registry across runtime restarts.
- Environment exposure is controlled by the runtime system environment boundary.

## Good agent practice

For foreground tasks:

1. Prefer `process_run` for direct executable calls.
2. Prefer `shell` only when shell semantics are needed.
3. Set a timeout for commands that may hang.
4. Inspect exit code, stdout, and stderr.

For background tasks:

1. Always set a meaningful label.
2. Add metadata for domain-specific processes.
3. Use `process_ensure` for desired-state processes like port-forwards.
4. Use `process_status` and `process_output` before assuming readiness.
5. Use `process_stop` when the process is no longer needed.
