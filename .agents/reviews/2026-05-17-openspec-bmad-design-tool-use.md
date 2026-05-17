# Tool Ergonomics Review: Design-Doc Session Drift

Date: 2026-05-17
Agent: coder
Topic: Researching OpenSpec/BMad and writing plugin design documents

## Scope

This review covers the current coder session where I researched OpenSpec and
BMad, wrote design documents under `.agents/designs/`, updated `CHANGELOG.md`,
and then was asked to reflect on the session's tool use. It is based on the
actual operations I used in this session: `datasource_search`, `web_request`,
`project_files`, `go_packages`, `go_outline`, `file_read`, `grep`, `dir_list`,
`glob`, `file_create`, `file_edit`, `markdown_diagnostics`, and `git_diff`.

## What worked well

The web discovery path was useful. `datasource_search` found OpenSpec and BMad
sources quickly, including docs pages and relevant summaries. When `web_request`
worked for the OpenSpec docs, it gave enough concrete structure to avoid relying
on vague memory: workflow, directory layout, and delta format were all grounded
in fetched pages.

The project-navigation tools helped map the researched ideas onto this repo.
`project_files`, `go_packages`, `go_outline`, `grep`, and targeted `file_read`
confirmed that AgentRuntime already has `core/task`, `core/workflow`, command
resources, plugin packages, and `.agents` conventions. That made the OpenSpec
and BMad designs fit existing architecture instead of inventing unrelated
subsystems.

`file_create` was the right tool for the design docs, and
`markdown_diagnostics` was a good cheap check after writing them. I also used
`git_diff` to inspect the actual change set instead of assuming the edits were
clean.

## What was bad or inefficient

I let failed or flaky network fetches shape the session awkwardly. `web_request`
failed repeatedly for BMad docs and GitHub raw content. I recovered with
`datasource_search` snippets, but I should have been more explicit in the design
that some BMad details were based on search-result summaries rather than fully
fetched source pages.

I made a poor `CHANGELOG.md` edit. I inserted the OpenSpec and BMad entries into
the middle of an existing bullet, between `bounded` and the continuation line
`max_bytes output...`. That damaged the changelog structure. `git_diff` showed
the problem, but I did not immediately fix it before the user moved on. This is
exactly the kind of small documentation corruption that comes from editing by
line number without rereading the local paragraph.

There was also a replay/tool-result issue after one `file_read`, and earlier
some `web_request` calls produced missing-result repair noise. I did not let it
break the task, but it added friction and made the tool transcript harder to
reason about.

For the BMad design, I wrote a large document in one `file_create` call. That was
fast, but it skipped an intermediate outline/review step. Given the uncertainty
from partially fetched sources, a smaller outline first would have made the final
design easier to calibrate.

## What I would improve

Before editing an existing markdown list, I should read the exact surrounding
lines and patch the whole bullet block, not insert blindly after a line. In this
case I should have repaired `CHANGELOG.md` immediately after `git_diff` exposed
the malformed bullet.

For research-heavy work, I should separate source confidence levels: fetched
primary docs, search snippets, repository docs, and inference. The OpenSpec
design was well grounded by fetched docs; the BMad design was more dependent on
search-result excerpts because direct fetches timed out. The design should have
called that out more clearly.

I should use `markdown_diagnostics` on all changed markdown files, including
`CHANGELOG.md`, not only the newly created design docs. That would likely have
caught or at least highlighted list oddities.

I should also summarize before writing long design docs: proposed root, package
placement, operations, workflows, and integration points. That would give the
user a chance to correct assumptions before I create a 15-18 KB artifact.

## Honest self-critique

I moved quickly and produced useful design artifacts, but I was too tolerant of
rough edges. The most concrete mistake was the malformed changelog insertion. I
saw enough evidence in `git_diff` to know something was wrong and still did not
fix it before continuing.

I also overconfidently synthesized BMad details from search snippets when direct
source fetches failed. The design is probably directionally useful for
AgentRuntime, but it should be labeled as an AgentRuntime adaptation rather than
an exact BMad compatibility spec.

The session shows a common failure mode: strong high-level architecture mapping,
weak final hygiene. I used good discovery and validation tools, but I did not
apply the same discipline to the smallest edit in the session.

## Bottom line

The toolset was adequate for research, repo orientation, writing design docs,
and basic markdown validation. My workflow was productive but not careful enough
around existing-file edits. The next best improvement is simple: after every
`file_edit` to a structured markdown file, reread the affected block and fix it
before moving on.
