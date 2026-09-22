# Acceptance Criteria

## Happy Path

* [ ] AC-001: User stop durably records pause intent before cancellation, retains logs/Evidence/work modifications, and requires explicit resume after process cleanup.
* [ ] AC-002: One validated `needs_human` result is classified once as a wait without consuming a technical attempt, and explicit resume rechecks current authorization, plan, pause, and recovery facts.

## Business Rules

* [ ] AC-003: Restart, recovery, logout, reboot, and supervisor restart retain persisted pause intent and never substitute automatic continuation.
* [ ] AC-004: An External Fulfillment Declaration binds named self-declared facts to the exact wait, node, plan digest, and authorization revision; it is invalidated by a plan/authorization change and does not resume work itself.

## Failure Cases

* [ ] AC-005: Malformed, absent, or crashed agent results retain their technical-attempt charge, and unknown worker ownership, stale declaration, or unmet resume condition stays stopped fail closed.

## Regression Requirements

* [ ] AC-006: Waits create neither `WAITING_HUMAN` nor `BLOCKED` Work Item state and declarations are neither Evidence, Gate resolution, nor Human final acceptance.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/runner/stop_control_test.go` | `live-worker-stop-fixture` | `pause-persisted-before-cancel` |
| `AC-002` | test | `internal/runner/human_wait_test.go` | `validated-needs-human-fixture` | `single-wait-without-attempt-charge` |
| `AC-003` | test | `internal/runner/stop_control_test.go` | `restart-and-recovery-fixtures` | `no-automatic-continuation` |
| `AC-004` | test | `internal/app/external_fulfillment_test.go` | `bound-declaration-fixtures` | `exact-binding-and-no-implicit-resume` |
| `AC-005` | test | `internal/runner/human_wait_test.go` | `invalid-result-and-ownership-fixtures` | `charged-fail-closed-stop` |
| `AC-006` | test | `internal/work/work_test.go` | `wait-boundary-regression-fixture` | `no-lifecycle-or-evidence-substitution` |

## Verification Notes

Use actual subprocesses for cancellation, recovery, and ownership behavior;
memory-only mocks cannot establish the process-control contract.
