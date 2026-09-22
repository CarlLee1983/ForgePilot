# Unsigned trial assets

`scripts/release/build_trial_assets.sh` creates reviewable **unsigned maintainer trial**
artifacts from one explicitly supplied, lowercase 40-character commit SHA. It
is a distribution helper outside the ForgePilot governance CLI; it neither
writes `.forgepilot/` nor runs `migrate`, and it does not create commits or
tags.

```sh
scripts/release/build_trial_assets.sh --output /tmp/forgepilot-trial <full-commit-sha>
```

Without `--output`, the output directory is
`dist/trial-assets/<full-commit-sha>`. The entrypoint extracts the requested
commit into an isolated temporary source tree, builds both `darwin/arm64` and
`darwin/amd64`, and publishes the complete staged directory only after every
check succeeds. Repeating the same commit is safe and produces the same asset
names and provenance fields.

An explicit output must be outside the source repository or beneath its ignored
`dist/` directory. The builder refuses paths that could replace source files,
Git metadata, or `.forgepilot/` state.

The architecture derivation rule is deliberately channel-free:

```sh
scripts/release/build_trial_assets.sh --asset-stem arm64
# forgepilot-darwin-arm64
```

The trial channel is an independent suffix, yielding
`forgepilot-darwin-arm64-unsigned-trial` and
`forgepilot-darwin-amd64-unsigned-trial`. A signed release candidate can reuse
the architecture stems without inheriting this unsigned-trial marker.

Each output directory contains:

- the two Mach-O assets;
- `SHA256SUMS`, generated with macOS `shasum -a 256`;
- `provenance.json`, with the input commit, a deterministic
  `0.0.0+commit.<full-commit-sha>` version, channel and target architecture for
  every asset; and
- `startup-evidence.json`, plus the matching-host CLI `--help` output.

The builder checks each Mach-O architecture with `file`. It directly runs the
asset matching the builder host architecture and records that startup result.
The other architecture is explicitly recorded as requiring its native host;
native cross-architecture installation acceptance belongs to the later release
work, not to this trial build.

These artifacts are not Developer ID signed, notarized, formally supported, or
evidence of native installation acceptance. Do not use them as a formal macOS
release or onboarding input. A future optional maintainer-trial installer may
consume this manifest only under its own separately reviewed contract.

## CI publication

`publish-trial-assets.yml` makes this same trial channel repeatable without making
it a normal-user installer. It has only a manual `workflow_dispatch` trigger. The
maintainer supplies one lowercase full 40-character commit SHA reachable from
`main`; CI derives `unsigned-trial/<commit>` rather than accepting an independent
release tag. A dispatch against another ref is skipped: the publishing control
plane in this reviewed workflow revision only runs from the default branch. The
repository-writer limitation below still applies.

Before enabling it, a repository administrator must enable GitHub **immutable
releases**, create the `unsigned-trial-publish` environment with a required
maintainer reviewer, create an `unsigned-trial-stage` environment, and limit both
environments with **Selected branches → exactly `main`** (not “protected branches
only”). Protect `unsigned-trial/*` tags from update or
deletion too. Put these **environment-scoped**, not repository-scoped, secrets in
both environments: `RELEASE_WRITE_TOKEN` (fine-grained Contents read/write) and
`RELEASE_GUARD_TOKEN` (Administration read plus Actions read). The guard token only
checks immutable-release and both environments' Selected branches → exactly `main`
policies immediately before every release write; publish also checks its required
reviewer. The write token alone can create a draft or publish it. Repository Actions
settings must force the default `GITHUB_TOKEN` to read-only. The workflow itself asks
only for `contents: read`, so a normal run does not receive incidental write
authority. This is defense in depth, not a separation from repository writers:
GitHub allows a writer who can modify and dispatch a workflow on another ref to
request a write-capable `GITHUB_TOKEN`. Treat all repository write/Actions-dispatch
access as release-authorized. If that is not an acceptable trust model, publish from
a separate controlled release repository or use organization workflow execution
protections. The workflow cannot create or weaken these repository controls.

After this change is merged, an administrator can run the repeatable manual
setup wizard; it opens the exact GitHub settings pages, asks before each external
write, and requires freshly pasted hidden values rather than reusing either token from
the ambient `.env` file:

```sh
scripts/release/configure_trial_publish.sh
```

The workflow checks out the exact commit, proves it is reachable from `main`,
runs `make verify` and the race gate, builds and revalidates all six bundle files,
and creates a draft prerelease. The draft remains reviewable until the protected
environment approves publication. A retry only uploads missing files whose
digest matches the rebuilt bundle; it never overwrites or deletes an asset.

The published notes retain the unsigned/notarization/Gatekeeper limitations. CI
artifacts, an immutable GitHub Release, and checksum/provenance provide transport
integrity and review evidence; they do not change Apple execution trust or the
supported source-built onboarding path.
