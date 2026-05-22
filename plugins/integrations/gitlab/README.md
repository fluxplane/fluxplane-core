# GitLab Plugin Implementation Status

Implemented write operations:

- Merge requests: create, edit, close, reopen, comment, inline comment with diff-position validation, discussion reply/resolve, emoji reaction, approve, unapprove, merge, rebase. Close and reopen can attach a reason note before the state change.
- Repository files: create, update, delete.
- Branches: create, delete, delete merged branches.
- Tags: create, delete.
- Commits: create a commit with multiple file actions through the GitLab Commits API.
- CI/CD variables: create, update, delete project variables.
- Pipelines: create, retry, cancel.
- Personal snippets: create, delete.

Implemented read surfaces:

- Project, group, user, membership, activity, merge request, diff, parsed diff line, note, discussion, approval, change summary, award emoji, pipeline, branch, tag, commit, repository tree, repository file, compare, blame, blob search, project language, project contributor, job, and bounded searchable job trace datasource entities.
- Personal snippets are metadata-only by default through `gitlab.snippet`; bounded content is explicit through the `files` relation or `gitlab.snippet_file` search/get.

Agent-facing usage examples live in [docs/plugins/gitlab.md](../../../docs/plugins/gitlab.md).

Backlog:

- Admin operations.
- Issues.
- Releases.
- Packages.
- Wiki.
- Deployments.
- Environments.

Admin operations are intentionally not registered yet. They should be added behind an explicit feature flag when admin mode is designed.
