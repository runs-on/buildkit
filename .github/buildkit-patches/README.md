# RunsOn BuildKit patch sets

Each directory contains the ordered `git format-patch` series for one upstream
BuildKit release line. The upstream tag synchronization workflow applies the
matching series with `git am`; any missing patch set or merge conflict fails the
workflow before a RunsOn tag is created.

Upstream tags are kept immutable. A successfully patched upstream tag such as
`v0.32.0` is published as `v0.32.0-runs-on.1` in Git and in public ECR.

`sync-after-tag` marks the last upstream tag handled when this automation was
introduced. The workflow considers every subsequently created upstream `v*`
tag, including patch releases made from older release branches.

When a new upstream release line needs a rebase:

1. Reapply and validate the patch series on the new upstream tag.
2. Add the resulting format-patch files under a new `v<major>.<minor>` directory.
3. Rerun the failed synchronization workflow.

