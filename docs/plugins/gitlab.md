# GitLab Plugin

> **Legacy core note:** the core `plugins/integrations/gitlab` implementation is
> retained as a legacy compatibility package. Active GitLab integration surfaces
> should be provided through dex via
> `github.com/fluxplane/fluxplane-dex/fluxplaneplugin`. See
> [Legacy integrations](legacy-integrations.md).

GitLab is exposed as native datasource entities and typed operations. Use
datasource list/search for compact discovery, get/relation for targeted details,
and operations only for side effects.

## Read Model

Useful entity names:

- `gitlab.project`: projects by id, path, text, and common filters.
- `gitlab.merge_request`: merge request summaries and detail by `project!iid`.
- `gitlab.merge_request_diff`, `gitlab.merge_request_diff_line`,
  `gitlab.merge_request_note`, `gitlab.discussion`,
  `gitlab.merge_request_approval`, `gitlab.merge_request_change`, and
  `gitlab.award_emoji`: MR review data.
- `gitlab.pipeline`, `gitlab.job`, and `gitlab.job_trace`: CI inspection.
- `gitlab.repository_tree`, `gitlab.repository_file`, `gitlab.compare`,
  `gitlab.blame`, and `gitlab.blob_search`: repository browsing and
  inspection.
- `gitlab.activity`, `gitlab.project_language`, and
  `gitlab.project_contributor`: project overview and enrichment.
- `gitlab.snippet`: personal snippet metadata.
- `gitlab.snippet_file`: explicit bounded snippet content.

Prefer stable IDs returned by previous records. Merge requests use
`project!iid`, for example `group/subgroup/project!123` or `742!123`.

Snippet metadata is intentionally content-light. Fetch content only after a
snippet is known:

```text
List gitlab.snippet
Relation gitlab.snippet id=77 relation=files
Search gitlab.snippet_file filters snippet_id=77 file_path=notes.md max_bytes=4096
```

Job traces are similarly bounded and explicit:

```text
Relation gitlab.job id=group/project!123 relation=trace
Get gitlab.job_trace id=group/project!123!trace
Search gitlab.job_trace filters project_id=group/project pipeline_id=456 job_name=test max_bytes=32768
```

Inline review targets are validated through parsed diff lines before a write:

```text
Search gitlab.merge_request_diff_line filters project_id=group/project merge_request_iid=123 path=runtime.go line=42 context=5
```

## Write Operations

Operations are named per plugin instance, for example `gitlab_mr`,
`gitlab_pipeline`, and `gitlab_snippet`.

Merge request examples:

```json
{"op":"create","project_id":"group/project","source_branch":"feature","target_branch":"main","title":"Add feature","draft":true}
{"op":"edit","project_id":"group/project","merge_request_iid":123,"labels_to_add":["reviewed"],"squash":true}
{"op":"close","project_id":"group/project","merge_request_iid":123,"reason":"Superseded by !124"}
{"op":"comment","project_id":"group/project","merge_request_iid":123,"body":"Looks good to me."}
{"op":"inline_comment","project_id":"group/project","merge_request_iid":123,"file_path":"runtime.go","line":42,"body":"Please check this edge case."}
{"op":"reply_discussion","project_id":"group/project","merge_request_iid":123,"discussion_id":"abc123","body":"Addressed in the latest push."}
{"op":"resolve_discussion","project_id":"group/project","merge_request_iid":123,"discussion_id":"abc123","resolved":true}
{"op":"react","project_id":"group/project","merge_request_iid":123,"emoji":"thumbsup"}
```

Pipeline examples:

```json
{"op":"create","project_id":"group/project","ref":"main","variables":[{"key":"ENV","value":"staging","variable_type":"env_var"}]}
{"op":"retry","project_id":"group/project","pipeline_id":456}
{"op":"cancel","project_id":"group/project","pipeline_id":456}
```

Snippet examples:

```json
{"op":"create","title":"Review notes","visibility":"private","files":[{"file_path":"notes.md","content":"# Notes\n"}]}
{"op":"delete","snippet_id":77}
```

Native operation string fields preserve Markdown, newlines, and backticks
because they are not parsed by a shell.

## Token Guidance

Keep default calls compact. Avoid fetching raw file, diff, trace, or snippet
content until the project, path, job, or snippet is known. Use relation limits,
cursors, and `max_bytes` where supported.

There is no GitLab-specific browser-open operation. Project, MR, pipeline, job,
and snippet records expose `web_url`; callers with a generic browser capability
can open that URL directly.
