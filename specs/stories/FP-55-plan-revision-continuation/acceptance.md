# Acceptance Criteria

## Happy Path

* [x] AC-001: `execution revise` previews and atomically commits an explicitly approved additive reviewed revision while preserving prior nodes, Story references, edges, Evidence, authorization history, and cumulative consumption.
* [x] AC-002: A confirmed-cleanup run stopped only by `MAX_STEPS` or `MAX_DURATION` auto-rolls over only when current authorization/bindings remain valid and cumulative caps permit it.

## Business Rules

* [x] AC-003: Explicit authorization resume and exact-run resume preserve their distinct contracts; neither resets total caps nor changes an old run's deadline, budget, or stop history.

## Failure Cases

* [x] AC-004: Delete/replace node or alter an existing dependency requires a new Goal, and attempts/no-progress, agent/verification timeout, capacity, waits, drift, expiry, or recovery block never auto-roll over.
* [x] AC-005: Plan or authorization drift after session completion prevents a new action or rollover; verification lawfully started before drift retains honest immutable Candidate Evidence then stops.

## Regression Requirements

* [x] AC-006: Existing freshness rules decide reused Evidence, DONE remains terminal, and machine verification is not presented as Human final acceptance.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/app/execution_revision_test.go` | `additive-reviewed-revision-fixture` | `history-and-consumption-preserved` |
| `AC-002` | test | `internal/runner/rollover_test.go` | `max-steps-and-max-duration-fixtures` | `only-eligible-runs-roll-over` |
| `AC-003` | test | `internal/app/execution_resume_test.go` | `exact-and-authorization-resume-fixtures` | `distinct-contracts-preserved` |
| `AC-004` | test | `internal/runner/rollover_test.go` | `ineligible-stop-reason-matrix` | `no-automatic-rollover` |
| `AC-005` | test | `internal/runner/verification_boundary_test.go` | `session-to-verification-drift-fixture` | `honest-evidence-then-stop` |
| `AC-006` | test | `internal/work/work_test.go` | `terminal-and-freshness-regression-fixture` | `existing-lifecycle-boundaries-preserved` |

## Verification Notes

Use a true worker subprocess for session/verification and rollover boundaries;
the test must demonstrate that Evidence is retained rather than merely mocked.
