# GitLab Plugin Implementation Status

Implemented write operations:

- Merge requests: create, close, reopen, comment, approve, unapprove, merge, rebase, retry pipeline, cancel pipeline.
- Repository files: create, update, delete.
- Branches: create, delete, delete merged branches.
- Tags: create, delete.
- Commits: create a commit with multiple file actions through the GitLab Commits API.
- CI/CD variables: create, update, delete project variables.

Backlog:

- Admin operations.
- Issues.
- Releases.
- Packages.
- Wiki.
- Deployments.
- Environments.

Admin operations are intentionally not registered yet. They should be added behind an explicit feature flag when admin mode is designed.
