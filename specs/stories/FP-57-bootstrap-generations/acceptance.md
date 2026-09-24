# Acceptance Criteria

## Happy Path

* [x] AC-001: Bootstrap stages and verifies a complete CLI, helper, and Codex skill/procedure payload;
  its generation tuple is the full source commit plus canonical payload digest, and one atomic pointer
  makes the tuple current.
* [x] AC-002: A caller using `retention-v1` can acquire and release an opaque reference to an exact
  generation tuple without recording or reading a target-repository or holder identity.

## Business Rules

* [x] AC-003: Prune removes only conclusively unretained non-current generations; uninstall preserves
  retained or uncertain generations and reports why removal is blocked.
* [x] AC-004: Bootstrap-controlled status, prune, uninstall, and retention-v1 operations make no network
  call and consume no credentials. Approved source build/verification may have only its declared
  repository-defined effects and is not represented as sandboxed.

## Failure Cases

* [x] AC-005: A pre-commit stage/verification/pointer failure leaves the prior generation selected.
  After an install/upgrade pointer commit, the authoritative transaction retains both generation
  identities and status remains nonzero until a freshly approved matching recovery completes or
  restores the recorded sequence. Prune/uninstall recovery covers completed command boundaries
  while surviving candidates match their recorded complete payloads. Unattributed staging before
  transaction publication and a partial candidate after interruption inside recursive deletion
  fail closed for manual inspection; neither is automatically deleted.
* [x] AC-006: Malformed, missing, or uncertain retention state fails closed and cannot authorize
  deletion.

## Regression Requirements

* [x] AC-007: Source-built Bootstrap remains separate from Repository Onboarding and does not scan or
  register target repositories.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `scripts/forgepilot-bootstrap_install_test.sh` | `disposable local Git checkout and user home` | `approved exact-commit install and upgrade publish complete verified generations through current; changed staged skill bytes are refused` |
| `AC-002` | test | `scripts/forgepilot-bootstrap_test.sh` | `prepublished local generations and opaque references` | `exact-tuple acquire/release, idempotence, hashed-only persistence, and shared-lock serialization pass; target/holder identity is absent` |
| `AC-003` | test | `scripts/forgepilot-bootstrap_test.sh` | `prepublished current, previous, retained, and two removable generations` | `approved uninstall removes one exact generation; approved prune removes both eligible generations and preserves protected generations` |
| `AC-004` | test + inspection | `scripts/forgepilot-bootstrap_isolation_test.sh`; controlled-command audit | `disposable HOME, PATH/Git credential tripwires, approved removal and retention operations` | `no tripwire invocation or secret persistence; controlled paths contain no network client or ambient credential read; approved source build remains outside this guarantee` |
| `AC-005` | test | `scripts/forgepilot-bootstrap_install_test.sh`; `scripts/forgepilot-bootstrap_crash_test.sh`; `scripts/forgepilot-bootstrap_test.sh` | `recorded initial, upgrade, uninstall, and prune transactions; 35 completed-statement crash boundaries; added-child and missing-file staged-candidate cases; unauthoritative pre-publication transaction stage` | `fresh matching approval completes or restores recorded state at each tested command boundary; changed or partial staged content blocks planning and deletion; unauthoritative staging blocks operation and is preserved` |
| `AC-006` | test | `scripts/forgepilot-bootstrap_test.sh` | `malformed marker and missing store after a valid prune plan` | `approved execution refuses deletion before publishing a removal transaction` |
| `AC-007` | test + inspection | `scripts/forgepilot-bootstrap_isolation_test.sh`; `scripts/forgepilot-bootstrap_test.sh`; command audit | `target Git repository as current directory, sentinel and `.forgepilot/` content, forbidden --target argument` | `target bytes, modes, and mtimes unchanged; target option rejected; Bootstrap contains no target-repository traversal or registration path` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `bootstrap.generation_id` | `0123456789abcdef0123456789abcdef01234567` | preserve | `manifest and generation path` | `scripts/forgepilot-bootstrap_test.sh` |
| `bootstrap.payload_digest` | `sha256:<64 lowercase hex>` | preserve | `manifest and retention tuple` | `scripts/forgepilot-bootstrap_test.sh` |
| `retention.reference_id` | `64 lowercase hex characters` | redact | `SHA-256-only retention marker filename` | `scripts/forgepilot-bootstrap_test.sh` |

## Verification Notes

The executable slice covers `status`, `retention-v1`, approved prune and exact-commit
uninstall, initial install, upgrade, isolation fixtures, and all 35 recorded
crash boundaries. V1 deliberately blocks automatic recovery of mid-command
partial deletion for manual inspection. AC-13 native Codex procedure use and
the distinct repository-write approval stop passed in an authenticated
Apple-Silicon disposable home; see `verification.md`. PraxisBound's
`review readiness-digests` command refreshes the upstream-owned
`readiness.json` digests after this contract update.
