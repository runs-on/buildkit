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
