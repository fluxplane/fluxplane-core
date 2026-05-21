# Tool Ergonomics Review: Countdown Process Concurrency

Date: 2026-05-20
Agent: coder
Topic: Tool use and workflow during a simple concurrent countdown request

## Scope

This review covers the current coder session only. The user asked me to run three tasks, each counting down from 10 seconds to 0. The session involved several attempts using `process_start`, `process_wait`, `process_output`, and `process_run`, plus follow-up discussion about the difference between `process_run` and `process_start`.

## What worked well

- The managed process tools eventually did support the intended workflow: start three background processes with `process_start`, then wait on each with `process_wait`.
- The final concurrent run used separate labels (`countdown-concurrent-1`, `countdown-concurrent-2`, `countdown-concurrent-3`) and a sufficient timeout (`20000ms`), which made the actual process execution succeed.
- `process_wait` returned clear stdout for each completed process, including all countdown lines from `10s` to `0s` and exit code `0`.
- The distinction between `process_run` and `process_start` became clear in the session: `process_run` blocks until completion, while `process_start` launches a background process and returns immediately.

## What was bad or inefficient

- My first attempt with `process_start` used `timeout_ms: 1000`, which was inappropriate for an 11-second countdown. That was my mistake. I should have set the timeout above the expected runtime immediately.
- After the first failed/partial attempt, I used a single `process_run` command that launched three shell background jobs internally. It produced the requested countdown output, but it did not satisfy the user's later clarified expectation of three separate managed tasks.
- I then repeated the same conceptual error by trying three `process_run` calls after discussing correct timeout handling. Since `process_run` waits, that was not the right tool for three concurrently managed tasks.
- The tool layer repeatedly emitted `tool_result_missing` and `orphan_tool_result` messages. Those made the interaction noisy and confusing. They looked like internal replay/bookkeeping artifacts rather than user-level process failures, but they obscured what was actually happening.
- I should not have made the user point out that three tasks should be started concurrently. The requirement was already obvious from “run 3 tasks” and especially from the follow-up conversation.

## What I would improve

- For concurrent managed tasks, I should use `process_start` first for all tasks, then `process_wait` or `process_output` afterward. I should not use `process_run` unless one blocking process is explicitly acceptable.
- I should calculate or estimate timeout requirements before invoking tools. A countdown from 10 to 0 with one-second sleeps needs about 11 seconds, so a timeout like `15000ms` or `20000ms` is the minimum sensible choice.
- I should explain tool-level replay anomalies separately from process behavior. In this session, the internal `tool_result_missing` messages were not the same as countdown failure, and I should have been clearer sooner.
- When demonstrating concurrency, I should prefer separate managed process labels over a shell one-liner with internal background jobs, because separate labels are easier to inspect, wait on, and reason about.

## Honest self-critique

I overcomplicated a trivial task and initially used the tools poorly. The correct workflow was simple: start three background processes with adequate timeouts, then wait for them. Instead, I first set an obviously too-short timeout, then papered over the issue with a shell-level parallel workaround, then briefly regressed to blocking `process_run` calls even after acknowledging that `process_start` was the right tool.

The user had to steer me back to the actual requirement. That is a workflow failure on my part, not a limitation of the user's wording. I also treated the internal replay artifacts as something to work around, but I allowed them to distract from the core execution semantics. I should have separated “tool transport noise” from “process execution result” more cleanly.

## Bottom line

The final execution was correct, but the path there was inefficient. For concurrent tasks in this environment, I should start all managed processes first with `process_start`, use realistic timeouts, then collect results with `process_wait` or `process_output`. I should avoid substituting a shell-backgrounding trick when the user is asking for separate concurrent tasks, and I should be more disciplined about choosing blocking versus non-blocking process tools.
