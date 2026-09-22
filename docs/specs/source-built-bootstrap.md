# Source-built Bootstrap — accepted implementation contract

## Status and boundary

This is the accepted design contract. `scripts/forgepilot-bootstrap` now exists as an incomplete development slice: `status`, `generation-v1 current`, and `retention-v1` are implemented, while install, upgrade, plan, prune, and uninstall are not yet available as a supported workflow. The governing decisions are [ADR-0030](../adr/0030-source-built-onboarding-without-apple-developer.md), [ADR-0032](../adr/0032-formal-macos-onboarding-is-apple-silicon-only.md), and [ADR-0033](../adr/0033-source-built-bootstrap-separates-distribution-from-onboarding.md). Existing FP-33 through FP-35 evidence describes the previous coupled procedure and remains historical evidence.

Bootstrap is a repository-external POSIX-shell distribution surface. It installs one source-built ForgePilot CLI, one Codex standalone skill, and the generation-matched Bootstrap control-plane helper into the current user's home. It is not a ForgePilot core command, plugin, release downloader, or Repository Onboarding.

## Invocation and protocol

The target lifecycle interface from a developer-obtained local ForgePilot checkout is:

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

Every installed generation also exposes a versioned, machine-only retention seam through
`~/.local/bin/forgepilot-bootstrap`:

```text
forgepilot-bootstrap generation-v1 current
forgepilot-bootstrap retention-v1 acquire --generation <lowercase-40-character-sha> --payload-digest sha256:<lowercase-64-character-sha> --reference <lowercase-64-character-token>
forgepilot-bootstrap retention-v1 release --generation <lowercase-40-character-sha> --payload-digest sha256:<lowercase-64-character-sha> --reference <lowercase-64-character-token>
```

`generation-v1 current` accepts no repository, target, or source path. After the same read-only, rechecked
managed-layout validation as `status`, it emits exactly one JSON value with `protocol_version: 1`, the exact
current `generation_id` and `payload_digest`, and absolute generation-local `forgepilot_path` and `helper_path`.
The paths are the immutable `versions/<commit>/` members observed in that read; callers must compare them to
the process image captured when ForgePilot started and its adjacent generation-local helper, rather than
re-resolving `current` or using PATH. Callers treat only the fixed managed Bootstrap stable link as the helper
anchor and require its one resolution to be that adjacent helper; any changed generation fails closed. The result
is one strict JSON object: every string is JSON-escaped and duplicate keys, unknown keys, and trailing JSON
values are protocol errors. A `HOME` containing a JSON control byte or non-UTF-8 bytes is refused before layout
inspection rather than emitting an ambiguous v1 path string.

The ForgePilot entrypoint first re-execs a stable CLI link to its exact generation-local image before command
processing. A source build that is already an exact path is not re-execed, but cannot be treated as a managed
generation later. This leaves no later `current` resolution in engine discovery.

`retention-v1` emits one JSON result with `protocol_version: 1`; it accepts no source, target, or repository path and
does not invoke a build, verification, Git, network, or credential helper. The reference is a random
256-bit bearer value supplied by ForgePilot; Bootstrap stores only its SHA-256. Acquire is idempotent
for the same generation tuple, and an existing reference cannot be rebound. Release removes only the
matching reference and is idempotent when that reference is already absent. Both operations take the
same exclusive lock as install, upgrade, prune, and uninstall. They only change retention metadata;
removing a generation still requires a separately planned and approved prune or uninstall.

`plan` has four mutually exclusive modes: initial-or-idempotent installation (the default), explicit upgrade, exact-generation uninstall, and prune. Every mode is inspection-only and persists no approved-plan artifact. Every mutating command repeats its operation-specific inputs, takes the exclusive Bootstrap lock, recomputes the canonical plan and identifier from those inputs and the locked managed-state facts, and proceeds only if they still match `--approve <plan-id>`; this is the explicit Bootstrap Approval. The identifier binds the protocol version, operation kind, explicit inputs, ordered action list, and inspected current, previous, retained-generation, transaction, ownership, mode, and link facts.

The source for install and upgrade must be an absolute local Git checkout. Remote URLs, scp references, credential-bearing URLs, branches, tags, `main`, `latest`, abbreviated SHA, ambient Git routing, and substituting the installer working tree for `--source` are rejected. Default installation refuses a valid installation whose current commit differs and directs the caller to explicitly re-plan with `--upgrade`. Upgrade requires a valid idle installation and a commit different from current; it never silently converts an initial install or same-SHA invocation into an upgrade. `uninstall --commit` removes exactly one manifest-proven inactive generation. `prune` binds and removes the exact ordered set of all manifest-proven inactive generations present at planning time. Planning and execution both refuse current, previous, unknown, substituted, or drifted paths.

`status` accepts no approval or source inputs and performs no recovery or mutation. It is an unlocked point-in-time read: it reads the atomically published manifest and pointers, rechecks transaction and observed facts before returning, and returns nonzero rather than presenting a coherent result if they changed during the read. It exits successfully only for a valid idle managed layout. An absent or invalid lock marker, unknown schema, unsafe ownership or mode, drift, a non-idle transaction, or changed read facts is reported and returns nonzero. This avoids claiming a POSIX-shell shared-lock primitive that macOS does not provide; only mutations take the Bootstrap exclusive lock. When a transaction is non-idle, unrelated plans fail closed. Only re-planning the same operation with the exact recorded identities may produce a recovery plan; executing that freshly approved matching operation may finish or restore the recorded transaction. `status` never repairs it.

Install and upgrade support Darwin arm64 only and require Git, `make`, and an already-installed Go toolchain compatible with the `go` directive at the exact source commit. They do not install Go, a package manager, dependencies, or a shell profile. Unsupported host, missing prerequisite, malformed identity, unsafe source checkout, or unavailable exact commit fails before staging or user-home writes. Status, uninstall, prune, and their plans require neither the source checkout nor Git, `make`, Go, or source build/verification code; they execute only Bootstrap control-plane logic, whether invoked from the installed helper or an explicitly chosen checkout. Retention protocol calls require none of those prerequisites either.

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
  retention/v1/<sha256-reference>          # exact tuple; raw reference is not stored
  versions/<commit>/
    bin/forgepilot
    libexec/forgepilot-bootstrap
    skills/codex/forgepilot-onboarding/
    docs/release/onboarding.md
~/.local/bin/forgepilot -> ~/.local/share/forgepilot/current/bin/forgepilot
~/.local/bin/forgepilot-bootstrap -> ~/.local/share/forgepilot/current/libexec/forgepilot-bootstrap
~/.agents/skills/forgepilot-onboarding
  -> ~/.local/share/forgepilot/current/skills/codex/forgepilot-onboarding
```

The generation identity is the pair `(source_commit, payload_digest)`, where source commit is the full lowercase Git SHA and payload digest is `sha256:<64 lowercase hex>` over `forgepilot-payload-v1\n` followed by a C-locale, sorted inventory of each relative path, entry kind, mode, and (for regular files) exact-byte SHA-256. The payload includes the CLI, installed helper, Codex skill tree, and matching onboarding procedure. Symlinks, hard links, special files, malformed paths, or any post-publication digest drift fail closed. A source commit names at most one installed payload under `versions/<commit>`; a second build for that commit with a different digest is rejected rather than replacing the generation.

`manifest.json` has schema version `1`, a `generations` object keyed by full source commit, and `current` / optional `previous` commit references. Each generation value contains exactly its `payload_digest` and managed relative path (`versions/<commit>`); validation also recomputes the complete payload digest. Each file in `retention/v1/` is named by the reference's lowercase SHA-256 and contains exactly three newline-terminated records: `forgepilot-retention-v1`, `generation=<full-source-commit>`, and `payload_digest=sha256:<64-lowercase-hex>`. The directory must exist, be user-owned and private, and contain only valid marker files; no marker stores the raw reference.

The approved action order is: prerequisite and source inspection; Bootstrap lock acquisition; staging; detached checkout of exact source commit; exact-commit Go compatibility check; user-owned `GOBIN` build; source `make verify`; staged CLI and helper protocol checks; immutable generation preparation and payload digest; authoritative transaction record; generation publication; `current` switch; manifest completion. Revalidation immediately precedes every mutation. In particular, no stable external link or `current` pointer is created before the transaction record exists.

A generation is immutable after verification. The managed lock marker is created only during initial installation and retained thereafter; mutations take the Bootstrap exclusive lock, while `status` only validates that marker during its unlocked point-in-time read. `current` is the only pointer replaced on upgrade, by same-directory atomic rename while holding that lock. External CLI, Bootstrap-helper, and skill links are created only on fully staged initial install and are never rewritten on upgrade; all resolve through `current`. The manifest is atomically replaced under the lock and inventories every retained verified generation by source commit, payload digest, and exact managed root; current and previous reference entries in that inventory. It also stores only schema version, expected link targets, and in-progress transaction identity. A versioned retention store records exact tuples keyed by the SHA-256 of the opaque reference, never its raw value. Both stores contain no target repository, `.forgepilot/` state, credential, environment value, holder identity, or raw subprocess output. Missing, malformed, changed, or unknown retention state is uncertainty and blocks destructive removal. Prune plans bind its canonical digest and count, not holder values; any acquire or release invalidates an earlier prune approval.

An upgrade records an authoritative transaction with `old_current`, `old_previous`, `new_current`, and phase `prepared`; it prepares `current.new`, changes phase to `switching`, atomically replaces `current`, writes `previous` from `old_current`, atomically writes the completed manifest, then removes the transaction record. An initial installation records the same transaction before any external-link or `current` mutation, with explicitly absent `old_current` and `old_previous`; after that record, it creates each expected stable link (which may deliberately dangle through `current`), changes phase to `switching`, atomically publishes `current`, atomically writes the initial manifest and empty versioned retention store, then removes the transaction record. All three stable entrypoints become functional through the one `current` publication.

Recovery under the exclusive lock uses only the recorded old/new generation tuples and phase; it never guesses from a directory. For an upgrade it either finishes that recorded sequence or restores `old_current` and `old_previous`. For an initial install it either idempotently finishes the recorded link/current/manifest sequence, or—if the recorded new generation can no longer be proven safe—removes only stable links that still exactly match that transaction's recorded targets, leaves no `current`/`previous`, and retains the incomplete transaction for matching re-plan and repair. Before deleting any generation, uninstall or prune records a removal transaction containing the operation and exact manifest entries approved for removal. Crash recovery may continue only those recorded removals; it never discovers deletion candidates from directory contents. A failed revalidation leaves the transaction for `status` and matching re-approval. A failed stage, verification, or failed atomic pointer replacement before commit leaves prior current selected. A failure after a successful current switch leaves the non-idle transaction authoritative, both exact generations retained, and `status` nonzero until only a freshly approved matching recovery completes or restores the recorded sequence. Existing unowned paths, partial layouts, unsafe owners/modes, unknown manifests, malformed retention state, drifted links, or legacy manual adapters fail closed. `status` reports incomplete managed transactions without declaring them repaired. No `--replace` exists.

Same-SHA default installation is idempotent only for the exact generation tuple. A different current commit requires `--upgrade` in both its plan and install invocation. Bootstrap retains current and previous; older verified generations persist until explicit `prune` or exact-commit `uninstall --commit`, and any retained or uncertain generation is ineligible for removal. `status` is read-only; approved uninstall and prune remove only their exact plan-bound, manifest-proven inactive targets other than current and previous. V1 has no rollback command, full Bootstrap uninstall, or protected-generation removal. No background update, time-based deletion, pointer-only promotion of an inactive generation, or target-repository deletion exists.

## Repository Onboarding after Bootstrap

The installed Codex skill is version-bound to the same `current` generation as the CLI. Its invocation receives an explicit absolute target and Candidate kind. It preflights target and Candidate, reuses existing `.forgepilot/` state, uses or drafts/reviews a PraxisBound Story, then obtains a distinct Repository Onboarding Approval before `forgepilot init`, `goal create`, `work add`, or `status`. Bootstrap grants none of that authority. The existing short prompt remains the zero-install route.

## Acceptance criteria

| ID | Criterion |
| --- | --- |
| AC-01 | `plan` supports only its four specified mutually exclusive modes and their applicable inputs. Every mode is inspection-only and has no staging, user-home mutation, target-repository, network, credential-helper, Agent-login, or source-code-execution side effect. |
| AC-02 | Install and upgrade require Darwin arm64, Git, make, a compatible installed Go toolchain, a safe absolute local source checkout, and an available lowercase full commit before staging or user-home writes. Status, uninstall, prune, and their plans do not require the source checkout, Git, make, Go, or source build/verification code; retention-v1 requires none of them either. |
| AC-03 | Human and NUL renderers use the same operation-tagged action list. Every install, upgrade, uninstall, or prune mutator repeats its applicable inputs and approval identifier and recomputes the plan under the exclusive lock. A changed operation, input, current/previous generation tuple, retention-store digest/count, transaction, ownership, mode, link, command, ordering, or path fact is rejected before mutation. Retention-v1 is limited to exact-tuple marker changes under that same lock and cannot remove generations. |
| AC-04 | Approved execution uses a detached exact checkout, source-builds with user-owned `GOBIN`, runs source `make verify`, and confirms staged CLI startup before publication. |
| AC-05 | Initial success creates the managed layout and makes CLI, Bootstrap helper, and skill resolve through one current generation. |
| AC-06 | Upgrade holds the exclusive lock, revalidates before mutation, and changes visible CLI, Bootstrap helper, and skill generation only by one current-pointer switch. |
| AC-07 | Crash injection covers initial install, upgrade, uninstall, and prune at every transaction, external-link, `current`/previous, generation-removal, and manifest boundary. Recovery uses only recorded identities and exact manifest entries; unrelated operations fail closed, matching recovery requires a freshly approved plan, and `status` never repairs. |
| AC-08 | Unowned CLI, skill, directory, symlink, partial layout, unsafe owner/mode, unknown manifest, drifted link, or legacy adapter is refused without replacement or deletion. |
| AC-09 | Manifest, retention markers, and retained diagnostics contain no target-repository data, `.forgepilot/` state, environment values, credentials, holder identity, raw reference token, or raw output. |
| AC-10 | Same-SHA default installation is idempotent. A different current commit is refused unless both plan and install repeat `--upgrade`. No command auto-updates, promotes a retained inactive generation as a rollback operation, or deletes by age. |
| AC-11 | `status` is an unlocked, rechecked point-in-time read: it returns nonzero for unsafe, changed, or non-idle state and never repairs it. Approved uninstall and prune take the exclusive lock and remove only their exact plan-bound, manifest-proven inactive targets with no retention reference or uncertainty. Planning and execution both refuse current and previous. V1 has no rollback command, full Bootstrap uninstall, or protected-generation removal. |
| AC-12 | Bootstrap never changes a shell profile, uses network or credentials in its own protocol, reads Agent login, or touches target repository, lifecycle state, migration, or Evidence. Before approval it executes no source checkout/build code; after approval it isolates controlled Git operations but explicitly does not claim to sandbox approved build/verification logic, which may have repository-defined effects. |
| AC-13 | In a clean Apple-Silicon disposable user home, real Codex discovers the managed symlink skill, reads the same generation procedure as the CLI, and stops before target writes until distinct approval. |
| AC-14 | Direct prompt onboarding remains usable without a skill and retains the same Story and repository-write boundaries. |

## Required verification for implementation

Implementation must add hermetic tests for action parsing, exact source binding, lock exclusion, transaction crash recovery, link and manifest ownership/drift, upgrade, uninstall, prune, redaction, no-network/no-credential-helper behavior, and no-target-write guarantees. It must also run a clean native Apple-Silicon acceptance in a disposable home that verifies real Codex discovery. At integration/final acceptance, run `make verify` and `go test -race -count=1 ./...`.
