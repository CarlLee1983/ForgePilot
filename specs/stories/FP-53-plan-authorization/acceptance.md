# Acceptance Criteria

## Happy Path

* [ ] AC-001: `execution plan` returns a pure versioned preview and token bound to exact request bytes, referenced artifact digests, Goal/workspace, and observed authorization state.
* [ ] AC-002: `execution authorize` accepts a current matching token and self-declared approver, then atomically persists complete node mapping, authorization revision one, bounded ledger, Worker Profile, and Goal witness.

## Business Rules

* [ ] AC-003: Every Plan Node Reference maps explicitly to exactly one Work Item, no mapping is inferred from Story path or `external_ref`, and authorization does not grant lifecycle or Human-review powers.

## Failure Cases

* [ ] AC-004: Incomplete mapping, topology mismatch, invalid limits/profile, stale token/input, concurrent authorization, or injected storage failure leaves no partial mapping, authorization, ledger, or witness.

## Regression Requirements

* [ ] AC-005: The preview remains read-only and the adopted binding preserves PraxisBound ownership of manifest semantics and coverage approval.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/app/execution_plan_test.go` | `bound-request-fixture` | `pure-token-with-all-bindings` |
| `AC-002` | test | `internal/app/execution_authorize_test.go` | `complete-approved-plan-fixture` | `single-atomic-revision-one-publication` |
| `AC-003` | test | `internal/app/execution_authorize_test.go` | `ambiguous-mapping-fixtures` | `explicit-one-to-one-mapping-required` |
| `AC-004` | test | `internal/app/execution_authorize_test.go` | `failure-and-contention-fixtures` | `no-partial-durable-state` |
| `AC-005` | test | `internal/cli/execution_plan_test.go` | `read-only-ownership-fixture` | `no-write-or-semantic-inference` |

## Verification Notes

Use storage crash/atomicity fixtures rather than only in-memory mocks. This
Story introduces persisted schema/transaction behavior, so run the affected
storage and application checks required by the verification matrix.
