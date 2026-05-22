# GitLab Dex-Parity Implementation Plan

## Goal

Bring `plugins/integrations/gitlab` to at least Dex GitLab parity while keeping
the agentruntime interface native, agent-first, and token-efficient.

Dex is a CLI that is deliberately useful to agents: compact default output,
full JSON/YAML when requested, local project resolution, stable references like
`project/path!123`, and review helpers that prevent invalid GitLab operations.
agentruntime should not wrap `dex gl` or preserve Dex command syntax. It should
provide the same or better capability through datasource entities, relations,
and typed operations.

## Current Gap Summary

Already covered by agentruntime:

- Projects, groups, users, memberships, branches, tags, commits, repository
  tree entries, repository files, MRs, MR diffs, MR notes, pipelines, jobs, and
  job traces as datasource entities.
- Write operations for MRs, repository files, branches, tags, multi-file
  commits, and project CI variables.
- Auth methods, token diagnostics, instance-aware GitLab configuration, and
  safety-envelope operation execution.

Missing or weaker than Dex:

- Activity overview across indexed/reachable projects.
- Rich project details: languages and top contributors.
- Rich MR details: approvals, changes, commits, discussions, diff file counts,
  conflict status, and project path in returned records.
- MR edit, discussion replies, inline comments with position validation, and
  award emoji reactions.
- Pipeline creation with variables as a first-class operation.
- Personal snippets: list/show/create/delete.
- Repository read helpers: file blame, compare refs, scoped diff output, and
  blob search.
- Diff ergonomics: parsed hunk lines, line search, line context, and dry-run
  validation for inline comment targets.
- Agent-facing usage documentation for native datasource/operation calls.

## Design Principles

- Keep all GitLab API interaction inside `plugins/integrations/gitlab`; do not
  add GitLab-specific behavior to adapters, orchestration, or runtime packages
  unless a generic datasource/operation contract is missing.
- Prefer datasource entities and relations for read paths. Add operations only
  for side effects.
- Default read results must be bounded and cheap. Full bodies, file contents,
  traces, or diff payloads require explicit `Get`, relation, or filter input.
- Use structured typed inputs/results for operations. Do not expose ad hoc
  `map[string]any` parsing.
- Preserve the current no-compat-shim posture: no Dex CLI wrapper, no
  `agentsdk` compatibility, no stale deprecated operation names.
- Preserve existing broader functionality. Do not remove branch/tag/commit,
  repo-file, or CI-variable operations while adding Dex parity.

## Implementation Phases

Implement this as a series of focused PR-sized changes even if a single agent
does the work in one branch. The recommended order is:

1. Shared parsing/helpers and client interface expansion.
2. Read-only datasource parity for repository/MR/CI inspection.
3. Diff parsing plus inline-comment validation.
4. New write operations and MR write extensions.
5. Documentation and final verification.

Do not start with all operations at once. The fake `gitlabClient` in
`plugin_test.go` will need to grow with every client interface method; keeping
the work sliced avoids making test failures opaque.

Each phase should leave the repo compiling and should include tests for the
newly introduced contracts before moving to the next phase. If implementation
needs to be split further, split by read entities first, then write operations:
repository inspection, MR review, CI, snippets.

### Phase 1: Reference Parsing, IDs, and Shared Helpers

Add helpers before adding new features so every new entity and operation uses
the same reference rules.

Files:

- `plugins/integrations/gitlab/models.go`
- `plugins/integrations/gitlab/datasource.go`
- `plugins/integrations/gitlab/diagnostics.go`
- `plugins/integrations/gitlab/plugin_test.go`

Work:

- Normalize MR reference parsing around `project!iid`.
  - Accept numeric project IDs: `742!123`.
  - Accept nested project paths: `group/subgroup/project!123`.
  - Reject missing project, missing IID, non-numeric IID, and whitespace-only
    refs with actionable errors.
- Keep record IDs stable:
  - MR: `<project-id-or-path>!<iid>` where available.
  - MR child records: `<project-id-or-path>!<iid>!<child-key>`.
  - Project child records: `<project-id-or-path>!<child-id-or-path>`.
  - Ref/path records: `<project-id-or-path>!<ref>!<path>`.
- Add one shared project identifier helper for all datasource and operation
  paths:
  - If input parses as an integer, call GitLab with numeric project ID.
  - Otherwise pass the raw path-with-namespace to the official GitLab client;
    its `ProjectID` formatter performs URL escaping at the HTTP boundary.
  - Preserve the original label in returned metadata.
- Keep the existing raw `!`-delimited record ID style for the first
  implementation pass.
  - Do not introduce a new escaping scheme in this work; it would churn
    existing repository tree/file/job IDs.
  - Project paths may contain `/` and remain readable, e.g.
    `group/subgroup/project!123`.
  - Refs, paths, discussion IDs, and other child IDs containing literal `!`
    are not supported through opaque record IDs in this pass. Agents can still
    reach those resources through `Search`/`List` filters such as `project_id`,
    `ref`, and `path`.
  - Add tests that document this behavior so a future escaping migration is
    deliberate.
- Add bounded string helpers:
  - `boundedText(value, maxBytes) (text string, truncated bool)`.
  - `boundedLines(value, maxLines, maxBytes)`.
  - Use these for traces, file previews, diff summaries, snippet content, and
    blob search snippets.
- Use these default bounds unless a caller supplies a stricter `max_bytes` or
  `context` filter on entities that explicitly support those filters:
  - Record content/preview: 4 KiB.
  - Diff summary or scoped diff preview: 16 KiB.
  - Job trace: 64 KiB.
  - Snippet content preview: 8 KiB.
  - Blob search snippet: 1 KiB.
  - Diff line context: default 3 lines, maximum 20 lines.

Tests:

- Extend current MR detector tests for nested paths and URL fragments.
- Add table tests for all valid/invalid ID forms.
- Verify errors name the expected reference format.

### Phase 2: Read Model Parity

Extend the datasource model first, with fake-client tests and bounded outputs.

Files:

- `plugins/integrations/gitlab/models.go`
- `plugins/integrations/gitlab/datasource.go`
- `plugins/integrations/gitlab/data.go`
- `plugins/integrations/gitlab/plugin.go`
- `plugins/integrations/gitlab/plugin_test.go`
- `apps/coder/bundle.go`
- `apps/coder/bundle_test.go`

Add entity types:

- `gitlab.activity`
  - Purpose: recent project activity summary, equivalent to `dex gl activity`.
  - Shape: project id/path/name/web URL, last activity timestamp, recent commit
    count, recent MR count, recent pipeline count.
  - Access: `Search`/`List` with filters `since`, `project_id`, `group_path`,
    `limit`.
  - Implementation: list projects by activity, then fetch recent commits,
    project MRs, and pipelines concurrently with a small bounded worker pool.
    Keep failures per project in metadata instead of failing the whole result
    unless project listing fails.
- `gitlab.compare`
  - Purpose: compare two refs in a project.
  - ID format: `<project>!<from>...<to>` for three-dot and
    `<project>!<from>..<to>` for straight compare.
  - Filters: `project_id`, `from`, `to`, `straight`, optional `path`.
  - Default behavior: summary only, with commits capped at 20 and file change
    summaries for all returned diffs. Include diff text only when `path` is set.
  - Fields: from, to, straight, path, head commit, timeout, same_ref, commits,
    files, additions, deletions, `diff_preview`, `truncated`.
- `gitlab.blame`
  - Purpose: file blame equivalent to `dex gl file blame`.
  - ID format: `<project>!<ref>!<path>!blame`.
  - Filters: `project_id`, `ref`, `path`.
  - Fields: file path, ref, ranges with commit id/title/author/date and line
    start/end. Keep line content bounded per range.
- `gitlab.blob_search`
  - Purpose: project-scoped GitLab blob search.
  - Filters: `project_id` required, `query` required through search request or
    filter. Do not add a `ref` filter in the first pass because GitLab's blob
    search endpoint returns refs but does not provide a simple ref-scoped query
    parameter in the current client contract.
  - Fields: basename, filename, path, ref, start_line, matching snippet.
  - Capability: `Search` only.
- `gitlab.project_language`
  - Purpose: project language breakdown.
  - Capabilities: `Search`.
  - Relation: Project -> `languages`.
  - Search filters: `project_id` required.
  - Fields: project_id, language, share.
  - Do not inline languages into `gitlab.project` list/search/corpus records.
- `gitlab.project_contributor`
  - Purpose: top repository contributors for a project.
  - Capabilities: `Search`.
  - Relation: Project -> `contributors`.
  - Search filters: `project_id` required, optional `max_count`.
  - Fields: project_id, name, email, commits, additions, deletions.
  - Do not inline contributors into `gitlab.project` list/search/corpus records.
- `gitlab.snippet`
  - Purpose: personal snippets list/show.
  - Capabilities: `List`, `Get`, `Relation`.
  - Fields: id, title, description, visibility, author username, web_url,
    raw_url, files, created_at, updated_at.
  - `List` and `Get` return metadata and file descriptors only. This is the
    token-saving default and maps to Dex's `show --no-content`.
  - Relation: Snippet -> `files` returns `gitlab.snippet_file` records with
    bounded content.
- `gitlab.snippet_file`
  - Purpose: content-bearing snippet files.
  - Capabilities: `Search`, `Get`.
  - ID format: `<snippet-id>!<file-path>`.
  - Search filters: `snippet_id` required, optional `file_path`, optional
    `max_bytes`.
  - Fields: snippet_id, file_path, raw_url, content, `truncated`.
  - If the official client returns a single concatenated content payload for a
    multi-file snippet, expose one synthetic record with file_path from the first
    snippet file and set `metadata["content_shape"]="single_payload"` so agents
    can reason about the limitation.
- `gitlab.discussion`
  - Purpose: MR discussion threads, not just flat notes.
  - ID format: `<project>!<iid>!<discussion-id>`.
  - Relation: MR -> `discussions`.
  - Fields: discussion_id, project_id, merge_request_iid, notes, resolvable,
    resolved, position fields when present.
- `gitlab.merge_request_approval`
  - Purpose: MR approval summary and approver list.
  - Capabilities: `Search`.
  - Relation: MR -> `approvals`.
  - Search filters: `project_id` and `merge_request_iid` required.
  - Fields: project_id, merge_request_iid, approvals_required, approvals_left,
    approved, approved_by.
  - Do not inline approval data into MR list/search/corpus records.
- `gitlab.merge_request_change`
  - Purpose: MR change totals and changed-file summaries from
    `GetMergeRequestChanges`.
  - Capabilities: `Search`.
  - Relation: MR -> `changes`.
  - Search filters: `project_id` and `merge_request_iid` required, optional
    `path`, optional `include_diff`, optional `max_bytes`.
  - Fields: project_id, merge_request_iid, additions, deletions, files,
    file summaries, optional bounded diff preview.
  - Use existing `gitlab.merge_request_diff` for file-level diff listing and
    `gitlab.merge_request_diff_line` for parsed review targets; this entity is
    the explicit heavier "MR changes summary" read path.
- `gitlab.award_emoji`
  - Purpose: expose reactions when GitLab returns them.
  - Relations: MR -> `award_emoji`, MR note/discussion note -> `award_emoji`.
  - Fields: id, name, user, created_at.
- `gitlab.job_trace`
  - Keep the existing entity, but add `Search` as well as `Get`/relation.
  - Purpose: fetch a job trace either by known job ID or by Dex-style
    `project + pipeline + job name` lookup.
  - Search filters: `project_id`, optional `job_id`, optional `pipeline_id`,
    optional `job_name`, optional `max_bytes`.
  - If `job_id` is present, fetch that trace directly. If `pipeline_id` and
    `job_name` are present, list pipeline jobs, match `job.name`
    case-sensitively, fail with available job names when there is no match, and
    fail with all matching job IDs when the name is ambiguous.

Base entity field expansion:

- Base entities may add only fields returned by the same GitLab endpoint already
  being called for that code path. Do not add hidden follow-up API calls to
  populate base entity fields in `List`, `Search`, `Get`, `Relation`, or
  `Corpus`.
- `Project`: add cheap fields already available from project list/get responses,
  such as `StarCount`, `ForksCount`, `LastActivityAt`, and `Topics`. Languages
  and contributors are separate relation entities.
- `Commit`: add stats only where the commit endpoint already returns stats.
  If list/search does not include stats, list/search records leave stats empty;
  `Get` can include stats because it calls the commit-detail endpoint.
- `MergeRequest`: add fields already returned by MR list/get responses, such as
  `ProjectPath`, `MergeStatus`, `HasConflicts`, `MergedAt`, and file-count
  hints where present. Approvals, discussions, commits, diff versions, and
  changes are separate relation entities/read paths.
- `Pipeline`: add duration, started_at, finished_at, and user username only
  where returned by the pipeline endpoint already being used.
- `RepositoryFile`: add `Search` capability in addition to the existing `Get`.
  Search filters are `project_id`, `ref`, `path`, optional `include_content`,
  and optional `max_bytes`. `include_content=false` returns file metadata only;
  `include_content=true` returns bounded decoded content with `truncated`.
  `Get` by encoded ID returns bounded content because `GetRequest` has no
  filters.
- `JobTrace`: keep current bounded trace behavior and expose max size and
  truncation metadata explicitly.

Enrichment and indexing rules:

- `Search`, `List`, `Get`, `Relation`, and `Corpus` must not call expensive
  per-record enrichment by default. They return fields already available from
  the endpoint they intentionally invoke plus compact metadata.
- Expensive or rate-limit-sensitive data must be modeled as explicit entities
  and relations. This includes project languages, project contributors, MR
  approvals, MR changes, MR discussions, MR diff lines, award emoji, job traces,
  snippet file content, blame, compare, and blob search.
- `Get` does not mean "fetch the entire graph." For example, `Get
  gitlab.merge_request` should call MR detail only; agents must use MR
  relations for approvals, commits, changes, discussions, diff lines, and award
  emoji.
- Mirror/index behavior is entity-specific. Adding a richer entity does not make
  base project/MR/pipeline corpus slower unless that richer entity is explicitly
  marked indexable or included in a materialized view.
- Indexable by default in this parity pass:
  `gitlab.project`, `gitlab.user`, `gitlab.group`, and
  `gitlab.user_membership` remain the only default indexed entities.
- Provider-backed but not indexable by default in this parity pass:
  merge requests, commits, pipelines, jobs, repository tree entries, repository
  files, project languages, project contributors, MR approvals, MR changes,
  discussions, award emoji, snippets, snippet files, job traces, blame, compare,
  blob search, and diff lines.
- A later rich-index phase may mark selected read-only entities indexable, but
  that must be a deliberate opt-in change with tests proving the base corpus
  call count did not change.
- `gitlab.activity` is the exception because its purpose is aggregation. It may
  perform bounded per-project calls, but it must limit concurrency to 4 workers
  and must cap the project set to the normalized request limit.
- If an explicit rich entity/relation fails, return that entity/relation error.
  Do not mutate the base record into a partial record except for
  `gitlab.activity`, where per-project partial metadata is part of the
  aggregation contract.
- Do not mark the new transient entities as indexable in the first pass:
  `gitlab.activity`, `gitlab.compare`, `gitlab.blame`, `gitlab.blob_search`,
  `gitlab.project_language`, `gitlab.project_contributor`,
  `gitlab.merge_request_approval`, `gitlab.merge_request_change`,
  `gitlab.snippet_file`, `gitlab.discussion`, `gitlab.award_emoji`, and
  `gitlab.merge_request_diff_line` stay live/provider-backed only.

Bundle/default exposure:

- Add the new entity constants to the default coder GitLab datasource in
  `apps/coder/bundle.go` so authenticated coder sessions can use them without
  app-specific config.
- Keep the datasource name and kind as `gitlab`; do not create separate
  datasource specs for snippets, CI, or repository inspection.
- Update bundle tests that assert the default GitLab entity set.

Client interface additions:

- `GetProjectLanguages(ctx, project)`.
  - Official client: `Projects.GetProjectLanguages`.
- `ListProjectContributors(ctx, project, opts)`.
  - Official client: `Repositories.Contributors`.
- `GetMergeRequestApprovals(ctx, project, iid)`.
  - Official client: `MergeRequests.GetMergeRequestApprovals`.
- `GetMergeRequestChanges(ctx, project, iid, opts)`.
  - Official client: `MergeRequests.GetMergeRequestChanges`.
- `GetMergeRequestCommits(ctx, project, iid, opts)`.
  - Official client: `MergeRequests.GetMergeRequestCommits`.
- `GetMergeRequestDiffVersions(ctx, project, iid, opts)`.
  - Official client: `MergeRequests.GetMergeRequestDiffVersions`.
- `ListMergeRequestDiscussions(ctx, project, iid, opts)`.
  - Official client: `Discussions.ListMergeRequestDiscussions`.
- `CreateMergeRequestDiscussion(ctx, project, iid, opts)`.
  - Official client: `Discussions.CreateMergeRequestDiscussion`.
- `AddMergeRequestDiscussionNote(ctx, project, iid, discussionID, opts)`.
  - Official client: `Discussions.AddMergeRequestDiscussionNote`.
- `ResolveMergeRequestDiscussion(ctx, project, iid, discussionID, opts)`.
  - Official client: `Discussions.ResolveMergeRequestDiscussion`.
- `GetFileBlame(ctx, project, path, opts)`.
  - Official client: `RepositoryFiles.GetFileBlame`.
- `CompareRefs(ctx, project, opts)`.
  - Official client: `Repositories.Compare`.
- `SearchBlobsByProject(ctx, project, query, opts)`.
  - Official client: `Search.BlobsByProject`.
- Personal snippets:
  - `ListSnippets(ctx, opts)` -> `Snippets.ListSnippets`.
  - `GetSnippet(ctx, snippetID)` -> `Snippets.GetSnippet`.
  - `GetSnippetContent(ctx, snippetID)` -> `Snippets.SnippetContent`.
  - `CreateSnippet(ctx, opts)` -> `Snippets.CreateSnippet`.
  - `DeleteSnippet(ctx, snippetID)` -> `Snippets.DeleteSnippet`.
- Pipeline create:
  - `CreatePipeline(ctx, project, opts)` -> `Pipelines.CreatePipeline`.
- Award emoji:
  - `ListMergeRequestAwardEmoji` -> `AwardEmoji.ListMergeRequestAwardEmoji`.
  - `ListMergeRequestAwardEmojiOnNote` ->
    `AwardEmoji.ListMergeRequestAwardEmojiOnNote`.
  - `CreateMergeRequestAwardEmoji` ->
    `AwardEmoji.CreateMergeRequestAwardEmoji`.
  - `CreateMergeRequestAwardEmojiOnNote` ->
    `AwardEmoji.CreateMergeRequestAwardEmojiOnNote`.

These methods are present in `gitlab.com/gitlab-org/api/client-go/v2 v2.24.1`.
Do not add raw HTTP fallbacks for this parity work. Add only thin
`officialClient` wrapper methods around the existing official client services.

Datasource behavior details:

- Keep `defaultPageSize = 20`.
- `Search` should use API search where available. If GitLab only supports list,
  list one bounded page and filter locally by title/path.
- `List` should always support pagination through existing cursor/page helpers.
- `Relation` should be the preferred way to move from summaries to details:
  - Project -> activity, commits, pipelines, jobs, branches, tags,
    repository_tree, users, groups, languages, contributors.
  - Project -> compare is not a relation because it needs `from` and `to`.
  - MR -> diffs, diff_lines, notes, discussions, approvals, changes, commits,
    pipelines, participants, reviewers, award_emoji.
  - Pipeline -> jobs, commit.
  - Job -> trace, pipeline, commit.
  - RepositoryTreeEntry -> file.
  - Snippet -> files.
- For `gitlab.compare`, advertise `Search` only in the first pass, with
  required filters `project_id`, `from`, and `to`. Do not implement `Get` for
  compare in this work.
- For `gitlab.blame`, support `Get` by encoded ID and `Search` with required
  filters. The `Search` form is the main agent-facing path because file paths
  and refs often come from previous tree/search results.
- For `gitlab.blob_search`, do not advertise `List` or `Get`; GitLab exposes
  it as a query endpoint.

Dex flag/filter coverage:

- Datasource requests use `Limit`, `Cursor`, `Query`, `Mode`, and `Filters`
  instead of CLI flags. Do not add a separate output-format layer: compact
  defaults live in `Record.Content`/metadata, and complete typed data lives in
  `Record.Raw`.
- Dex `--no-cache` maps to provider-backed datasource access. Agents should use
  live/provider mode where the caller surface exposes `SearchRequest.Mode`;
  the plugin should not introduce a Dex-style local project cache or cache
  invalidation command.
- Project search/list filters:
  - `query` or `SearchRequest.Query` searches project name/path.
  - `group_path` filters paths under a namespace prefix.
  - `archived`, `visibility`, `owned`, `starred`, `membership` pass through
    when supported by the official client.
  - `order_by` accepts GitLab-supported values such as `id`, `name`, `path`,
    `created_at`, `updated_at`, and `last_activity_at`.
  - `sort` accepts `asc` or `desc`. Dex's `--sort activity` should map to
    `order_by=last_activity_at`; Dex's `--sort created` maps to
    `order_by=created_at`.
- Commit search/list filters: `project_id` is required; accept `ref`,
  `ref_name`, or `branch` as aliases for the GitLab ref, plus `since`, `until`,
  `path`, and `author`. `Get` for a commit must include stats.
- Merge request search/list filters:
  - `state`, `scope`, `source_branch`, `target_branch`, `labels`,
    `author_username`, `assignee_username`, `reviewer_username`, `order_by`,
    and `sort` pass through when the client option supports them.
  - Default list/search behavior should match Dex's agent-friendly default:
    opened MRs and drafts excluded unless the caller supplies state/scope/draft
    filters.
  - `include_wip=true` includes drafts/WIP. Internally GitLab uses draft flags,
    but accept Dex vocabulary because agents will search for it.
  - `conflicts_only=true` may require fetching MR detail for the bounded page;
    apply it after the page fetch and keep the page bounded.
- Pipeline search/list filters: `project_id` is required; accept `status`,
  `ref`, `source`, `sha`, `updated_after`, `updated_before`, `order_by`, and
  `sort` where the client supports them.
- Job list/search filters: `project_id` is required; accept optional
  `pipeline_id`, `scope`, `include_retried`, `job_name`, and `status`.
  `job_name` is a local exact-name filter used for Dex-style log lookup.
- Repository tree filters: `project_id` is required; accept `ref`, `path`, and
  `recursive`.
- Repository file search filters: accept `project_id`, `ref`, `path`,
  `include_content`, and `max_bytes`. Default `ref` is `HEAD` if GitLab accepts
  it; otherwise use the project's default branch when already known from a
  previous project record. `Get` cannot accept filters, so use `Search` when
  metadata-only or stricter `max_bytes` behavior is needed.
- Compare filters: `project_id`, `from`, and `to` are required; accept
  `straight`, `path`, `include_diff`, and `max_bytes`. Without `path`, always
  return summary only even if `include_diff=true`, preserving Dex's protection
  against accidentally dumping large diffs.
- Blame filters: `project_id`, `path`, optional `ref`, optional `range_start`,
  and optional `range_end`.
- Blob search filters: `project_id` and query are required. The query may come
  from `SearchRequest.Query` or the `query` filter.
- Snippet filters: `gitlab.snippet` `List`/`Get` remains metadata-only and
  page-bounded; use `gitlab.snippet_file` search/relation with `snippet_id`,
  optional `file_path`, and optional `max_bytes` to fetch content.

Filter contract:

- Required filters should fail fast with messages of the form
  `gitlab <entity> <field> filter is required`.
- Numeric filters (`merge_request_iid`, `pipeline_id`, `line`, etc.) should
  parse with `strconv.ParseInt` and return a field-specific error on invalid
  input.
- Boolean filters (`straight`, `include_diff`, `internal`, `resolved`) should
  accept Go boolean strings (`true`, `false`, `1`, `0`) through
  `strconv.ParseBool`.
- Time filters (`since`, `updated_after`) should accept RFC3339 timestamps and
  Dex-style relative durations such as `7d`, `4h`, and `30m`. Implement relative
  duration parsing locally in the GitLab plugin if no shared helper exists; do
  not add a broad runtime utility for this plugin-specific need.

Tests:

- Verify new entity specs advertise only supported capabilities.
- Verify source spec includes new entities and data views where relevant.
- Do not add default `DataViews` for transient/live entities in this pass. Keep
  existing project/user/group/membership views stable.
- Add fake-client tests for list/get/search/relation happy paths and missing
  required filters.
- Add fake-client call-count tests proving base `Search`, `List`, `Get`,
  `Relation`, and `Corpus` paths do not call rich endpoints unless the requested
  entity/relation is the rich endpoint itself.
- Add corpus tests proving project/user/group/membership mirroring call patterns
  do not change when rich entities are registered.
- Assert returned `Record.Content` is compact and raw structs contain the full
  typed fields expected by agents.

### Phase 3: Diff Parsing and Inline Comment Ergonomics

Port Dex's review-support concept, not its CLI output.

Files:

- Add `plugins/integrations/gitlab/diffparse.go`
- Add `plugins/integrations/gitlab/diffparse_test.go`
- Update `plugins/integrations/gitlab/models.go`
- Update `plugins/integrations/gitlab/datasource.go`
- Update `plugins/integrations/gitlab/operations_mr.go`

Add models:

- `MergeRequestDiffLine`
  - Entity type: `gitlab.merge_request_diff_line`.
  - ID: `<project>!<iid>!<path>!<new-line-or-old-line>`.
  - Fields: project_id, merge_request_iid, old_path, new_path, line_type
    (`ctx`, `add`, `del`), old_line, new_line, content, hunk_header.
- `InlineCommentTarget`
  - Not necessarily a datasource entity; can be an operation result type.
  - Fields: valid, reason, project_id, merge_request_iid, path, line,
    line_type, old_line, new_line, base_sha, start_sha, head_sha,
    position_type, surrounding_lines.

Diff parser requirements:

- Parse unified diff hunks from GitLab MR diff text.
- Track old and new line counters from hunk headers.
- Mark added, deleted, and context lines separately.
- Support:
  - list all changed files with compact summaries.
  - get parsed lines for one file.
  - search parsed lines with substring or regexp.
  - inspect one target line with configurable context count.
- Reject inline-comment targets that are not present in a diff hunk.
- Warn but still identify deleted lines separately; default inline comments
  should target new-file lines unless an operation explicitly supports old-line
  positions.

Datasource additions:

- Add `MergeRequestDiffEntity` relation `lines` to
  `gitlab.merge_request_diff_line`.
- Add `Search` for `gitlab.merge_request_diff_line` with filters:
  `project_id`, `merge_request_iid`, `path`, `query`, `line`, `context`.
- The validation/dry-run read path is:
  - `query` empty, `line` set: return the target line plus bounded context when
    valid.
  - invalid `line`: return a failed search error that includes available new
    line ranges and whether the requested line is outside the diff.
  - `query` set: return matching parsed lines with old/new line numbers and
    line type.
  - `context` parses as an integer, defaults to 3, and is capped at 20.

Operation additions in `gitlab_mr`:

- Do not add `validate_inline_comment` as a write operation. It is a read helper
  and should be exposed through datasource search/get behavior for
  `gitlab.merge_request_diff_line`, plus a pure helper used internally by
  `inline_comment`.
- Add op `inline_comment`.
  - Required: `project_id`, `merge_request_iid`, `file_path`, `line`, `body`.
  - Implementation must first validate the target and then call GitLab
    discussion creation with a position.
  - Result returns discussion ID, note ID, web URL if available, and target
    line metadata.
  - Use `Discussions.CreateMergeRequestDiscussion` with
    `CreateMergeRequestDiscussionOptions.Position`.
  - Position fields come from `GetMergeRequestDiffVersions` and the parsed diff:
    `base_sha`, `start_sha`, `head_sha`, `old_path`, `new_path`,
    `position_type=text`, and either `new_line` for added/context lines or
    `old_line` only if `line_side=old` is explicitly supplied.
- Add op `reply_discussion`.
  - Required: `project_id`, `merge_request_iid`, `discussion_id`, `body`.
- Add op `resolve_discussion`.
  - Required: `project_id`, `merge_request_iid`, `discussion_id`, `resolved`.
  - Official client: `Discussions.ResolveMergeRequestDiscussion`.
  - This is not listed prominently in Dex's quick reference, but it is a natural
    companion to discussion replies and keeps review workflows complete.

Tests:

- Use Dex-style fixtures for adds, deletes, renames, context lines, empty lines,
  and multiple hunks.
- Verify line search returns only bounded matching lines and includes line
  numbers for agents to reuse.
- Verify dry-run validation explains available ranges on invalid line input.
- Verify inline comment operation does not call the write API when validation
  fails.
- Verify inline comment builds the exact `PositionOptions` for added lines,
  context lines, and explicitly requested old/deleted lines.

### Phase 4: New Write Operations

Keep side-effecting capabilities in typed operations, each with explicit risk,
access descriptors, and operation tests.

Files:

- Add `plugins/integrations/gitlab/operations_pipeline.go`
- Add `plugins/integrations/gitlab/operations_snippet.go`
- Extend `plugins/integrations/gitlab/operations_mr.go`
- Update `plugins/integrations/gitlab/operations.go`
- Update `plugins/integrations/gitlab/plugin.go`
- Update `plugins/integrations/gitlab/plugin_test.go`

Operation: `gitlab_pipeline`

- Input:
  - `op`: enum `create`, `retry`, `cancel`.
  - `project_id`: required.
  - `pipeline_id`: required for `retry` and `cancel`.
  - `ref`: required for `create`.
  - `variables`: optional array of `{key,value,variable_type}` for create.
    `variable_type` maps to GitLab `VariableTypeValue` and should allow
    `env_var` and `file`.
  - Do not implement GitLab pipeline `inputs` in this parity pass; variables
    cover Dex behavior.
- Result:
  - `op`, `project_id`, `pipeline_id`, `status`, `ref`, `sha`, `web_url`,
    `message`.
- Implementation:
  - Move `retry_pipeline` and `cancel_pipeline` behavior out of `gitlab_mr`.
    Remove those ops from `MRActionInput` when introducing `gitlab_pipeline`;
    do not keep compatibility aliases.
  - Register as high-risk write operation because it mutates CI execution.

Operation: `gitlab_snippet`

- Input:
  - `op`: enum `create`, `delete`.
  - `snippet_id`: required for delete.
  - `title`: required for create.
  - `description`: optional.
  - `visibility`: enum `private`, `internal`, `public`; default `private`.
  - `files`: required for create, array of `{file_path, content}`.
- Result:
  - `op`, `snippet_id`, `title`, `visibility`, `web_url`, `message`.
- Implementation:
  - Do not read local files in the operation. Agents should pass content
    explicitly; filesystem reads belong to filesystem tools/systems, not the
    GitLab operation.
  - Use personal snippets, not project snippets, because Dex's `snippet`
    commands operate on the authenticated user's personal snippets.
  - Support multi-file create through `CreateSnippetOptions.Files`. Do not use
    legacy single-file `file_name`/`content` fields.

Operation: extend `gitlab_mr`

- Extend op `create`.
  - Required: `project_id`, `source_branch`, `target_branch`, `title`.
  - Optional: description, labels, assignee_ids, reviewer_ids, draft, squash,
    should_remove_source_branch.
  - Do not infer project or current branch from the local Git checkout inside
    the GitLab operation. Dex can do that as a shell-local CLI; agentruntime
    operations should be deterministic network operations over explicit input.
    A caller that wants current-branch ergonomics can discover that through git
    tooling and pass the values explicitly.
- Add op `edit`.
  - Optional fields: title, description, target_branch, labels_to_add,
    labels_to_remove, assignee_ids, reviewer_ids, draft, squash,
    should_remove_source_branch.
  - Model `draft`, `squash`, and `should_remove_source_branch` as pointer-bool
    fields internally so omitted is distinct from false. This is required to
    support Dex's `--draft`/`--no-draft` and `--squash`/`--no-squash`
    behavior.
  - Require at least one update field.
- Extend `close` and `reopen`.
  - Optional field: `reason`.
  - If `reason` is non-empty, create a regular MR note with that body before
    changing state. Return both the state-change result and the note ID. If the
    note creation fails, do not change state; this preserves the user's stated
    reason instead of silently dropping it.
- Add op `reply_discussion`.
- Add op `inline_comment`.
- Add op `react`.
  - Required: `emoji`.
  - Optional: `note_id` to react to a note instead of the MR.
  - Normalize optional leading/trailing `:` from emoji names before passing to
    GitLab.
- Verify and preserve the existing Dex merge ergonomics already present in
  `MRActionInput`:
  - `should_remove_source_branch`
  - `squash`
  - `auto_merge` / merge when pipeline succeeds
  - merge commit message
  - squash commit message

Operation registration expectations:

- Current tests expect six operations. Update to the final count:
  - `gitlab_mr`
  - `gitlab_repo_file`
  - `gitlab_branch`
  - `gitlab_tag`
  - `gitlab_commit`
  - `gitlab_ci_variable`
  - `gitlab_pipeline`
  - `gitlab_snippet`
- Keep operation names instance-named through `operationruntime.NewNamedInstance`.

Operation semantics:

- `gitlab_pipeline`
  - `create`: effects `network`, `write_external`, `create`; high risk;
    non-idempotent.
  - `retry`/`cancel`: effects `network`, `write_external`, `update`; high risk;
    non-idempotent.
- `gitlab_snippet`
  - `create`: effects `network`, `write_external`, `create`; high risk;
    non-idempotent.
  - `delete`: effects `network`, `write_external`, `delete`, `destructive`;
    critical risk; non-idempotent.
- `gitlab_mr`
  - `edit`, `comment`, `reply_discussion`, `inline_comment`, `react`,
    `resolve_discussion`, `approve`, `unapprove`, `rebase`, `merge`,
    `close`, and `reopen` remain network write operations.
  - `merge` and `close` stay high risk; do not mark them critical because they
    are reversible or controlled by GitLab permissions/policy, but they still
    require approval under normal write policy.
- Split the current shared `gitlabWriteSpec` helper into smaller constructors
  so delete and critical-risk operations advertise accurate effects instead of
  hiding all actions behind one generic create/update effect set.

Tests:

- Validate required fields per op.
- Assert official client option structs receive the exact values supplied.
- Assert write operations request GitLab network write access.
- Assert no local filesystem or process access descriptors are requested.

### Phase 4.5: Migration/Cleanup of Current Pipeline Ops

The current `gitlab_mr` operation includes `retry_pipeline` and
`cancel_pipeline`, which are CI actions rather than MR actions. When
`gitlab_pipeline` lands:

- Remove `retry_pipeline` and `cancel_pipeline` from `MRActionInput` and
  `executeMRAction` in the same change that adds `gitlab_pipeline`.
- Update tests that currently exercise pipeline actions through `gitlab_mr` to
  exercise them through `gitlab_pipeline`.
- Do not leave deprecated aliases or compatibility wrappers. This repo is still
  pre-1.0 and AGENTS.md explicitly says to replace stale shapes.
- Keep changelog language clear: pipeline retry/cancel moved from the MR
  operation into a dedicated pipeline operation.

### Phase 5: Documentation and Agent Guidance

Add docs that let an agent use the native plugin without rediscovering shapes.

Files:

- Add `docs/plugins/gitlab.md`
- Update `plugins/integrations/gitlab/README.md`
- Update `docs/configuration.md` only if new datasource entities need a config
  example.
- Update `CHANGELOG.md` when implementing behavior, because GitLab capability
  changes are user-visible. For this plan-only file, do not update changelog.

`docs/plugins/gitlab.md` content:

- Explain the agent-first model:
  - Search/list for compact discovery.
  - Relation/get for full details.
  - Operation calls only for side effects.
  - Prefer `project/path!iid` MR refs.
  - Native operation string fields preserve Markdown, newlines, and backticks
    because they are not parsed by a shell. The Dex temp-file advice still
    applies only when a human chooses to wrap these operations in shell
    commands.
  - There is no GitLab-specific browser-open operation. MR/project/pipeline
    records must expose `web_url`; callers that have a generic browser surface
    can open that URL directly.
- Include native examples for:
  - Find a project.
  - Use live/provider search instead of the semantic index when fresh project
    data is required.
  - List open MRs, including draft and conflict filters.
  - Inspect MR summary, diffs, discussions, and pipelines.
  - Open an MR by taking the MR record's `web_url` and passing it to the
    caller's browser/open capability.
  - Search a diff line before commenting.
  - Dry-run validate an inline comment target.
  - Post inline comment or discussion reply.
  - Close/reopen with a reason note.
  - Create, retry, cancel pipeline.
  - Fetch a job trace by job ID and by pipeline ID plus job name.
  - Read file metadata through repository file search, fetch bounded content by
    `Get`, and use blame, compare refs, and blob search.
  - List/show snippet metadata, fetch snippet file content through relation or
    search, and create/delete snippets.
- Include token guidance:
  - Avoid fetching raw file/diff/trace content until a path or job is known.
  - Use relation limits and cursors.
  - Use structured record IDs from prior results.

## Detailed Parity Matrix

| Dex capability | agentruntime target |
|---|---|
| Dex output flags `-o json`, `-o yaml`, `--compact` | Native datasource result split: compact `Record.Content`/metadata and full typed `Record.Raw`; no plugin output-format code |
| `dex gl activity` | `gitlab.activity` list/search with `since` filter |
| `dex gl index`, project autocomplete, `proj ls --no-cache` | Existing datasource semantic index plus provider/live mode; no Dex cache clone |
| `dex gl proj ls/show`, `--sort`, `-d`, group prefix | Existing project entity plus explicit languages/contributors relations; project filters `group_path`, `order_by`, `sort` |
| `dex gl commit ls/show`, `--since`, `--branch` | Existing commit entity plus stats; commit filters `since`, `until`, `ref`/`branch`, `path`, `author` |
| `dex gl mr ls/show`, `--state`, `--scope`, `--include-wip`, `--conflicts-only` | Existing MR entity plus explicit approvals, changes, discussions, commits, and diff-line relations; MR filters for state/scope/draft/conflicts |
| `dex gl mr diff` | MR diff entity plus `gitlab.merge_request_diff_line` relation/search |
| `dex gl mr open` | MR `web_url` plus caller/generic browser capability; no GitLab-specific open op |
| `dex gl mr comment` | `gitlab_mr` ops `comment`, `reply_discussion`, `inline_comment` |
| `dex gl mr comment --dry-run` | read-only inline-target validation through `gitlab.merge_request_diff_line` search/get helpers |
| `dex gl mr react` | `gitlab_mr` op `react` |
| `dex gl mr close/reopen --reason`, approve, merge | Existing `gitlab_mr`, with close/reopen reason note and extended merge fields |
| `dex gl mr create/edit` | Existing create extended with draft/squash/remove-source-branch plus new `edit` op with pointer-bool fields |
| `dex gl pipeline ls/show/jobs/logs` | Existing pipeline/job/job_trace entities and relations; add job-trace search by pipeline ID plus job name |
| `dex gl pipeline create/retry/cancel` | New `gitlab_pipeline` operation |
| `dex gl snippet ls/show --no-content/create/delete` | New snippet metadata entity, snippet_file content relation/search, and `gitlab_snippet` operation |
| `dex gl file show/meta` | Existing repository_file get plus repository_file search for metadata-only and bounded content control |
| `dex gl file blame` | New `gitlab.blame` entity |
| `dex gl tree --ref --path --recursive` | Existing repository_tree entity with explicit tree filters |
| `dex gl diff --path --straight` | New `gitlab.compare` entity with summary-by-default and scoped diff preview |
| `dex gl search blobs` | New `gitlab.blob_search` search entity |
| Dex multiline shell-content warning | Native typed operation fields preserve multiline Markdown; docs warn only for shell wrappers |
| Dex command aliases (`gl`, `pipe`, `snip`) | Not applicable; native operation/entity names are canonical and documented |

## Acceptance Criteria

- An agent can complete a full MR review workflow without shelling out to Dex:
  find project, list MRs, inspect MR details, inspect changed files, search or
  validate target lines, comment inline, reply to discussions, approve or merge.
- An agent can debug CI without Dex:
  list pipelines, inspect jobs, fetch bounded traces, retry/cancel/create a
  pipeline.
- An agent can inspect repository state without cloning:
  list tree, read file metadata/content, blame a file, compare refs, search
  blobs.
- An agent can manage personal snippets at Dex parity:
  list/show/create/delete.
- Default datasource responses remain compact and bounded. No list/search call
  should return unbounded diff, file, snippet, or trace bodies.
- Base datasource and corpus paths do not slow down from hidden enrichment:
  fake-client tests prove no languages, contributors, approvals, changes,
  discussions, traces, file contents, blame, compare, or blob-search endpoints
  are called unless the caller requested that explicit entity or relation.
- Rich GitLab data is exposed through first-class datasource entities/relations,
  and only project/user/group/membership remain indexed by default in this
  parity pass.
- All new side effects go through typed operations with safety descriptors and
  tests.
- `go test ./plugins/integrations/gitlab ./apps/launch ./apps/coder` passes.
- If shared datasource or operation contracts are changed, `go test
  ./internal/architecture` and the relevant broader package set pass.

## Implementation Notes and Risks

- GitLab API version and official client coverage differ from Dex's
  `github.com/xanzy/go-gitlab` usage, but the required parity endpoints are
  available in `gitlab.com/gitlab-org/api/client-go/v2 v2.24.1`. Prefer the
  current official client module already used by the plugin, and add thin
  `officialClient` wrapper methods only.
- Some GitLab data is expensive or permission-dependent:
  languages, contributors, approvals, changes, discussions, traces, file
  content, blame, compare, and blob search may fail for projects the token can
  otherwise see. Keep these behind explicit entities/relations and return hard
  errors from those requested read paths; do not degrade base records into
  partial records except for `gitlab.activity`.
- Inline comment positions require current MR diff refs (`base_sha`,
  `start_sha`, `head_sha`). Fetch the MR version/diff metadata during
  validation and reuse it for the write call.
- `internal` snippet visibility is not accepted on GitLab.com SaaS. Let GitLab
  return the provider error; do not hard-code host-specific policy beyond clear
  validation of the enum.
- Do not add admin operations in this work. The existing README explicitly keeps
  admin mode behind a future feature flag design.

## Verification Commands

Run focused checks during implementation:

```bash
go test ./plugins/integrations/gitlab
go test ./apps/launch ./apps/coder
```

Run broader checks only if shared contracts are touched:

```bash
go test ./core/datasource ./runtime/datasource ./orchestration/resourcecatalog
go test ./internal/architecture
```

Run `task verify` only when preparing a commit or when the final implementation
touches enough shared behavior to justify the full local quality gate.
