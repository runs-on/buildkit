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

## Published tags

Each patched release is published to
`public.ecr.aws/c5h5o9k1/runs-on/buildkit` for `linux/amd64` and
`linux/arm64` under its immutable tag, for example `v0.33.0-runs-on.1`.
After the image passes a smoke test on both architectures, final releases also
move two tags forward:

- `v<major>.<minor>-runs-on`, for example `v0.33-runs-on`: the latest release of
  that line.
- `buildx-stable-1`: the latest release overall, like upstream's
  `moby/buildkit:buildx-stable-1`.

Moving tags only go forward: a later patch release of an older line never takes
`buildx-stable-1` back. Each one records the release it points to in the
`org.opencontainers.image.version` annotation of its index:

```console
$ docker buildx imagetools inspect public.ecr.aws/c5h5o9k1/runs-on/buildkit:buildx-stable-1 \
    --format '{{index .Manifest.Annotations "org.opencontainers.image.version"}}'
v0.33.0-runs-on.1
```
