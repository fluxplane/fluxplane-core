# File edit tool

The `file_edit` tool is the preferred way for agents to make precise changes to existing files. It is built for careful source edits: small replacements, line-based insertions, range rewrites, deletions, and coordinated multi-change patches.

Unlike a shell script or broad text rewrite, `file_edit` treats edits as structured operations. Each operation says exactly what should change and where. That makes the edit easier to review, safer to apply, and easier for an agent to explain afterwards.

## Why it is powerful

### Edits are explicit

Every change is represented as an operation: replace exact text, insert before or after a line, replace a line range, delete a range, append, or prepend. This keeps intent close to the edit itself.

Instead of asking an agent to rewrite a whole file, you can ask it to change only the part that matters.

### Multi-edits use original file coordinates

A single `file_edit` call can contain many operations. The important detail is that line numbers and exact-text patches are resolved against the original file, before any changes are merged.

That means an agent can confidently make several changes in one pass without recalculating line numbers after each insertion. This is especially useful for documentation updates, refactors, and coordinated code changes.

### Overlapping changes are guarded

Operations are expected to target separate regions of the original file. This keeps edits deterministic and avoids hidden conflicts between changes.

Same-boundary inserts are allowed and applied in request order, which makes it easy to place several snippets at the same location intentionally.

### It supports review-friendly workflows

`file_edit` can be used with dry runs and different diff modes. This makes it useful both for quick edits and for careful review workflows where an agent should preview or summarize exactly what will change.

### It preserves surrounding content

Because `file_edit` works at exact text or line ranges, it avoids unnecessary churn. The rest of the file remains untouched, which keeps diffs smaller and easier to review.

## Core operations

`file_edit` supports these edit shapes:

- **Patch exact text**: replace one exact text match with new content.
- **Insert after a line**: add content immediately after a line from the original file.
- **Insert before a line**: add content immediately before a line from the original file.
- **Replace a range**: replace an inclusive line range with new content.
- **Delete a range**: remove an inclusive line range.
- **Append**: add content to the end of the file.
- **Prepend**: add content to the beginning of the file.

## Example: focused text replacement

Use an exact-text patch when a phrase or small block should be replaced without affecting the rest of the file.

```text
Replace:
  Requires Go 1.26+.

With:
  Requires Go 1.26 or newer.
```

This is ideal for small wording changes, renamed identifiers, or targeted documentation fixes.

## Example: insert a note after a heading

Line-based insertion is useful when the location matters more than the surrounding text.

```text
After line 30, insert:
  > This command uses the same runtime as embedded applications.
```

Because the insertion is tied to an original line number, it can be combined with other edits in the same call without shifting later coordinates.

## Example: replace a section body

Range replacement is useful when a paragraph or section needs to be rewritten as a unit.

```text
Replace lines 21 through 25 with:
  Fluxplane Engine provides durable building blocks for agent systems.
  Sessions can resume, events can replay, and tools run through a safety envelope.
```

This keeps the section heading and following section intact while changing only the intended body.

## Example: coordinated multi-edit

The strongest use case is a multi-edit where several independent changes are applied together.

```text
In one edit:
  1. Replace the title with a more specific title.
  2. Insert a short note after the badge block.
  3. Add a new link before the project status section.
  4. Append a maintenance marker at the end.
```

Conceptually:

```text
patch title:
  # Fluxplane Engine
  -> # Fluxplane Engine Guide

insert after line 15:
  <!-- badges end -->

insert before line 95:
  - [File edit tool](docs/ttools/file-edit.md)

append:
  <!-- maintained with structured file edits -->
```

All line references are resolved against the original file. The inserted badge note does not shift the later insertion before the project status section. The operations are merged into one final edit.

## Example: same-location inserts

Multiple inserts can intentionally target the same boundary.

```text
After line 1, insert:
  First note.

After line 1, insert:
  Second note.
```

The result preserves the operation order:

```text
# Heading
First note.
Second note.
```

This is useful when an agent builds a block from separate concerns while keeping the final order predictable.

## When to use `file_edit`

Use `file_edit` when:

- editing an existing file;
- making a small or medium targeted change;
- applying several independent edits to one file;
- preserving surrounding content matters;
- the change should be easy to inspect and explain;
- line-based or exact-text intent is clearer than rewriting the whole file.

For new files, use file creation. For existing files, prefer `file_edit` over ad hoc shell commands or full-file rewrites.

## Good agent practice

A strong `file_edit` workflow is:

1. Read the relevant file or line range.
2. Identify the smallest safe edit operations.
3. Apply them with `file_edit`.
4. Read the changed region or inspect the diff.
5. Summarize what changed.

This keeps edits controlled, reviewable, and easy to trust.
