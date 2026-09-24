# Source-built Bootstrap

> **Supported source-built Apple-Silicon path.** The repository contains
> executable plan, install, upgrade, prune, uninstall, status, generation, and
> retention paths. Disposable-home tests pass all 35 transaction-boundary
> crash cases; a real Codex session discovered the managed skill, read the
> matching procedure, and stopped before repository writes pending distinct
> approval. This is not a signed, notarized, or no-Go prebuilt release.

V1 removal recovery covers completed command boundaries and is bounded by the
recorded transaction and complete payload
identity. A crash before `transaction.json` is published can leave an
unattributed staging file; a crash inside `rm -rf` can leave a partial
generation. Both states fail closed for manual inspection. Recovery does not
delete those uncertain paths automatically.

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
