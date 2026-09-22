# Acceptance Criteria

## Happy Path

* [ ] AC-001: An exact complete backup matching the last known authorization identity and accounting
  witness restores the Goal in paused state with its ledger and history intact.
* [ ] AC-002: An explicit approved complete legacy Goal mapping cuts over atomically, starts new
  accounting at cutover, preserves historical Work Items/Evidence/runs, and rejects direct legacy resume.

## Business Rules

* [ ] AC-003: Historical consumption is visibly excluded from the new ledger and is never invented or
  derived from logs or run records.
* [ ] AC-004: No reset, ledger-clear, force-bypass, Evidence reassignment, or DONE reopen path exists.

## Failure Cases

* [ ] AC-005: Missing, corrupt, stale, incomplete, or identity/witness-mismatched backup fails closed
  without restoring, consuming, or mutating Goal state.
* [ ] AC-006: Missing approval, incomplete mapping, dependency mismatch, or interrupted cutover fails
  atomically and leaves the legacy Goal unchanged and non-resumable.

## Regression Requirements

* [ ] AC-007: `make verify` passes.
* [ ] AC-008: `go test -race -count=1 ./...` passes.
* [ ] AC-009: Real-subprocess recovery and opt-in Codex smoke evidence follow existing credential,
  exact opt-in, and external-artifact rules.
* [ ] AC-010: Native macOS acceptance records supported login, reboot, and sleep/wake outcomes and does
  not claim execution while logged out or asleep.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `go test ./internal/storage ./internal/app` | `exact complete matching backup fixture` | `Goal restores paused with matching ledger and preserved history` |
| `AC-002` | test | `go test ./internal/app ./internal/runner` | `approved complete legacy Goal mapping fixture` | `atomic cutover preserves old history and rejects legacy resume` |
| `AC-003` | test | `go test ./internal/storage ./internal/app` | `legacy state with historical run records` | `new ledger begins at cutover with explicit excluded-history marker` |
| `AC-004` | test | `go test ./internal/cli ./internal/app ./internal/work` | `command and state-transition inventory` | `no reset bypass reassignment or reopen behavior is reachable` |
| `AC-005` | test | `go test ./internal/storage ./internal/app` | `missing corrupt stale incomplete and mismatch backup fixtures` | `restoration fails closed without mutation` |
| `AC-006` | test | `go test ./internal/app ./internal/storage` | `approval mapping dependency and interruption fixtures` | `cutover rejects or rolls back atomically` |
| `AC-007` | command | `make verify` | `repository checkout` | `exit 0` |
| `AC-008` | command | `go test -race -count=1 ./...` | `repository checkout` | `exit 0` |
| `AC-009` | test | `go test ./internal/runner ./internal/agent` | `real subprocess fixture and FORGEPILOT_CODEX_SMOKE=1 opt-in environment` | `process evidence passes; smoke runs only for exact opt-in and exports artifacts only by existing rule` |
| `AC-010` | human | `docs/operations/macos-supervision-acceptance.md` | `native Apple Silicon macOS acceptance session` | `documented lifecycle outcomes state supported limits` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `backup.authorization_identity` | `goal-42:authorization-v3` | preserve | `restored authorization record` | `go test ./internal/storage ./internal/app` |
| `backup.accounting_witness` | `ledger-sha256-abc123` | preserve | `restored ledger record` | `go test ./internal/storage ./internal/app` |
| `cutover.approval` | `human:maintainer:2026-09-19` | preserve | `atomic cutover record` | `go test ./internal/app ./internal/storage` |
| `cutover.goal_mapping` | `node-a=work-1,node-b=work-2` | reject | `cutover boundary` | `go test ./internal/app ./internal/storage` |

## Verification Notes

Exact smoke opt-in is `FORGEPILOT_CODEX_SMOKE=1`; other spellings do not start Codex. If credentials
are unavailable, preserve sanitized evidence and report the product-scope limitation rather than
substituting credentials or weakening isolation.
