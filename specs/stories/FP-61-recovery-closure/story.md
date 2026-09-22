# Story: FP-61 close backup, migration, and native acceptance

## Goal

Bounded execution restores only provably matching authorization/accounting state, migrates legacy runs
explicitly and atomically, and closes the program with full, race, subprocess, opt-in Codex, and native
macOS evidence.

## Context

ADR-0035 forbids reconstructing consumption from run records or logs. A Goal restores only from an
exact complete backup matching the last known authorization identity and accounting witness, and always
returns paused. Legacy pre-authorization runs are historical: an explicitly approved cutover maps the
whole Goal without inventing consumption, forbids direct legacy resume, and preserves Work Items and
Evidence without reassignment. This final slice records native limits rather than claiming unattended
operation through logout or sleep.

## Classification

* Security sensitive: yes
* Baseline conformance: no
* Task mode: execution

## Authority

* plan: yes
* modify: yes
* add_dependency: no
* migration: yes
* commit: no
* push: no
* deploy: no

## Risk

* Level: high
* Reason: `accounting-integrity`
* Reason: `migration-safety`

## Scope

### In Scope

* Fail-closed backup identity/accounting-witness validation and paused restoration.
* Explicit atomic legacy Goal cutover, preservation of historical Work Items/Evidence/runs, and legacy
  resume rejection.
* Repository full/race gates and consolidated real-subprocess, opt-in Codex, and native macOS acceptance
  evidence under existing credential/artifact rules.

### Out of Scope

* Resetting or clearing an authorization ledger, deriving consumption from logs, reopening DONE Work
  Items, reassigning historical Evidence, automatic credential login, or changing external release flow.

## Inputs

* Last-known authorization identity and accounting witness, candidate complete backup, legacy Goal state,
  explicit cutover approval, and existing test/acceptance environments.

## Outputs

* Paused restored Goal or fail-closed block, atomic cutover record, preserved historical state, and
  reproducible acceptance evidence.

## Rules

* R1: Only an exact complete backup matching authorization identity and accounting witness may restore;
  restore always starts paused.
* R2: Missing, corrupt, stale, or mismatched data blocks the Goal; logs and run history never infer
  consumption, and there is no reset/force-bypass path.
* R3: Legacy cutover is explicit, atomic, complete-Goal mapped, starts its ledger at cutover, preserves
  historical state, and makes legacy runs non-resumable.

## Expected Errors

* Missing, corrupt, stale, or mismatch backup rejects restoration without state mutation.
* Incomplete mapping, absent approval, or interrupted legacy cutover rejects/rolls back atomically.

## Dependencies

* GitHub issues #51 through #60; ADR-0035.

## Constraints

* Full gate is `make verify`; final race gate is `go test -race -count=1 ./...`.
* Real subprocess tests remain required; Codex smoke is opt-in only when `FORGEPILOT_CODEX_SMOKE=1`,
  and artifacts follow existing rules.
* Native macOS evidence documents login/reboot/sleep-wake support limits without claiming logged-out or
  sleeping execution.

## Guidance

Relevant:

* decision: `ADR-0035` — exact backup recovery, legacy cutover, and acceptance boundaries
* decision: `ADR-0020` — fail-closed process recovery

Not applicable:

* automatic ledger reset or inferred historical accounting

## Trust Boundary Fields

* `backup.authorization_identity` — identity that must exactly match the last known authorization.
* `backup.accounting_witness` — last-known ledger witness that must exactly match before restoration.
* `cutover.approval` — explicit human authorization for the legacy Goal cutover.
* `cutover.goal_mapping` — complete explicit legacy-to-plan-node mapping, never inferred from paths.
