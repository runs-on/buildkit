# RunsOn BuildKit patch sets

Each directory contains the ordered `git format-patch` series for one upstream
BuildKit release line. The upstream tag synchronization workflow applies the
matching series with `git am`; a merge conflict fails the workflow before a
RunsOn tag is created. Releases of a line without a patch set are skipped with
a warning on the workflow run.

Upstream tags are kept immutable. A successfully patched upstream tag such as
`v0.32.0` is published as `v0.32.0-runs-on.1` in Git and in public ECR.

`sync-after-tag` marks the last upstream tag handled when this automation was
introduced. The workflow considers every subsequently created final upstream
release tag (`vX.Y.Z`), including patch releases made from older release
branches. Release candidates are skipped.

When upstream starts a new release line, the daily sync tries to port the
previous line's series to it (`port-patch-set.yml`):

1. It applies the series to the line's first release with `git am --3way`,
   dropping patches upstream already contains: cherry-picks whose original
   commit is in the release, or patches with no changes left.
2. It builds the result, runs the unit tests of the patched packages and the
   cache integration tests, and checks that the series applies to every
   release of the line.
3. If everything passes, it opens a pull request adding the
   `v<major>.<minor>` directory. Merging it lets the sync publish the line.
   Otherwise it opens an issue with the conflict or the failed check.

It only ports from the immediately previous line. It skips a line while its
`patch-set/<line>` branch exists, while an issue for it is open, or while an
open pull request adds its directory by hand. Close the issue or delete the
branch to retry, or run the workflow by hand with `dry-run` to preview a port.

