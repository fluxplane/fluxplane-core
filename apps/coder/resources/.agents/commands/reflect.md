---
description: Write a markdown self-reflection review about this coder session's tool use.
---
Write one honest markdown self-reflection review about this current coder session's tool use and workflow.

Optional user focus:
{{ .Argument }}

The review must be based on the current session, not on a fresh sub-agent session.

Create exactly one new markdown file under `.agents/reviews/`.

Use this outline unless the focus strongly suggests otherwise:

# Tool Ergonomics Review: <Title>

Date: <date>
Agent: coder
Topic: <topic>

## Scope

## What worked well

## What was bad or inefficient

## What I would improve

## Honest self-critique

## Bottom line

Rules:

- Be concrete and honest. Do not flatter the tool system.
- Mention specific operations/tools used when relevant.
- Prefer examples from this current session over generic advice.
- Include what you personally should have done differently.
- Create `.agents/reviews` if needed.
- Do not modify existing review files.
- Do not commit.
- Final response: only the path written and 2-4 bullet summary points.
