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
- **Purpose**: Handle multi-turn reasoning when agent reaches step limit without final decision
- **Max Iterations**: ~3 (default `maxContinuations`)
- **Trigger**: Agent completes max steps but hasn't called STOP operation
- **Action**: Send "Continue." message back as new input observation
- **Result**: Outer loop iterates to gather final decision or continue further

### Inner Loop: Step Loop
- **Purpose**: Execute single steps until agent makes a decision or hits step limit
- **Max Steps**: 50 (default `maxSteps`)
- **Flow per step**:
  1. **Materialize Context** → Render context blocks, invoke providers
  2. **Build Transcript** → Combine history + context + pending items
  3. **LLM Step** → Call agent with observations and context
  4. **Check Decision**:
     - `OPERATION` → Execute tool calls (step 5), loop back
     - `STOP` → Exit inner loop (continue to continuation check)
     - `FAILED` → Persist repair, exit with error

### Operation Execution (Step 5)
- **For each operation call**:
  1. Safety envelope checks (ACL, scope validation)
  2. Operation executor runs (with sandboxing)
  3. Effect result collected
  4. Tool result items added to pending transcript

## Critical State Management

### Persisted (ThreadStore)
```
• InputReceived            → User input + metadata
• AgentStepCompleted       → Agent decision for replay
• OperationRequested       → Operation call details
• OperationCompleted       → Operation result + side effects
• OutboundProduced         → Messages sent to user
• RuntimeEmitted           → Plan/skill/subagent events
• ContextUpdate            → Context block changes
```

### Local (In-memory during ExecuteInboundInput)
```
• agentState               → State ref from agent decisions
• effects[]                → Accumulated operation results
• observations[]           → Tool results feeding back to agent
• pending[]                → Transcript items waiting for LLM
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
    │   └─ FAILED: Persist repair, return error
    │
    ├─ Decision == STOP (terminal)
    │   ├─ Commit context changes
    │   └─ Return success (outer loop checks continuation)
    │
    └─ Decision == OPERATION (non-terminal)
        ├─ Execute operations → effects
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

    ├─ Agent decision is STOP?
    │   └─ NO: Continue checking...
    │
    ├─ continuation < maxContinuations?
    │   ├─ YES: pending = ["Continue."]
    │   │        observations = [{"kind": "session.continuation"}]
    │   │        loop back to inner loop
    │   │
    │   └─ NO: Return terminal result
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

1. **Outer Loop** (Continuation): Handles step limit without final decision
2. **Inner Loop** (Steps): Observe → Decide → Execute until decision

Each step:
- Renders fresh context
- Builds conversation transcript
- Calls LLM agent
- Executes operations (tool calls)
- Feeds results back as observations

All decisions, steps, and operations are **persisted to ThreadStore** for replay, audit, and resume.
