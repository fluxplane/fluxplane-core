# Agent Runtime Harness - Loop Architecture Summary

## Quick Visual Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                     AGENT EXECUTION FLOW                         │
└──────────────────────────────────────────────────────────────────┘

    INPUT (channel message)
           │
           ▼
    ┌─────────────────────┐
    │  HARNESS.SERVICE    │
    │  HandleInbound()    │
    └──────────┬──────────┘
               │
               ▼
    ┌─────────────────────┐
    │  SESSION EXECUTOR   │ ◄─── Orchestration Layer
    │  ExecuteInboundInput│
    └──────────┬──────────┘
               │
    ╔══════════▼══════════╗
    ║ CONTINUATION LOOP   ║ (max ~3 iterations)
    ║ (Outer Loop)        ║
    ╠═════════════════════╣
    ║                     ║
    ║  ╔═════════════╗    ║
    ║  ║  STEP LOOP  ║    ║ (max 50 steps)
    ║  ║ (Inner Loop)║    ║
    ║  ╠═════════════╣    ║
    ║  ║             ║    ║
    ║  ║ 1. Context  ║    ║
    ║  ║ 2. TX Build ║    ║
    ║  ║ 3. LLM Call ║    ║
    ║  ║ 4. Decision ║    ║
    ║  ║ 5. Execute  ║    ║
    ║  ║    Ops      ║    ║
    ║  ║             ║    ║
    ║  ╚═════════════╝    ║
    ║       │ STOP?       ║
    ║       └─ YES ──→ End║
    ║       └─ NO ──→ Re- ║
    ║                loop ║
    ║          Continue.  ║
    ║                     ║
    ╚═════════════════════╝
               │
               ▼
    OUTPUT (outbound message)
```

## The Two Nested Loops

### Outer Loop: Continuation Loop
- **Purpose**: Follow up after a terminal response when a configured stop condition says useful work remains
- **Max Iterations**: 3 by default, configured by `turns.continuation.max_continuations`
- **Trigger**: `turns.continuation.stop_condition`
- **Evaluator**: Prompt stop conditions use a runtime-provided evaluator model that must call the typed `continuation_decision` tool
- **Action**: Send the stop evaluator's `continue_instruction` back as a `session.continuation` observation
- **Result**: Outer loop iterates until the stop condition returns `stop` or the cap is reached

### Inner Loop: Step Loop
- **Purpose**: Execute single steps until agent makes a decision or hits step limit
- **Max Steps**: 50 by default, configured by `turns.max_steps`
- **Flow per step**:
  1. **Materialize Context** → Render context blocks, invoke providers
  2. **Build Transcript** → Combine history + context + pending items
  3. **LLM Step** → Call agent with observations and context
  4. **Check Decision**:
     - `OPERATION` → Execute tool calls (step 5), loop back
     - `STOP` → Exit inner loop (continue to continuation check)
     - `FAILED` → Exit with error without persisting partial provider transcript items

### Operation Execution (Step 5)
- **For each operation call**:
  1. Safety envelope checks (ACL, scope validation)
  2. Operation executor runs (with sandboxing)
  3. Effect result collected
  4. Assistant tool-call items, `OperationCompleted`, and matching provider tool-result items are committed together
  5. Tool results become committed pending transcript items for the next LLM step

## Critical State Management

### Persisted (ThreadStore)
```
• InputReceived            → User input + metadata
• AgentStepCompleted       → Agent decision for replay
• OperationRequested       → Operation call details
• OperationCompleted       → Operation result + side effects
• OutboundProduced         → Messages sent to user
• RuntimeEmitted           → Task/skill/session-agent events
• ContextUpdate            → Context block changes
```

### Local (In-memory during ExecuteInboundInput)
```
• agentState               → State ref from agent decisions
• effects[]                → Accumulated operation results
• observations[]           → Tool results feeding back to agent
• pending[]                → Transcript items waiting for LLM, including already committed tool results
• localTranscript          → Conversation history for this turn
• localContinuation        → Provider continuation handle (for resume)
```

## Key Orchestration Components

| Layer | Component | Responsibility |
|-------|-----------|-----------------|
| **Channel** | `harness.Service` | Route inbound → session, manage session catalog |
| **Session** | `session.Session` | Orchestrate input → output via observe-decide-apply |
| **Runtime.Agent** | `llmagent` | LLM provider integration, model calls |
| **Runtime.Context** | `materializer` | Render context blocks, invoke providers |
| **Runtime.Conversation** | `projector` | Build transcript, manage continuation |
| **Runtime.Operation** | `executor` | Execute operations with safety checks |
| **Persistence** | `thread.Store` | Read/write thread history + events |

## Decision Tree (Per Step)

```
Agent.Step() returns StepResult
    │
    ├─ Status != OK
    │   └─ FAILED: Return error; do not persist partial provider transcript items
    │
    ├─ Decision == STOP (terminal)
    │   ├─ Commit context changes
    │   └─ Return success (outer loop checks continuation)
    │
    └─ Decision == OPERATION (non-terminal)
        ├─ Validate provider call IDs against assistant tool calls emitted by the model step
        ├─ Execute operations → effects
        ├─ Commit assistant tool-call items + OperationCompleted + provider tool result atomically
        ├─ Observations += effects (tool.result)
        │
        ├─ step == maxSteps-1?
        │   ├─ YES: Return boundary result (outer loop continues)
        │   └─ NO: Repeat inner loop
```

## Continuation Logic

After inner loop exits:

```
shouldContinueAfterTerminal(continuation, agentResult)?

    ├─ turns.continuation.stop_condition configured?
    │   └─ NO: Return terminal result
    │
    ├─ stop condition says continue?
    │   └─ NO: Return terminal result
    │
    ├─ continuation < turns.continuation.max_continuations?
    │   ├─ YES: pending = [continue_instruction]
    │   │        observations = [{"kind": "session.continuation"}]
    │   │        loop back to inner loop
    │   │
    │   └─ NO: Return continuation_limit_exceeded
    │
    └─ Return applyTerminalAgentDecision()
       └─ Generate outbound message
```

## Example Flow

```
User: "Write a function to sum an array"

┌─ CONTINUATION #0
│  ┌─ STEP 0: Context ("Python code") + Transcript → LLM
│  │  Agent: "I'll use code_executor to write this"
│  │  Decision: OPERATION (code_executor)
│  │
│  ├─ STEP 1: Execute code_executor → EffectResult
│  │  Observations += tool result
│  │  Agent: "Here's the function..."
│  │  Decision: STOP (send response)
│  │
│  └─ INNER LOOP: Exit (2 steps < 50)
│
├─ CONTINUATION CHECK:
│  ├─ Agent.Decision == STOP? YES
│  └─ Exit (no continuation)
│
└─ OUTPUT: User message with code
```

## Summary

The agent loop is a **two-level nested machine**:

1. **Outer Loop** (Continuation): Handles stop-condition-driven follow-up turns
2. **Inner Loop** (Steps): Observe → Decide → Execute until decision

Each step:
- Renders fresh context
- Builds conversation transcript
- Calls LLM agent
- Executes operations (tool calls)
- Feeds results back as observations

All decisions, steps, and operations are **persisted to ThreadStore** for replay, audit, and resume.
