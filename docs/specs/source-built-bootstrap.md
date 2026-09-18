# Source-built Bootstrap — accepted implementation contract

## Status and boundary

This is an accepted design contract for a future implementation; it does not mean `scripts/forgepilot-bootstrap` exists today. The governing decisions are [ADR-0030](../adr/0030-source-built-onboarding-without-apple-developer.md), [ADR-0032](../adr/0032-formal-macos-onboarding-is-apple-silicon-only.md), and [ADR-0033](../adr/0033-source-built-bootstrap-separates-distribution-from-onboarding.md). Existing FP-33 through FP-35 evidence describes the previous coupled procedure and remains historical evidence.

Bootstrap is a repository-external POSIX-shell distribution surface. It installs one source-built ForgePilot CLI and one Codex standalone skill into the current user's home. It is not a ForgePilot core command, plugin, release downloader, or Repository Onboarding.

## Invocation and protocol

From a developer-obtained local ForgePilot checkout, the future interface is:

```text
./scripts/forgepilot-bootstrap plan --agent codex --source <absolute-local-checkout> --commit <lowercase-40-character-sha>
./scripts/forgepilot-bootstrap install --agent codex --source <absolute-local-checkout> --commit <lowercase-40-character-sha> --approve <plan-id>

./scripts/forgepilot-bootstrap plan --upgrade --agent codex --source <absolute-local-checkout> --commit <lowercase-40-character-sha>
./scripts/forgepilot-bootstrap install --upgrade --agent codex --source <absolute-local-checkout> --commit <lowercase-40-character-sha> --approve <plan-id>

./scripts/forgepilot-bootstrap status

./scripts/forgepilot-bootstrap plan --uninstall --commit <lowercase-40-character-sha>
./scripts/forgepilot-bootstrap uninstall --commit <lowercase-40-character-sha> --approve <plan-id>

./scripts/forgepilot-bootstrap plan --prune
./scripts/forgepilot-bootstrap prune --approve <plan-id>
```

`plan` has four mutually exclusive modes: initial-or-idempotent installation (the default), explicit upgrade, exact-generation uninstall, and prune. Every mode is inspection-only and persists no approved-plan artifact. Every mutating command repeats its operation-specific inputs, takes the exclusive Bootstrap lock, recomputes the canonical plan and identifier from those inputs and the locked managed-state facts, and proceeds only if they still match `--approve <plan-id>`; this is the explicit Bootstrap Approval. The identifier binds the protocol version, operation kind, explicit inputs, ordered action list, and inspected current, previous, retained-generation, transaction, ownership, mode, and link facts.

The source for install and upgrade must be an absolute local Git checkout. Remote URLs, scp references, credential-bearing URLs, branches, tags, `main`, `latest`, abbreviated SHA, ambient Git routing, and substituting the installer working tree for `--source` are rejected. Default installation refuses a valid installation whose current commit differs and directs the caller to explicitly re-plan with `--upgrade`. Upgrade requires a valid idle installation and a commit different from current; it never silently converts an initial install or same-SHA invocation into an upgrade. `uninstall --commit` removes exactly one manifest-proven inactive generation. `prune` binds and removes the exact ordered set of all manifest-proven inactive generations present at planning time. Planning and execution both refuse current, previous, unknown, substituted, or drifted paths.

`status` accepts no approval or source inputs and performs no recovery or mutation. It is an unlocked point-in-time read: it reads the atomically published manifest and pointers, rechecks transaction and observed facts before returning, and returns nonzero rather than presenting a coherent result if they changed during the read. It exits successfully only for a valid idle managed layout. An absent or invalid lock marker, unknown schema, unsafe ownership or mode, drift, a non-idle transaction, or changed read facts is reported and returns nonzero. This avoids claiming a POSIX-shell shared-lock primitive that macOS does not provide; only mutations take the Bootstrap exclusive lock. When a transaction is non-idle, unrelated plans fail closed. Only re-planning the same operation with the exact recorded identities may produce a recovery plan; executing that freshly approved matching operation may finish or restore the recorded transaction. `status` never repairs it.

Install and upgrade support Darwin arm64 only and require Git, `make`, and an already-installed Go toolchain compatible with the `go` directive at the exact source commit. They do not install Go, a package manager, dependencies, or a shell profile. Unsupported host, missing prerequisite, malformed identity, unsafe source checkout, or unavailable exact commit fails before staging or user-home writes. Status, uninstall, prune, and their plans require neither source nor those prerequisites and execute no source-controlled code.

Before approval Bootstrap executes no source-controlled code. Its controlled Git operations unset ambient Git routing, disable lazy fetch and replacement objects, ignore system/global config, disable credential helpers and askpass, and reject source attributes that would run checkout filters or transform source bytes. After approval, `go install` and the repository-owned `make verify` deliberately execute the declared source; they are not a sandbox and may have repository-defined local or network effects. Bootstrap never treats those effects as its own credential or network capability and never retains their raw output or environment.

The human renderer and `--format actions` renderer share one action list. The machine stream begins with `forgepilot-bootstrap-plan-v1` followed by NUL and then NUL-delimited phase, action ID, working directory, effect, argument count, and argv. An executor rejects unknown versions, malformed or truncated records, duplicate or reordered IDs, unexpected commands, path escapes, and facts that changed after approval. It evaluates argv, never generated shell text.

## Managed generation and recovery

```text
~/.local/share/forgepilot/
  lock
  manifest.json
  transaction.json                         # authoritative while non-idle
  current -> versions/<commit>
  previous -> versions/<commit>            # optional
  versions/<commit>/
    bin/forgepilot
    skills/codex/forgepilot-onboarding/
    docs/release/onboarding.md
~/.local/bin/forgepilot -> ~/.local/share/forgepilot/current/bin/forgepilot
~/.agents/skills/forgepilot-onboarding
  -> ~/.local/share/forgepilot/current/skills/codex/forgepilot-onboarding
```

The approved action order is: prerequisite and source inspection; Bootstrap lock acquisition; staging; detached checkout of exact source commit; exact-commit Go compatibility check; user-owned `GOBIN` build; source `make verify`; staged `forgepilot --help`; immutable generation preparation; authoritative transaction record; generation publication; `current` switch; manifest completion. Revalidation immediately precedes every mutation. In particular, no stable external link or `current` pointer is created before the transaction record exists.

A generation is immutable after verification. The managed lock marker is created only during initial installation and retained thereafter; mutations take the Bootstrap exclusive lock, while `status` only validates that marker during its unlocked point-in-time read. `current` is the only pointer replaced on upgrade, by same-directory atomic rename while holding that lock. External CLI and skill links are created only on fully staged initial install and are never rewritten on upgrade; both resolve through `current`. The manifest is atomically replaced under the lock and inventories every retained verified generation by commit and exact managed root; current and previous reference entries in that inventory. It also stores only schema version, expected link targets, and in-progress transaction identity. It contains no target repository, `.forgepilot/` state, credential, environment value, or raw subprocess output.

An upgrade records an authoritative transaction with `old_current`, `old_previous`, `new_current`, and phase `prepared`; it prepares `current.new`, changes phase to `switching`, atomically replaces `current`, writes `previous` from `old_current`, atomically writes the completed manifest, then removes the transaction record. An initial installation records the same transaction before any external-link or `current` mutation, with explicitly absent `old_current` and `old_previous`; after that record, it creates each expected stable link (which may deliberately dangle through `current`), changes phase to `switching`, atomically publishes `current`, atomically writes the initial manifest, then removes the transaction record. Thus neither stable entrypoint is functional before the one `current` publication.

Recovery under the exclusive lock uses only the recorded old/new identities and phase; it never guesses from a directory. For an upgrade it either finishes that recorded sequence or restores `old_current` and `old_previous`. For an initial install it either idempotently finishes the recorded link/current/manifest sequence, or—if the recorded new generation can no longer be proven safe—removes only stable links that still exactly match that transaction's recorded targets, leaves no `current`/`previous`, and retains the incomplete transaction for matching re-plan and repair. Before deleting any generation, uninstall or prune records a removal transaction containing the operation and exact manifest entries approved for removal. Crash recovery may continue only those recorded removals; it never discovers deletion candidates from directory contents. A failed revalidation leaves the transaction for `status` and matching re-approval. A failed operation therefore preserves prior current and previous generations, or preserves an auditable initial-install or removal transaction; it may leave recognizable staging but never auto-deletes it. Existing unowned paths, partial layouts, unsafe owners/modes, unknown manifests, drifted links, or legacy manual adapters fail closed. `status` reports incomplete managed transactions without declaring them repaired. No `--replace` exists.

Same-SHA default installation is idempotent. A different current commit requires `--upgrade` in both its plan and install invocation. Bootstrap retains current and previous; older verified generations persist until explicit `prune` or exact-commit `uninstall --commit`. `status` is read-only; approved uninstall and prune remove only their exact plan-bound, manifest-proven generations other than current and previous. V1 has no rollback command, full Bootstrap uninstall, or protected-generation removal. No background update, time-based deletion, pointer-only promotion of an inactive generation, or target-repository deletion exists.

## Repository Onboarding after Bootstrap

The installed Codex skill is version-bound to the same `current` generation as the CLI. Its invocation receives an explicit absolute target and Candidate kind. It preflights target and Candidate, reuses existing `.forgepilot/` state, uses or drafts/reviews a PraxisBound Story, then obtains a distinct Repository Onboarding Approval before `forgepilot init`, `goal create`, `work add`, or `status`. Bootstrap grants none of that authority. The existing short prompt remains the zero-install route.

## Acceptance criteria

| ID | Criterion |
| --- | --- |
| AC-01 | `plan` supports only its four specified mutually exclusive modes and their applicable inputs. Every mode is inspection-only and has no staging, user-home mutation, target-repository, network, credential-helper, Agent-login, or source-code-execution side effect. |
| AC-02 | Install and upgrade require Darwin arm64, Git, make, a compatible installed Go toolchain, a safe absolute local source checkout, and an available lowercase full commit before staging or user-home writes. Status, uninstall, prune, and their plans do not require source, Git, make, or Go and execute no source-controlled code. |
| AC-03 | Human and NUL renderers use the same operation-tagged action list. Every mutator repeats its applicable inputs and approval identifier and recomputes the plan under the exclusive lock. A changed operation, input, current/previous identity, retained-generation set, transaction, ownership, mode, link, command, ordering, or path fact is rejected before mutation. |
| AC-04 | Approved execution uses a detached exact checkout, source-builds with user-owned `GOBIN`, runs source `make verify`, and confirms staged CLI startup before publication. |
| AC-05 | Initial success creates the managed layout and makes CLI and skill resolve through one current generation. |
| AC-06 | Upgrade holds the exclusive lock, revalidates before mutation, and changes visible CLI and skill generation only by one current-pointer switch. |
| AC-07 | Crash injection covers initial install, upgrade, uninstall, and prune at every transaction, external-link, `current`/previous, generation-removal, and manifest boundary. Recovery uses only recorded identities and exact manifest entries; unrelated operations fail closed, matching recovery requires a freshly approved plan, and `status` never repairs. |
| AC-08 | Unowned CLI, skill, directory, symlink, partial layout, unsafe owner/mode, unknown manifest, drifted link, or legacy adapter is refused without replacement or deletion. |
| AC-09 | Manifest and retained diagnostics contain no target-repository data, `.forgepilot/` state, environment values, credentials, or raw output. |
| AC-10 | Same-SHA default installation is idempotent. A different current commit is refused unless both plan and install repeat `--upgrade`. No command auto-updates, promotes a retained inactive generation as a rollback operation, or deletes by age. |
| AC-11 | `status` is an unlocked, rechecked point-in-time read: it returns nonzero for unsafe, changed, or non-idle state and never repairs it. Approved uninstall and prune take the exclusive lock and remove only their exact plan-bound, manifest-proven inactive targets. Planning and execution both refuse current and previous. V1 has no rollback command, full Bootstrap uninstall, or protected-generation removal. |
| AC-12 | Bootstrap never changes a shell profile, accesses remote source, prompts for or invokes credential helpers, reads Agent login, or touches target repository, lifecycle state, migration, or Evidence. Before approval it executes no source-controlled code; after approval it isolates controlled Git operations but explicitly does not claim to sandbox approved build/verification logic. |
| AC-13 | In a clean Apple-Silicon disposable user home, real Codex discovers the managed symlink skill, reads the same generation procedure as the CLI, and stops before target writes until distinct approval. |
| AC-14 | Direct prompt onboarding remains usable without a skill and retains the same Story and repository-write boundaries. |

## Required verification for implementation

Implementation must add hermetic tests for action parsing, exact source binding, lock exclusion, transaction crash recovery, link and manifest ownership/drift, upgrade, uninstall, prune, redaction, no-network/no-credential-helper behavior, and no-target-write guarantees. It must also run a clean native Apple-Silicon acceptance in a disposable home that verifies real Codex discovery. At integration/final acceptance, run `make verify` and `go test -race -count=1 ./...`.
