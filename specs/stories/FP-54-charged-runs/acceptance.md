# Acceptance Criteria

## Happy Path

* [x] AC-001: Direct run and exact-run resume both admit only under the same current authorization and make durable idempotent run/action reservations before an implementation or repair worker launches.

## Business Rules

* [x] AC-002: Total run, step, and per-node technical-attempt caps remain cumulative across new runs and exact-run resumes; Run Record history cannot reset or reconstruct ledger authority.

## Failure Cases

* [x] AC-003: Missing, expired, profile-mismatched, or accounting-inconsistent authorization, and any unconfirmed crash during charged preparation, refuse before worker launch and never receive a free retry.
* [x] AC-004: Injected crashes at each ledger/witness/Run Record/Pending ordering boundary preserve fail-closed ownership and prevent duplicate or uncharged launch.

## Regression Requirements

* [x] AC-005: Manual work, verification, review, and governance preserve their current contracts, while exact-run resume does not extend its original deadline or budget.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/runner/charged_recovery_test.go` | `authorized-direct-and-resume-fixtures` | `charge-before-worker-launch` |
| `AC-002` | test | `internal/work/execution_charge_test.go`, `internal/runner/charged_recovery_test.go` | `multi-run-cap-fixture` | `cumulative-caps-never-reset` |
| `AC-003` | test | `internal/runner/charged_recovery_test.go` | `invalid-authorization-and-crash-fixtures` | `no-worker-and-no-free-retry` |
| `AC-004` | test | `internal/runner/charged_recovery_test.go` | `ordering-crash-matrix` | `fail-closed-durable-ownership` |
| `AC-005` | test | `internal/cli/run_test.go` | `unaffected-contract-fixture` | `non-run-contracts-and-exact-run-preserved` |

## Verification Notes

Crash/ownership coverage must use real subprocesses and persistent fixtures;
in-memory mocks alone cannot prove the required ordering behavior.
