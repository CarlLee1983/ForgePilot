# Source-built Bootstrap development status

> **Not a supported installer yet.** The repository contains an incomplete
> development slice for `status`, `generation-v1 current`, and `retention-v1`; lifecycle commands such as
> `plan`, `install`, `upgrade`, `prune`, and `uninstall` are unavailable. Today,
> use the source-built onboarding procedure and short prompt in this directory.

The intended Bootstrap makes a developer-obtained ForgePilot source version
available to a Codex user without inspecting or initializing any repository. It
remains source-built: the user supplies an absolute local source checkout and a
complete 40-character commit SHA, and it requires an already-installed
compatible Go toolchain on an Apple-Silicon Mac.

```text
./scripts/forgepilot-bootstrap plan --agent codex --source <absolute-local-source> --commit <sha>
./scripts/forgepilot-bootstrap install --agent codex --source <absolute-local-source> --commit <sha> --approve <plan-id>
```

The first command is inspection-only. The second is a separate explicit
approval for user-home changes only. It repeats the shown inputs and only runs
if their recomputed plan still matches `<plan-id>`. It stages, builds, and
verifies the exact source, then exposes a managed `forgepilot` CLI and Codex
skill through one current-generation pointer. It does not use `curl | sh`,
download an unsigned binary, contact a remote source, install Go, change a
shell profile, access Agent credentials, or initialize a repository. The
approved source build and `make verify` are intentionally not sandboxed:
their repository-defined logic can have local or network effects, so only use
a source checkout and SHA you trust.

After Bootstrap, Repository Onboarding remains separate: invoke the installed
skill with an explicit target repository and COMMIT or SNAPSHOT Candidate,
review the plan and Story, then explicitly approve any `forgepilot init` /
Goal / Work Item writes. The zero-install alternative remains [the short
onboarding prompt](onboarding-prompt.md).

The complete accepted contract and future test requirements are in [the
source-built Bootstrap specification](../specs/source-built-bootstrap.md).
