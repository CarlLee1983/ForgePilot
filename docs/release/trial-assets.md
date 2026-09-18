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
